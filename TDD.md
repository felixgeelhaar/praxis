# Praxis — Technical Design Document (TDD)

This document specifies the domain model, services, contracts, storage, and core flows of Praxis. It is the authoritative reference for what Praxis *is* at the code level.

> **Scope reminder.** Praxis is the **execution layer** of the cognitive stack (Mnemos · Chronos · Nous · Praxis). It does not decide what to do; it executes what it is told under policy and emits a complete record. Decision logic lives in Nous.

---

## 1. Purpose

Praxis is the system that:

> Exposes a registry of capabilities, executes actions against them under policy, and records every outcome.

It is responsible for:

- naming **capabilities** (tools, APIs, integrations)
- validating action input/output against schemas
- enforcing **policy** before any side effect
- providing **idempotency**, retries, and **dry-run**
- persisting a complete **audit trail** of inputs, outputs, and external identifiers
- emitting outcomes back to **Mnemos** so the loop closes

## 2. Core Responsibilities

| Responsibility | Description |
|---|---|
| **Capability registry** | The catalog of named, schema'd, permissioned units of effect. |
| **Policy enforcement** | Allow/deny each `Execute` against a configured ruleset. |
| **Action execution** | Run a capability handler with validated input. |
| **Idempotency** | Re-executing the same action ID never produces double effects. |
| **Dry-run** | Simulate without side effect, returning a faithful preview. |
| **Audit** | Persist every action's full lifecycle, evidence, and policy decision. |
| **Outcome writeback** | Send completion events back into Mnemos. |

---

## 3. Domain Model (DDD)

### 3.1 Aggregate: `Capability`

```go
type Capability struct {
    Name         string         // stable identifier, e.g. "send_message"
    Description  string         // human-readable
    InputSchema  any            // schema for the payload (JSON Schema or Go struct registration)
    OutputSchema any            // schema for the result
    Permissions  []string       // scopes required to invoke
    Simulatable  bool           // can DryRun produce a faithful preview?
    Idempotent   bool           // is the underlying side effect idempotent on the destination?
}
```

A `Capability` is a *contract* — not the handler that runs it. Handlers register *for* a capability name at startup.

### 3.2 Aggregate: `Action`

```go
type Action struct {
    ID              string                 // stable, caller-supplied or generated; the idempotency key
    Capability      string                 // matches Capability.Name
    Payload         map[string]any
    Caller          CallerRef              // who is invoking (Nous, agent, user)
    Scope           []string               // permission scopes presented by the caller
    IdempotencyKey  string                 // defaults to Action.ID
    Status          ActionStatus
    Result          map[string]any
    Error           *ActionError
    PolicyDecision  *PolicyDecision        // populated before any execution
    ExecutedAt      *time.Time
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

#### `ActionStatus` (state machine)

```go
type ActionStatus string

