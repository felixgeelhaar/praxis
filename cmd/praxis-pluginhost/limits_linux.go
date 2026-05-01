//go:build linux

package main

import (
	"fmt"
	"syscall"
)

// applyResourceBudget enforces the budget via Linux setrlimit:
//
//   - CPU: RLIMIT_CPU caps cumulative process CPU seconds. The kernel
//     sends SIGXCPU on soft limit and SIGKILL on hard limit; we set
//     both to the same value so a runaway plugin terminates.
//   - Memory: RLIMIT_AS caps the process virtual address space. AS
//     limits work better than DATA on Linux because the Go runtime's
//     mmap-heavy allocator otherwise sails past DATA limits.
//
// Phase 4 first cut. cgroup v2 (memory.max, cpu.max) lands in a
// follow-up: it is the only mechanism that survives subprocess
// fork/exec inside the plugin and cleanly handles cumulative metrics.
func applyResourceBudget(b budget) error {
	if b.cpuSeconds > 0 {
		lim := syscall.Rlimit{Cur: b.cpuSeconds, Max: b.cpuSeconds}
		if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &lim); err != nil {
			return fmt.Errorf("RLIMIT_CPU: %w", err)
		}
	}
	if b.memBytes > 0 {
		lim := syscall.Rlimit{Cur: b.memBytes, Max: b.memBytes}
		if err := syscall.Setrlimit(syscall.RLIMIT_AS, &lim); err != nil {
			return fmt.Errorf("RLIMIT_AS: %w", err)
		}
	}
	return nil
}
