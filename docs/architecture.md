# Architecture

This document describes how Praxis is composed at runtime: the executor flow, internal services, and the cognitive-stack loop in which Praxis sits at exactly one position.

For the canonical domain model, contracts, and storage, see [TDD.md](../TDD.md).

---

## 1. Praxis in the Cognitive Stack

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   Mnemos    │    │   Chronos   │    │    Nous     │    │   Praxis    │
│  (memory)   │ ─► │  (signals)  │ ─► │ (decisions) │ ─► │ (execution) │
│             │    │             │    │             │    │             │
│ • events    │    │ • patterns  │    │ • goals     │    │ • capabili- │
│ • memories  │    │ • anomalies │    │ • tasks     │    │   ties      │
│ • entities  │    │ • trends    │    │ • plans     │    │ • actions   │
└─────────────┘    └─────────────┘    └─────────────┘    └─────┬───────┘
       ▲                                                       │
       │                                                       ▼
       │                                              capability handlers
       │                                              (slack, email, github,
       │                                              calendar, tickets, ...)
       │                                                       │
       │                outcomes (writeback)                   │
       └───────────────────────────────────────────────────────┘
```

Praxis is downstream of Nous and upstream of the outside world. Outcomes flow back to Mnemos so the loop closes.

---

## 2. Executor Flow

Praxis is, at the core, a small linear pipeline. Every step is testable in isolation.

### 2.1 `Execute`

```
1. receive Action
2. resolve Capability                    → reject "unknown_capability" on miss
3. validate Payload vs InputSchema       → reject "validation_failed" on miss
4. evaluate Policy                       → record PolicyDecision; deny → reject
5. check IdempotencyKey
     - terminal Result exists?           → return it (no double effect)
     - in-flight Action exists?          → return current Status
6. transition Action to "executing"; persist
7. invoke handler via HandlerRunner       (timeouts + panic recovery)
8. validate Output vs OutputSchema
9. transition to "succeeded" / "failed"; persist Result
10. append AuditEvent("executed" / "failed")
11. emit OutcomeEvent to Mnemos
12. return Result
```

### 2.2 `DryRun`

```
1. receive Action
2. resolve Capability                    → reject on miss
3. validate Payload vs InputSchema
4. evaluate Policy                       → record PolicyDecision
5. if Capability.Simulatable:
     handler.Simulate(payload) → preview
   else:
     return best-effort preview          (validation + policy only)
6. persist audit row with Status = simulated
7. return Simulation
```

Two flows; same skeleton; one set of guarantees.

---

## 3. Service Topology

```
                ┌───────────────────────┐
   Nous ──►     │      PraxisAPI         │  (the only public surface)
                └────────────┬───────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │         Executor              │
              │                               │
              │  CapabilityRegistry  ──────► resolve
              │  SchemaValidator     ──────► validate
              │  PolicyEngine        ──────► allow / deny
              │  IdempotencyKeeper   ──────► dedup
              │  HandlerRunner       ──────► invoke handler
              │  AuditLog            ──────► append events
              │  OutcomeEmitter      ──────► → Mnemos
              └──────────────┬───────────────┘
                             │
                             ▼
                    capability handlers
              (slack, email, github, ...)
