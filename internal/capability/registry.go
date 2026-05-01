package capability

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

var (
	ErrUnknownCapability = errors.New("unknown capability")
	ErrEmptyOrgID        = errors.New("orgID required for tenant registration")
	// ErrIncompatibleSchema is returned by Register when the new
	// capability introduces a breaking change against the previous
	// version and the registry is configured in strict compat mode.
	// Phase 5 schema versioning.
	ErrIncompatibleSchema = errors.New("capability schema introduces breaking change")
)

// CompatChecker compares previous and new capability snapshots and
// returns a list of breaking changes. The internal/schema package
// supplies the production implementation; tests can inject a stub.
type CompatChecker func(prev, next domain.Capability) []CompatIssue

// CompatIssue is the structural shape of a breaking change. Mirrors
// schema.Issue without importing the schema package, so the registry
// stays decoupled from the checker implementation.
type CompatIssue struct {
	Code    string
	Field   string
	Message string
}

// CompatMode mirrors schema.CompatMode at the registry boundary.
type CompatMode string

const (
	CompatOff    CompatMode = "off"
	CompatWarn   CompatMode = "warn"
	CompatStrict CompatMode = "strict"
)

type Handler interface {
	Name() string
	Execute(ctx context.Context, payload map[string]any) (map[string]any, error)
	Simulate(ctx context.Context, payload map[string]any) (map[string]any, error)
}

// Describer is implemented by handlers that publish a full Capability
// descriptor (schemas, permissions, simulatable, idempotent flags).
// Registry.Register prefers it over the synthetic default.
type Describer interface {
	Capability() domain.Capability
}

// Compensator is implemented by handlers that can reverse a successfully
// executed action — for example, deleting an issue created by
// github_create_issue, or cancelling a meeting whose ICS was emitted.
//
// Compensate receives the original action's payload and result. It returns
// the compensating action's output (audit-only — not re-played through the
// regular pipeline) and an error if the reversal failed. Best-effort
// reversals (vendors that cannot exactly undo the side effect) should
// surface that in the output payload.
type Compensator interface {
	Compensate(ctx context.Context, originalPayload, originalOutput map[string]any) (map[string]any, error)
}

// Registry holds two layers of capabilities:
//
//   - Global: visible to every caller (Register / Get* / List).
//   - Tenant-private: visible only to callers within the same OrgID
//     (RegisterTenant / *ForCaller).
//
// Resolution order for *ForCaller methods is tenant-private first, then
// global. This lets a tenant override a globally-registered capability
// with a local implementation without rewiring the executor.
//
// Phase 3 M3.3.
type Registry struct {
	mu sync.RWMutex

	handlers     map[string]Handler
	capabilities map[string]domain.Capability

	tenantHandlers     map[string]map[string]Handler
	tenantCapabilities map[string]map[string]domain.Capability

	compatMode    CompatMode
	compatChecker CompatChecker
	onBreak       func(capName string, issues []CompatIssue)
}

func New() *Registry {
	return &Registry{
		handlers:           make(map[string]Handler),
		capabilities:       make(map[string]domain.Capability),
		tenantHandlers:     make(map[string]map[string]Handler),
		tenantCapabilities: make(map[string]map[string]domain.Capability),
		compatMode:         CompatOff,
	}
}

// SetCompatMode configures the schema compatibility check applied at
// Register time. When mode is CompatOff the checker is skipped. When
// CompatStrict, Register returns ErrIncompatibleSchema if the new
// capability introduces a breaking change against the previous
// registration. CompatWarn invokes onBreak (if set) but allows the
// registration to succeed. Phase 5 schema versioning.
func (r *Registry) SetCompatMode(mode CompatMode, checker CompatChecker, onBreak func(string, []CompatIssue)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compatMode = mode
	r.compatChecker = checker
	r.onBreak = onBreak
}

// Register adds a handler to the global registry, visible to every caller.
func (r *Registry) Register(h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	desc := describe(h)
	if r.compatMode != CompatOff && r.compatChecker != nil {
		if prev, ok := r.capabilities[h.Name()]; ok {
			if issues := r.compatChecker(prev, desc); len(issues) > 0 {
				if r.onBreak != nil {
					r.onBreak(h.Name(), issues)
				}
				if r.compatMode == CompatStrict {
					return fmt.Errorf("%w: %s introduces %d issue(s)", ErrIncompatibleSchema, h.Name(), len(issues))
				}
			}
		}
	}
	r.handlers[h.Name()] = h
	r.capabilities[h.Name()] = desc
	return nil
}

