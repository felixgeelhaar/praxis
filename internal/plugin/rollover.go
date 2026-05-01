package plugin

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// versionedHandler wraps a plugin-supplied handler so the Manager can
// retire it gracefully on reload. Every Execute and Simulate call
// increments an in-flight WaitGroup; Drain blocks until those calls
// return. New traffic always lands on the latest registration in the
// registry — Go's GC keeps the old wrapper alive as long as there are
// in-flight goroutines referencing it, so callers in mid-flight finish
// on the version they started with.
//
// version is informational: the Manager bumps it monotonically per
// reload so logs and metrics can correlate "version 7 of pagerduty
// drained at 14:02".
type versionedHandler struct {
	inner    capability.Handler
	version  uint64
	inflight sync.WaitGroup

	retired atomic.Bool // set by Retire; surfaced via IsRetired for tests/diagnostics

	// describer and compensator capture optional interfaces the inner
	// handler may implement, hoisted to wrapper-level methods so the
	// registry's Describer detection and the executor's Compensator
	// resolution see through the wrapper.
	describer   capability.Describer
	compensator capability.Compensator
}

func newVersionedHandler(inner capability.Handler, version uint64) *versionedHandler {
	v := &versionedHandler{inner: inner, version: version}
	if d, ok := inner.(capability.Describer); ok {
		v.describer = d
	}
	if c, ok := inner.(capability.Compensator); ok {
		v.compensator = c
	}
	return v
}

// Name implements capability.Handler.
func (v *versionedHandler) Name() string { return v.inner.Name() }

// Execute proxies through to the wrapped handler while tracking the
// in-flight count.
func (v *versionedHandler) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	v.inflight.Add(1)
	defer v.inflight.Done()
	return v.inner.Execute(ctx, payload)
}

// Simulate proxies through to the wrapped handler while tracking the
// in-flight count. Dry-runs participate in drain so reloads block on
// long-running simulations the same way they do on real executes.
func (v *versionedHandler) Simulate(ctx context.Context, payload map[string]any) (map[string]any, error) {
	v.inflight.Add(1)
	defer v.inflight.Done()
	return v.inner.Simulate(ctx, payload)
}

// Capability forwards to the wrapped handler when it implements
// Describer. Defining the method directly on versionedHandler lets the
// registry's type-assertion pick it up through the wrapper.
func (v *versionedHandler) Capability() domain.Capability {
	if v.describer != nil {
		return v.describer.Capability()
	}
	// Fall back to the synthetic descriptor the registry would
	// otherwise generate. Keeping this in lock-step with
	// registry.describe avoids a behavioural skew when an unwrapped
	// handler becomes wrapped on reload.
	return domain.Capability{Name: v.inner.Name(), Simulatable: true, Idempotent: true}
}

// Compensate forwards to the wrapped handler when it implements
// Compensator. Required so the executor's Revert flow finds the
// compensator through the wrapper. The method is only present at
// runtime when the inner handler implemented Compensator at wrap time
// — see compensatableHandler below.
func (v *versionedHandler) Compensate(ctx context.Context, originalPayload, originalOutput map[string]any) (map[string]any, error) {
	v.inflight.Add(1)
	defer v.inflight.Done()
	return v.compensator.Compensate(ctx, originalPayload, originalOutput)
}

// Retire marks the wrapper as no longer the active version. Drain
// callers can spin until IsRetired is true to confirm the swap.
func (v *versionedHandler) Retire() { v.retired.Store(true) }

// IsRetired reports whether Retire has been called.
func (v *versionedHandler) IsRetired() bool { return v.retired.Load() }

// Drain blocks until every in-flight call against this version has
// returned. The Manager swaps to the new wrapper before Retire+Drain,
// so by definition no new calls arrive on this wrapper after Retire.
func (v *versionedHandler) Drain() { v.inflight.Wait() }

// DrainCtx is Drain with cancellation. Returns ctx.Err if the context
// fires before in-flight calls complete; useful for bounded reload
// timeouts.
func (v *versionedHandler) DrainCtx(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		v.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wrapForVersioning returns a capability.Handler that fronts inner
// with version-tracking. When inner implements Compensator the
// returned wrapper also satisfies Compensator (via versionedHandler's
// embedded method); otherwise the Compensate method panics if called,
// which the executor never does because GetHandler returns the bare
// interface and the type assertion to capability.Compensator fails.
//
// To make the type-assertion behave the same as the underlying
// handler, we return a separate type when inner is not a Compensator.
func wrapForVersioning(inner capability.Handler, version uint64) (handler capability.Handler, vh *versionedHandler) {
	v := newVersionedHandler(inner, version)
	if v.compensator != nil {
		return v, v
	}
	return &noncompensatingWrapper{v: v}, v
}

// noncompensatingWrapper exposes Execute/Simulate/Name/Capability but
// not Compensate, so a runtime assertion `_, ok := h.(Compensator)`
// returns false — preserving the inner handler's lack of compensation
// support through the wrapper.
type noncompensatingWrapper struct{ v *versionedHandler }

func (n *noncompensatingWrapper) Name() string { return n.v.Name() }
func (n *noncompensatingWrapper) Execute(ctx context.Context, p map[string]any) (map[string]any, error) {
	return n.v.Execute(ctx, p)
}
func (n *noncompensatingWrapper) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return n.v.Simulate(ctx, p)
}
func (n *noncompensatingWrapper) Capability() domain.Capability { return n.v.Capability() }
