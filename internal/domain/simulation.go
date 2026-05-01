package domain

type Simulation struct {
	ActionID       string
	PolicyDecision PolicyDecision
	Validation     ValidationReport
	Preview        map[string]any
	Reversible     bool
}

type ValidationReport struct {
	Valid  bool
	Errors []string
}
