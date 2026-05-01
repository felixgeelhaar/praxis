package domain

type MnemosEvent struct {
	Type       string
	ActionID   string
	Capability string
	Caller     CallerRef
	Status     string
	ExternalID string
	Timestamp  int64
}
