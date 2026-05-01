
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
