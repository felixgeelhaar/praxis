# Quickstart

Get a Praxis instance running and execute a real action against an external vendor in under five minutes. Assumes `go 1.26+`, `docker`, and a Slack workspace you control (the example uses Slack; swap in any registered handler).

## 1. Install

Pick one:

```bash
# From a release binary
curl -fsSL -o praxis.tar.gz \
  https://github.com/felixgeelhaar/praxis/releases/download/v0.2.0/praxis_0.2.0_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz
tar -xzf praxis.tar.gz
sudo install -m 0755 praxis /usr/local/bin/

# Or from source
go install github.com/felixgeelhaar/praxis/cmd/praxis@v0.2.0

# Or via Docker
docker pull ghcr.io/felixgeelhaar/praxis:0.2.0
```

Verify:

```bash
praxis version
```

## 2. Configure

Praxis is configured entirely through environment variables. The minimum for a local single-node deployment:

```bash
export PRAXIS_DB_TYPE=sqlite
export PRAXIS_DB_CONN=$HOME/.praxis/praxis.db
export PRAXIS_API_TOKEN=$(openssl rand -hex 32)
export SLACK_TOKEN=xoxb-...   # bot token from your workspace
mkdir -p "$(dirname "$PRAXIS_DB_CONN")"
```

For production-grade settings (TLS, plugin trust, retention, etc.) see [SECURITY.md](../SECURITY.md).

## 3. Run

```bash
praxis serve
```

The server logs `praxis server listening` once it is ready on `:8080`. `/healthz` and `/metrics` are open by default; everything under `/v1/*` requires the bearer token you exported above.

## 4. Discover capabilities

```bash
curl -s -H "Authorization: Bearer $PRAXIS_API_TOKEN" \
  http://localhost:8080/v1/capabilities | jq '.[].name'
```

You should see `slack_send_message`, `send_email`, `http_request`, `github_create_issue`, `github_add_comment`, `linear_create_issue`, `linear_transition_status`, `calendar_schedule`. The exact list depends on which vendor env vars (`SLACK_TOKEN`, `GITHUB_TOKEN`, `LINEAR_TOKEN`, `SMTP_*`) you set — handlers register themselves on startup.

## 5. Execute an action

Send a Slack message:

```bash
curl -s -X POST -H "Authorization: Bearer $PRAXIS_API_TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8080/v1/actions \
  -d '{
    "id": "act-quickstart-1",
    "capability": "slack_send_message",
    "payload": {"channel": "C0123456789", "text": "Hello from Praxis"},
    "caller": {"type": "user", "id": "you@example.com", "name": "You"},
    "scope": ["send"],
    "idempotency_key": "quickstart-1"
  }' | jq
```

Re-running the same `idempotency_key` returns the same `Action` row instead of double-posting — Praxis is idempotent by design.

## 6. Inspect the audit log

```bash
curl -s -H "Authorization: Bearer $PRAXIS_API_TOKEN" \
  "http://localhost:8080/v1/audit/act-quickstart-1" | jq
```

Every lifecycle event (received → validated → policy_decided → executed → succeeded) is visible. The replay-from-audit canary in CI guarantees that any action's full history can be reconstructed from these events alone.

## 7. Try a dry run

`POST /v1/actions/{id}/dry-run` runs the executor pipeline without invoking the vendor. Useful for previewing the policy decision and the validated payload:

```bash
curl -s -X POST -H "Authorization: Bearer $PRAXIS_API_TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8080/v1/actions/act-quickstart-1/dry-run \
  -d '{}' | jq
```

## 8. Where to next

- **Plugins**: drop a signed `.so` into `PRAXIS_PLUGIN_DIR` (or run with `PRAXIS_PLUGIN_OUT_OF_PROCESS=1` for kernel-enforced sandboxing). See [SECURITY.md §Plugin trust](../SECURITY.md#plugin-trust).
- **MCP federation**: aggregate upstream MCP servers under one Praxis. Point `PRAXIS_MCP_FEDERATION_CONFIG` at a YAML file with `upstreams:`. HTTPS upstreams are supported via `client.HTTPTransport`; pin the trust store with `ca_bundle:`.
- **Tracing**: set `PRAXIS_OTLP_ENDPOINT` to send distributed traces. See [docs/architecture.md](architecture.md).
- **Tenancy**: every action carries an `OrgID` / `TeamID` via `X-Praxis-Org-ID` / `X-Praxis-Team-ID` request headers; capabilities can be tenant-scoped.

For operational concerns (rotation, monitoring, incident response) see [docs/runbook.md](runbook.md).
