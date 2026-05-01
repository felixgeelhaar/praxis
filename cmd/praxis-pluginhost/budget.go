package main

import (
	"fmt"
	"os"
	"strconv"
)

// Environment variables the parent (Praxis ProcessOpener) sets on the
// child to convey the plugin's declared ResourceBudget. Names are part
// of the wire contract — never rename without bumping a protocol
// version.
const (
	envCPUSeconds = "PRAXIS_PLUGIN_BUDGET_CPU_SEC"
	envMemBytes   = "PRAXIS_PLUGIN_BUDGET_MEM_BYTES"
)

// budget is the parsed shape of the env-vars. Zero fields disable
// the corresponding limit.
type budget struct {
	cpuSeconds uint64
	memBytes   uint64
}

// readBudgetEnv parses the child's resource-budget environment. Bad
// values fail the host startup rather than silently running unlimited
// — an operator who set the env var wants the limit, not "didn't
// notice the typo and got root-equivalent access by accident."
func readBudgetEnv() (budget, error) {
	var b budget
	if v := os.Getenv(envCPUSeconds); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return b, fmt.Errorf("%s: %w", envCPUSeconds, err)
		}
		b.cpuSeconds = n
	}
	if v := os.Getenv(envMemBytes); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return b, fmt.Errorf("%s: %w", envMemBytes, err)
		}
		b.memBytes = n
	}
	return b, nil
}

// applyBudgetFromEnv reads the budget env-vars and asks the
// platform-specific applyResourceBudget to enforce them via
// setrlimit. The function returns nil when no env-vars are set so
// development workflows that spawn pluginhost directly don't fail
// with "no budget."
func applyBudgetFromEnv() error {
	b, err := readBudgetEnv()
	if err != nil {
		return err
	}
	if b.cpuSeconds == 0 && b.memBytes == 0 {
		return nil
	}
	return applyResourceBudget(b)
}
