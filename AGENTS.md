# AGENTS.md

Guidance for AI coding agents (Claude Code, Cursor, Copilot, etc.) working in the Praxis repository.

---

## What Praxis is

Praxis is the **execution layer** of the four-system cognitive stack:

| Layer | Role |
|---|---|
| **Mnemos** | Memory & knowledge — what happened, what we know, with evidence |
| **Chronos** | Temporal pattern engine — what is changing, what's unusual |
| **Nous** | Coordination & intelligence — what matters, what should be done |
| **Praxis** | **Execution & capabilities — what can be done, and what happened when we did it** |

Praxis **executes** what it is told. It does not decide. Read [README.md](./README.md), [Vision.md](./Vision.md), and [TDD.md](./TDD.md) before non-trivial work.

---

## Architecture invariants

These are **non-negotiable**. If a change requires breaking one, propose it explicitly.

1. **Single responsibility.** Praxis exposes capabilities and executes actions. It does not decide, prioritize, or evaluate risk — those belong to **Nous**. It does not detect patterns (Chronos) or store knowledge (Mnemos).
2. **The public API is three verbs.** `ListCapabilities`, `Execute`, `DryRun`. Anything else is the wrong layer.
3. **Capabilities, not endpoints.** A capability is a named, schema'd, permissioned unit of side effect. Vendor specifics live in handlers, never in the domain.
4. **Policy by default.** Every `Execute` records a `PolicyDecision`, even when the Phase-1 default is a global allow.
5. **Idempotency is non-negotiable.** Stable `Action.ID`. Forwarded vendor idempotency keys. Re-executing the same action ID never produces double effects.
6. **Dry-run is first-class.** For every simulatable capability, `DryRun` is a faithful preview without side effects.
7. **Evidence everywhere.** Every action persists inputs, outputs, policy decision, vendor identifiers, and timestamps. The audit log *is* the product.
8. **Outcomes flow back to Mnemos.** Every terminal action emits an event so the next decision can see what happened.

---

## Code layout (target)

```
cmd/praxis/                CLI entrypoint
internal/
  domain/                  Capability, Action, Result, Simulation, PolicyDecision, ...
  ports/                   Repository ports + external interfaces (MnemosWriter)
  capability/              CapabilityRegistry + built-in capability descriptors
  schema/                  Input/output schema validation
  policy/                  PolicyEngine (Phase 1 = global allow; Phase 2 = scoped rules)
  idempotency/             IdempotencyKeeper
  executor/                Executor — orchestrates Execute / DryRun
  handlerrunner/           Timeouts, panic recovery, vendor error normalization
  handlers/                Per-capability adapters (slack, email, github, ...)
  audit/                   AuditLog (append-only)
  outcome/                 OutcomeEmitter (writeback to Mnemos)
  store/                   Repository facade + shared backend test suite
    memory/                In-process backend (reference impl, no SQL)
    sqlite/                sqlc-generated for SQLite (local-first)
      migrations/
      sqlcgen/
    postgres/              sqlc-generated for Postgres (production)
      migrations/
      sqlcgen/
  config/                  Environment-driven config
client/                    Public Go client (mirrors Mnemos / Chronos)
sql/
  sqlite/queries.sql       sqlc input — SQLite dialect
  postgres/queries.sql     sqlc input — Postgres dialect
sqlc.yaml                  Two engines: sqlite + postgresql
```

The domain layer (`internal/domain/`) and executor (`internal/executor/`) **never import a concrete backend or handler** — only ports and registry interfaces.

---

## Engineering rules

### Tests
- Every domain rule, state transition, and executor branch has a table-driven test.
- New capability handlers ship with vendor-sandboxed integration tests (skipped without credentials).
- Any change touching the executor flow MUST add a test that exercises the exact path.
- A "replay from audit" test reconstructs an action's lifecycle from `audit_events` alone — this is the canary for evidence completeness.

