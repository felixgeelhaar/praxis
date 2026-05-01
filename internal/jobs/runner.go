// Package jobs drains pending async actions through the executor.
//
// An async action is persisted in the validated state by Execute(); this
// package's Runner polls the action repo for those rows and calls
// Executor.Resume on each. At-least-once delivery is enforced by the
// IdempotencyKeeper — a Resume that races with another worker on the same
// Action.ID is safe.
package jobs

import (
	"context"
	"time"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// Resumer is the subset of executor.Executor used by the runner. Defined
// as an interface so the runner is unit-testable without a full executor.
type Resumer interface {
	Resume(ctx context.Context, action domain.Action) (domain.Result, error)
}

// Config tunes the runner.
type Config struct {
	BatchSize    int
	PollInterval time.Duration
}

// Runner periodically drains async actions.
type Runner struct {
	logger  *bolt.Logger
	actions ports.ActionRepo
	exec    Resumer
	cfg     Config
}

// New constructs a Runner with sensible defaults.
func New(logger *bolt.Logger, actions ports.ActionRepo, exec Resumer, cfg Config) *Runner {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 32
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	return &Runner{logger: logger, actions: actions, exec: exec, cfg: cfg}
}

// Run blocks until ctx is cancelled, draining the async queue at every tick.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Drain(ctx); err != nil && r.logger != nil {
				r.logger.Error().Err(err).Msg("jobs: drain")
			}
		}
	}
}

// Drain processes one batch of pending async actions. Returned errors come
// from the action repo lookup; per-action failures are logged and skipped
// so a single bad action does not stall the queue.
func (r *Runner) Drain(ctx context.Context) error {
	batch, err := r.actions.ListPendingAsync(ctx, r.cfg.BatchSize)
	if err != nil {
		return err
	}
	for _, a := range batch {
		if _, err := r.exec.Resume(ctx, a); err != nil && r.logger != nil {
			r.logger.Error().Err(err).Str("action_id", a.ID).Msg("jobs: resume failed")
		}
	}
	return nil
}
