# Integrations

Praxis sits at exactly one position in the four-layer cognitive stack. This document specifies the contracts at every boundary it touches.

For internal architecture see [architecture.md](./architecture.md). For the canonical domain model see [TDD.md](../TDD.md).

---

## 1. Boundary Map

```
                    ┌──────────┐
                    │   Nous   │   (decisions / action requests)
                    └────┬─────┘
                         │  Execute / DryRun
                         ▼
                    ┌──────────┐
                    │  Praxis  │
                    └────┬─────┘
              ┌──────────┼──────────┐
              ▼          ▼          ▼
          handlers   handlers   handlers
          (slack)    (email)    (github, ...)

                         │
                         │  outcome events
                         ▼
                    ┌──────────┐
                    │  Mnemos  │
                    └──────────┘
```

Praxis has three live boundaries:

1. **Inbound from callers** (Nous, agents, humans, schedulers) — the public API.
2. **Outbound to handlers** (vendor adapters) — internal to the binary.
3. **Outbound to Mnemos** — the outcome writeback channel.

It does **not** consume Mnemos or Chronos directly. Praxis is told what to do; it does not derive it.

Every boundary has:

- a **Go interface** in `internal/ports/`,
- a **wire test** asserting the schema we depend on, and
- a **fake** in `internal/ports/fakes/` for unit tests.

---

## 2. Inbound: Public API (the caller boundary)

The public surface is intentionally tiny:

```go
type PraxisAPI interface {
    ListCapabilities(ctx context.Context) ([]Capability, error)
    Execute(ctx context.Context, action Action) (Result, error)
    DryRun(ctx context.Context, action Action) (Simulation, error)
}
```

