# CLAUDE.md

Project-specific guidance for Claude Code working in the Praxis repository.

> Praxis is the **execution layer** of the four-system cognitive stack: Mnemos (memory) · Chronos (time) · Nous (decisions) · **Praxis (execution)**. Read [README.md](./README.md), [Vision.md](./Vision.md), and [TDD.md](./TDD.md) before non-trivial work.

For full agent-facing engineering rules see [AGENTS.md](./AGENTS.md). This file captures Claude-Code-specific conventions and the high-leverage rules an agent should never get wrong.

---

## High-leverage rules

1. **Praxis executes. Praxis does not decide.**
   - Decision logic lives in **Nous**, not here. If you're reaching for risk scoring, prioritization, or "should we run this?", you're in the wrong layer.

2. **The public API is three verbs.**
   - `ListCapabilities`, `Execute`, `DryRun`. Never expand it.

3. **Every action is named, validated, and policy-checked.**
   - Capability lookup → schema validate → policy decide → idempotency check → run.
   - Don't skip steps "for now."

4. **Idempotency is not optional.**
   - Stable `Action.ID`. Forward vendor idempotency keys. Re-executing the same key never produces double effects.

5. **Evidence-first.**
   - Every lifecycle event hits the audit log. The replay-from-audit test must keep passing.

6. **Outcomes flow back to Mnemos.**
   - A terminal action without an outcome event is a bug.

7. **Use the foundation libraries — don't reinvent.**
   - **Logging:** `bolt` (never `log`, never raw `slog`).
   - **State machines:** `statekit` (never string switches for `Action.Status`).
   - **HTTP:** `axi-go` via `cmd/praxis/axikernel.go` (mirror Mnemos's `axikernel`).
   - **Retries:** `fortify/retry` with the project defaults (5xx + 429 retry, 4xx fail fast).
   - **Circuit breakers:** `fortify/circuit` (Phase 2 vendor handlers).
   - **MCP (Phase 3):** `mcp-go`.
   - **Agent compatibility:** capability descriptors consumable by `agent-go`.
   - **SQL:** `sqlc` only — never hand-written `database/sql` / `pgx`.

8. **Storage is multi-backend, forward-only.**
   - Three backends behind one set of repository ports: `memory` (reference), `sqlite` (local-first), `postgres` (production).
   - Domain code never imports a backend directly.
   - A schema change ships to **all three** backends in the same commit (or it doesn't ship).
   - Never edit a committed migration. Add a new one.
   - Run `make sqlc` after every schema change and commit the generated code with the migration.

---

## Useful commands

```bash
make check              # format, lint, test, build (CI equivalent)
make test               # unit tests + memory/SQLite store tests
make integration        # adds Postgres-backed tests (containerised)
make sqlc               # regenerate sqlc query code (both engines)
make build              # build bin/praxis
```

---

## Where things live

| Concern | Path |
|---|---|
| Domain types | `internal/domain/` |
| Repository ports + external interfaces | `internal/ports/` |
| Capability registry | `internal/capability/` |
| Schema validation | `internal/schema/` |
| Policy engine | `internal/policy/` |
| Idempotency keeper | `internal/idempotency/` |
| Executor (Execute / DryRun) | `internal/executor/` |
| Handler runner (timeouts, recovery) | `internal/handlerrunner/` |
| Per-capability handlers | `internal/handlers/<vendor>/` |
| Audit log | `internal/audit/` |
| Outcome writeback to Mnemos | `internal/outcome/` |
| Repository facade + shared tests | `internal/store/` |
| In-memory backend (reference) | `internal/store/memory/` |
| SQLite backend (local-first) | `internal/store/sqlite/` (sqlcgen + migrations) |
| Postgres backend (production) | `internal/store/postgres/` (sqlcgen + migrations) |
| sqlc inputs | `sql/sqlite/queries.sql`, `sql/postgres/queries.sql` |
| sqlc config | `sqlc.yaml` (two engines) |
| CLI entry | `cmd/praxis/` |
| Public Go client | `client/` |

---

## Definition of done

A change is done when:

- [ ] `make check` passes
- [ ] Tests cover the new behaviour
- [ ] Replay-from-audit test passes for any action whose lifecycle changed
- [ ] If SQL changed, migration + regenerated `sqlc` are committed together for all three backends
- [ ] No expansion of the public API beyond `ListCapabilities` / `Execute` / `DryRun`
- [ ] No handler without idempotency at the destination
- [ ] Outcomes still flow back to Mnemos for terminal actions
- [ ] Docs updated for any user-visible behaviour change
- [ ] Conventional-commit message; atomic commits

---

## When proposing larger changes

Use `EnterPlanMode` and write the plan first when the change:

- adds a new capability handler that talks to an external vendor
- modifies the executor flow
- changes the policy engine semantics
- alters the audit-event shape
- modifies the Mnemos outcome contract
- spans multiple phases of the [Roadmap](./Roadmap.md)

A short plan saves a long rewrite.
