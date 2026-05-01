//go:build darwin

package main

import (
	"fmt"
	"syscall"
)

// applyResourceBudget enforces the budget via Darwin setrlimit. macOS
// supports RLIMIT_CPU the same way Linux does. RLIMIT_AS exists but is
// unreliable on Darwin — we use RLIMIT_DATA as the closest substitute
// for the heap ceiling. RLIMIT_DATA caps the data segment; combined
// with the Go runtime's heap-mostly allocation pattern it gives a
// usable approximation. Operators who need stricter isolation should
// run on Linux + cgroups v2, which Phase 4 lands as a follow-up.
func applyResourceBudget(b budget) error {
	if b.cpuSeconds > 0 {
		lim := syscall.Rlimit{Cur: b.cpuSeconds, Max: b.cpuSeconds}
		if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &lim); err != nil {
			return fmt.Errorf("RLIMIT_CPU: %w", err)
		}
	}
	if b.memBytes > 0 {
		lim := syscall.Rlimit{Cur: b.memBytes, Max: b.memBytes}
		if err := syscall.Setrlimit(syscall.RLIMIT_DATA, &lim); err != nil {
			return fmt.Errorf("RLIMIT_DATA: %w", err)
		}
	}
	return nil
}
