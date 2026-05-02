# Operational Runbook

What to do when something goes wrong, and how to perform the routine credential / cert / plugin rotations Praxis is designed for.

## Health probes

| Endpoint | Purpose | Auth |
|---|---|---|
| `GET /healthz` | liveness; returns `200` once the HTTP server is bound | none |
| `GET /metrics` | Prometheus exposition; `praxis_*` metrics | none |
| `GET /v1/capabilities` | readiness; returns the registered capability set | bearer |

For Kubernetes:

```yaml
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
  initialDelaySeconds: 5
readinessProbe:
  httpGet: { path: /healthz, port: 8080 }
  initialDelaySeconds: 5
```

## Key metrics

Production deployments should alert on:

- `praxis_actions_failed_total` rate over `praxis_actions_total` rate — error budget.
- `praxis_request_duration_ms_avg` — request latency.
- `praxis_plugin_load_total{result="..."}` — non-`success` outcomes signal a misconfigured plugin (signature, manifest, ABI, dlopen, crash).
- `praxis_plugin_memory_peak_bytes` and `praxis_plugin_cpu_seconds_total` — kernel-recorded usage when out-of-process loader is active.
- `praxis_audit_purge_total{result="error"}` — retention scheduler errors.
- `praxis_mcp_federation_status{status!="connected"}` — federation upstream disconnects.

## Rotations (zero-restart)

### TLS / mTLS certificates

Certificates load through `tlsLoader` which holds the active cert behind `atomic.Pointer`. Rotate:

```bash
# Replace the cert + key files in place (or via symlink swap).
kill -HUP $(pgrep -f 'praxis serve')
```

The `GetCertificate` callback picks up the new cert on the next TLS handshake; existing connections complete on the previous cert.

### API bearer token

Set `PRAXIS_API_TOKEN_FILE` (instead of `PRAXIS_API_TOKEN`) to make the token rotatable. Then:

```bash
echo -n "$NEW_TOKEN" > /etc/praxis/api-token   # operator-controlled file
kill -HUP $(pgrep -f 'praxis serve')
```

The auth middleware reads through the loader on every request, so requests already in flight when SIGHUP arrives may complete with the previous token, but new requests after the reload must present the new one.

### Plugin signing keys

Two paths:

1. **PEM key bundle** (`PRAXIS_PLUGIN_TRUSTED_KEYS`): replace the PEM bundle on disk and SIGHUP, which re-runs the plugin pipeline. Plugins signed by the rotated-out key fail verification on the next load.
2. **Fulcio identity policy** (`PRAXIS_PLUGIN_FULCIO_*`): rotation is by replacing `PRAXIS_PLUGIN_FULCIO_SUBJECTS` / `_ISSUER` env vars and restarting (env-var changes require restart; the trust policy is read once at startup).

### Plugin artefacts

`fsnotify` driven hot-reload picks up changes to `PRAXIS_PLUGIN_DIR` automatically when `PRAXIS_PLUGIN_AUTORELOAD` is enabled (default). On platforms without inotify, send `SIGHUP` to force a re-scan.

### Database connection

`PRAXIS_DB_CONN` requires a restart. For Postgres, point at a pgbouncer / pgpool instance to insulate Praxis from primary failover.

## Backups

The audit log is the canonical record of what happened. Backup priority:

1. `audit_events` table — replay-from-audit canary in CI guarantees this is sufficient to reconstruct every action's lifecycle.
2. `actions` table — operational state.
3. `idempotency_keys` — necessary to preserve idempotency guarantees across restores.
4. `outcome_outbox` — necessary to avoid losing un-delivered Mnemos events.
5. `policy_rules` — captured in config; less critical to back up if your config repo is the source of truth.
6. `capabilities` — re-registered on startup; not load-bearing.
7. `capability_history` — operator visibility only.

## Common incidents

### Bearer rejected with 401 after rotation

Either the new token was not flushed to disk before SIGHUP (race), or the file was empty / whitespace-only (loader rejects this and keeps the previous token). Check logs for `API token reload failed; previous token still active`. Re-write the file and re-send SIGHUP.

### Plugin load failures

`praxis_plugin_load_total{result="signature_failed"}` — verify the artefact matches the trusted key bundle. `_unsafe_artifact"}` — the manifest's artifact path escaped the plugin directory; reject. `_abi_mismatch"}` — plugin built against an older ABI; rebuild against the current `plugin.ABIVersion`. `_crashed"}` — child process died after load; inspect stderr captured via the plugin manager and the cgroup `memory.events` / `cpu.stat` files (linux out-of-process loader only).

### Federation upstream stuck disconnected

`praxis_mcp_federation_status{status="disconnected"}` lingering: check `Authorization: Bearer ...` token freshness in `mcp.federation.yaml`, the upstream's TLS cert validity (Praxis verifies against `ca_bundle` if pinned), and runtime logs for `federation: dial transport` errors. The supervisor reconnects with exponential backoff (1s → 30s, ×2); once the upstream is reachable, it self-heals.

### Audit retention not running

Symptom: `audit_events` row count grows past expected window. Check `PRAXIS_AUDIT_RETENTION` env var (per-tenant `key=duration` pairs); a malformed pair is dropped silently. Verify `praxis_audit_purge_total{org_id, result}` shows ticks at the configured interval; if it's flat at zero, the scheduler never started — confirm the env var is set in the deployment, not just locally.

### High latency at p99

`praxis_request_duration_ms_avg` is an avg, not a histogram. For p99 work, enable OTel tracing (`PRAXIS_OTLP_ENDPOINT`) and inspect spans in your observability stack. The executor opens spans for `executor.execute`, `handler.<capability>`, and outbound HTTP wrapped in `otelhttp.NewTransport` — a slow dependency surfaces immediately on the trace waterfall.

## Disaster recovery drill

Once per quarter, exercise:

1. Stop the running instance.
2. Restore the Postgres database from the latest backup into a fresh instance.
3. Start a new Praxis pointed at the restored DB.
4. Verify: `curl /v1/audit/{action_id}` for an action submitted before the failure shows the full lifecycle.
5. Submit a new action with a previously-used `idempotency_key`; the response must match the restored historical row exactly.

A failure on step 5 means the restore lost idempotency state — investigate before declaring the DR plan ready.
