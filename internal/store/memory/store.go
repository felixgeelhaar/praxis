// Package memory is the reference in-process backend for the storage ports.
//
// It is the canonical implementation: the SQLite and Postgres backends must
// pass the same shared store contract suite (internal/store/storetest).
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// New returns a fully-wired in-memory ports.Repos.
func New() *ports.Repos {
	return &ports.Repos{
		Capability:  newCapabilityRepo(),
		Action:      newActionRepo(),
		Idempotency: newIdempotencyRepo(),
		Audit:       newAuditRepo(),
		Policy:      newPolicyRepo(),
		Outbox:      newOutboxRepo(),
		Close:       func() error { return nil },
	}
}

// --- capabilities ---

type capabilityRepo struct {
	mu   sync.RWMutex
	caps map[string]domain.Capability
}

func newCapabilityRepo() *capabilityRepo {
	return &capabilityRepo{caps: make(map[string]domain.Capability)}
}

func (r *capabilityRepo) Upsert(_ context.Context, c domain.Capability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps[c.Name] = c
	return nil
}

func (r *capabilityRepo) Get(_ context.Context, name string) (domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.caps[name]
	if !ok {
		return domain.Capability{}, ports.ErrNotFound
	}
	return c, nil
}

func (r *capabilityRepo) List(_ context.Context) ([]domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Capability, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// --- actions ---

type actionRepo struct {
	mu      sync.RWMutex
	actions map[string]domain.Action
}

func newActionRepo() *actionRepo {
	return &actionRepo{actions: make(map[string]domain.Action)}
}

func (r *actionRepo) Save(_ context.Context, a domain.Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = time.Now()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = a.UpdatedAt
	}
	r.actions[a.ID] = a
	return nil
}

func (r *actionRepo) Get(_ context.Context, id string) (domain.Action, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actions[id]
	if !ok {
		return domain.Action{}, ports.ErrNotFound
	}
	return a, nil
}

func (r *actionRepo) UpdateStatus(_ context.Context, id string, s domain.ActionStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Status = s
	a.UpdatedAt = time.Now()
	r.actions[id] = a
	return nil
}

func (r *actionRepo) PutResult(_ context.Context, id string, res domain.Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actions[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Result = res.Output
	a.Status = res.Status
	a.Error = res.Error
	completed := res.CompletedAt
	a.CompletedAt = &completed
	a.UpdatedAt = time.Now()
	r.actions[id] = a
	return nil
}

// --- idempotency ---

type idempotencyEntry struct {
	result    domain.Result
	expiresAt time.Time
}

type idempotencyRepo struct {
	mu   sync.RWMutex
	keys map[string]idempotencyEntry
}

func newIdempotencyRepo() *idempotencyRepo {
	return &idempotencyRepo{keys: make(map[string]idempotencyEntry)}
}

func (r *idempotencyRepo) Lookup(_ context.Context, key string) (*domain.Result, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.keys[key]
	if !ok {
		return nil, ports.ErrNotFound
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		return nil, ports.ErrNotFound
	}
	res := e.result
	return &res, nil
}

func (r *idempotencyRepo) Remember(_ context.Context, key string, res domain.Result, ttl int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(time.Duration(ttl) * time.Second)
	}
	r.keys[key] = idempotencyEntry{result: res, expiresAt: expires}
	return nil
}

// --- audit ---

type auditRepo struct {
	mu     sync.RWMutex
	events []domain.AuditEvent
}

func newAuditRepo() *auditRepo {
	return &auditRepo{}
}

func (r *auditRepo) Append(_ context.Context, e domain.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	r.events = append(r.events, e)
	return nil
}

func (r *auditRepo) ListForAction(_ context.Context, actionID string) ([]domain.AuditEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.AuditEvent, 0)
	for _, e := range r.events {
		if e.ActionID == actionID {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r *auditRepo) Search(_ context.Context, q ports.AuditQuery) ([]domain.AuditEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.AuditEvent, 0)
	for _, e := range r.events {
		if q.Capability != "" {
			cap, _ := e.Detail["capability"].(string)
			if !strings.EqualFold(cap, q.Capability) {
				continue
			}
		}
		if q.CallerType != "" {
			ct, _ := e.Detail["caller_type"].(string)
			if !strings.EqualFold(ct, q.CallerType) {
				continue
			}
		}
		if q.From > 0 && e.CreatedAt.Unix() < q.From {
			continue
		}
		if q.To > 0 && e.CreatedAt.Unix() > q.To {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// --- policy ---

type policyRepo struct {
	mu    sync.RWMutex
	rules map[string]domain.PolicyRule
}

func newPolicyRepo() *policyRepo {
	return &policyRepo{rules: make(map[string]domain.PolicyRule)}
}

func (r *policyRepo) ListRules(_ context.Context) ([]domain.PolicyRule, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.PolicyRule, 0, len(r.rules))
	for _, rule := range r.rules {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *policyRepo) UpsertRule(_ context.Context, rule domain.PolicyRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.ID] = rule
	return nil
}

func (r *policyRepo) DeleteRule(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

// --- outbox ---

type outboxRepo struct {
	mu      sync.RWMutex
	entries map[string]ports.OutboxEnvelope
}

func newOutboxRepo() *outboxRepo {
	return &outboxRepo{entries: make(map[string]ports.OutboxEnvelope)}
}

func (r *outboxRepo) Enqueue(_ context.Context, env ports.OutboxEnvelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if env.CreatedAt.IsZero() {
		env.CreatedAt = time.Now()
	}
	r.entries[env.ID] = env
	return nil
}

func (r *outboxRepo) NextBatch(_ context.Context, limit int, before time.Time) ([]ports.OutboxEnvelope, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ports.OutboxEnvelope, 0)
	for _, e := range r.entries {
		if e.DeliveredAt != nil {
			continue
		}
		if e.NextAttempt.After(before) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextAttempt.Before(out[j].NextAttempt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *outboxRepo) MarkDelivered(_ context.Context, id string, deliveredAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return ports.ErrNotFound
	}
	e.DeliveredAt = &deliveredAt
	r.entries[id] = e
	return nil
}

func (r *outboxRepo) BumpAttempt(_ context.Context, id string, nextAttempt time.Time, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return ports.ErrNotFound
	}
	e.Attempts++
	e.NextAttempt = nextAttempt
	e.LastError = lastError
	r.entries[id] = e
	return nil
}
