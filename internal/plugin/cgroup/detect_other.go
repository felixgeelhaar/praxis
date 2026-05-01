//go:build !linux

package cgroup

// DefaultRootDir is unused on non-Linux platforms; declared so call
// sites can reference the same name without a build tag.
const DefaultRootDir = ""

// UnifiedMount is unused on non-Linux platforms.
const UnifiedMount = ""

// Detect always reports unavailable on non-Linux hosts. The runtime
// falls back to setrlimit (the Phase 4 mechanism) on darwin/BSD and
// the no-op limiter on windows.
func Detect() Status {
	return Status{Reason: "cgroup v2 not supported on this platform; falling back to setrlimit"}
}

// DetectAt mirrors Linux's signature so the caller does not branch on
// build tag. Always reports unavailable.
func DetectAt(_, _ string) Status { return Detect() }
