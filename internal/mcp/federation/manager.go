package federation

import (
	"context"
	"sync"

	"github.com/felixgeelhaar/praxis/internal/capability"
)

// Status reports an upstream's connection state to the metrics layer.
// Stable string constants — operators alert on these.
const (
	StatusUp   = "up"
	StatusDown = "down"
)

// Registry is the subset of *capability.Registry the federation
// Manager needs. Defined as an interface so tests can pass a stub
// without wiring a full registry.
type Registry interface {
	Register(h capability.Handler) error
	Unregister(name string)
}

// Manager wires a Supervisor to the capability registry. Each
// upstream's tools register on connect and unregister on transport
// failure — same crash-recovery shape the plugin Manager uses for
// out-of-process plugins.
//
// Phase 5 federated MCP.
type Manager struct {
	cfg      Config
	reg      Registry
	OnStatus func(upstream, status string)

	supervisor *Supervisor

	mu          sync.Mutex
	upstreamCap map[string][]string // upstream name -> registered capability names
}

// NewManager constructs a Manager with the registry adapter wired.
func NewManager(cfg Config, reg Registry) *Manager {
	m := &Manager{
		cfg:         cfg,
		reg:         reg,
		upstreamCap: map[string][]string{},
	}
	m.supervisor = NewSupervisor(cfg)
	m.supervisor.OnConnect = m.handleConnect
	m.supervisor.OnDisconnect = m.handleDisconnect
	return m
}

// Run blocks until ctx is cancelled. Convenience wrapper around the
// supervisor; bootstraps that need direct supervisor access can pass
// the Manager's Supervisor field instead.
func (m *Manager) Run(ctx context.Context) { m.supervisor.Run(ctx) }

// Supervisor returns the underlying Supervisor so tests can inject a
// fake Connect or tweak Backoff without re-implementing the wiring.
func (m *Manager) Supervisor() *Supervisor { return m.supervisor }

func (m *Manager) handleConnect(conn *Connection) {
	regs := Registrations(conn)
	names := make([]string, 0, len(regs))
	for _, r := range regs {
		if err := m.reg.Register(r.Handler); err != nil {
			// Registry rejection (compat-strict mode, etc.) leaves the
			// already-registered tools in place. The supervisor will
			// fire OnDisconnect → handleDisconnect on transport
			// failure, which sweeps everything. No partial-state
			// leak risk.
			continue
		}
		names = append(names, r.Capability.Name)
	}
	m.mu.Lock()
	m.upstreamCap[conn.UpstreamName] = names
	m.mu.Unlock()

	if m.OnStatus != nil {
		m.OnStatus(conn.UpstreamName, StatusUp)
	}
}

func (m *Manager) handleDisconnect(upstream string, _ error) {
	m.mu.Lock()
	names := m.upstreamCap[upstream]
	delete(m.upstreamCap, upstream)
	m.mu.Unlock()
	for _, n := range names {
		m.reg.Unregister(n)
	}
	if m.OnStatus != nil {
		m.OnStatus(upstream, StatusDown)
	}
}

// LoadedCapabilities returns a snapshot of which capabilities each
// upstream currently contributes. Used by tests + future admin
// endpoints.
func (m *Manager) LoadedCapabilities() map[string][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]string, len(m.upstreamCap))
	for k, v := range m.upstreamCap {
		out[k] = append([]string(nil), v...)
	}
	return out
}
