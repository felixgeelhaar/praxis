# Praxis — Product Requirements Document (PRD)

## 1. Overview

Praxis is the **execution layer** of the four-system cognitive stack (Mnemos · Chronos · Nous · Praxis). It exposes a registry of *capabilities*, executes them under policy, and records outcomes — providing the safe, observable, governed surface through which decisions become action.

Praxis does **not** decide what should happen. That belongs to **Nous**. Praxis takes a fully-formed action request and runs it.

## 2. Problem Statement

### 2.1 The Execution Gap

Decision systems (humans, agents, Nous) are getting better at producing instructions. The systems that *carry out* those instructions are still:

- **Brittle** — every integration reinvents retry, idempotency, and error handling.
- **Untraceable** — actions are taken from anywhere; the audit trail is scattered across vendor logs.
- **Ungoverned** — policy lives in code reviews and tribal knowledge, not in a single enforcement point.
- **Unrehearsable** — *"will this break anything?"* is rarely answerable before the fact.

### 2.2 The Cost

- AI agents kept on a short leash because we can't guarantee what they'll do.
- Engineering effort lost to one-off retry/dedup glue per integration.
- Compliance and audit hell when "what did the system do last Tuesday at 3:14 PM" cannot be answered.
- Duplicate side effects (the same Slack message sent twice) that erode trust.

### 2.3 Root Cause

There is no shared layer that:

1. Names every capability.
2. Validates input against a known schema.
3. Checks policy before any side effect.
4. Provides idempotency, retries, and dry-run as first-class verbs.
5. Persists a complete record of inputs, outputs, and external identifiers.

## 3. Strategic Vision

> Praxis is the trustworthy hands of any intelligent system.

Position relative to peers in the cognitive stack:

| Layer | Question |
|---|---|
| Mnemos | "What is true and why?" |
| Chronos | "What patterns are happening?" |
| Nous | "What should be done — and when?" |
| **Praxis** | **"What can be done? What happened when we did it?"** |

## 4. Public Contract

The minimal user-facing API:

```go
type PraxisAPI interface {
    ListCapabilities(ctx context.Context) ([]Capability, error)
    Execute(ctx context.Context, action Action) (Result, error)
    DryRun(ctx context.Context, action Action) (Simulation, error)
}
```

Three verbs. Anything more lives in another layer.

## 5. Phased Roadmap

### Phase 1 — Execution Primitive (MVP)

**Goal:** Prove that a tiny, well-shaped execution API plus two real capability handlers is dramatically more useful than ad-hoc tool calls.

**Target Users:**

- Developers building AI agents who need a clean action surface.
- Operators of automations who want one audit trail.
- The Nous layer (when it lands) as its execution backend.

**Deliverables:**

- [ ] Capability registry with input/output schema validation.
- [ ] Synchronous `Execute` with stable `Action.ID` and idempotency keys.
- [ ] `DryRun` for capabilities that support simulation.
- [ ] Audit log of every action: inputs, outputs, status, external identifiers.
- [ ] Two real handlers: `send_message` (Slack) and `send_email`.
- [ ] CLI: `praxis caps list`, `praxis run <capability> --payload …`, `praxis log show <action-id>`.
- [ ] Multi-backend storage (memory / SQLite / Postgres).
- [ ] Outcome writeback into Mnemos.

**Excluded from MVP:**

- Policy / RBAC (a single global allow-list is fine).
- Async actions and long-running jobs.
- Plugin / dynamic capability loading.
- MCP-compatible capability surface.
- Multi-tenant.

**Success Metrics:**

- 100% of executed actions are reconstructible from the audit log alone.
- Re-executing the same `Action.ID` produces zero double effects (verified by handler-level tests).
- Time-to-add-a-new-capability (handler) measured in hours, not days.
- Zero un-validated payloads reach a handler.

---

### Phase 2 — Policy & Resilience

**Goal:** Make Praxis safe to point at production. Add policy, async, retries, rate limits, and full dry-run parity.

**Target Users:** Teams running real automations and AI agents in business-critical paths.

**Deliverables:**

