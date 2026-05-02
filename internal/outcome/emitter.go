// Package outcome closes the cognitive loop: every terminal Action is
// posted to Mnemos as a `praxis.action_completed` event. The emitter is
// outbox-backed so Execute() never blocks on Mnemos availability.
//
// Flow:
//  1. Executor calls Emit(ev). The event is enqueued in OutboxRepo.
//  2. A background worker drains the outbox, posting to Mnemos with
//     fortify/retry (5xx + 429 retry, 4xx fail fast).
//  3. Failed deliveries bump attempts/next_attempt with exponential backoff.
//
// The Phase-1 contract: never block, never lose events, surface failures
// via metrics so an operator can intervene.
package outcome

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/fortify/retry"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// MnemosURL configures the upstream Mnemos endpoint for praxis.action_completed.
type Config struct {
	URL        string
	Token      string
	HTTPClient *http.Client

	BatchSize    int
	PollInterval time.Duration
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration

	// IDFactory generates outbox envelope IDs. Defaults to a time-based ID;
	// tests inject a stable factory.
	IDFactory func() string

	// Now is the time source. Defaults to time.Now.
	Now func() time.Time
}

// Emitter is the executor.Outcomes implementation backed by an outbox.
type Emitter struct {
	cfg    Config
	repo   ports.OutboxRepo
	logger *bolt.Logger

	mu        sync.Mutex
	failures  uint64
	delivered uint64
}

// New constructs an Emitter and applies sensible defaults.
func New(logger *bolt.Logger, repo ports.OutboxRepo, cfg Config) *Emitter {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 32
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 1 * time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 1 * time.Minute
	}
	if cfg.IDFactory == nil {
		cfg.IDFactory = defaultIDFactory()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Emitter{cfg: cfg, repo: repo, logger: logger}
}

// Emit enqueues a Mnemos event for asynchronous delivery. Never blocks.
func (e *Emitter) Emit(ctx context.Context, ev domain.MnemosEvent) error {
	now := e.cfg.Now()
	env := ports.OutboxEnvelope{
		ID:          e.cfg.IDFactory(),
		ActionID:    ev.ActionID,
		Event:       ev,
		NextAttempt: now,
		CreatedAt:   now,
	}
	if err := e.repo.Enqueue(ctx, env); err != nil {
		if e.logger != nil {
			e.logger.Error().Err(err).Str("action_id", ev.ActionID).Msg("outbox enqueue failed")
		}
		return fmt.Errorf("outbox enqueue: %w", err)
	}
	return nil
}

// Run drives the outbox drainer until ctx is cancelled. Safe to invoke once
// at startup; cancel ctx on shutdown to drain in-flight work.
func (e *Emitter) Run(ctx context.Context) {
	if e.cfg.URL == "" {
		// No Mnemos endpoint configured — events stay in the outbox.
		// An operator must inspect the table; we never silently drop.
		<-ctx.Done()
		return
	}
	t := time.NewTicker(e.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Drain(ctx); err != nil && e.logger != nil {
				e.logger.Error().Err(err).Msg("outbox drain")
			}
		}
	}
}

// Drain processes one batch from the outbox.
func (e *Emitter) Drain(ctx context.Context) error {
	batch, err := e.repo.NextBatch(ctx, e.cfg.BatchSize, e.cfg.Now())
	if err != nil {
		return fmt.Errorf("next batch: %w", err)
	}
	for _, env := range batch {
		if err := e.deliver(ctx, env); err != nil {
			e.bumpFailure()
		}
	}
	return nil
}

func (e *Emitter) deliver(ctx context.Context, env ports.OutboxEnvelope) error {
	r := retry.New[struct{}](retry.Config{
		MaxAttempts:   e.cfg.MaxAttempts,
		InitialDelay:  e.cfg.InitialDelay,
		MaxDelay:      e.cfg.MaxDelay,
		Multiplier:    2.0,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   isRetryable,
	})
	_, err := r.Do(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, e.post(ctx, env.Event)
	})
	if err != nil {
		next := e.cfg.Now().Add(backoff(env.Attempts+1, e.cfg.InitialDelay, e.cfg.MaxDelay))
		_ = e.repo.BumpAttempt(ctx, env.ID, next, err.Error())
		return err
	}
	if err := e.repo.MarkDelivered(ctx, env.ID, e.cfg.Now()); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	e.bumpDelivered()
	return nil
}

func (e *Emitter) post(ctx context.Context, ev domain.MnemosEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
	}
	resp, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("mnemos: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &httpError{status: resp.StatusCode}
}

// httpError carries the status code so isRetryable can classify it
// (5xx + 429 → retry; anything else → fail fast).
type httpError struct{ status int }

func (e *httpError) Error() string {
	return "mnemos: HTTP " + strconv.Itoa(e.status)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var he *httpError
	if errors.As(err, &he) {
		return he.status == http.StatusTooManyRequests || (he.status >= 500 && he.status < 600)
	}
	// network errors are retryable
	return true
}

func backoff(attempt int, initial, maxDelay time.Duration) time.Duration {
	d := initial
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > maxDelay {
			d = maxDelay
			break
		}
	}
	return d
}

// Stats returns current delivered/failure counters. Useful for /metrics.
func (e *Emitter) Stats() (delivered, failures uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delivered, e.failures
}

func (e *Emitter) bumpDelivered() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.delivered++
}

func (e *Emitter) bumpFailure() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures++
}

func defaultIDFactory() func() string {
	var n uint64
	var mu sync.Mutex
	return func() string {
		mu.Lock()
		n++
		out := fmt.Sprintf("o-%d-%d", time.Now().UnixNano(), n)
		mu.Unlock()
		return out
	}
}
