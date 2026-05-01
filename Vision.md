# Praxis — Vision

## Vision Statement

Praxis is the trustworthy hands of any intelligent system — the substrate that turns decisions into observable, governed, reversible action.

---

## The Problem

Modern systems — human and AI — are surprisingly good at deciding *what* should happen and surprisingly bad at *doing it well*.

- **Tool use is brittle.** Every agent reinvents how to call APIs, retry, deduplicate, and prove what happened.
- **Side effects are scattered.** Slack messages, GitHub comments, calendar invites, ticket updates — all sent from somewhere, recorded nowhere.
- **Policy is implicit.** Who is allowed to do what, in which scope, with what audit trail, lives in tribal knowledge and fragile glue code.
- **Reversibility is an afterthought.** Dry-run and rollback exist when someone remembered to build them — usually they didn't.

The cost: fragile automations, untraceable changes, and a chilling effect on giving AI systems any real action surface.

## The Cost of Inertia

Without an execution layer, organizations pay in:

- **Audit blind spots** — "did the agent really send that message? to whom? when?"
- **Risk aversion** — agents are kept on a short leash because we can't tell whether they'll behave.
- **Boilerplate** — every new integration is a snowflake of retry logic, idempotency, and logging.
- **Trust ceilings** — automation hits a wall the moment the action surface meets a regulated process.

---

## The Shift

We are moving from:

> *intent → ad-hoc tool calls*

to:

> *intent → governed capability → observable action*

Decisions deserve a substrate that takes them seriously. Praxis is that substrate.

---

## The Big Idea

Praxis introduces a new layer:

> A registry of *capabilities*, exposed through a tiny API, executed under policy, with idempotency, dry-run, and a permanent audit trail.

This is not a workflow engine.
This is not an agent framework.
This is not a tool router.

This is:

> *execution as infrastructure*

---

## Why Now

AI systems are crossing the line from speakers to actors.

But they:

- call APIs without idempotency
- retry without backoff
- act without policy
- leave no trail their operator can audit

Without an execution layer:

> AI is helpful, but never accountable.

Praxis fills that gap — for AI agents and human-driven automation alike.

---

## Future State

In a world with Praxis:

- every action is **named** (a registered capability)
- every action is **policy-checked** (allow / deny / dry-run)
- every action is **idempotent** (the same intent never produces double effects)
- every action is **auditable** (who, what, when, with what result)
- every action is **reversible** where the destination allows it

---

## For Humans

Praxis enables:

- **Confidence** — automation you can read the trail of
- **Reuse** — write the Slack capability once; ten agents and three workflows use it
- **Governance** — policy lives in one place, not scattered through every script
- **Reversibility** — a real dry-run before anything ships

---

## For AI

Praxis enables:

- **A clean tool surface** — `ListCapabilities`, `Execute`, `DryRun`, nothing else
- **Safe agency** — the agent can act, but only through capabilities and only under policy
- **Closed-loop behaviour** — every action's outcome flows back into Mnemos as memory the next decision can use

---

## Core Principles

### 1. Single responsibility

Praxis executes. Praxis does not decide, prioritize, or evaluate risk. Those belong to **Nous**. Pattern detection belongs to **Chronos**. Memory belongs to **Mnemos**. The boundaries are non-negotiable.

### 2. Capabilities, not endpoints

A capability is a named, schema'd, permissioned unit of side effect. The same capability looks identical whether invoked by a human, an agent, or a scheduled job.

### 3. Policy by default

Every `Execute` is checked against a policy. The default deny-list is small; the default allow-list is empty. Trust is granted explicitly.

### 4. Idempotency is non-negotiable

Every action has a stable identity. Re-executing the same `Action.ID` never produces double effects. Handlers that can't honour this don't ship.

### 5. Dry-run as a first-class verb

For every capability that can simulate, `DryRun` returns a faithful preview — same validation, same policy check, no side effect.

### 6. Evidence everywhere

Every executed action stores its inputs, outputs, policy decision, and external identifiers. The audit trail is *the product*, not a side report.

---

## Mental Model

`Capability → Action → Policy → Execute → Result → Audit`

Each arrow is enforced. Each step is observable.

---

## Architecture Vision

```
              Nous (decisions)
                    │
                    ▼
              ┌──────────┐
              │  Praxis  │ ──► capability handlers (slack, email, github, ...)
              └─────┬────┘
                    │ outcomes
                    ▼
                Mnemos (memory)
```

Consumers:

- **Nous** — decides and dispatches
- **AI agents** — discover capabilities, dry-run, execute
- **Operators** — invoke capabilities directly through CLI / API
- **Schedulers / workflows** — invoke capabilities on a cadence

---

## Platform Vision

Praxis evolves into:

- a **local-first execution daemon** for individuals
- a **team capability registry** with policy and audit
- the **action backend** for trustworthy AI agents
- a **standard interface** for executing in regulated environments

---

## Strategic Phasing

- **Phase 1 — Execution Primitive:** capability registry, synchronous `Execute`, idempotency, audit log, one or two real handlers.
- **Phase 2 — Policy & Resilience:** policy engine, async actions, retries with backoff, rate limiting, `DryRun` parity.
- **Phase 3 — Capability Ecosystem:** plugin architecture, MCP-compatible capability surface, multi-tenant policy, org-level audit.

---

## Category Definition

**Cognitive Execution Infrastructure.**

---

## Strategic Position

Praxis is not:

- a workflow engine (Zapier, Temporal)
- a feature flag service
- an agent framework
- a tool router

Praxis is:

> *the safe, observable, policy-enforced way an intelligent system reaches into the world*

---

## End State

- Mnemos is to knowledge what Git is to code.
- Chronos is to time what observability is to systems.
- Nous is to coordination what an operating system is to processes.
- **Praxis is to action what a system call is to syscalls** — the small, audited, governed surface through which everything else gets done.

---

## Final Insight

We don't need more agents.

We need:

> *one substrate where every decision becomes a named, governed, observable action — and every action becomes evidence the next decision can trust.*
