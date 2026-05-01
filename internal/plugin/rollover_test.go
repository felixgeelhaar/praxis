package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

type blockingHandler struct {
	name    string
	gate    chan struct{}
	started chan struct{}
	calls   int
}

func newBlockingHandler(name string) *blockingHandler {
	return &blockingHandler{name: name, gate: make(chan struct{}), started: make(chan struct{}, 1)}
}

func (h *blockingHandler) Name() string { return h.name }
func (h *blockingHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	h.calls++
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.gate
	return map[string]any{"name": h.name}, nil
}
func (h *blockingHandler) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.Execute(ctx, p)
}

func TestVersionedHandler_DrainBlocksUntilExecuteReturns(t *testing.T) {
	inner := newBlockingHandler("h")
	wrap, vh := wrapForVersioning(inner, 1)

	go func() {
		_, _ = wrap.Execute(context.Background(), nil)
	}()
	<-inner.started

	drained := make(chan struct{})
	go func() {
		vh.Drain()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("Drain returned before Execute completed")
	case <-time.After(40 * time.Millisecond):
	}

	close(inner.gate)
	select {
	case <-drained:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Drain did not return after Execute completed")
	}
}

func TestVersionedHandler_DrainCtxCancellation(t *testing.T) {
	inner := newBlockingHandler("h")
	wrap, vh := wrapForVersioning(inner, 1)
	defer close(inner.gate)

	go func() {
		_, _ = wrap.Execute(context.Background(), nil)
	}()
	<-inner.started

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := vh.DrainCtx(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("DrainCtx err=%v want DeadlineExceeded", err)
	}
}

func TestVersionedHandler_NewVersionTakesNewTraffic(t *testing.T) {
	old := newBlockingHandler("old")
	wrapOld, vhOld := wrapForVersioning(old, 1)

	fresh := newBlockingHandler("new")
	wrapNew, _ := wrapForVersioning(fresh, 2)

	// Old call started.
	var oldOut map[string]any
	oldDone := make(chan struct{})
	go func() {
		oldOut, _ = wrapOld.Execute(context.Background(), nil)
		close(oldDone)
	}()
	<-old.started

	// Mark old retired and start new traffic; new handler should run
	// the new wrapper.
	vhOld.Retire()
	if !vhOld.IsRetired() {
		t.Error("IsRetired should be true after Retire")
	}

	var newOut map[string]any
	newDone := make(chan struct{})
	go func() {
		newOut, _ = wrapNew.Execute(context.Background(), nil)
		close(newDone)
	}()
	<-fresh.started

	close(fresh.gate)
	<-newDone
	if newOut["name"] != "new" {
		t.Errorf("new traffic ran on %v want new", newOut["name"])
	}

	close(old.gate)
	<-oldDone
	if oldOut["name"] != "old" {
		t.Errorf("in-flight old call ran on %v want old", oldOut["name"])
	}
}

type compensatingHandler struct{ blockingHandler }

func (compensatingHandler) Compensate(_ context.Context, _ map[string]any, _ map[string]any) (map[string]any, error) {
	return map[string]any{"reverted": true}, nil
}

func TestWrapForVersioning_PreservesCompensator(t *testing.T) {
	inner := &compensatingHandler{blockingHandler: blockingHandler{name: "c", gate: make(chan struct{}, 1), started: make(chan struct{}, 1)}}
	wrap, _ := wrapForVersioning(inner, 1)
	c, ok := wrap.(capability.Compensator)
	if !ok {
		t.Fatal("compensator interface lost through wrapper")
	}
	out, err := c.Compensate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Compensate: %v", err)
	}
	if out["reverted"] != true {
		t.Errorf("out=%v", out)
	}
}

func TestWrapForVersioning_NonCompensatorStaysNonCompensator(t *testing.T) {
	inner := newBlockingHandler("plain")
	defer close(inner.gate)
	wrap, _ := wrapForVersioning(inner, 1)
	if _, ok := wrap.(capability.Compensator); ok {
		t.Error("non-compensating inner should not gain Compensator interface through wrapper")
	}
}

type describingHandler struct {
	blockingHandler
	desc domain.Capability
}

func (h describingHandler) Capability() domain.Capability { return h.desc }

func TestVersionedHandler_PreservesDescriber(t *testing.T) {
	inner := &describingHandler{
		blockingHandler: blockingHandler{name: "d", gate: make(chan struct{}, 1), started: make(chan struct{}, 1)},
		desc:            domain.Capability{Name: "d", Description: "via plugin"},
	}
	wrap, _ := wrapForVersioning(inner, 1)
	d, ok := wrap.(capability.Describer)
	if !ok {
		t.Fatal("Describer interface lost through wrapper")
	}
	if d.Capability().Description != "via plugin" {
		t.Errorf("description=%q lost through wrapper", d.Capability().Description)
	}
}

func TestVersionedHandler_ConcurrentExecute(t *testing.T) {
	inner := &countingHandler{name: "c"}
	wrap, vh := wrapForVersioning(inner, 1)

	const N = 25
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wrap.Execute(context.Background(), nil)
		}()
	}
	wg.Wait()
	vh.Drain()
	if inner.calls != N {
		t.Errorf("calls=%d want %d", inner.calls, N)
	}
}

type countingHandler struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (h *countingHandler) Name() string { return h.name }
func (h *countingHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	return nil, nil
}
func (h *countingHandler) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.Execute(ctx, p)
}