const (
    StatusPending   ActionStatus = "pending"     // received, not yet evaluated
    StatusValidated ActionStatus = "validated"   // schema + policy passed
    StatusExecuting ActionStatus = "executing"   // handler running
    StatusSucceeded ActionStatus = "succeeded"
    StatusFailed    ActionStatus = "failed"
    StatusSimulated ActionStatus = "simulated"   // dry-run terminal state
    StatusRejected  ActionStatus = "rejected"    // policy denied
)
```

Allowed transitions:

```
pending   → validated, rejected, simulated
validated → executing, simulated
executing → succeeded, failed
succeeded → (terminal)
failed    → (terminal; retries create a *new* Action with a derived ID)
rejected  → (terminal)
simulated → (terminal)
```

### 3.3 Value Object: `Result`

```go
type Result struct {
    ActionID    string
    Status      ActionStatus
    Output      map[string]any
    ExternalID  string                 // vendor-side handle, e.g. Slack message ts
    StartedAt   time.Time
    CompletedAt time.Time
    Attempts    int
    Error       *ActionError
}
```

`Result` is what the caller of `Execute` receives. It is also persisted in the audit log.

### 3.4 Value Object: `Simulation`

```go
type Simulation struct {
    ActionID       string
    PolicyDecision PolicyDecision
    Validation     ValidationReport
    Preview        map[string]any   // what the handler would do/send
    Reversible     bool
}
```

`DryRun` returns a `Simulation`. No state is persisted to the destination, but a row is written to the audit log with `Status = simulated` for traceability.

### 3.5 Entity: `PolicyDecision`

```go
type PolicyDecision struct {
    Decision     string    // "allow" | "deny"
    RuleID       string    // which rule matched
    Reason       string    // explainable
    EvaluatedAt  time.Time
}
```

Every `Execute` and `DryRun` records a `PolicyDecision`. Phase 1 ships a global allow-list; Phase 2 ships scope-based rules.

### 3.6 Value Object: `CallerRef`

```go
type CallerRef struct {
    Type string  // "nous" | "agent" | "user" | "scheduler"
    ID   string
    Name string
}
```

The caller is recorded on every action so the audit trail answers *"who asked for this?"* without ambiguity.

### 3.7 Value Object: `ActionError`

```go
type ActionError struct {
    Code     string  // "validation_failed" | "policy_denied" | "handler_error" | "timeout"
    Message  string
    Vendor   map[string]any  // raw error from the destination, when applicable
    Retryable bool
}
```

---

## 4. The Public API

```go
type PraxisAPI interface {
    ListCapabilities(ctx context.Context) ([]Capability, error)
    Execute(ctx context.Context, action Action) (Result, error)
    DryRun(ctx context.Context, action Action) (Simulation, error)
}
```

Three verbs. Anything else lives in another layer.

---

## 5. Core Flows

### 5.1 Capability Registration

At startup, each handler registers itself against a capability name:

```go
registry.Register(slack.NewSendMessageHandler(slackClient))
registry.Register(email.NewSendEmailHandler(smtp))
registry.Register(github.NewCreateIssueHandler(ghClient))
```

The registry is the source of truth for `ListCapabilities`. Handlers may not be hot-swapped during a single action; reloading is a startup-time operation in Phase 1, dynamic in Phase 3.

### 5.2 Execute

```
1. receive Action
2. resolve Capability by name (else: reject "unknown_capability")
3. validate Payload against InputSchema (else: reject "validation_failed")
4. evaluate Policy → PolicyDecision (deny → reject)
5. check IdempotencyKey:
     - if a terminal Result exists → return it (no double effect)
     - if an in-flight Action exists → return current Status
6. transition Action to "executing", persist
7. invoke handler
8. validate Output against OutputSchema
9. transition to "succeeded" / "failed", persist Result
10. write audit row
11. emit outcome event to Mnemos
12. return Result
```

The flow is intentionally linear and testable. Every step has its own unit test.

### 5.3 DryRun

```
1. receive Action
2. resolve Capability (else: reject)
3. validate Payload against InputSchema
4. evaluate Policy → PolicyDecision
5. if Capability.Simulatable:
     invoke handler.Simulate(payload) → preview
6. else:
     return a best-effort preview limited to validation + policy
