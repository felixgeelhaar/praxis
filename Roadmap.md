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
