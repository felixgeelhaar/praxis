// Package cgroup probes the host for cgroup v2 support and the
// presence of a delegated subtree the praxis runtime can use to
// enforce per-plugin memory and CPU caps. Phase 5 out-of-process
// resource isolation (M3.1 follow-up).
//
// Detection is the only thing this package does; spawning the child
// into a cgroup and applying memory.max / cpu.max happens in
// follow-up tasks (t-cgroup-v2-spawn / t-cgroup-v2-usage-metrics).
//
// Linux-only. The other-OS file returns Status{Available: false} so
// the caller can fall back to setrlimit without a build error on
// macOS or other developer platforms.
package cgroup

// Status reports what the runtime can rely on at startup. Available
// is the headline: when false, the caller falls back to setrlimit
// (the Phase 4 mechanism) and emits Reason in a structured log so
// operators understand why the stricter mechanism was unavailable.
type Status struct {
	Available    bool
	Root         string // e.g. /sys/fs/cgroup/praxis
	UnifiedMount string // e.g. /sys/fs/cgroup
	Reason       string // human-readable fallback explanation when Available=false
}