- [ ] Policy engine: scope-based allow/deny rules with explainable decisions.
- [ ] Async actions with status polling and webhook delivery on completion.
- [ ] Retry strategy per capability (exponential backoff, max attempts, jitter).
- [ ] Rate limiting per capability and per caller.
- [ ] `DryRun` for *every* capability (parity with `Execute`).
- [ ] Audit log enriched with policy decision and the rule that matched.
- [ ] More handlers: GitHub, Linear/Jira, Calendar, generic HTTP.

**Success Metrics:**

- Zero unauthorized actions in audit (every Execute has a recorded policy decision).
- ≥ 99% of transient handler failures recovered by retry.
- DryRun output matches Execute output ≥ 95% of the time on identical inputs.

---

### Phase 3 — Capability Ecosystem

**Goal:** Open the surface. Let third parties register capabilities. Speak MCP. Scale to org-level governance.

**Target Users:** Platform teams, regulated environments, AI agent vendors.

**Deliverables:**

- [ ] Plugin architecture for out-of-tree capability handlers.
- [ ] MCP-compatible capability discovery (`ListCapabilities` exposed as MCP tools).
- [ ] Multi-tenant policy with org / team / user scopes.
- [ ] Org-level dashboards and exportable audit (SOC 2 / HIPAA / GDPR friendly).
- [ ] Reversibility primitives: per-action `compensate` for capabilities that support it.

**Success Metrics:**

- Third-party plugins shipped in production by ≥ 3 organizations.
- Zero policy bypasses in audit.
- Audit export passes a real compliance review.

## 6. Core Use Cases

### Use Case 1 — Agent Action Surface (Phase 1)

> A coding agent wants to send a Slack update when a long-running task finishes.

The agent calls `Execute({capability: "send_message", payload: {...}})`. Praxis validates, sends, returns the message ID, and writes an audit record. The agent knows it succeeded; the team can audit it later.

### Use Case 2 — Nous Intervention (Phase 1 → 2)

> Nous determines a follow-up nudge is warranted and dispatches a `send_message` action with a stable idempotency key.

Praxis enforces policy, runs the handler, deduplicates if Nous accidentally retries, and writes the outcome back to Mnemos so the next decision sees what happened.

### Use Case 3 — Compliant Automation (Phase 3)

> A regulated team wants every customer-facing message routed through a single audited path.

Praxis becomes the only path: capabilities are registered, scoped to roles, dry-runnable, and logged. Audit export satisfies the auditor on first ask.

## 7. Product Principles

1. **Single responsibility.** Praxis executes. Praxis does not decide.
2. **Capabilities, not endpoints.** Named, schema'd, permissioned units of effect.
3. **Policy by default.** No execution without a policy decision on file.
4. **Idempotency is non-negotiable.** Every action has a stable identity.
5. **Dry-run is first-class.** Simulating is as supported as executing.
6. **Evidence everywhere.** The audit trail *is* the product.
7. **Local-first defaults.** A laptop with SQLite is a complete deployment.

## 8. Non-Goals

- A workflow engine (control flow, conditionals, loops). Praxis runs single actions.
- A scheduler or cadence runner.
- A decision engine — that's Nous.
- A pattern detector — that's Chronos.
- A memory store — that's Mnemos.
- An agent framework.

## 9. Open Questions

- [ ] How are capability schemas declared — hand-written Go structs, JSON Schema, both?
- [ ] What is the right default idempotency-key TTL?
- [ ] Should `DryRun` for non-simulatable capabilities return an error or a "best-effort preview"?
- [ ] How do we model partial failure for capabilities that touch multiple systems?
- [ ] What is the right boundary between "Praxis built-in handler" and "external plugin"?
- [ ] How do we test handlers against real vendors without flakiness?

## 10. Definition of Success

Praxis is successful when:

- AI agents and humans use the same execution surface for the same capabilities.
- Every action a system takes is reconstructible from the audit log alone.
- New capabilities are added in hours and trusted in production immediately.
- Operators stop writing per-integration retry/dedup code.
- Nous (and any other decider) treats Praxis as the only legitimate way to act.
