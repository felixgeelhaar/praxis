//go:build !linux && !darwin

package main

import (
	"fmt"
	"os"
)

// applyResourceBudget on unsupported platforms (windows, freebsd, etc.)
// emits a warning and returns nil so the host still serves the plugin
// — operators who need real enforcement should run Linux (cgroups v2)
// or Darwin (setrlimit).
func applyResourceBudget(b budget) error {
	if b.cpuSeconds > 0 || b.memBytes > 0 {
		fmt.Fprintf(os.Stderr,
			"praxis-pluginhost: resource limits requested (cpu=%ds mem=%dB) but this platform has no enforcement; running unbounded\n",
			b.cpuSeconds, b.memBytes)
	}
	return nil
}
