# Changelog

All notable changes to Praxis are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged and published via tag-triggered CI; this file is the human-readable summary.

## [Unreleased]

No unreleased changes.

## [0.2.0] — 2026-05-02

Phase 6 closes out: production hardening + supply-chain security.

### Added — Phase 6

- **HTTP API TLS + mTLS**: server-side TLS via `PRAXIS_TLS_CERT_FILE` / `PRAXIS_TLS_KEY_FILE`; mutual TLS via `PRAXIS_MTLS_CLIENT_CA_FILE` (requires TLS). `cmd/praxis/tls.go` `tlsLoader` swaps the active cert via `atomic.Pointer` on SIGHUP so rotation is zero-downtime.
- **Out-of-process plugin loader as a config flag**: `PRAXIS_PLUGIN_OUT_OF_PROCESS=1` switches the runtime to `ProcessOpener` for kernel-enforced resource limits; in-process `DefaultOpener` remains the default. Requires `PRAXIS_PLUGINHOST_BINARY=/path/to/praxis-pluginhost`.
- **Persistent capability change history**: `capability_history` table on sqlite + postgres (migration 005); `domain.CapabilityHistoryEntry` + `ports.CapabilityHistoryRepo` + adapters across all three backends. `Registry.SetHistoryRepo` mirrors every breaking-change entry into the repo; `GET /v1/capabilities/{name}/changelog` reads through it so the changelog survives restarts.
- **Sigstore Fulcio keyless plugin verification**: `internal/plugin.KeylessVerifier` + `LoadFulcioRoots` + identity-bound trust policy `(SubjectGlob, Issuer)`. `PRAXIS_PLUGIN_FULCIO_ROOTS` / `PLUGIN_FULCIO_SUBJECTS` / `PLUGIN_FULCIO_ISSUER` opt operators in. Pipeline dispatches between PEM-key (`<artifact>.sig`) and keyless (`<artifact>.cert`) per plugin so a fleet can migrate one at a time. Stdlib-only; no sigstore-go dependency.
- **MCP federation HTTP transport**: federation upstreams configured with `url:` now connect via `client.HTTPTransport` from mcp-go v1.10. New `Upstream` fields: `ca_bundle` (PEM bundle pinned for the upstream's TLS cert) and `insecure_skip_verify` (dev only). Token forwarded as `Authorization: Bearer ...`.
- **Security baseline + CI gate**: `.nox/vex.json` (OpenVEX v0.2.0) carries one statement per firing nox rule with `status` / `justification` / `impact_statement`. `security.yml` runs `nox scan` on every PR + push, uploads SARIF + SBOM, and now hard-fails when a finding category has no VEX statement — accepting a finding is a deliberate commit, not silent drift.

### Changed

- mcp-go bumped from v1.9.0 to v1.10.0.
- `ErrURLTransportUnsupported` retained as a deprecated sentinel; HTTP federation no longer returns it.
- `findings.json` snapshot dropped from the repo (nox 0.7.0 has no `--exclude` and recursively scans its own JSON, drowning real signal — see Nox-HQ/nox#38). CI scans regenerate findings each run.

### Fixed

- Lint sweep: `cap` / `max` / `strat` shadowing fixes; HTTP body-close defers tightened; `revive` exported-name rule disabled project-wide for parity with mnemos / chronos.
- Goreleaser archive-count mismatch (`praxis-pluginhost` only builds linux+darwin); CI golangci-lint bumped to v2.5.0 for Go 1.26 compatibility.

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
