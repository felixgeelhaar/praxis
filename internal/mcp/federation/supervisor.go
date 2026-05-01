package federation

import (
	"context"
	"sync"
	"time"
)

// Supervisor maintains live connections to every Upstream in a
// federation Config. It runs one goroutine per upstream that
// reconnects with exponential backoff when the transport breaks.
//
// Phase 5 federated MCP. The plugin.Manager-style integration that
// turns each Connection's tools into Praxis capabilities lands in
// t-mcp-federation-handler.
type Supervisor struct {
	cfg Config

	// OnConnect receives a fresh Connection when an upstream comes
	// online (or comes back after a reconnect). Bootstrap wires this
	// to the plugin.Manager so the upstream's tools register as Praxis
	// capabilities. Synchronous: the supervisor blocks until the
	// callback returns, so wiring it to a slow registrar will throttle
	// reconnect attempts.
	OnConnect func(*Connection)

	// OnDisconnect fires when a Connection's transport breaks. The
	// supervisor immediately starts the reconnect loop after the
	// callback returns. Bootstrap wires this to crash recovery so
	// the upstream's tools deregister cleanly.
	OnDisconnect func(upstream string, err error)

	// Backoff controls reconnect timing. Defaults: InitialDelay=1s,
	// MaxDelay=30s, Multiplier=2.0.
	Backoff BackoffConfig

	// Connect is the production entry point; tests inject a stub so
	// they don't need to spawn real subprocesses. Defaults to
	// federation.Connect.
	Connect func(ctx context.Context, upstream Upstream) (*Connection, error)

	wg sync.WaitGroup
}

// BackoffConfig parameters the per-upstream reconnect loop.
type BackoffConfig struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// NewSupervisor wires a Config and applies defaults to any zero-value
// fields the caller leaves untouched.
func NewSupervisor(cfg Config) *Supervisor {
	return &Supervisor{cfg: cfg, Connect: Connect}
}

// Run launches per-upstream goroutines and blocks until ctx is
// cancelled. Each goroutine calls Connect, fires OnConnect, then
// listens on Watch for transport failure. On failure it fires
// OnDisconnect, sleeps the backoff, and tries again.
func (s *Supervisor) Run(ctx context.Context) {
	bo := s.Backoff
	if bo.InitialDelay == 0 {
		bo.InitialDelay = 1 * time.Second
	}
	if bo.MaxDelay == 0 {
		bo.MaxDelay = 30 * time.Second
	}
	if bo.Multiplier == 0 {
		bo.Multiplier = 2.0
	}

	for _, u := range s.cfg.Upstreams {
		s.wg.Add(1)
		go func(u Upstream) {
			defer s.wg.Done()
			s.superviseUpstream(ctx, u, bo)
		}(u)
	}
	s.wg.Wait()
}

func (s *Supervisor) superviseUpstream(ctx context.Context, u Upstream, bo BackoffConfig) {
	delay := bo.InitialDelay
	for {
		conn, err := s.Connect(ctx, u)
		if err != nil {
			if s.OnDisconnect != nil {
				s.OnDisconnect(u.Name, err)
			}
			if !sleepCtx(ctx, delay) {
				return
			}
			delay = nextDelay(delay, bo)
			continue
		}
		// Reset backoff once we're connected.
		delay = bo.InitialDelay
		if s.OnConnect != nil {
			s.OnConnect(conn)
		}
		// Wait for transport failure or ctx cancel.
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return
		case err := <-conn.Watch():
			_ = conn.Close()
			if s.OnDisconnect != nil {
				s.OnDisconnect(u.Name, err)
			}
		}
		if !sleepCtx(ctx, delay) {
			return
		}
		delay = nextDelay(delay, bo)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextDelay(cur time.Duration, bo BackoffConfig) time.Duration {
	next := time.Duration(float64(cur) * bo.Multiplier)
	if next > bo.MaxDelay {
		return bo.MaxDelay
	}
	return next
}
