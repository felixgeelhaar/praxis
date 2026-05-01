package ports

import (
	"context"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

type MnemosWriter interface {
	AppendEvent(ctx context.Context, event domain.MnemosEvent) error
}

type MnemosEvent struct {
	Type       string
	ActionID   string
	Capability string
	Caller     domain.CallerRef
	Status     string
	ExternalID string
	Timestamp  int64
}