Available as both an in-process Go interface and an HTTP service (`praxis serve`, built on [`axi-go`](https://github.com/felixgeelhaar/axi-go)). Phase 3 adds an MCP surface via [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) — same executor underneath.

### 2.1 HTTP

| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Liveness probe + version |
| `/v1/capabilities` | GET | List registered capabilities |
| `/v1/capabilities/{name}` | GET | Show capability detail (schema, permissions) |
| `/v1/actions` | POST | `Execute` an action |
| `/v1/actions/{id}` | GET | Get current action status / result |
| `/v1/actions/{id}/dry-run` | POST | `DryRun` (idempotent) |
| `/v1/audit?action_id=...` | GET | Reconstruct an action's full lifecycle from audit |

### 2.2 Authentication

`Authorization: Bearer <token>` is required for all `POST` endpoints when `PRAXIS_API_TOKEN` is set. Reads stay open by default — useful for browse-only dashboards.

### 2.3 Caller identity

Every `Execute` and `DryRun` request must identify the caller via the `Caller` field on the `Action`:

```json
{
  "id":         "act_2026-04-27T12:34:56Z_xyz",
  "capability": "send_message",
  "payload":    { "channel": "#general", "text": "..." },
  "caller":     { "type": "nous", "id": "nous-instance-1" },
  "scope":      ["messaging.write"]
}
```

The caller is recorded on the action and on every audit event so the question *"who asked for this?"* always has an answer.

---

## 3. Inbound from Nous (canonical caller)

Nous is the primary opinionated caller. The interaction is purely through the public API — Nous holds no privileged channel.

A typical sequence:

1. Nous decides an intervention is warranted.
2. Nous calls `ListCapabilities` (cached) to confirm the capability exists.
3. Nous (optionally) calls `DryRun` to validate.
4. Nous calls `Execute` with a stable `Action.ID`.
5. Nous receives a `Result` synchronously (Phase 1) or polls (`async` mode in Phase 2).

### 3.1 Idempotency

Nous SHOULD construct deterministic `Action.ID`s (e.g. `nous_<commitment_id>_<intervention_id>`). Re-calling `Execute` with the same ID returns the original `Result` without invoking the handler.

### 3.2 Failure handling

Nous receives:

- `Result.Status = "succeeded"` — done.
- `Result.Status = "failed"` — `Result.Error.Retryable` indicates whether Nous should retry (with a *new* derived ID; see [TDD §5.6](../TDD.md#56-failure--retries)).
- `Result.Status = "rejected"` — capability unknown, validation failed, or policy denied. Nous should not retry blindly.

---

## 4. Outbound: Capability Handlers

Handlers are the only path Praxis uses to affect the outside world. Anything else is a layering violation.

### 4.1 Interface

```go
type Handler interface {
    Capability() Capability
    Validate(ctx context.Context, payload map[string]any) error
    Execute(ctx context.Context, in HandlerInput) (HandlerOutput, error)
    Simulate(ctx context.Context, in HandlerInput) (HandlerOutput, error) // optional; nil means "not simulatable"
}

type HandlerInput struct {
    ActionID       string
    Payload        map[string]any
    IdempotencyKey string
    Caller         CallerRef
}

type HandlerOutput struct {
    Output     map[string]any
    ExternalID string
    Reversible bool
}
```

Handlers are registered with the registry at startup:

```go
registry.Register(slack.NewSendMessageHandler(slackClient))
registry.Register(email.NewSendEmailHandler(smtp))
registry.Register(github.NewCreateIssueHandler(ghClient))
```

### 4.2 Idempotency contract

Every handler **must** be idempotent at the destination. The `HandlerInput.IdempotencyKey` is forwarded to the vendor where supported (Slack `client_msg_id`, Stripe `Idempotency-Key`, GitHub `request_id`, etc.). Handlers that can't honour idempotency don't ship.

### 4.3 Output shape

```jsonc
{
  "output":      { /* vendor-specific success payload */ },
  "external_id": "<vendor-side identifier>",   // e.g. Slack message ts, GitHub issue node_id
  "reversible":  true                           // can this action be compensated?
}
```

The `external_id` is the canonical handle for compensation, audit cross-reference, and human follow-up.

### 4.4 Error classification

```go
type HandlerError struct {
    Code      string             // "validation" | "auth" | "rate_limited" | "vendor" | "timeout"
    Message   string
    Retryable bool
    Vendor    map[string]any     // raw vendor error
}
```

Phase 2's retry strategy depends on this classification — get it right.

---

## 5. Praxis → Mnemos (outcome writeback)

Every terminal action emits an event into Mnemos so the cognitive loop closes.

### 5.1 Interface

```go
type MnemosWriter interface {
    AppendOutcome(ctx context.Context, ev OutcomeEvent) error
}

type OutcomeEvent struct {
    ActionID    string
    Capability  string
    Caller      CallerRef
    Status      string             // "succeeded" | "failed" | "simulated" | "rejected"
    ExternalID  string
    OccurredAt  time.Time
    Detail      map[string]any
}
```

### 5.2 Wire format

The Phase-1 implementation appends an event to Mnemos's HTTP registry:

```bash
POST /v1/events
{
  "events": [{
    "id":             "praxis_<action_id>",
    "schema_version": "v1",
    "content":        "praxis.action_completed",
    "metadata": {
      "kind":         "praxis.action_completed",
      "action_id":    "...",
      "capability":   "send_message",
      "caller":       { "type": "nous", "id": "..." },
      "status":       "succeeded",
      "external_id":  "1714208096.000200",
      "detail":       { /* trimmed result */ }
    },
    "timestamp": "2026-04-27T12:34:56Z"
  }]
}
```

Authentication: `Authorization: Bearer <token>` if `PRAXIS_MNEMOS_TOKEN` is set.

### 5.3 Resilience

The emitter is **non-blocking**. If Mnemos is unavailable:

- the outcome is queued locally (in the same store as actions, table `outcome_outbox`),
- a background worker retries via [`fortify/retry`](https://github.com/felixgeelhaar/fortify) (5xx + 429 retry, 4xx fail fast, exponential backoff with jitter — same defaults as Mnemos's own client),
- `Execute` never blocks on Mnemos.

A `praxis_outcome_emit_failures_total` metric surfaces sustained failure for alerting.

### 5.4 What Praxis does *not* do with Mnemos

- It does not query Mnemos for context. (That's Nous's job.)
- It does not maintain its own knowledge graph.
- It does not reason about contradictions or evidence.

Praxis writes to Mnemos exactly once per terminal action, and never reads from it.

---

## 6. (Not) integrating with Chronos

Praxis has **no Chronos dependency**. Pattern detection feeds *Nous*, not Praxis. If a Praxis change starts to feel like it needs a Chronos signal, the right move is:

1. Push the decision back into Nous.
2. Keep Praxis stateless on signals.

The boundary is a feature.

---

## 7. Failure Semantics at Boundaries

| Boundary failure | Praxis behaviour |
|---|---|
| Caller posts an unknown capability | `Result.Status = "rejected"` with `Code = unknown_capability`; audit row written. |
| Caller posts an invalid payload | `Result.Status = "rejected"` with `Code = validation_failed`. |
| Caller is not authorized (Phase 2+) | `Result.Status = "rejected"` with `Code = policy_denied`; matched rule recorded. |
| Handler returns retryable error | Phase 1: caller decides; Phase 2: per-capability retry policy applies, parent ↔ child audit linkage. |
| Handler returns non-retryable error | `Result.Status = "failed"`; audit preserves vendor detail; no automatic retry. |
| Handler exceeds timeout | Same as non-retryable; idempotency key released after a grace period. |
| Mnemos unavailable | Outcome queued; pipeline does not stall. Operator alerted via metric. |
| Storage unavailable | Pipeline halts; CLI exits non-zero. No silent fallbacks. |

The principle: *be loud about what you cannot do; never silently make something up.*

---

## 8. Versioning & Compatibility

- Praxis pins to a **major** version of the Mnemos API (`/v1`).
- Within a major version, Praxis tolerates **additive** changes (new optional fields).
- Capability schemas are versioned per capability; old action records remain readable.
- Outbound action payloads are versioned per handler; handlers may evolve independently.

---

## 9. Testing the Boundaries

Four layers, each with its own test:

| Layer | Test type | Lives in |
|---|---|---|
| Caller ↔ public API | HTTP contract tests + Go-client wire tests | `client/`, `internal/api/server_test.go` |
| Executor ↔ handler | Mocked handler unit tests; vendor-sandboxed integration tests | `internal/executor/`, `internal/handlers/<vendor>/` |
| Praxis ↔ Mnemos | Wire test against a real (test-instance) Mnemos | `internal/outcome/wire_test.go` |
| Repository contract | Shared store test suite re-run against `memory`, `sqlite`, and `postgres` | `internal/store/storetest/` |

Together they catch wire drift, handler regressions, and storage-backend skew before they reach a user.
