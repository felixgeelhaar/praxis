package ports

import (
	"context"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

type CapabilityRepo interface {
	Upsert(ctx context.Context, c domain.Capability) error
	Get(ctx context.Context, name string) (domain.Capability, error)
	List(ctx context.Context) ([]domain.Capability, error)
}

type ActionRepo interface {
	Save(ctx context.Context, a domain.Action) error
	Get(ctx context.Context, id string) (domain.Action, error)
	UpdateStatus(ctx context.Context, id string, s domain.ActionStatus) error
	PutResult(ctx context.Context, id string, r domain.Result) error
	// ListPendingAsync returns up to `limit` async actions still in the
	// validated state — the queue the jobs runner drains.
	ListPendingAsync(ctx context.Context, limit int) ([]domain.Action, error)
}

type IdempotencyRepo interface {
	Lookup(ctx context.Context, key string) (*domain.Result, error)
	Remember(ctx context.Context, key string, r domain.Result, ttl int64) error
}

type AuditRepo interface {
	Append(ctx context.Context, e domain.AuditEvent) error
	ListForAction(ctx context.Context, actionID string) ([]domain.AuditEvent, error)
	Search(ctx context.Context, q AuditQuery) ([]domain.AuditEvent, error)
	// PurgeBefore deletes audit rows created strictly before `before`
	// whose org_id is exactly orgID. An empty orgID targets only events
	// that were stamped with no org (anonymous / system events). Always
	// exact-match — there is no global cross-tenant purge through this
	// port, so a misconfigured retention policy cannot leak between
	// tenants. Returns the number of rows deleted. Phase 3 M3.3.
	PurgeBefore(ctx context.Context, orgID string, before int64) (int64, error)
}

type AuditQuery struct {
	Capability string
	CallerType string
	OrgID      string // Phase 3 M3.3: tenant scope; "" matches any org
	From, To   int64
}

type PolicyRepo interface {
	ListRules(ctx context.Context) ([]domain.PolicyRule, error)
	UpsertRule(ctx context.Context, r domain.PolicyRule) error
	DeleteRule(ctx context.Context, id string) error
}

// CapabilityHistoryRepo persists the per-capability changelog the
// registry's compat checker writes when a re-registration introduces
// breaking changes. Phase 6 — replaces the in-memory implementation
// from Phase 5 so the audit-of-schema-drift survives restarts.
type CapabilityHistoryRepo interface {
	Append(ctx context.Context, e domain.CapabilityHistoryEntry) error
	ListForCapability(ctx context.Context, name string) ([]domain.CapabilityHistoryEntry, error)
}
