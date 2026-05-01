
## Plugin pipeline integration into cmd/praxis startup

Plugin discovery, signature verification, ABI load and sandbox wrapping are implemented as libraries but cmd/praxis bootstrap does not call them. Phase 4 wires the full pipeline: at startup, scan PRAXIS_PLUGIN_DIR, verify each Discovered against PRAXIS_PLUGIN_TRUSTED_KEYS, dlopen the artefact (Go plugin package), retrieve the exported `Plugin` symbol, call plugin.Load with the runtime registry as the Loader. Failures must (a) log the offending plugin and reason via bolt, (b) emit a metric praxis_plugin_load_total{result=...}, (c) refuse to start the server when the operator opts into strict mode (PRAXIS_PLUGIN_STRICT=1). Healthy plugins surface in /v1/capabilities. Cross-platform: Linux + macOS only (Go plugin package limitation). Replace the placeholder registerHandlers wiring with a registerCorePlus(loader) flow that registers built-in handlers first, plugins second, and rejects duplicate names.

---

## Audit retention scheduler

Service.PurgeExpired enforces per-tenant retention windows but no background worker invokes it. Add a scheduler that runs PurgeExpired every PRAXIS_AUDIT_RETENTION_INTERVAL (default 1h), logs deletion counts per tenant, exposes praxis_audit_purge_total{org_id,result}. Scheduler shares the bootstrap context so it stops on SIGTERM; first run happens after 5 minutes of uptime to avoid blocking startup. Failure to purge for one tenant must not stop the sweep for others (per-tenant errors logged, surfaced in metric, sweep continues).

---

## MCP and CLI tenant-aware capability discovery

HTTP /v1/capabilities derives a Caller from X-Praxis-* headers and uses ListCapabilitiesForCaller, but the MCP surface and the CLI still call the global ListCapabilities. Phase 4 plumbs caller context end-to-end: MCP server reads tenant identity from the MCP session/handshake (mcp-go conventions) and routes through ListCapabilitiesForCaller; CLI gains --org and --team flags on `praxis caps list` and `praxis caps show`. Anonymous CLI invocations stay global-only. The runtime contract is: every capability discovery path is tenant-aware or explicitly anonymous; there is no third option that silently leaks private capabilities.

---

## Out-of-process plugin loader for true resource isolation

