package domain

// Capability is a named, schema'd, permissioned unit of side effect.
// It is a contract — not the handler that runs it.
type Capability struct {
	Name         string
	Description  string
	InputSchema  any
	OutputSchema any
	Permissions  []string
	Simulatable  bool
	Idempotent   bool
	Retry        *RetryConfig
}

type RetryConfig struct {
	MaxAttempts  int
	InitialDelay int64
	MaxDelay     int64
	Multiplier   float64
}
