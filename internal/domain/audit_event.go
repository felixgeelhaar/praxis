package domain

import "time"

type AuditEvent struct {
	ID        string
	ActionID  string
	Kind      string
	Detail    map[string]any
	CreatedAt time.Time
}
