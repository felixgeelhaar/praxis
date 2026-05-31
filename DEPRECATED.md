# DEPRECATED

**Status:** Archived 2026-05-31
**Final release:** `v0.3.1-final`
**Authoritative successor:** Orchestration primitives (idempotency, outbox,
audit, capability handlers) were inlined into
[obviahq/obvia](https://github.com/obviahq/obvia) in
[PR #5](https://github.com/obviahq/obvia/pull/5). Vendor execution for
agent workflows lives in agent runtimes (Claude Code, Codex, Hermes,
Nomi, OpenClaw, NanoClaw) via MCP servers.

## Why this was archived

1. **Vendor handlers are the wrong shape for agent-driven workflows.** Agent
   runtimes connect to vendors through MCP servers and tool definitions.
   They do not import Go vendor-handler libraries. The six Praxis handlers
   (Slack, GitHub, Linear, HTTP, Calendar, Email) were dead weight for the
   agent path.
2. **The only programmatic consumer was Obvia**, and it now inlines what it
   needs — idempotency keeper, outbox emitter, audit trail, policy gate —
   plus direct vendor SDK calls. See the migration PR.
3. **Multi-tenant service complexity** (out-of-process plugin host, Fulcio
   keyless signing, capability registry as a service) exists only because
   Praxis was a service. With execution moving in-process into Obvia, that
   complexity no longer earns its keep.

See [ADR 0006 in Mnemos](https://github.com/felixgeelhaar/mnemos/blob/main/docs/adr/0006-archive-praxis.md)
for the full decision record.

## What survived (in Obvia)

| Praxis primitive | Obvia location |
|---|---|
| Idempotency keeper (LRU + TTL + per-key locking) | [`internal/application/remediation/idempotency_cache.go`](https://github.com/obviahq/obvia/blob/main/internal/application/remediation/idempotency_cache.go) |
| Append-only JSONL audit log | [`internal/application/remediation/audit_log.go`](https://github.com/obviahq/obvia/blob/main/internal/application/remediation/audit_log.go) |
| Docker + Prometheus capability handlers | [`internal/application/remediation/capabilities.go`](https://github.com/obviahq/obvia/blob/main/internal/application/remediation/capabilities.go) |
| Inline action executor + policy stub | [`internal/application/remediation/inline_client.go`](https://github.com/obviahq/obvia/blob/main/internal/application/remediation/inline_client.go) |

## What did not survive

These components exist at the `v0.3.1-final` tag but were not lifted into
any new library because their value was tied to running Praxis as a service:

- HTTP / gRPC API server and middleware
- Out-of-process plugin host with Fulcio keyless signing
- Vendor handlers (Slack, GitHub, Linear, HTTP, Calendar, Email) —
  duplicated by first-party MCP servers and direct SDKs
- Capability registry as a service
- Praxis MCP server

## Recovery path

```bash
git clone https://github.com/felixgeelhaar/praxis.git
cd praxis
git checkout v0.3.1-final
```

The repo remains read-only and publicly readable. Source, tags, and history
are preserved.

## Replacement guidance

| If you wanted... | Use instead |
|---|---|
| Vendor capability execution from an agent | Agent runtime's MCP servers (Slack/GitHub/Linear all have first-party MCP) |
| Programmatic action execution with safety primitives | [Obvia's inlined remediation package](https://github.com/obviahq/obvia/tree/main/internal/application/remediation) — copy what you need |
| Action / outcome storage | [Mnemos](https://github.com/felixgeelhaar/mnemos) `record_action` / `record_outcome` MCP tools |
| Policy gating | OPA, Kyverno, or inline the small allow/deny engine from this repo |

## Related archives

- [Olymp](https://github.com/felixgeelhaar/olymp) — orchestration runtime, archived
- [Nous](https://github.com/felixgeelhaar/nous) — reasoning service, archived; risk + intervention survive in [decisionkit](https://github.com/felixgeelhaar/decisionkit)
