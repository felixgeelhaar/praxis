package domain

type CallerRef struct {
	Type   string
	ID     string
	Name   string
	OrgID  string // Phase 3 M3.3: tenant org
	TeamID string // Phase 3 M3.3: tenant team within OrgID
}