### TDD
Follow Red → Green → Refactor. Atomic commits. Conventional commits (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`).

### Storage (multi-backend)
- Three backends behind one set of repository ports:
  - `memory` — reference implementation, used by tests and ephemeral runtime.
  - `sqlite` — local-first / single-user / embedded.
  - `postgres` — production / team / multi-tenant.
- Schema lives in `internal/store/<backend>/migrations/`, applied in order, **forward-only**. Never edit a committed migration.
- All SQL goes through `sqlc`-generated code. Hand-written `database/sql` or `pgx` is a code-review red flag.
- A change touching one backend's schema MUST be paralleled in the other two so the shared test suite passes for all three.
- New tables MUST have indices that match real query patterns — see [TDD §7](./TDD.md#7-storage-multi-backend-sqlc-typed).

### Foundation libraries (canonical — same as Mnemos)

Praxis uses the `felixgeelhaar/*` library family. **Do not roll your own** when one of these already covers the use case.

| Library | Role | Don't reinvent |
|---|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | Structured logging | `log.Println`, raw `slog`, `fmt.Printf` for diagnostics |
| [`fortify/retry`](https://github.com/felixgeelhaar/fortify) | Retry with backoff + jitter | hand-rolled `for { sleep }` retry loops |
| [`fortify/circuit`](https://github.com/felixgeelhaar/fortify) | Circuit breakers | ad-hoc failure-counting wrappers |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | State machines | string switches for status transitions |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | HTTP framework | `net/http` mux + ad-hoc middleware chains |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | MCP server (Phase 3) | hand-rolled MCP wire format |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | Agent framework / capability descriptors | bespoke tool-spec formats |
| `sqlc` | Typed SQL queries | hand-written `database/sql` / `pgx` calls |

Concrete rules:

- Every service constructor accepts a `*bolt.Logger`. No package-level loggers.
- The `Action` FSM lives in `internal/domain/action_fsm.go` using `statekit`. Status transitions go through the FSM, not free-form string assignment.
- The HTTP server (`cmd/praxis/axikernel.go`) follows Mnemos's `axikernel` pattern — import `axi-go`, wire routes there, map domain errors to HTTP status in one place.
- Retries use `fortify/retry.Config` with the project default (5xx + 429 retry, 4xx fail fast, exponential backoff with jitter). Same defaults as Mnemos.
- The Phase-3 MCP surface is implemented with `mcp-go` and shares the same executor as the HTTP surface.
- Capability descriptors are shaped to be consumable by `agent-go` agents.

### Configuration
Environment variables only. Naming convention: `PRAXIS_<AREA>_<KEY>`.

### Logging & metrics
- Structured logs via `bolt` only — key/value pairs, no string interpolation, no `fmt.Printf` for diagnostics.
- Every action emits metrics: capability, status, duration, attempts.
- Errors are wrapped with `fmt.Errorf("%w", err)` and surface a clear trail.

### Schemas
- Every capability has a stable input/output schema. Phase 1 ships JSON Schema + Go-struct registration.
- Schema changes are versioned; old action records remain readable.

### Handlers
- Idempotent at the destination: forward vendor-side idempotency keys when the vendor supports them.
- Bounded execution time (per-handler timeout, recoverable from panic).
- Errors classified as retryable vs non-retryable.
- No I/O outside `internal/handlers/` — domain code never touches the network or vendor SDKs.

---

## Working with sibling systems

Praxis sits in a four-layer stack. When your change crosses a boundary:

- **Inputs from Nous.** The decision layer hands Praxis a fully-formed `Action`. Praxis never reaches *into* Nous.
- **Outcomes to Mnemos.** After every terminal action, Praxis appends a `praxis.action_completed` event. The Mnemos contract is in `internal/ports/mnemos.go`.
- **No direct Chronos coupling.** Praxis does not consume signals. If you find yourself needing a signal, the decision belongs to Nous, and the action belongs back to you.

Do **not** depend on private internals of any sibling system.

---

## What *not* to do

- Don't add decision logic ("should we run this?") inside Praxis. Bounce it back to Nous.
- Don't expand the public API beyond `ListCapabilities`, `Execute`, `DryRun`.
- Don't bypass `Executor` to call handlers directly.
- Don't allow a handler to be invoked without a `PolicyDecision` on file.
- Don't write a handler that isn't idempotent at the destination.
- Don't store outcomes only in Praxis; they must also flow to Mnemos.
- Don't ship a capability without a schema.
- Don't mock the database in tests — use the in-memory backend or the real SQLite/Postgres test suite.

---

## Useful entry points for an agent

- New capability? → `internal/handlers/<vendor>/`, register in `internal/capability/registry.go`
- New schema validator feature? → `internal/schema/`
- New policy rule type? → `internal/policy/`
- New audit query? → `internal/audit/queries.go` + sqlc regen
- Schema change? → add a migration in `internal/store/postgres/migrations/<n>_<slug>.sql` **and** `internal/store/sqlite/migrations/<n>_<slug>.sql`, mirror in `internal/store/memory/`, then `make sqlc`

---

## Definition of done (for a change)

- [ ] `make check` passes
- [ ] Tests cover the new behaviour (table-driven where appropriate)
- [ ] Replay-from-audit test still passes for affected actions
- [ ] If SQL changed, all three backends and `sqlc` are updated together
- [ ] Docs updated for any user-visible behaviour change (TDD, README, or `docs/`)
- [ ] No new handler without idempotency at the destination
- [ ] No expansion of the public API beyond the three verbs
- [ ] Conventional-commit message; atomic commits — one logical change per commit
