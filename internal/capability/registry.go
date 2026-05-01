package capability

import (
	"context"
	"errors"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

var ErrUnknownCapability = errors.New("unknown capability")

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

type Registry struct {
	handlers     map[string]Handler
	capabilities map[string]domain.Capability
}

func New() *Registry {
	return &Registry{
		handlers:     make(map[string]Handler),
		capabilities: make(map[string]domain.Capability),
	}
}

func (r *Registry) Register(h Handler) error {
	r.handlers[h.Name()] = h
	if d, ok := h.(Describer); ok {
		r.capabilities[h.Name()] = d.Capability()
	} else {
		r.capabilities[h.Name()] = domain.Capability{
			Name:        h.Name(),
			Simulatable: true,
			Idempotent:  true,
		}
	}
	return nil
}

func (r *Registry) GetHandler(name string) (Handler, error) {
	h, ok := r.handlers[name]
	if !ok {
		return nil, ErrUnknownCapability
	}
	return h, nil
}

func (r *Registry) ListCapabilities(ctx context.Context) ([]domain.Capability, error) {
	list := make([]domain.Capability, 0, len(r.capabilities))
	for _, c := range r.capabilities {
		list = append(list, c)
	}
	return list, nil
}

func (r *Registry) GetCapability(name string) (domain.Capability, error) {
	c, ok := r.capabilities[name]
	if !ok {
		return domain.Capability{}, ErrUnknownCapability
	}
	return c, nil
}