7. persist audit row with Status = simulated
8. return Simulation
```

`DryRun` never invokes the destination's mutating endpoints.

### 5.4 Idempotency

Every `Action.ID` is also the default `IdempotencyKey`. The `IdempotencyKeeper` stores `(key → final Result)` once an action terminates. Re-executing the same key returns the stored Result without invoking the handler again.

Handlers that talk to vendors with their own idempotency mechanisms (Slack `client_msg_id`, Stripe `Idempotency-Key`) MUST forward the key. The `Idempotent` flag on `Capability` declares this contract.

### 5.5 Outcome Writeback

After every terminal `Action`, Praxis appends an event to Mnemos:

```json
{
  "type":         "praxis.action_completed",
  "action_id":    "...",
  "capability":   "send_message",
  "caller":       { "type": "nous", "id": "..." },
  "status":       "succeeded",
  "external_id":  "vendor-handle",
  "timestamp":    "2026-04-27T12:34:56Z"
}
```

Mnemos treats it as a normal event and surfaces it in future recalls — so the next decision can see what was done.

### 5.6 Failure & Retries

In Phase 1, retries are the caller's responsibility (using the same `IdempotencyKey` is safe). In Phase 2, Praxis owns retries with per-capability backoff/jitter and a `max_attempts` setting.

A failed terminal action is never silently retried. Retries are *new actions* whose IDs derive from the original (e.g. `<orig>-r1`) and whose audit rows reference the parent.

---

## 6. Internal Services

| Service | Responsibility |
|---|---|
| **CapabilityRegistry** | In-memory catalog of registered capabilities + handlers. |
| **SchemaValidator** | Validate `Payload` and `Output` against capability schemas. |
| **PolicyEngine** | Evaluate a request against the configured ruleset. |
| **IdempotencyKeeper** | Persist `(key → Result)` and short-circuit duplicates. |
| **Executor** | Orchestrate the flow in §5.2 / §5.3. |
| **HandlerRunner** | Invoke a registered handler with timeouts and panic recovery. |
| **AuditLog** | Append-only record of every action lifecycle event. |
| **OutcomeEmitter** | Push outcome events to Mnemos. |

Each service is independently testable. Cross-service communication goes through the domain types in §3 — no shared mutable state.

---

## 7. Storage (multi-backend, `sqlc`-typed)

Praxis is **storage-agnostic at the domain layer** and ships three backends, mirroring the Chronos pattern:

| Backend | Use case | Config |
|---|---|---|
| `memory` | Tests, ephemeral runs, examples | `PRAXIS_DB_TYPE=memory` |
| `sqlite` | Local-first / single-user / embedded (default) | `PRAXIS_DB_TYPE=sqlite`, default `praxis.db` |
| `postgres` | Production / team / multi-tenant | `PRAXIS_DB_TYPE=postgres` |

Domain code talks only to repository **ports** in `internal/ports/`. Each backend lives in `internal/store/<backend>/` and is selected at startup.

### 7.1 Repository ports

```go
// internal/ports/repo.go
type CapabilityRepo interface {
    Upsert(ctx context.Context, c domain.Capability) error
    Get(ctx context.Context, name string) (domain.Capability, error)
    List(ctx context.Context) ([]domain.Capability, error)
}

type ActionRepo interface {
    Save(ctx context.Context, a domain.Action) error
    Get(ctx context.Context, id string) (domain.Action, error)
    UpdateStatus(ctx context.Context, id string, s domain.ActionStatus) error
    PutResult(ctx context.Context, id string, r domain.Result) error
}

type IdempotencyRepo interface {
    Lookup(ctx context.Context, key string) (*domain.Result, error)
    Remember(ctx context.Context, key string, r domain.Result, ttl time.Duration) error
}

type AuditRepo interface {
    Append(ctx context.Context, e domain.AuditEvent) error
    ListForAction(ctx context.Context, actionID string) ([]domain.AuditEvent, error)
    Search(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error)
}

type PolicyRepo interface { // Phase 2+
    ListRules(ctx context.Context) ([]domain.PolicyRule, error)
    UpsertRule(ctx context.Context, r domain.PolicyRule) error
    DeleteRule(ctx context.Context, id string) error
}
```

### 7.2 `sqlc` configuration

`sqlc.yaml` defines two engines, one per relational backend:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/sqlite/queries.sql"
    schema:  "internal/store/sqlite/migrations/"
    gen:
      go:
        package: "sqlcgen"
        out:     "internal/store/sqlite/sqlcgen"
        sql_package: "database/sql"
        emit_interface: true
        emit_json_tags: true
  - engine: "postgresql"
    queries: "sql/postgres/queries.sql"
    schema:  "internal/store/postgres/migrations/"
    gen:
      go:
        package: "sqlcgen"
        out:     "internal/store/postgres/sqlcgen"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_json_tags: true
```

The `memory` backend is hand-written and is the canonical reference — the SQL backends must round-trip every test the memory backend passes.

### 7.3 Schema — Postgres

