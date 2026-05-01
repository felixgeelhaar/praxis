package domain

import "time"

type ActionMode string

const (
	ModeSync  ActionMode = "sync"
	ModeAsync ActionMode = "async"
)

type ActionStatus string

const (
	StatusPending   ActionStatus = "pending"
	StatusValidated ActionStatus = "validated"
	StatusExecuting ActionStatus = "executing"
	StatusSucceeded ActionStatus = "succeeded"
	StatusFailed    ActionStatus = "failed"
	StatusSimulated ActionStatus = "simulated"
	StatusRejected  ActionStatus = "rejected"
)

type Action struct {
	ID             string
	Capability     string
	Payload        map[string]any
	Caller         CallerRef
	Scope          []string
	IdempotencyKey string
	Mode           ActionMode
	Status         ActionStatus
	Result         map[string]any
	Error          *ActionError
	PolicyDecision *PolicyDecision
	ExecutedAt     *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
