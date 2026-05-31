# Praxis — Archived

> **This repository is archived as of 2026-05-31.** Final release: `v0.3.1-final`.
> The cognitive-stack architecture Praxis was part of has been simplified.
> See below for what to use instead.

## What to use instead

| If you wanted... | Use instead |
|---|---|
| Vendor capability execution from an agent | Your agent runtime's MCP servers (Slack, GitHub, Linear all have first-party MCP) or direct SDK calls |
| Programmatic action execution with idempotency + audit + outbox | The inlined remediation primitives in [obvia/internal/application/remediation](https://github.com/obviahq/obvia/tree/main/internal/application/remediation) — copy what you need; they were extracted from this repo |
| Action / outcome storage | [Mnemos](https://github.com/felixgeelhaar/mnemos) — `record_action` / `record_outcome` MCP tools |
| Policy gating | Open Policy Agent, Kyverno, or your own gate (Praxis's policy engine was small enough to inline if you need it) |

## Why this was archived

1. **Vendor handlers are the wrong shape for agent-driven workflows.** Agent
   runtimes (Claude Code, Codex, Hermes, Nomi, OpenClaw, NanoClaw, ...) connect
   to vendors through MCP servers and tool definitions. They do not import Go
   vendor-handler libraries. The six Praxis handlers (Slack/GitHub/Linear/HTTP/
   Calendar/Email) were dead weight for the agent path.
2. **Obvia was the only programmatic consumer**, and it has now inlined
   the orchestration primitives it needed (idempotency keeper, outbox
   emitter, audit trail, policy gate) plus direct vendor calls. See
   [obviahq/obvia#5](https://github.com/obviahq/obvia/pull/5).
3. **The plugin host + Fulcio signing** existed only because Praxis was a
   multi-tenant service. With execution moving in-process into Obvia, that
   complexity no longer earns its keep.

See [ADR 0006 in Mnemos](https://github.com/felixgeelhaar/mnemos/blob/main/docs/adr/0006-archive-praxis.md)
for the full decision record.

## What was preserved

The full Praxis source code is preserved at the `v0.3.1-final` tag. The
orchestration primitives (idempotency, outbox, audit, capability handlers
for Docker + Prometheus) were lifted verbatim into Obvia in
[obviahq/obvia#5](https://github.com/obviahq/obvia/pull/5).

The following components are NOT preserved as separate libraries because
they have no consumer:

- HTTP / gRPC API server
- Out-of-process plugin host with Fulcio keyless signing
- Capability registry as a service
- The Praxis MCP server
- Six vendor handlers (Slack, GitHub, Linear, HTTP, Calendar, Email) — agents
  reach these via MCP; programmatic systems use direct SDK calls

## Recovery path

```bash
git clone https://github.com/felixgeelhaar/praxis.git
cd praxis
git checkout v0.3.1-final
```

The repo remains read-only and publicly readable. Source, tags, and history
are preserved.

## Related archives

- [Olymp](https://github.com/felixgeelhaar/olymp) — orchestration runtime, archived
- [Nous](https://github.com/felixgeelhaar/nous) — reasoning service, archived; risk + intervention engines survive in [decisionkit](https://github.com/felixgeelhaar/decisionkit)

## License

Unchanged from the v0.3.1-final tag. See `LICENSE`.