```sql
create table capabilities (
  name          text primary key,
  description   text,
  input_schema  jsonb not null,
  output_schema jsonb not null,
  permissions   text[] not null default '{}',
  simulatable   boolean not null default false,
  idempotent    boolean not null default false,
  registered_at timestamptz not null
);

create table actions (
  id              text primary key,
  capability      text not null references capabilities(name),
  payload         jsonb not null,
  caller_type     text not null,
  caller_id       text not null,
  scope           text[] not null default '{}',
  idempotency_key text not null,
  status          text not null,
  result          jsonb,
  error           jsonb,
  policy_decision jsonb,
  executed_at     timestamptz,
  created_at      timestamptz not null,
  updated_at      timestamptz not null
);

create unique index idx_actions_idempotency on actions (idempotency_key);
create index        idx_actions_capability  on actions (capability, created_at desc);
create index        idx_actions_caller      on actions (caller_type, caller_id, created_at desc);

create table audit_events (
  id          uuid primary key,
  action_id   text not null references actions(id) on delete cascade,
  kind        text not null,            -- "received", "validated", "policy", "executed", "failed", "simulated"
  detail      jsonb not null,
  created_at  timestamptz not null
);

create index idx_audit_action on audit_events (action_id, created_at);
create index idx_audit_kind   on audit_events (kind, created_at desc);

create table policy_rules ( -- Phase 2+
  id          text primary key,
  capability  text,                     -- nullable: applies to all when null
  caller_type text,
  scope       text[],
  decision    text not null,            -- "allow" | "deny"
  reason      text,
  created_at  timestamptz not null,
  updated_at  timestamptz not null
);
```

### 7.4 Schema — SQLite

The same logical schema, adapted to SQLite types:

```sql
create table capabilities (
  name          text primary key,
  description   text,
  input_schema  text not null,          -- JSON
  output_schema text not null,          -- JSON
  permissions   text not null default '[]',  -- JSON array
  simulatable   integer not null default 0,
  idempotent    integer not null default 0,
  registered_at text not null
);

create table actions (
  id              text primary key,
  capability      text not null references capabilities(name),
  payload         text not null,        -- JSON
  caller_type     text not null,
  caller_id       text not null,
  scope           text not null default '[]',  -- JSON array
  idempotency_key text not null,
  status          text not null,
  result          text,                 -- JSON
  error           text,                 -- JSON
  policy_decision text,                 -- JSON
  executed_at     text,
  created_at      text not null,
  updated_at      text not null
);

create unique index idx_actions_idempotency on actions (idempotency_key);
create index        idx_actions_capability  on actions (capability, created_at desc);
create index        idx_actions_caller      on actions (caller_type, caller_id, created_at desc);

create table audit_events (
  id         text primary key,           -- uuid as text
  action_id  text not null references actions(id) on delete cascade,
  kind       text not null,
  detail     text not null,              -- JSON
  created_at text not null
);

create index idx_audit_action on audit_events (action_id, created_at);
create index idx_audit_kind   on audit_events (kind, created_at desc);

create table policy_rules (              -- Phase 2+
  id          text primary key,
  capability  text,
  caller_type text,
  scope       text not null default '[]',
  decision    text not null,
  reason      text,
  created_at  text not null,
  updated_at  text not null
);
```

### 7.5 Memory backend

Pure in-process implementation in `internal/store/memory/`. Used by:

- unit tests of the executor and engines
- examples in docs
- the `PRAXIS_DB_TYPE=memory` runtime mode (ephemeral)

It is the canonical reference for the repository contract; the integration tests in `internal/store/sqlite/` and `internal/store/postgres/` re-run the same shared test suite to guarantee parity.

### 7.6 Selection at runtime

```go
// internal/store/store.go
func Open(ctx context.Context, cfg config.DB) (Repos, error) {
    switch cfg.Type {
    case "memory":
        return memory.New(), nil
    case "sqlite":
        return sqlite.Open(ctx, cfg.Conn)
    case "postgres":
        return postgres.Open(ctx, cfg.Conn)
    default:
        return nil, fmt.Errorf("unknown db type: %q", cfg.Type)
    }
}
```

Domain code never imports a concrete backend — only the repository ports.

---

## 8. The System Loop (cognitive stack)

Praxis sits at exactly one position in the four-layer loop:

```
1. observe         → Mnemos  (events, memories)
2. detect          → Chronos (signals, anomalies)
3. decide          → Nous    (decisions, action requests)
4. act             → Praxis  (Execute / DryRun)
5. learn           → Mnemos  (outcome events from Praxis)
6. loop
```

Each step is observable and replayable. The loop closes only when an outcome is written back to Mnemos.

---

## 9. Design Principles

### 9.1 Single responsibility

