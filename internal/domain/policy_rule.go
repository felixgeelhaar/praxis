package domain

import "time"

type PolicyRule struct {
	ID         string
	Capability string
	CallerType string
	Scope      []string
	Decision   string
	Reason     string
	Priority   int    // lower runs first; rules with equal priority break ties on ID
	OrgID      string // Phase 3 M3.3: tenant org scope; "" matches any org
	TeamID     string // Phase 3 M3.3: tenant team scope; "" matches any team
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
