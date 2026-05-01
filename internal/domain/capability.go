package domain

// Capability is a named, schema'd, permissioned unit of side effect.
// It is a contract — not the handler that runs it.
//
// InputSchemaVersion / OutputSchemaVersion are semver strings (e.g.
// "1", "1.2", "2.0.0"). The compatibility checker (Phase 5) uses them
// to decide whether a re-registration breaks existing callers; missing
// values are treated as "1" so legacy capabilities behave as if every
// reload is a v1 upgrade until the operator opts in to versioning.
type Capability struct {
	Name                string
	Description         string
	InputSchema         any
	InputSchemaVersion  string
	OutputSchema        any
	OutputSchemaVersion string
	Permissions         []string
	Simulatable         bool
	Idempotent          bool
	Retry               *RetryConfig
	RateLimit           *RateLimitConfig
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
