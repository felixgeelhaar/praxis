package federation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/mcp/federation"
)

// stubRegistry records register/unregister calls without depending
// on the full capability.Registry implementation.
type stubRegistry struct {
	mu           sync.Mutex
	registered   map[string]bool
	unregistered []string
}

func newStubRegistry() *stubRegistry {
	return &stubRegistry{registered: map[string]bool{}}
}

func (r *stubRegistry) Register(h capability.Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered[h.Name()] = true
	return nil
}

func (r *stubRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unregistered = append(r.unregistered, name)
	delete(r.registered, name)
}

func (r *stubRegistry) isRegistered(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registered[name]
}

func TestManager_RegistersToolsOnConnect(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "vendor-x", Command: []string{"/bin/x"}},
	}}
	reg := newStubRegistry()
	mgr := federation.NewManager(cfg, reg)
	conn := &federation.Connection{
		UpstreamName: "vendor-x",
		Tools: []federation.Tool{
			{Name: "create_ticket"},
			{Name: "close_ticket"},
		},
	}
	mgr.Supervisor().Connect = func(_ context.Context, _ federation.Upstream) (*federation.Connection, error) {
		return conn, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	if !reg.isRegistered("vendor-x__create_ticket") {
		t.Error("create_ticket not registered")
	}
	if !reg.isRegistered("vendor-x__close_ticket") {
		t.Error("close_ticket not registered")
	}
	loaded := mgr.LoadedCapabilities()
	if len(loaded["vendor-x"]) != 2 {
		t.Errorf("LoadedCapabilities=%v", loaded)
	}
}

func TestManager_DeregistersOnDisconnect(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "flap", Command: []string{"/bin/flap"}},
	}}
	reg := newStubRegistry()
	mgr := federation.NewManager(cfg, reg)
	mgr.Supervisor().Backoff.InitialDelay = 5 * time.Millisecond

	conn := federation.NewConnectionForTest("flap", []federation.Tool{{Name: "send"}})
	connectAttempts := 0
	mgr.Supervisor().Connect = func(_ context.Context, _ federation.Upstream) (*federation.Connection, error) {
		connectAttempts++
		if connectAttempts == 1 {
			return conn, nil
		}
		// Subsequent reconnects keep failing so the test stays in
		// the "down" branch.
		return nil, errors.New("upstream gone")
	}

	statusCh := make(chan string, 4)
	mgr.OnStatus = func(_ string, status string) {
		statusCh <- status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go mgr.Run(ctx)

	if got := <-statusCh; got != federation.StatusUp {
		t.Fatalf("first status=%s want up", got)
	}

	// Force a transport failure on the live connection.
	federation.TriggerCloseForTest(conn, errors.New("EOF"))

	if got := <-statusCh; got != federation.StatusDown {
		t.Fatalf("disconnect status=%s want down", got)
	}

	loaded := mgr.LoadedCapabilities()
	if _, ok := loaded["flap"]; ok {
		t.Errorf("expected flap removed from LoadedCapabilities: %+v", loaded)
	}
	if reg.isRegistered("flap__send") {
		t.Error("flap__send should be unregistered after disconnect")
	}
}

func TestManager_OnStatusFiresUpAndDown(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "broken", Command: []string{"/bin/broken"}},
	}}
	reg := newStubRegistry()
	mgr := federation.NewManager(cfg, reg)
	mgr.Supervisor().Backoff.InitialDelay = 5 * time.Millisecond
	mgr.Supervisor().Connect = func(_ context.Context, _ federation.Upstream) (*federation.Connection, error) {
		return nil, errors.New("dial failed")
	}
	statusCh := make(chan string, 8)
	mgr.OnStatus = func(_ string, status string) {
		statusCh <- status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	mgr.Run(ctx)

	close(statusCh)
	var sawDown bool
	for s := range statusCh {
		if s == federation.StatusDown {
			sawDown = true
		}
	}
	if !sawDown {
		t.Error("OnStatus(down) never fired despite consistent connect failures")
	}
}
