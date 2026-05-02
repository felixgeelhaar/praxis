# Changelog

All notable changes to Praxis are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged and published via tag-triggered CI; this file is the human-readable summary.

## [Unreleased]

### Planned — Phase 6

See [docs/backlog.md](docs/backlog.md). Highlights: MCP federation HTTP transport once mcp-go ships it, Sigstore Fulcio keyless plugin verification, in-process mTLS on the HTTP API, out-of-process loader as a config flag, persistent capability changelog, security CI gate via OpenVEX.

## [0.1.0] — 2026-05-02

First tagged release. Covers Phases 1–5 of the roadmap.

### Added — Phase 5 (production-grade observability + federation)

- **OpenTelemetry tracing**: tracer provider bootstrap with OTLP/gRPC + OTLP/HTTP exporters wired via `PRAXIS_OTLP_ENDPOINT`, `PRAXIS_OTLP_PROTOCOL`, `PRAXIS_OTLP_INSECURE`, `PRAXIS_TRACE_SAMPLE`. `executor.Execute / DryRun / Resume / Revert` open root spans with `praxis.action.id`, `praxis.capability`, `praxis.outcome`, tenant scopes; `handler.<capability>` child span captures vendor latency. Sandbox HTTP client wraps `otelhttp.NewTransport` so outbound vendor calls carry W3C `traceparent`. Per-tool spans on the MCP surface.
- **Capability schema versioning**: `domain.Capability.InputSchemaVersion` + `OutputSchemaVersion` (default `"1"`); migration 004 on sqlite + postgres. `internal/schema.CheckCompat` detects breaking changes (required field added/removed, type changed, enum narrowed) on re-registration. Modes: `off` (default) / `warn` / `strict` via `PRAXIS_SCHEMA_COMPAT`. `GET /v1/capabilities/{name}/changelog` renders the in-memory history of breaking re-registrations.
- **Performance benchmarks + regression gate**: `BenchmarkExecute_Memory` / `_Parallel`, `BenchmarkDryRun_Memory`, `BenchmarkProcessOpener_Execute` / `_Parallel`. `make bench` for exploratory runs; `make bench-check` compares against `bench/baseline.txt` via `cmd/benchcheck` and fails on >1.20× regressions. `cmd/benchdocs` renders `docs/benchmarks.md` from raw output; tag-triggered `bench-publish.yml` workflow opens a PR with the latest snapshot on each release.
- **cgroup v2 enforcement (Linux)**: `internal/plugin/cgroup` detects the unified mount + delegated subtree at startup. `ProcessOpener.CgroupParent` creates a per-plugin cgroup before fork/exec, writes `memory.max` + `cpu.max` from `ResourceBudget`, attaches the child PID after `Start`, and reclaims the directory on kill. `praxis_plugin_memory_peak_bytes` (gauge) + `praxis_plugin_cpu_seconds_total` (counter) surface kernel-recorded usage in `/metrics`. macOS / non-Linux paths fall through to `setrlimit` (Phase 4).
- **Federated MCP**: aggregate upstream MCP servers as Praxis capabilities. `mcp.federation.yaml` (path via `PRAXIS_MCP_FEDERATION_CONFIG`) declares stdio-transport upstreams with optional `Token` + `Allow` allowlist. Tools register under `<upstream>__<tool>` namespaces and run through the same executor pipeline as local handlers. `Supervisor` reconnects on transport failure with exponential backoff (1s → 30s, ×2). `praxis_mcp_federation_status{upstream,status}` gauge reports per-upstream connection state.

### Added — Phase 4 (M3.1 lifecycle, MCP, CLI tenancy)

- **Plugin pipeline integration into `cmd/praxis` startup**: `loadPlugins` runs `Discover` → `LoadTrustedKeys` → `VerifyDiscovered` → `dlopen` → `plugin.Load` against the runtime registry. `PRAXIS_PLUGIN_STRICT=1` aborts startup on any per-plugin error. `praxis_plugin_load_total{result}` records every load attempt by outcome.
- **Audit retention scheduler**: `audit.NewScheduler` ticks every `PRAXIS_AUDIT_RETENTION_INTERVAL` (default 1h) after `PRAXIS_AUDIT_RETENTION_INITIAL_DELAY` (default 5m). `praxis_audit_purge_total{org_id,result}` counter via `OnPurge` hook.
- **MCP + CLI tenant-aware capability discovery**: MCP `list_capabilities` accepts `org_id`/`team_id`; `Execute` and per-tool calls populate `Action.Caller.OrgID/TeamID`. CLI `praxis caps list/show` accept `--org=<id>` / `--team=<id>` flags.
- **Out-of-process plugin loader scaffolding**: `cmd/praxis-pluginhost` child binary loads one `.so` and serves IPC over stdin/stdout (line-delimited JSON-RPC, no protobuf dep). `internal/plugin/process.go` `ProcessOpener` proxies the `Plugin` interface; `processPlugin` correlates concurrent calls via a pending map keyed by `Frame.ID`. `BudgetedPlugin` declares CPU + memory limits enforced by `setrlimit` on Linux + Darwin (env-var protocol: `PRAXIS_PLUGIN_BUDGET_CPU_SEC`, `PRAXIS_PLUGIN_BUDGET_MEM_BYTES`). Crash recovery via the new `Watchable` interface: dispatcher's terminal error fires `ResultCrashed` and deregisters the plugin's capabilities.
- **Plugin lifecycle**: fsnotify-driven hot reload of `PRAXIS_PLUGIN_DIR` (toggle via `PRAXIS_PLUGIN_AUTORELOAD`); SIGHUP forces a full re-scan; graceful rollover with in-flight drain via `versionedHandler` (Retire/Drain/DrainCtx); `praxis plugins list / reload <name>` CLI subcommand backed by `GET /v1/plugins` and `POST /v1/plugins/{name}/reload`.

### Added — Phase 3

- Capability registry tenant scoping (`RegisterTenant`, `GetHandlerForCaller`, `ListCapabilitiesForCaller`); audit retention windows + cross-tenant access controls (`SearchForCaller`, `ListForActionByCaller`, `ErrCrossTenantAccess`); plugin discovery (`PRAXIS_PLUGIN_DIR` + `manifest.json`); cosign-blob signature verification (`PRAXIS_PLUGIN_TRUSTED_KEYS`); in-process resource sandbox (CPU timeout + egress allowlist via `Sandboxed`).

### Added — earlier phases

- DDD domain types, statekit-backed Action FSM, repository ports, multi-backend storage (memory + sqlite + postgres), capability registry + schema validator, policy engine (allow / deny / scoped rules), idempotency keeper, handler runner with retry + circuit breaker + Retry-After, executor (Execute / DryRun / Resume / Revert), audit log + replay-from-audit canary, outcome writeback to Mnemos via outbox, async actions + jobs runner + webhook callbacks, HTTP API via axi-go, MCP surface via mcp-go, public Go client, real handlers (slack, email, http, github, linear, calendar), per-capability rate limiting, audit export + dashboards.
