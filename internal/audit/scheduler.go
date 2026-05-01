package audit

import (
	"context"
	"time"

	"github.com/felixgeelhaar/bolt"
)

// SchedulerConfig drives the retention sweep. InitialDelay defers the
// first sweep so a freshly started server doesn't burn cycles competing
// with bootstrap; Interval is the cadence between subsequent sweeps.
// Now is overridable so tests can drive deterministic timestamps without
// a wall clock.
type SchedulerConfig struct {
	InitialDelay time.Duration
	Interval     time.Duration
	Now          func() time.Time
}

// Scheduler runs Service.PurgeExpired on a fixed cadence. Per-tenant
// deletion counts are logged and reported through OnPurge so the bootstrap
// can hook in metrics. Phase 4 M3.3.
type Scheduler struct {
	svc     *Service
	cfg     SchedulerConfig
	logger  *bolt.Logger
	OnPurge func(orgID string, deleted int64, err error)
}

// NewScheduler wires a Service to a logger and configuration. Defaults:
// InitialDelay=5m, Interval=1h, Now=time.Now.
func NewScheduler(svc *Service, logger *bolt.Logger, cfg SchedulerConfig) *Scheduler {
	if cfg.InitialDelay == 0 {
		cfg.InitialDelay = 5 * time.Minute
	}
	if cfg.Interval == 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Scheduler{svc: svc, cfg: cfg, logger: logger}
}

// Run blocks until ctx is cancelled, sweeping every Interval after an
// initial InitialDelay. A timer is used (not a ticker) so the first
// sweep aligns with InitialDelay rather than firing immediately.
func (s *Scheduler) Run(ctx context.Context) {
	timer := time.NewTimer(s.cfg.InitialDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.sweep(ctx)
			timer.Reset(s.cfg.Interval)
		}
	}
}

// RunOnce runs a single sweep synchronously. Used by tests and the SIGHUP
// handler that wants to force a retention pass.
func (s *Scheduler) RunOnce(ctx context.Context) {
	s.sweep(ctx)
}

func (s *Scheduler) sweep(ctx context.Context) {
	deleted, err := s.svc.PurgeExpired(ctx, s.cfg.Now())
	if err != nil {
		s.logger.Error().Err(err).Msg("audit retention sweep failed")
		// Still report partial deletions so the metric reflects what landed.
	}
	for orgID, count := range deleted {
		if s.OnPurge != nil {
			s.OnPurge(orgID, count, nil)
		}
		s.logger.Info().
			Str("org_id", orgID).
			Int64("deleted", count).
			Msg("audit retention sweep")
	}
	if err != nil && s.OnPurge != nil {
		// Surface the error against the empty orgID so a single failed
		// tenant does not silently drop from the metric.
		s.OnPurge("", 0, err)
	}
}
