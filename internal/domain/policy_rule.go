package domain

import "time"

type PolicyRule struct {
	ID         string
	Capability string
	CallerType string
	Scope      []string
	Decision   string
	Reason     string
	Priority   int // lower runs first; rules with equal priority break ties on ID
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
