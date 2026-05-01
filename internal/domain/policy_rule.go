package domain

import "time"

type PolicyRule struct {
	ID         string
	Capability string
	CallerType string
	Scope      []string
	Decision   string
	Reason     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
