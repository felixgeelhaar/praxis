package domain

import "time"

type PolicyDecision struct {
	Decision    string
	RuleID      string
	Reason      string
	Rule        *PolicyRule
	EvaluatedAt time.Time
}
