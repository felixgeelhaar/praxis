package federation_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/mcp/federation"
)

func TestSupervisor_FiresOnConnectPerUpstream(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "a", Command: []string{"/bin/a"}},
		{Name: "b", Command: []string{"/bin/b"}},
	}}
	sup := federation.NewSupervisor(cfg)
	sup.Connect = func(_ context.Context, u federation.Upstream) (*federation.Connection, error) {
		return &federation.Connection{UpstreamName: u.Name}, nil
	}
	var (
		mu      sync.Mutex
		connect []string
	)
	sup.OnConnect = func(c *federation.Connection) {
		mu.Lock()
		defer mu.Unlock()
		connect = append(connect, c.UpstreamName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	sup.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(connect) != 2 {
		t.Errorf("OnConnect=%v want both upstreams", connect)
	}
}

func TestSupervisor_ReconnectsAfterFailure(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "flaky", Command: []string{"/bin/flaky"}},
	}}
	sup := federation.NewSupervisor(cfg)
	sup.Backoff.InitialDelay = 10 * time.Millisecond
	sup.Backoff.MaxDelay = 20 * time.Millisecond
	sup.Backoff.Multiplier = 2.0

	var attempts int32
	failOnce := atomic.Bool{}

	conn := &federation.Connection{UpstreamName: "flaky"}
	sup.Connect = func(_ context.Context, u federation.Upstream) (*federation.Connection, error) {
		atomic.AddInt32(&attempts, 1)
		if !failOnce.Swap(true) {
			return nil, errors.New("dial failed")
		}
		return conn, nil
	}

	connected := make(chan struct{})
	sup.OnConnect = func(_ *federation.Connection) {
		select {
		case connected <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sup.Run(ctx)

	select {
	case <-connected:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("never connected after reconnect; attempts=%d", atomic.LoadInt32(&attempts))
	}
	if atomic.LoadInt32(&attempts) < 2 {
		t.Errorf("attempts=%d want ≥2", atomic.LoadInt32(&attempts))
	}
}

func TestSupervisor_ContextCancelStopsLoop(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "x", Command: []string{"/bin/x"}},
	}}
	sup := federation.NewSupervisor(cfg)
	sup.Connect = func(_ context.Context, _ federation.Upstream) (*federation.Connection, error) {
		return &federation.Connection{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sup.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

func TestSupervisor_OnDisconnectFiresOnFailure(t *testing.T) {
	cfg := federation.Config{Upstreams: []federation.Upstream{
		{Name: "down", Command: []string{"/bin/down"}},
	}}
	sup := federation.NewSupervisor(cfg)
	sup.Backoff.InitialDelay = 5 * time.Millisecond
	sup.Connect = func(_ context.Context, _ federation.Upstream) (*federation.Connection, error) {
		return nil, errors.New("dial failed")
	}
	disconnects := make(chan string, 5)
	sup.OnDisconnect = func(name string, _ error) {
		select {
		case disconnects <- name:
		default:
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	sup.Run(ctx)

	if len(disconnects) == 0 {
		t.Error("OnDisconnect never fired despite consistent dial failures")
	}
}

func TestConnect_RejectsBothURLAndCommand(t *testing.T) {
	_, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "ambiguous", URL: "https://example.com", Command: []string{"echo"},
	})
	if err == nil {
		t.Fatal("expected error for upstream with both url and command")
	}
}

func TestConnect_RejectsNeitherURLNorCommand(t *testing.T) {
	_, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "empty",
	})
	if err == nil {
		t.Fatal("expected error for upstream with neither url nor command")
	}
}
