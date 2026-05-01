package domain

import "time"

// AuditEvent records one lifecycle moment of an Action. The (OrgID, TeamID)
// pair is stamped at append time from the action's caller and drives both
// per-tenant retention and access controls (Phase 3 M3.3): an org's
// retention window is applied independently of every other tenant's, and
// audit reads enforce that a caller only sees their own org's events.
type AuditEvent struct {
	ID        string
	ActionID  string
	Kind      string
	OrgID     string
	TeamID    string
	Detail    map[string]any
	CreatedAt time.Time
}
