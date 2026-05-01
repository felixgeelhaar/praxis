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
	RateLimit    *RateLimitConfig
}

type RetryConfig struct {
	MaxAttempts  int
	InitialDelay int64
	MaxDelay     int64
	Multiplier   float64
}

// RateLimitConfig caps per-caller invocations of a capability.
//
// Rate is the steady-state token replenishment per Interval; Burst is the
// maximum number of tokens the bucket can hold (allowing short bursts above
// Rate). When Burst is 0 it defaults to Rate.
type RateLimitConfig struct {
	Rate     int
	Burst    int
	Interval int64 // nanoseconds; 0 → 1 second
}
