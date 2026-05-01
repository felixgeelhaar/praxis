package audit

// Lifecycle kinds (TDD §5.2 / §5.3, Roadmap M1.5).
//
// These constants are the canonical taxonomy of audit events. Adding a new
// kind requires a corresponding test in the replay-from-audit canary so the
// audit log remains a complete reconstruction of the action's lifecycle.
const (
	KindReceived    = "received"
	KindValidated   = "validated"
	KindPolicy      = "policy"
	KindExecuted    = "executed"
	KindSucceeded   = "succeeded"
	KindFailed      = "failed"
	KindSimulated   = "simulated"
	KindRejected    = "rejected"
	KindThrottled   = "policy_throttled"
	KindCompensated = "compensated"
)
