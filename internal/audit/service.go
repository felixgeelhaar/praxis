package audit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// ErrCrossTenantAccess signals that a non-empty CallerRef.OrgID attempted
// to read or purge audit data outside its own org. The Service surfaces
// this rather than silently filtering — the caller should know its query
// was rejected (Phase 3 M3.3 access control).
var ErrCrossTenantAccess = errors.New("cross-tenant audit access denied")

// Service is the audit log facade exposed to the executor and HTTP layer.
type Service struct {
	repo      ports.AuditRepo
	retention RetentionPolicy
}

// RetentionPolicy maps an OrgID to its audit retention window. The empty
// key carries the default applied to events that have no orgID stamp
// (system / anonymous events) or to tenants with no override. A zero
// duration means "retain forever" and disables the purge for that scope.
//
// Example:
//
//	RetentionPolicy{
//	    "":       365 * 24 * time.Hour, // default 1 year
//	    "org-x":  90  * 24 * time.Hour, // org-x retains 90 days
//	    "org-y":  0,                    // org-y opts out of purge
//	}
type RetentionPolicy map[string]time.Duration

// New constructs a Service with no retention policy configured. Callers
// that need per-tenant retention call WithRetention before scheduling
// PurgeExpired.
func New(repo ports.AuditRepo) *Service {
	return &Service{repo: repo}
}

// WithRetention installs a retention policy. Returns the Service so it
// composes with constructor-style wiring.
func (s *Service) WithRetention(p RetentionPolicy) *Service {
	s.retention = p
	return s
}

func (s *Service) Append(ctx context.Context, event domain.AuditEvent) error {
	return s.repo.Append(ctx, event)
}

func (s *Service) ListForAction(ctx context.Context, actionID string) ([]domain.AuditEvent, error) {
	return s.repo.ListForAction(ctx, actionID)
}

// ListForActionByCaller is access-controlled: a caller with a non-empty
// OrgID may only read events whose OrgID matches; mismatches surface
// ErrCrossTenantAccess. Anonymous callers (empty OrgID) bypass the check
// to preserve backward-compatibility with system tooling.
func (s *Service) ListForActionByCaller(ctx context.Context, actionID string, caller domain.CallerRef) ([]domain.AuditEvent, error) {
	events, err := s.repo.ListForAction(ctx, actionID)
	if err != nil {
		return nil, err
	}
	if caller.OrgID == "" {
		return events, nil
	}
	for _, e := range events {
		if e.OrgID != "" && e.OrgID != caller.OrgID {
			return nil, ErrCrossTenantAccess
		}
	}
	return events, nil
}

func (s *Service) Search(ctx context.Context, q ports.AuditQuery) ([]domain.AuditEvent, error) {
	return s.repo.Search(ctx, q)
}

// SearchForCaller scopes a Search to the caller's tenant. Callers with a
// non-empty OrgID have q.OrgID rewritten to match — even if they tried
// to query a different tenant's events, the rewrite forces the result
// set into their own org. Anonymous callers (empty OrgID) pass through
// unchanged so background sweeps and CLI tools keep working.
func (s *Service) SearchForCaller(ctx context.Context, q ports.AuditQuery, caller domain.CallerRef) ([]domain.AuditEvent, error) {
	if caller.OrgID != "" {
		if q.OrgID != "" && q.OrgID != caller.OrgID {
			return nil, ErrCrossTenantAccess
		}
		q.OrgID = caller.OrgID
	}
	return s.repo.Search(ctx, q)
}

// Purge deletes all audit events older than `before` for the given org.
// An empty orgID purges across every tenant — callers must ensure that
// is the intended scope (typically only the global retention sweep does
// this with orgID == "" and a uniform cutoff).
func (s *Service) Purge(ctx context.Context, orgID string, before time.Time) (int64, error) {
	return s.repo.PurgeBefore(ctx, orgID, before.Unix())
}

// PurgeExpired walks the configured RetentionPolicy and deletes events
// older than each tenant's retention window. Returns the per-tenant
// deletion counts so the caller can record metrics. Tenants with a zero
// duration are skipped (retain-forever semantics).
//
// The default entry (key "") applies to events whose OrgID is empty —
// it does not act as a fallback for tenants missing from the policy.
// Tenants without an explicit entry are intentionally retained: a new
// org should not silently inherit the default.
func (s *Service) PurgeExpired(ctx context.Context, now time.Time) (map[string]int64, error) {
	if len(s.retention) == 0 {
		return map[string]int64{}, nil
	}
	out := make(map[string]int64, len(s.retention))
	for orgID, window := range s.retention {
		if window <= 0 {
			continue
		}
		cutoff := now.Add(-window)
		deleted, err := s.repo.PurgeBefore(ctx, orgID, cutoff.Unix())
		if err != nil {
			return out, err
		}
		out[orgID] = deleted
	}
	return out, nil
}

func (s *Service) Format(event domain.AuditEvent) string {
	data, _ := json.MarshalIndent(event.Detail, "", "  ")
	return string(data)
}