// RegisterTenant adds a handler to the registry visible only to callers
// whose CallerRef.OrgID matches orgID. Returns ErrEmptyOrgID when orgID
// is empty — anonymous tenant scoping defeats the isolation guarantee.
func (r *Registry) RegisterTenant(orgID string, h Handler) error {
	if orgID == "" {
		return ErrEmptyOrgID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tenantHandlers[orgID] == nil {
		r.tenantHandlers[orgID] = make(map[string]Handler)
		r.tenantCapabilities[orgID] = make(map[string]domain.Capability)
	}
	r.tenantHandlers[orgID][h.Name()] = h
	r.tenantCapabilities[orgID][h.Name()] = describe(h)
	return nil
}

// Unregister removes a globally-registered handler by name. Returns
// silently if no handler with that name exists — used by the plugin
// Manager during crash recovery, where the caller has already lost
// authoritative state.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handlers, name)
	delete(r.capabilities, name)
}

// UnregisterTenant removes a tenant-scoped handler by name. Mirrors
// Unregister for the per-org registries.
func (r *Registry) UnregisterTenant(orgID, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.tenantHandlers[orgID]; ok {
		delete(h, name)
	}
	if c, ok := r.tenantCapabilities[orgID]; ok {
		delete(c, name)
	}
}

// GetHandler resolves a global handler. Tenant-private handlers are not
// reachable through this method; use GetHandlerForCaller instead.
func (r *Registry) GetHandler(name string) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	if !ok {
		return nil, ErrUnknownCapability
	}
	return h, nil
}

// GetHandlerForCaller resolves a handler for a specific caller. Tenant-
// private handlers (registered via RegisterTenant for caller.OrgID) take
// precedence over globally registered ones — a tenant can override a
// global capability locally. Anonymous callers (empty OrgID) only see
// global handlers.
func (r *Registry) GetHandlerForCaller(name string, caller domain.CallerRef) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if caller.OrgID != "" {
		if tenant, ok := r.tenantHandlers[caller.OrgID]; ok {
			if h, ok := tenant[name]; ok {
				return h, nil
			}
		}
	}
	if h, ok := r.handlers[name]; ok {
		return h, nil
	}
	return nil, ErrUnknownCapability
}

func (r *Registry) ListCapabilities(ctx context.Context) ([]domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]domain.Capability, 0, len(r.capabilities))
	for _, c := range r.capabilities {
		list = append(list, c)
	}
	return list, nil
}

// ListCapabilitiesForCaller returns every global capability plus the
// caller's tenant-private capabilities. A tenant override of a global cap
// is represented once, with the tenant descriptor winning.
func (r *Registry) ListCapabilitiesForCaller(_ context.Context, caller domain.CallerRef) ([]domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	merged := make(map[string]domain.Capability, len(r.capabilities))
	for n, c := range r.capabilities {
		merged[n] = c
	}
	if caller.OrgID != "" {
		if tenant, ok := r.tenantCapabilities[caller.OrgID]; ok {
			for n, c := range tenant {
				merged[n] = c // tenant wins over global
			}
		}
	}
	list := make([]domain.Capability, 0, len(merged))
	for _, c := range merged {
		list = append(list, c)
	}
	return list, nil
}

func (r *Registry) GetCapability(name string) (domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.capabilities[name]
	if !ok {
		return domain.Capability{}, ErrUnknownCapability
	}
	return c, nil
}

// GetCapabilityForCaller mirrors GetHandlerForCaller for descriptors.
func (r *Registry) GetCapabilityForCaller(name string, caller domain.CallerRef) (domain.Capability, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if caller.OrgID != "" {
		if tenant, ok := r.tenantCapabilities[caller.OrgID]; ok {
			if c, ok := tenant[name]; ok {
				return c, nil
			}
		}
	}
	if c, ok := r.capabilities[name]; ok {
		return c, nil
	}
	return domain.Capability{}, ErrUnknownCapability
}

func describe(h Handler) domain.Capability {
	if d, ok := h.(Describer); ok {
		return d.Capability()
	}
	return domain.Capability{
		Name:        h.Name(),
		Simulatable: true,
		Idempotent:  true,
	}
}
