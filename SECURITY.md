# Security

## Reporting a vulnerability

Email **felix.geelhaar@gmail.com** with `[PRAXIS SECURITY]` in the subject. Do not open a public issue. Expect an initial response within five business days.

## Threat model

Praxis is the execution layer of the Mnemos / Chronos / Nous / Praxis cognitive stack. It executes named, schema-validated, policy-checked actions against external vendors (Slack, GitHub, Linear, calendar, generic HTTP, plus operator-loaded plugins). The trust boundary depends on the entrypoint:

- **CLI (`praxis run`, `praxis caps`, `praxis log`, `praxis plugins`)**: trusted; the operator runs the binary locally and owns the database file. The CLI talks to the running server via HTTP for plugin admin operations.
- **HTTP API (`praxis serve`)**: every `/v1/*` endpoint requires a Bearer token from `PRAXIS_API_TOKEN`. `/healthz` and `/metrics` are open by default — appropriate for in-cluster scrapers behind a TLS-terminating ingress.
- **MCP server (`praxis mcp`, stdio)**: trusted; agents talking to Praxis over MCP run alongside the binary.
- **Plugins (`PRAXIS_PLUGIN_DIR`)**: untrusted by default. Every plugin must be signed (cosign-blob compatible ECDSA P-256) and verified against the operator's `PRAXIS_PLUGIN_TRUSTED_KEYS` bundle before load. Unsigned plugins fail with `ErrSignatureMissing`; plugins signed by an unknown key fail with `ErrSignatureInvalid`.
- **Federated MCP upstreams (`PRAXIS_MCP_FEDERATION_CONFIG`)**: each upstream's tools register under a `<upstream>__<tool>` namespace and run through the same policy / schema / idempotency / audit pipeline as local handlers. The token in the federation config is forwarded as the Bearer header for HTTP transports.

Production deployments must:

- Run behind TLS at the ingress.
- Configure `PRAXIS_API_TOKEN` with a cryptographically random value (≥ 32 bytes hex).
- Configure `PRAXIS_PLUGIN_TRUSTED_KEYS` with the operator's public-key bundle. Treat the corresponding private keys as the most sensitive material in the deployment — they gate every plugin load.
- Treat the database file (`PRAXIS_DB_CONN`) as PII-bearing — back up, encrypt at rest if the underlying engine supports it, and restrict filesystem access. Audit events stamp tenant identity (`OrgID`, `TeamID`) and policy decisions; retention windows are operator-controlled via `PRAXIS_AUDIT_RETENTION`.
- Run plugins out-of-process when memory enforcement matters (cgroup v2 on Linux). The in-process sandbox enforces CPU timeout and HTTP egress allowlist but cannot enforce `MaxMemoryBytes` because the Go allocator is shared with the host.

## Authentication surfaces

| Surface | Mechanism | Env var | Default behaviour |
|---|---|---|---|
| HTTP `/v1/*` | Bearer token | `PRAXIS_API_TOKEN` | open if unset (local dev only) |
| HTTP `/healthz` + `/metrics` | none | – | open |
| HTTP transport (TLS) | server cert | `PRAXIS_TLS_CERT_FILE` + `PRAXIS_TLS_KEY_FILE` | plain HTTP if unset |
| HTTP transport (mTLS) | client cert verified against CA bundle | `PRAXIS_MTLS_CLIENT_CA_FILE` (requires TLS) | server-only TLS if unset |
| MCP (stdio) | none | – | – |
| Plugin loading | cosign-blob signature verification | `PRAXIS_PLUGIN_TRUSTED_KEYS` | refuses every plugin if unset |
| Federation push (per upstream) | bearer token forwarded to upstream | upstream `token` field in `mcp.federation.yaml` | unauthenticated if unset |

Tenant scoping is **not** an auth boundary — `X-Praxis-Org-ID` and `X-Praxis-Team-ID` are advisory headers used to scope capability discovery and audit reads. The Bearer token is the auth boundary; tenant scoping happens within an authenticated session.

## Plugin trust

Plugin loading is the most security-critical surface. The pipeline is:

1. **Discovery**: `Discover(PRAXIS_PLUGIN_DIR)` scans subdirectories for `manifest.json` declaring `name`, `version`, `abi`, `artifact`. Path traversal in the artifact field (absolute paths, `..` segments) is rejected with `ErrUnsafeArtifact`.
2. **Verification**: `VerifyDiscovered` reads `<artifact>.sig` and verifies the SHA-256 digest of the artefact under any trusted key in the bundle. Empty bundle fails closed (`ErrNoTrustedKeys`).
3. **ABI check**: `Plugin.ABI()` must equal `plugin.ABIVersion`. Mismatches fail with `ABIMismatchError`.
4. **Sandbox** (in-process): every handler is wrapped in `Sandboxed` if the plugin implements `BudgetedPlugin`. CPU timeout enforced via `context.WithTimeout`; egress filtered via `plugin.HTTPClient(ctx)`.
5. **Cgroup** (out-of-process, Linux): `praxis-pluginhost` runs in a per-plugin cgroup v2 directory under `/sys/fs/cgroup/praxis`. `memory.max` and `cpu.max` enforced by the kernel.

