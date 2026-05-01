# Praxis

**The execution layer of the cognitive stack.**

Praxis exposes safe, observable, policy-enforced capabilities. It is the layer that *acts* — never the layer that decides.

---

## The Cognitive Stack

Praxis is one of four loosely-coupled, independently-usable systems:

| System | Question it answers | Role |
|---|---|---|
| **Mnemos** | *What happened? What do we know?* | Memory & knowledge |
| **Chronos** | *What is changing? What's unusual?* | Time & pattern perception |
| **Nous** | *What matters? What should be done?* | Coordination & intelligence |
| **Praxis** | ***What can be done? What happened when we did it?*** | **Execution & capabilities** |

Together they form a closed loop:

```
observe → understand → detect → decide → act → learn
```

Each system is **independently usable** and connected by explicit contracts — no shared internals, no hidden coupling.

---

## What Praxis is (and isn't)

**Praxis is:**

- A **registry of capabilities** (tools, APIs, integrations).
- A **policy-enforcing executor** with retries, idempotency, and dry-run.
- An **audit trail** of every action and its outcome.

**Praxis is not:**

- A decision engine — that's Nous.
- A risk evaluator — that's Nous.
- A pattern detector — that's Chronos.
- A memory store — that's Mnemos.

Praxis is the *only* path the cognitive stack uses to affect the outside world. Everything else is decision; this is execution.

---

## The Public Contract

Praxis exposes one small, opinionated API:

```go
type PraxisAPI interface {
    ListCapabilities(ctx context.Context) ([]Capability, error)
    Execute(ctx context.Context, action Action) (Result, error)
    DryRun(ctx context.Context, action Action) (Simulation, error)
}
```

Three verbs. Anything more is the wrong layer.

---

## A Concrete Example

Walking through the loop end-to-end:

1. A user says: *"I'll follow up with Alex tomorrow."*
2. **Mnemos** stores the event and extracts the memory.
3. **Chronos** later detects a *no-response pattern* against Alex.
4. **Nous** interprets the signals + memory, identifies the commitment risk, decides an intervention is warranted, and forms an action: *"draft and send a follow-up to Alex."*
5. **Praxis** receives the action, looks up the `send_message` capability, validates the payload, checks policy, executes — and records the result with a stable `external_id`.
6. Praxis writes the outcome back as an event into **Mnemos**, closing the loop.

Praxis appears at exactly one step: the act of doing.

---

## Tech Stack

- **Language:** Go (≥ 1.25)
- **Architecture:** Domain-Driven Design — capabilities, actions, results as first-class domain objects
- **Storage (multi-backend):**
  - `memory` — tests / ephemeral runs
  - `sqlite` — local-first / single-user / embedded (default)
  - `postgres` — production / team / multi-tenant
- **Query layer:** `sqlc`-generated typed queries (one engine per relational backend)
- **Foundation libraries (shared with Mnemos / Chronos / Nous):**
  - [`bolt`](https://github.com/felixgeelhaar/bolt) — structured logging
  - [`fortify`](https://github.com/felixgeelhaar/fortify) — retry / circuit-breaker primitives
  - [`statekit`](https://github.com/felixgeelhaar/statekit) — `Action` lifecycle state machine
  - [`axi-go`](https://github.com/felixgeelhaar/axi-go) — HTTP framework (`praxis serve`)
  - [`mcp-go`](https://github.com/felixgeelhaar/mcp-go) — MCP capability surface (Phase 3)
  - [`agent-go`](https://github.com/felixgeelhaar/agent-go) — capability descriptors consumable by `agent-go` agents
- **Capability handlers:** pluggable adapters for Slack, email, GitHub, ticketing, calendar, ...

---

## Status

**Pre-alpha.** Domain model, public contract, and integration boundaries are documented. MVP implementation in progress — see the [Roadmap](./Roadmap.md).

---

## Documentation

| Doc | Purpose |
|---|---|
| [Vision](./Vision.md) | The *why* — execution as infrastructure |
| [PRD](./PRD.md) | What we're building, for whom, in which phases |
| [TDD](./TDD.md) | Domain model, services, contracts, storage |
| [Roadmap](./Roadmap.md) | Phased delivery |
| [Architecture](./docs/architecture.md) | Internal services, executor flow, failure modes |
| [Integrations](./docs/integrations.md) | Nous → Praxis · Praxis → Mnemos · Praxis → handlers |
| [Contributing](./CONTRIBUTING.md) | How to contribute |
| [Agents](./AGENTS.md) | Guidance for AI coding agents working in this repo |

---

## License

MIT — see [LICENSE](./LICENSE).