```

Each box is a small, focused service. They communicate through the domain types in [TDD §3](../TDD.md#3-domain-model-ddd) — no shared mutable state.

---

## 4. Internal Services

### 4.1 CapabilityRegistry

- In-memory catalog populated at startup.
- `Register(handler)` declares the `Capability` and the function that runs it.
- `List` and `Get` are pure reads, safe for concurrent use.
- Phase 3 adds dynamic plugin loading; Phase 1 is startup-only.

### 4.2 SchemaValidator

- Validates `Action.Payload` against `Capability.InputSchema` before any side effect.
- Validates handler `Output` against `OutputSchema` after execution — a malformed result is a hard failure.
- Phase 1: JSON Schema first; Go-struct registration as ergonomic alternative.

### 4.3 PolicyEngine

- Receives `(Capability, Action, Caller)` and returns a `PolicyDecision`.
- Phase 1: global allow-list; every decision is still recorded explicitly.
- Phase 2: scoped rules `(capability, caller_type, scope) → allow | deny` with first-match-wins and a `default deny` switch.

### 4.4 IdempotencyKeeper

- Maps `IdempotencyKey → Result` after termination.
- Maps `IdempotencyKey → Status` while in flight.
- Backed by the same multi-backend store as everything else; TTL is configurable.

### 4.5 Executor

- Pure orchestrator. Owns no I/O of its own.
- Implements both `Execute` and `DryRun` flows above.
- Status transitions go through the `statekit` FSM in `internal/domain/action_fsm.go` — never free-form string assignment.
- Every branch corresponds to a named test in `internal/executor/executor_test.go`.

### 4.6 HandlerRunner

- Runs a handler with a per-capability timeout, panic recovery, and structured error normalization.
- Distinguishes retryable from non-retryable errors (Phase 2 retry policy depends on this).
- Forwards vendor idempotency keys when the capability declares `Idempotent = true`.

### 4.7 AuditLog

- Append-only stream of `AuditEvent { action_id, kind, detail, created_at }`.
- Kinds: `received`, `validated`, `policy`, `executed`, `succeeded`, `failed`, `simulated`.
- Reconstructible by design: a single action's full lifecycle is queryable from this table alone.

### 4.8 OutcomeEmitter

- After every terminal `Action`, posts a `praxis.action_completed` event to Mnemos.
- Retries via `fortify/retry` with the project default (5xx + 429 retry, 4xx fail fast, exponential backoff with jitter).
- Resilient: outbox + retries when Mnemos is unavailable; never blocks `Execute`.
- See [docs/integrations.md](./integrations.md#5-praxis--mnemos-outcome-writeback).

---

## 5. Public API as a Service Boundary

The public API is the *only* way callers (Nous, agents, humans, schedulers) interact with Praxis:

```go
type PraxisAPI interface {
    ListCapabilities(ctx context.Context) ([]Capability, error)
    Execute(ctx context.Context, action Action) (Result, error)
    DryRun(ctx context.Context, action Action) (Simulation, error)
}
```

Three surfaces, one executor underneath:

- **Go interface** (in-process) — `internal/executor` directly.
- **HTTP** — `cmd/praxis/axikernel.go` wires routes via `axi-go`, mirroring Mnemos's `axikernel` pattern.
- **MCP** (Phase 3) — `mcp-go` exposes the same capabilities as MCP tools.

The wire protocol is intentionally narrow; advanced features (async, retries, policy) all live behind the same three verbs.

---

## 6. Failure Modes

Praxis fails loudly. Silence is reserved for "everything is fine."

| Failure | Behaviour |
|---|---|
| Unknown capability | Action rejected with `unknown_capability`; audit row written. |
| Schema validation failure | Action rejected with `validation_failed`; audit row written. |
| Policy denied | Action rejected with `policy_denied`; audit row records the matched rule. |
| Handler panic | Recovered by `HandlerRunner`; action marked `failed` with `Code = handler_error`; audit row preserves stack. |
| Handler timeout | Action marked `failed` with `Code = timeout`; idempotency key released after a grace period. |
| Mnemos unreachable | Outcome queued; `Execute` does not block. Operator alerted via metrics. |
| Storage unavailable | Pipeline halts; CLI exits non-zero. No silent fallbacks. |

---

## 7. Operability

Every service emits:

- **Structured logs** (key/value, no string interpolation).
- **Metrics** with low cardinality (per-capability, not per-action).
- **Traces** for `Execute` / `DryRun` from receive → terminal.

Standard metrics (sketch):

- `praxis_actions_total{capability, status}`
- `praxis_action_duration_seconds{capability}`
- `praxis_policy_decisions_total{decision}`
- `praxis_idempotency_hits_total`
- `praxis_outcome_emit_failures_total`

---

## 8. Configuration

Every knob is an environment variable. Naming: `PRAXIS_<AREA>_<KEY>`.

| Var | Default | Purpose |
|---|---|---|
| `PRAXIS_DB_TYPE` | `sqlite` | Storage backend: `memory`, `sqlite`, or `postgres` |
| `PRAXIS_DB_CONN` | `praxis.db` | Connection string / file path (ignored for `memory`) |
| `PRAXIS_HTTP_PORT` | `7779` | HTTP API port |
| `PRAXIS_HTTP_HOST` | `127.0.0.1` | HTTP API host |
| `PRAXIS_MNEMOS_URL` | — | Mnemos endpoint for outcome writeback |
| `PRAXIS_MNEMOS_TOKEN` | — | Mnemos auth token (optional) |
| `PRAXIS_HANDLER_TIMEOUT` | `30s` | Per-handler timeout default |
| `PRAXIS_IDEMPOTENCY_TTL` | `24h` | How long an idempotency key is honoured |
| `PRAXIS_POLICY_MODE` | `allow` | `allow` (Phase 1) / `enforce` (Phase 2+) |

Phase-1 ships these. Later vars (retries, async, policy rule paths) land with the phases that need them.

---

## 9. Foundation Libraries

Praxis uses the same `felixgeelhaar/*` library family as Mnemos so the entire cognitive stack shares one operational vocabulary:

| Library | Where it shows up here |
|---|---|
| `bolt` | All structured logging (every service constructor takes a `*bolt.Logger`) |
| `fortify/retry` | `OutcomeEmitter`, public Go client, vendor-handler retry wrapper (Phase 2) |
| `fortify/circuit` | Vendor-handler circuit breakers (Phase 2) |
| `statekit` | `Action` lifecycle FSM in `internal/domain/action_fsm.go` |
| `axi-go` | HTTP server (`cmd/praxis/axikernel.go`) |
| `mcp-go` | MCP capability surface (Phase 3) |
| `agent-go` | Capability descriptors consumable by `agent-go` agents |
| `sqlc` | Generated query layer for SQLite + Postgres backends |

See [TDD §9.7](../TDD.md#97-foundation-libraries) for the rule set.

---

## 10. Extension Points

Praxis is designed to be extended without touching the executor:

- **New capability:** add a handler under `internal/handlers/<vendor>/`, register it in `internal/capability/registry.go`.
- **New schema validator feature:** extend `internal/schema/`.
- **New policy rule type:** extend `internal/policy/` (Phase 2+).
- **New audit query:** add to `sql/<dialect>/queries.sql`, regenerate `sqlc`.

Every extension point has tests and a registration site that keeps surface area discoverable.

---

## 11. Out of Scope (here)

- The wire shape of every Mnemos endpoint — see [docs/integrations.md](./integrations.md).
- Phase-2+ policy engine details — captured in [Roadmap §Phase 2](../Roadmap.md#phase-2--policy--resilience).
- Plugin ABI specifics — Phase 3 concern.
- The Nous decision pipeline — that's a different repository.
