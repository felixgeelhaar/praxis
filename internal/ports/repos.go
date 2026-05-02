package ports

import (
	"context"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

// OutboxRepo persists outcome envelopes for asynchronous delivery to Mnemos.
// The outbox decouples Execute() from Mnemos availability.
type OutboxRepo interface {
	Enqueue(ctx context.Context, env OutboxEnvelope) error
	NextBatch(ctx context.Context, limit int, before time.Time) ([]OutboxEnvelope, error)
	MarkDelivered(ctx context.Context, id string, deliveredAt time.Time) error
	BumpAttempt(ctx context.Context, id string, nextAttempt time.Time, lastError string) error
}

// OutboxEnvelope wraps a Mnemos event with delivery metadata.
type OutboxEnvelope struct {
	ID          string
	ActionID    string
	Event       domain.MnemosEvent
	Attempts    int
	NextAttempt time.Time
	DeliveredAt *time.Time
	LastError   string
	CreatedAt   time.Time
}

// Repos is the storage facade. Every backend (memory, sqlite, postgres)
// returns a fully-wired *Repos. The executor and other layers depend only
// on these interfaces.
type Repos struct {
	Capability        CapabilityRepo
	Action            ActionRepo
	Idempotency       IdempotencyRepo
	Audit             AuditRepo
	Policy            PolicyRepo
	Outbox            OutboxRepo
	CapabilityHistory CapabilityHistoryRepo
	Close             func() error
}