In-process sandbox enforces CPU timeout and HTTP egress allowlist but cannot enforce MaxMemoryBytes (Go's allocator is shared with the host process) and cannot stop a malicious plugin that bypasses plugin.HTTPClient. Phase 4 spec: each plugin runs in a child process spawned from a praxis-pluginhost binary; parent communicates via gRPC over stdin/stdout (or unix socket); host applies cgroups (Linux) / setrlimit (BSDs) for CPU and memory; network egress filtered at the host firewall layer using the AllowedHosts policy. Plugin ABI v1 stays unchanged from the plugin author's perspective: the praxis-pluginhost shim implements the Plugin interface and forwards each call across the IPC boundary. Failure modes documented: child crash → registration removed + metric incremented; OOM → MaxMemoryBytes enforced (the field gains real teeth).

---

## Plugin lifecycle: hot reload and rotation

Today plugins are loaded once at startup and stay until process exit. Phase 4 adds: filesystem watch on PRAXIS_PLUGIN_DIR using fsnotify; on manifest.json or .sig change, re-verify and re-load the affected plugin atomically (drain in-flight calls, swap registration); SIGHUP triggers a full re-scan; graceful version rollover where the old version completes its in-flight calls while new traffic routes to the new version; CLI `praxis plugins list` shows loaded plugins with version + signature digest; `praxis plugins reload <name>` forces a single-plugin reload. Operator can opt out of file-watch via PRAXIS_PLUGIN_AUTORELOAD=0.

---

## OpenTelemetry tracing across executor and handlers

Today every action lifecycle event hits the audit log but there is no distributed-trace propagation: a Praxis Execute called from an upstream service shows up as a black box in the caller's trace. Phase 5 adds OTel: every Execute, DryRun, Resume, and Revert opens a span; handler Execute / Simulate / Compensate inherit a child span; outbound HTTP requests via plugin.HTTPClient inject W3C traceparent so vendor latency lands in the same trace. Span attributes carry capability name, action ID, status, policy decision, and tenant OrgID. OTLP exporter wires up via PRAXIS_OTLP_ENDPOINT (gRPC) and PRAXIS_OTLP_PROTOCOL (grpc|http). Sampling configurable via PRAXIS_TRACE_SAMPLE (0..1). Existing audit log stays canonical for Praxis-internal lifecycle replay; OTel is for system-wide correlation.

---

## cgroups v2 enforcement for out-of-process plugins (Linux)

Phase 4's setrlimit-based budget enforcement caps the plugin host process but leaks across fork/exec inside the plugin and gives no cumulative metrics. cgroups v2 closes both gaps on Linux. Phase 5 work: parent creates a per-plugin cgroup under /sys/fs/cgroup/praxis/&lt;plugin-name&gt;.scope; writes memory.max + cpu.max from ResourceBudget; spawns the praxis-pluginhost child via clone3 with CLONE_INTO_CGROUP so the child enters the cgroup atomically (no race window); reads memory.peak + cpu.stat at plugin reload to surface usage in /metrics. Falls back to setrlimit when cgroup mount unavailable (containers without delegated cgroups, non-systemd hosts). Required permission: the praxis user needs to own the cgroup subtree; document setup in docs/cgroups.md. macOS path stays setrlimit-only.

---

## Capability schema versioning and compatibility checker

Today a capability's InputSchema can change between plugin reloads with no breaking-change check. A handler that accepted {to, body} could be silently replaced by one demanding {recipient, message_body} and existing callers would discover the break only at runtime. Phase 5 adds schema versioning: domain.Capability gains InputSchemaVersion + OutputSchemaVersion (semver-loose); registry rejects a re-registration whose major version dropped backward-compat (renamed required field, narrowed enum, tightened type). Compatibility check is configurable per registration: strict (refuse downgrade), warn (audit-only), off (current behaviour). Adds praxis_capability_breaking_change_total{capability,outcome} metric. Per-capability changelog rendered in /v1/capabilities/{name}.

---

## Performance benchmark suite and SLOs

Praxis has no published latency or throughput targets. Operators have no way to tell whether their deployment is healthy beyond &quot;tests pass.&quot; Phase 5 establishes: benchmark suite under bench/ exercising the full Execute path against the in-memory backend (synthetic handler, no vendor I/O), the SQLite backend, and the Postgres backend; targets baked into Makefile (make bench) and CI (regression detection via benchstat); published SLOs — p50 Execute latency &lt; 2ms (memory) / &lt; 10ms (sqlite) / &lt; 25ms (postgres) at 100 concurrent calls; throughput floor — 5k actions/sec on memory backend. Out-of-process plugin path benchmarked separately so operators can budget the IPC cost. Bench results auto-published to docs/benchmarks.md on tag.

---

## Federated MCP: aggregate upstream MCP servers as Praxis capabilities

Praxis sits in the cognitive stack as the execution layer; agents reach it via MCP. Phase 5 turns Praxis into an MCP federation hub so an agent talking to one Praxis instance can reach every tool the operator has approved across multiple upstream MCP servers — without each tool re-implementing policy/audit. Implementation: cmd/praxis adds an mcp.federation.yaml config listing upstream MCP servers (URL or stdio command + token); a federation goroutine connects to each upstream, fetches its tool catalogue, and registers a federatedHandler per upstream tool. Calls flow through the same executor (policy → schema → idempotency → audit → emit) before being forwarded. Audit detail records the upstream server identity. Failures (upstream down, auth rejected) deregister gracefully via the same Watchable pattern the out-of-process loader uses. Schema is fetched at federation time and surfaced unchanged — operators see one tool catalogue. Phase 5's biggest ecosystem play.

---
