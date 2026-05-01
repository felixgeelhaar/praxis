package domain

import "time"

type Result struct {
	ActionID    string
	Status      ActionStatus
	Output      map[string]any
	ExternalID  string
	StartedAt   time.Time
	CompletedAt time.Time
	Attempts    int
	Error       *ActionError
}
