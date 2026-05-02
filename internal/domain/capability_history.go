package domain

import "time"

// CapabilityHistoryEntry records one breaking-change re-registration
// of a capability. The registry's compat checker writes one of these
// per re-registration that produces issues. Phase 6 t-changelog-schema.
type CapabilityHistoryEntry struct {
	ID                string
	CapabilityName    string
	RecordedAt        time.Time
	PrevInputVersion  string
	PrevOutputVersion string
	NextInputVersion  string
	NextOutputVersion string
	Issues            []CapabilityHistoryIssue
}

// CapabilityHistoryIssue mirrors capability.CompatIssue / schema.Issue
// at the domain layer so the persisted shape stays decoupled from the
// in-memory checker types.
type CapabilityHistoryIssue struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}
