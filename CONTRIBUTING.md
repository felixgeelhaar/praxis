# Contributing to Praxis

Thanks for your interest in contributing! Praxis is the **execution layer** of the four-system cognitive stack (Mnemos · Chronos · Nous · Praxis) — please skim [README.md](./README.md), [Vision.md](./Vision.md), and [TDD.md](./TDD.md) before opening anything non-trivial.

---

## Getting set up

```bash
git clone https://github.com/felixgeelhaar/praxis.git
cd praxis
make check     # format, lint, test, build
```

Requirements:

- Go 1.25+
- `sqlc` (for query regeneration when you change SQL)
- SQLite 3.40+ (bundled with most systems; required for the local-first backend)
- Postgres 15+ for the production-backend integration tests; a Docker image works fine

Praxis uses the `felixgeelhaar/*` foundation libraries — please use them rather than rolling your own:

| Library | Use it for |
|---|---|
| [`bolt`](https://github.com/felixgeelhaar/bolt) | All structured logging (no `log`, no raw `slog`) |
| [`fortify`](https://github.com/felixgeelhaar/fortify) | Retries (`fortify/retry`) and circuit breakers (`fortify/circuit`) |
| [`statekit`](https://github.com/felixgeelhaar/statekit) | The `Action` lifecycle FSM — never string-switch transitions |
| [`axi-go`](https://github.com/felixgeelhaar/axi-go) | The HTTP server (`cmd/praxis/axikernel.go`) |
| [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) | The MCP capability surface (Phase 3) |
| [`agent-go`](https://github.com/felixgeelhaar/agent-go) | Capability descriptors consumed by `agent-go` agents |
| `sqlc` | Typed query layer for SQLite + Postgres |

---

## Branching & commits

- Work on a feature branch — `feature/<short-slug>`, `fix/<short-slug>`, etc.
- Commits follow the [Conventional Commits](https://www.conventionalcommits.org/) spec:
  `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`, `perf:`.
- Keep commits **atomic** — one logical change per commit. The CI passes per-commit, not just per-PR.

---

## Test-driven development

Praxis follows TDD:

1. **Red** — write a failing test that captures the desired behavior.
2. **Green** — make it pass with the smallest reasonable change.
3. **Refactor** — clean up while staying green.

Specific expectations:

- **Domain logic** has table-driven unit tests.
- **Storage** ships with a single shared test suite (`internal/store/storetest/`) that runs against all three backends — `memory`, `sqlite`, and `postgres`. A schema or query change fails CI if any backend diverges.
- **Executor flow** changes require a test that exercises the exact path through `internal/executor/`.
- **New capability handlers** ship with vendor-sandboxed integration tests (skipped without credentials) and idempotency tests (re-execution must not double-effect).
- **Replay-from-audit** test reconstructs an action's full lifecycle from `audit_events` alone — keep it green.

`make test` runs unit tests + memory/SQLite store tests; `make integration` adds the Postgres-backed suite (containerised).

---

## Pull request checklist

Before opening a PR:

- [ ] `make check` passes locally.
- [ ] New behaviour has tests; bug fixes have a regression test.
- [ ] Replay-from-audit test still passes for any action whose lifecycle changed.
- [ ] If a new handler shipped, idempotency at the destination is tested.
- [ ] Foundation libraries used as appropriate (no hand-rolled logger / retry loop / state machine / HTTP mux when `bolt` / `fortify` / `statekit` / `axi-go` already cover it).
- [ ] If SQL changed, the migration is added to **both** `internal/store/sqlite/migrations/` and `internal/store/postgres/migrations/`, mirrored in `internal/store/memory/`, and the regenerated `sqlc` code is committed.
- [ ] Docs were updated if user-visible behaviour changed (`TDD.md`, `README.md`, or `docs/`).
- [ ] No expansion of the public API beyond `ListCapabilities` / `Execute` / `DryRun`.
- [ ] No decision logic introduced inside Praxis (that belongs to Nous).
- [ ] Outcomes still flow back to Mnemos for terminal actions.
- [ ] Commit messages follow Conventional Commits.

PRs are reviewed for:

- **Architecture invariants** ([AGENTS.md](./AGENTS.md#architecture-invariants))
- **Test quality**, not just coverage
- **Operability** — logs, metrics, error wrapping
- **Security** — no secrets in code, no SQL injection vectors, validated payloads

---

## Working across sibling systems

Praxis lives inside a four-layer cognitive stack: Mnemos · Chronos · Nous · Praxis. Praxis has exactly two boundaries with siblings:

1. **Inbound:** Nous (and any other caller) uses the public `PraxisAPI`. Don't grant Nous a privileged channel.
2. **Outbound:** Praxis writes outcome events to Mnemos via `MnemosWriter`.

If your change touches the Mnemos contract:

1. Discuss the contract change in `Mnemos/` first.
2. Update the corresponding port in `internal/ports/mnemos.go` after the upstream change is merged.
3. Bump the integration-test fixtures together so the stack stays in sync.

Praxis does **not** depend on Chronos. If you find yourself reaching for a Chronos signal, the decision belongs in Nous, not Praxis.

Do **not** depend on private internals of any sibling system.

---

## Reporting bugs

When filing a bug, include:

- Praxis version (`praxis --version` once available)
- Storage backend (`memory` / `sqlite` / `postgres`) and version
- Minimal reproduction: capability, payload, expected result, observed result
- Logs at `info` level for the affected window

If the bug is a wrong / duplicate / lost action, please attach the relevant `audit_events` rows for the action ID — they reconstruct the full lifecycle and are the most useful artifact.

---

## Proposing larger changes

Open a discussion (or draft PR with the doc only) before writing code if your change:

- adds a new capability handler that talks to an external vendor
- modifies the executor flow
- changes the policy engine semantics (Phase 2+)
- alters the audit-event shape
- modifies the Mnemos outcome contract
- spans multiple phases of the [Roadmap](./Roadmap.md)

We'd rather have a 30-minute alignment conversation than rewrite 500 lines.

---

## Code of conduct

By participating you agree to abide by the [Code of Conduct](./CODE_OF_CONDUCT.md). In short: be kind, be precise, assume good faith.