Strict mode (`PRAXIS_PLUGIN_STRICT=1`) refuses to start the server when any plugin fails to load. Production deployments should run strict.

## Container

Build with the supplied `Dockerfile`. Run as the unprivileged `praxis` user with a read-only root and a writable volume for the database:

```bash
docker run --read-only \
  -v praxis-data:/var/lib/praxis \
  -v praxis-plugins:/etc/praxis/plugins:ro \
  -v praxis-keys:/etc/praxis/keys:ro \
  -e PRAXIS_DB_CONN=/var/lib/praxis/praxis.db \
  -e PRAXIS_PLUGIN_DIR=/etc/praxis/plugins \
  -e PRAXIS_PLUGIN_TRUSTED_KEYS=/etc/praxis/keys/trusted.pub \
  -e PRAXIS_API_TOKEN=<random-32-bytes-hex> \
  -p 8080:8080 \
  ghcr.io/felixgeelhaar/praxis serve
```

Pin the base image to a digest before deploying to production.

## Dependencies

Direct dependencies tracked in `go.mod`. Refresh:

```bash
go get -u ./...
go mod tidy
make check       # fmt + vet + test + build
make bench-check # perf regression gate
```

Dependabot opens weekly PRs for `gomod` and `github-actions` ecosystems (see `.github/dependabot.yml`).

## Audit log

Every action lifecycle event is appended to `audit_events` with the tenant's `org_id` and `team_id` stamped at write time. The replay-from-audit canary test (`internal/audit/replay_test.go`) runs in CI: every action's full lifecycle must be reconstructible from `audit_events` alone. A failing canary blocks merge.

`PRAXIS_AUDIT_RETENTION` configures per-tenant retention windows (`*=720h,org-x=2160h,org-y=0`); `0` opts a tenant out of purges. The retention scheduler runs once an hour after a five-minute startup delay; cancellation propagates from the bootstrap context.

## Security baseline (`.nox/vex.json`)

`.nox/vex.json` (OpenVEX v0.2.0) carries one statement per [`nox`](https://github.com/nox-hq/nox) v0.7.0 rule category that fires on the praxis source tree. Each statement records `status`, `justification`, and a human-readable `impact_statement` so future maintainers see *why* a rule was accepted, not just that it was. The `security.yml` CI workflow runs a fresh scan on every push and PR and uploads SARIF + SBOM artefacts to GitHub Code Scanning. New rule categories surface as PR annotations.

A standalone `findings.json` is intentionally not committed: nox 0.7.0 has no `--exclude` flag (see [Nox-HQ/nox#38](https://github.com/Nox-HQ/nox/issues/38)) and recursively scans its own JSON output, producing thousands of self-referential matches that drown out the real signal. CI scans regenerate findings each run.

Categories currently waived:

| Rule | Count | Notes |
|---|---:|---|
| `SEC-545` (PagerDuty key heuristic) | 28 | False positives matching `pagerduty_create_incident` test plugin name. |
| `SEC-163` (high-entropy hex) | 25 | Build-metadata strings + go.sum hashes. |
| `DATA-001` (email in code) | 21 | Test fixture contact addresses + `.claude/settings.local.json`. |
| `IAC-013` (action pinned to tag, not SHA) | 14 | Same trade-off mnemos / chronos accept. |
| `SEC-161` (high-entropy assignment) | 10 | Test seed values. |
| `SEC-430` / `SEC-073` / `IAC-254` / `IAC-351` | 9 | postgres test DB creds in CI workflow (ephemeral, not production). |
| `IAC-314` / `IAC-315` (workflow write perms) | 3 | release.yml + bench-publish.yml legitimately need write perms. |
| `CONT-001` (base image not pinned to digest) | 2 | Pin before production deploy. |
| `SEC-437` (Slack token heuristic) | 1 | False positive matching example text in README. |
| `SEC-629` (LOB API key heuristic) | 1 | False positive matching test fixture. |
| `SEC-162` / `DATA-005` | 4 | Roady metadata + docker-compose example IP. |

Refresh locally:

```bash
make nox-scan                # writes .nox/out/ artefacts
git diff .nox/vex.json       # review any vex changes the operator made
```

Each rule category is waived in `.nox/vex.json` (OpenVEX v0.2.0) with explicit `status`, `justification`, and `impact_statement`. The `security.yml` CI workflow diffs new scans against the committed baseline + vex; unbaselined findings fail the build. To accept a new finding, add a statement to `.nox/vex.json` and document the reasoning.

## Known gaps

- mTLS for the MCP surface is operator-provided (mcp-go ships only stdio today; in-process TLS lands once the upstream adds a TCP transport). HTTP API mTLS is in-process via `PRAXIS_TLS_CERT_FILE` / `PRAXIS_TLS_KEY_FILE` / `PRAXIS_MTLS_CLIENT_CA_FILE` (Phase 6).
- The in-process sandbox cannot enforce `MaxMemoryBytes`; the field is reserved and enforced only on the cgroup v2 path.
- Federation upstream URL transport is not implemented yet (mcp-go v1.9 ships only stdio); URL upstreams in the federation config fail with `ErrURLTransportUnsupported`.
- Plugin signatures rely on operator-managed key bundles. Sigstore Fulcio / Rekor integration for keyless verification is on the roadmap.