```
Mnemos  = memory
Chronos = patterns
Nous    = decisions
Praxis  = execution
```

Praxis does not interpret signals, evaluate risk, choose what to do, or learn what worked. Those belong upstream.

### 9.2 Capabilities, not endpoints

Each capability is a named, schema'd, permissioned unit of side effect. Vendor specifics live in handlers, never in the domain.

### 9.3 Policy by default

Every `Execute` produces a `PolicyDecision` — even if the Phase-1 default is a global allow. The decision is on file, always.

### 9.4 Idempotency is non-negotiable

Stable `Action.ID`. Forwarded vendor idempotency keys. Re-executing the same action ID never produces double effects.

### 9.5 Dry-run is first-class

`DryRun` parity with `Execute` for every simulatable capability.

### 9.6 Evidence everywhere

Every action persists inputs, outputs, policy decision, vendor identifiers, and timestamps. The audit trail *is* the product.

---

## 9.7 Foundation Libraries

Praxis stands on the same `felixgeelhaar/*` library set as Mnemos so that every system in the cognitive stack shares one operational vocabulary. **Don't roll your own** when one of these covers the use case.

| Library | Role in Praxis | Where it appears |
|---|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | Structured logging | Every service (`internal/...`), CLI, HTTP server. Never `log.Println`, never raw `slog`. |
| [`fortify/retry`](https://github.com/felixgeelhaar/fortify) | Retry with backoff + jitter | `OutcomeEmitter` (Mnemos writeback), HTTP client in `client/`, retryable-handler wrapper (Phase 2) |
| [`fortify/circuit`](https://github.com/felixgeelhaar/fortify) | Circuit breakers | Per-vendor handler protection (Phase 2) |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | State machines | `Action` lifecycle (`pending → validated → executing → succeeded/failed`), capability registration FSM |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | HTTP framework | `praxis serve` — the public HTTP surface; mirrors Mnemos's `axikernel` pattern in `cmd/praxis/axikernel.go` |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | MCP server | Phase 3: `ListCapabilities` exposed as MCP tool discovery; `Execute` / `DryRun` as MCP tool calls |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | Agent framework | Capability descriptors consumable by `agent-go` agents; agents call Praxis via the public API |
| [`sqlc`](https://sqlc.dev) | Typed SQL | All Postgres + SQLite queries (see §7) |

### 9.7.1 Concretely

- **Logging** — every service receives a `*bolt.Logger` via constructor injection. No package-level `log.Default()`.
- **State machines** — `internal/domain/action_fsm.go` defines the `Action` FSM using `statekit`; transitions in §3.2 are not free-form, they go through the FSM.
- **HTTP** — `cmd/praxis/axikernel.go` wires routes, middleware, and error mapping using `axi-go`, mirroring `Mnemos/cmd/mnemos/axikernel.go`.
- **Retries** — `fortify/retry.Config` is the only retry primitive. Same defaults as Mnemos: 5xx + 429 retry, 4xx fail fast, exponential backoff with jitter.
- **Circuit breakers** — `fortify/circuit` wraps vendor handlers in Phase 2 to prevent cascade failure.
- **MCP** — Phase 3 wires `mcp-go` to expose capabilities as MCP tools. The Phase-1 HTTP API and the Phase-3 MCP surface share the same executor underneath.
- **Agents** — Praxis publishes capability descriptors compatible with `agent-go` so any `agent-go` agent can `ListCapabilities`, `DryRun`, and `Execute` against Praxis without bespoke glue.

---

## 10. MVP Scope (Phase 1)

Build only:

- Capability registry with schema validation
- Synchronous `Execute` with idempotency
- `DryRun` for capabilities that support it
- Audit log
- Two real handlers: Slack `send_message`, SMTP `send_email`
- Outcome writeback to Mnemos
- Memory + SQLite + Postgres backends
- CLI: `caps list`, `run`, `log show`

Example:

```bash
praxis caps list
praxis run send_message --payload '{"channel":"#general","text":"hello"}'
praxis log show <action-id>
```

That's enough to be the execution backend for Nous and a clean tool surface for any agent.

---

## 11. Final Definition

> Praxis is the trustworthy hands of any intelligent system — the substrate that turns decisions into observable, governed, reversible action.
