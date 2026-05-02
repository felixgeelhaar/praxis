# Praxis — Roadmap

This roadmap maps the [PRD](./PRD.md) phases onto concrete deliverables, milestones, and exit criteria. It is intentionally narrow at the start — Phase 1 ships *one valuable thing*: a tiny, well-shaped execution API plus two real capability handlers.

Praxis is the **execution layer** of the cognitive stack (Mnemos · Chronos · Nous · Praxis). Decisions belong to Nous; Praxis runs what it is told under policy and emits a complete record.

Status legend: ☐ planned · ◑ in progress · ☑ done

---

## Phase 1 — Execution Primitive (MVP)

> Goal: a tiny, well-shaped execution API plus two real capability handlers, dramatically more useful than ad-hoc tool calls.

### Milestones

#### M1.1 — Domain & Storage Foundation
- ☐ Go module scaffold (`praxis/`, internal layout matching Mnemos/Chronos)
- ☐ Domain types in `internal/domain/` per [TDD §3](./TDD.md#3-domain-model-ddd) — `Capability`, `Action`, `Result`, `Simulation`, `PolicyDecision`, `CallerRef`
- ☐ `Action` FSM via [`statekit`](https://github.com/felixgeelhaar/statekit) in `internal/domain/action_fsm.go`
- ☐ Repository ports in `internal/ports/` + shared backend test suite (`internal/store/storetest/`)
- ☐ Memory backend (reference impl, no SQL) — `internal/store/memory/`
- ☐ SQLite backend with `sqlc`-generated queries + migrations — `internal/store/sqlite/`
- ☐ Postgres backend with `sqlc`-generated queries + migrations — `internal/store/postgres/`
- ☐ `sqlc.yaml` configured for both engines (sqlite + postgresql)
- ☐ Backend selection driven by `PRAXIS_DB_TYPE` / `PRAXIS_DB_CONN`

#### M1.2 — Capability Registry & Schema Validation
- ☐ `CapabilityRegistry` with programmatic `Register(handler)`
- ☐ Input/output schema validation (JSON Schema first; Go-struct registration as ergonomic alternative)
- ☐ Capability table-driven tests covering valid, malformed, and unknown payloads

#### M1.3 — Executor (sync)
- ☐ `Executor.Execute` implementing the flow in [TDD §5.2](./TDD.md#52-execute)
- ☐ `IdempotencyKeeper` short-circuit for repeat keys
- ☐ Per-handler timeout + panic recovery in `HandlerRunner`
- ☐ Phase-1 `PolicyEngine` — global allow-list with explainable decisions

#### M1.4 — DryRun
- ☐ `Executor.DryRun` per [TDD §5.3](./TDD.md#53-dryrun)
- ☐ `Capability.Simulatable` honoured
- ☐ Audit row written with `Status = simulated`

#### M1.5 — Audit Log
- ☐ `AuditRepo.Append` for each lifecycle event (received, validated, policy, executed, succeeded/failed, simulated)
- ☐ `praxis log show <action-id>` reconstructs the full action lifecycle from audit alone
- ☐ Search by capability, caller, time window

#### M1.6 — Real Handlers
- ☐ `send_message` — Slack (Web API), forwarding `client_msg_id` for idempotency
- ☐ `send_email` — SMTP, with deduplication via Message-ID
- ☐ Handler-level integration tests against vendor sandboxes (skipped without credentials)

#### M1.7 — Outcome Writeback to Mnemos
- ☐ `OutcomeEmitter` posts a `praxis.action_completed` event to Mnemos after each terminal action
- ☐ Configurable Mnemos endpoint via `PRAXIS_MNEMOS_URL` / `PRAXIS_MNEMOS_TOKEN`
- ☐ Retries via [`fortify/retry`](https://github.com/felixgeelhaar/fortify) — same defaults as Mnemos (5xx + 429 retry, 4xx fail fast, exponential backoff with jitter)
- ☐ Resilient to Mnemos being unavailable (outbox + retry; never blocks `Execute`)

#### M1.8 — HTTP API, CLI & Operability
- ☐ HTTP server via [`axi-go`](https://github.com/felixgeelhaar/axi-go) in `cmd/praxis/axikernel.go` (mirrors Mnemos's `axikernel`)
- ☐ Endpoints: `GET /v1/capabilities`, `POST /v1/actions`, `POST /v1/actions/{id}/dry-run`, `GET /v1/actions/{id}`, `GET /v1/audit`
- ☐ `praxis caps list` / `caps show <name>`
- ☐ `praxis run <capability> --payload <json>` / `--dry-run`
- ☐ `praxis log show <action-id>`
- ☐ Structured logs via [`bolt`](https://github.com/felixgeelhaar/bolt), `/healthz` endpoint, basic Prometheus metrics
- ☐ Public Go client in `client/` using `bolt` + `fortify/retry` (same shape as `Mnemos/client`)

### Phase 1 exit criteria

- 100% of executed actions are reconstructible from the audit log alone (verified by a "replay from audit" test).
- Re-executing the same `Action.ID` produces zero double effects (verified by handler-level idempotency tests).
- Time-to-add-a-new-capability is measured in hours, not days.
- Zero un-validated payloads reach a handler.
- Outcomes appear in Mnemos as events within the configured SLA after each terminal action.

---

## Phase 2 — Policy & Resilience

> Goal: make Praxis safe to point at production. Add policy, async, retries, rate limits, full DryRun parity.

### Themes

- **Policy:** scope-based allow/deny rules, explainable decisions, audit-attached.
- **Async actions:** long-running work without blocking callers.
- **Resilience:** retries, backoff, jitter, rate limits.
- **DryRun parity:** every capability, not just the convenient ones.

### Milestones (sketch)

#### M2.1 — Policy Engine
- ☐ Rule shape: `(capability, caller_type, scope) → allow | deny`
- ☐ `PolicyRepo` storage across all three backends
- ☐ Rule precedence + "first match wins" + "default deny" mode
- ☐ Every `Execute` records the matched rule on the action

#### M2.2 — Async Actions
- ☐ `Action.Mode = sync | async`
- ☐ Job runner with at-least-once execution and idempotency-key dedup
- ☐ Status polling via `praxis status <action-id>`
- ☐ Optional webhook delivery on completion

#### M2.3 — Retry Strategy
- ☐ Per-capability config via [`fortify/retry`](https://github.com/felixgeelhaar/fortify): `max_attempts`, `initial_backoff`, `max_backoff`, `jitter`
- ☐ Per-capability circuit breakers via [`fortify/circuit`](https://github.com/felixgeelhaar/fortify) for vendor outages
- ☐ Failed terminal actions create a child Action with derived ID; audit links parent ↔ child
- ☐ Retryable vs non-retryable error classification

#### M2.4 — Rate Limiting
- ☐ Per-capability and per-caller limits
- ☐ Respect vendor rate limits (parse vendor `Retry-After`)
- ☐ Surfaced in audit as `policy_throttled` events

#### M2.5 — DryRun Parity
- ☐ Every capability ships a `Simulate` implementation
- ☐ Diff test: simulated output matches executed output ≥ 95% of the time on identical inputs

#### M2.6 — More Handlers
- ☐ GitHub: create issue, comment, request review
- ☐ Linear / Jira: create issue, transition status
- ☐ Calendar: schedule meeting, send invite
- ☐ Generic HTTP capability for one-off integrations

### Phase 2 exit criteria

- Zero unauthorized actions in audit (every `Execute` has a recorded policy decision).
- ≥ 99% of transient handler failures recovered by retry.
- Async actions complete and surface their status with no manual polling required.
- Rate limits never produce "double effect" failures.

---

## Phase 3 — Capability Ecosystem

> Goal: open the surface. Third-party plugins, MCP-compatible discovery, multi-tenant policy, exportable audit.

### Themes

- **Plugins:** out-of-tree capability handlers loaded via a stable ABI.
- **MCP surface:** `ListCapabilities` becomes MCP tool discovery; `Execute` becomes MCP tool invocation.
- **Multi-tenant governance:** org / team / user scopes with policy and audit.
- **Reversibility:** per-action `compensate` for capabilities that support it.

### Milestones (sketch)

#### M3.1 — Plugin Architecture
- ☐ Stable handler ABI (Go interface + version contract)
- ☐ Plugin discovery on disk; signature verification
- ☐ Sandbox boundary for resource limits (CPU, memory, network egress)

#### M3.2 — MCP Capability Surface
- ☐ MCP server via [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) exposing `ListCapabilities` as tool discovery
- ☐ `Execute` and `DryRun` as MCP tool calls (same executor as the HTTP surface)
- ☐ Capability descriptors usable by [`agent-go`](https://github.com/felixgeelhaar/agent-go) agents without bespoke glue
- ☐ Audit retained even when invoked through MCP

#### M3.3 — Multi-Tenant Policy
- ☐ Org / team / user scope hierarchy
- ☐ Per-tenant audit retention and access controls
- ☐ Tenant-scoped capability registries (each tenant can register private capabilities)

#### M3.4 — Reversibility
- ☐ `Capability.Compensate` for actions whose effects can be reversed
- ☐ `praxis revert <action-id>` invokes the compensating action under the same audit umbrella
- ☐ Best-effort flag for vendors that don't support exact reversal

#### M3.5 — Compliance & Governance
- ☐ Exportable audit (JSON, CSV) suitable for SOC 2 / HIPAA / GDPR review
- ☐ PII redaction policy in audit detail
- ☐ Org-level dashboards (capability usage, policy denials, error rates)

### Phase 3 exit criteria

- Third-party plugins shipped in production by ≥ 3 organizations.
- Zero policy bypasses in audit.
- Audit export passes a real compliance review.
- Reversible actions can be rolled back from the CLI without a code change.

---

## Phase 4 — Plugin Lifecycle, MCP, and Tenancy

Goal: make plugin operations a first-class runtime concern (not a one-shot startup load) and tighten tenant scoping across CLI + MCP surfaces.

- ☑ Plugin pipeline integrated into `cmd/praxis` startup (`Discover` → `LoadTrustedKeys` → `VerifyDiscovered` → `dlopen` → `plugin.Load`); `PRAXIS_PLUGIN_STRICT=1` aborts startup on any per-plugin error.
- ☑ Audit retention scheduler (`audit.NewScheduler`) ticking every `PRAXIS_AUDIT_RETENTION_INTERVAL` after `PRAXIS_AUDIT_RETENTION_INITIAL_DELAY`, with `praxis_audit_purge_total{org_id,result}` per-tenant counter.
- ☑ MCP + CLI tenant-aware capability discovery (`org_id`/`team_id` flow through `list_capabilities`, executor calls, and `praxis caps list/show --org=<id> --team=<id>`).
- ☑ Out-of-process plugin loader scaffolding (`cmd/praxis-pluginhost` child binary; `internal/plugin/process.go` `ProcessOpener`; line-delimited JSON-RPC IPC; `BudgetedPlugin` enforced by `setrlimit`; crash recovery via `Watchable`).
- ☑ Plugin lifecycle (fsnotify-driven hot reload toggle via `PRAXIS_PLUGIN_AUTORELOAD`; SIGHUP full re-scan; graceful rollover with in-flight drain via `versionedHandler`; `praxis plugins list / reload <name>` CLI subcommand backed by `GET /v1/plugins` and `POST /v1/plugins/{name}/reload`).

### Phase 4 exit criteria

- Plugin reload (hot or SIGHUP) never drops an in-flight call.
- A plugin crash never leaves the runtime registry inconsistent with the loaded set.
- Tenant scoping is consistent across HTTP, MCP, and CLI; cross-tenant reads return `ErrCrossTenantAccess`.

---

## Phase 5 — Observability, Schema Versioning, and Federation

Goal: production-grade observability + capability evolution + multi-server reach.

- ☑ OpenTelemetry tracing (OTLP/gRPC + OTLP/HTTP exporters via `PRAXIS_OTLP_ENDPOINT` / `_PROTOCOL` / `_INSECURE` / `PRAXIS_TRACE_SAMPLE`; `executor.Execute / DryRun / Resume / Revert` open root spans; `handler.<capability>` child spans capture vendor latency; sandbox HTTP client wraps `otelhttp.NewTransport` so outbound calls carry W3C `traceparent`; per-tool spans on the MCP surface).
- ☑ Capability schema versioning (`InputSchemaVersion` / `OutputSchemaVersion` defaulted `"1"`; migration 004 on sqlite + postgres; `internal/schema.CheckCompat` detects breaking changes; `off` / `warn` / `strict` modes via `PRAXIS_SCHEMA_COMPAT`; `GET /v1/capabilities/{name}/changelog` renders breaking-change history).
- ☑ Performance benchmarks + regression gate (`make bench` / `make bench-check`; `cmd/benchcheck` + `bench/baseline.txt` fail on >1.20× regressions; `cmd/benchdocs` renders `docs/benchmarks.md`; tag-triggered `bench-publish.yml` opens a snapshot PR per release).
- ☑ cgroup v2 detection on Linux (`internal/plugin/cgroup`) — kernel-enforced `memory.max` + `cpu.max` via per-plugin cgroups, with `praxis_plugin_memory_peak_bytes` + `praxis_plugin_cpu_seconds_total` surfaced. macOS / non-Linux paths fall through to setrlimit.
- ☑ Federated MCP (aggregate upstream MCP servers as Praxis capabilities; `mcp.federation.yaml` declares stdio-transport upstreams with optional `Token` + `Allow`; tools register under `<upstream>__<tool>`; `Supervisor` reconnect with exponential backoff; `praxis_mcp_federation_status{upstream,status}` gauge).

### Phase 5 exit criteria

- Distributed traces stitch executor → handler → outbound vendor across Praxis + Mnemos.
- A breaking schema change is rejected before the new capability replaces the old one in strict mode.
- Bench-check catches a >20% regression in CI before it reaches main.

---

## Phase 6 — Production Hardening + Supply-Chain Security

Goal: lock down the trust boundaries (HTTP API, plugin loading, MCP federation) and make the security baseline auditable, not advisory.

- ☑ HTTP API TLS + mTLS (`PRAXIS_TLS_CERT_FILE` / `PRAXIS_TLS_KEY_FILE`; `PRAXIS_MTLS_CLIENT_CA_FILE` requires TLS); `tlsLoader` swaps the active cert via `atomic.Pointer` on SIGHUP for zero-downtime rotation.
- ☑ cgroup v2 spawn (per-plugin cgroup created before fork/exec, child PID attached after `Start`, directory reclaimed on kill).
- ☑ Out-of-process plugin loader as a config flag (`PRAXIS_PLUGIN_OUT_OF_PROCESS=1` + `PRAXIS_PLUGINHOST_BINARY`); in-process `DefaultOpener` remains the default.
- ☑ Persistent capability change history (`capability_history` migration 005 on sqlite + postgres; `domain.CapabilityHistoryEntry` + `ports.CapabilityHistoryRepo` + adapters across all three backends; `Registry.SetHistoryRepo` mirrors every breaking-change entry; changelog endpoint reads through the repo).
- ☑ Sigstore Fulcio keyless plugin verification (`internal/plugin.KeylessVerifier` with identity-bound `(SubjectGlob, Issuer)` policy; `PRAXIS_PLUGIN_FULCIO_ROOTS` / `_SUBJECTS` / `_ISSUER`; pipeline dispatches between PEM-key and keyless per plugin; stdlib-only).
- ☑ MCP federation HTTP transport (mcp-go v1.10.0 `client.HTTPTransport`; `Upstream.ca_bundle` pins the trust store, `insecure_skip_verify` for dev, `token` forwarded as Bearer).
- ☑ Security baseline + CI gate (`.nox/vex.json` carries one OpenVEX statement per firing rule; `security.yml` hard-fails on unbaselined finding categories; SARIF + SBOM uploaded unconditionally).

### Phase 6 exit criteria

- HTTP API speaks TLS / mTLS in every production deployment; cert rotation is operational, not a redeploy.
- Plugin loading verifies signatures against either an operator's PEM bundle or a Fulcio identity policy, never an empty trust store.
- Every nox finding category is either fixed or carries a VEX statement with status + justification + impact, enforced at PR time.

---

## Cross-cutting (every phase)

- **Schema discipline:** every capability has a versioned input/output schema.
- **Test discipline:** every change touching the executor flow ships with a unit test that exercises the *exact* path.
- **Multi-backend parity:** schema and behaviour changes go to all three backends in the same commit.
- **Observability first:** every component emits structured logs and metrics from day one.
- **Local-first defaults:** runs on a developer laptop with SQLite, no SaaS required.

---

## What we are *not* doing (yet)

- A workflow engine (control flow, conditionals, loops). Praxis runs single actions.
- A scheduler / cadence runner.
- A decision engine — that's Nous.
- A pattern detector — that's Chronos.
- A memory store — that's Mnemos.
- An agent framework.

---

## Open questions blocking the roadmap

These are tracked in [PRD §9](./PRD.md#9-open-questions). Each must have a decision before the milestone that depends on it ships.
