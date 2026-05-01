package handlerrunner

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/fortify/circuitbreaker"
	"github.com/felixgeelhaar/fortify/retry"
	"github.com/felixgeelhaar/praxis/internal/capability"
)

var ErrTimeout = errors.New("handler timeout")
var ErrPanic = errors.New("handler panic")

// IsRetryable reports whether an error from a handler should be retried by
// the caller. Timeouts and 5xx/429-shaped errors are retryable; panics and
// well-formed 4xx are not.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTimeout) {
		return true
	}
	if errors.Is(err, ErrPanic) {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "429") ||
		strings.Contains(msg, "temporary") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection") {
		return true
	}
	for _, code := range []string{"500", "501", "502", "503", "504"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

type Config struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64

	// Circuit breaker config
	CbMaxRequests uint32
	CbInterval    time.Duration
	CbTimeout     time.Duration
}

type Runner struct {
	logger         *bolt.Logger
	cfg            Config
	retryStrat     retry.Retry[map[string]any]
	circuitBreaker circuitbreaker.CircuitBreaker[map[string]any]
}

func New(logger *bolt.Logger, cfg Config) *Runner {
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay == 0 {
		cfg.InitialDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = 5 * time.Second
	}
	if cfg.Multiplier == 0 {
		cfg.Multiplier = 2.0
	}
	if cfg.CbMaxRequests == 0 {
		cfg.CbMaxRequests = 10
	}
	if cfg.CbInterval == 0 {
		cfg.CbInterval = 10 * time.Second
	}
	if cfg.CbTimeout == 0 {
		cfg.CbTimeout = 30 * time.Second
	}

	retryCfg := retry.Config{
		MaxAttempts:   cfg.MaxAttempts,
		InitialDelay:  cfg.InitialDelay,
		MaxDelay:      cfg.MaxDelay,
		Multiplier:    cfg.Multiplier,
		BackoffPolicy: retry.BackoffExponential,
		Jitter:        true,
		IsRetryable:   IsRetryable,
	}

	cbCfg := circuitbreaker.Config{
		MaxRequests: cfg.CbMaxRequests,
		Interval:    cfg.CbInterval,
		Timeout:     cfg.CbTimeout,
	}

	return &Runner{
		logger:         logger,
		cfg:            cfg,
		retryStrat:     retry.New[map[string]any](retryCfg),
		circuitBreaker: circuitbreaker.New[map[string]any](cbCfg),
	}
}

func (r *Runner) Run(ctx context.Context, h capability.Handler, payload map[string]any) (map[string]any, error) {
	execute := func(ctx context.Context) (map[string]any, error) {
		resultCh := make(chan struct {
			output map[string]any
			err    error
		}, 1)
		go func() {
			defer func() {
				if p := recover(); p != nil {
					resultCh <- struct {
						output map[string]any
						err    error
					}{nil, ErrPanic}
				}
			}()
			out, err := h.Execute(ctx, payload)
			resultCh <- struct {
				output map[string]any
				err    error
			}{out, err}
		}()

		select {
		case res := <-resultCh:
			return res.output, res.err
		case <-time.After(30 * time.Second):
			return nil, ErrTimeout
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	output, err := r.circuitBreaker.Execute(ctx, func(ctx context.Context) (map[string]any, error) {
		return r.retryStrat.Do(ctx, execute)
	})

	if err != nil {
		r.logger.Error().Err(err).Str("handler", h.Name()).Msg("handler failed after retries")
	}

	return output, err
}

func (r *Runner) RunNoRetry(ctx context.Context, h capability.Handler, payload map[string]any) (map[string]any, error) {
	type result struct {
		output map[string]any
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				resultCh <- result{err: ErrPanic}
			}
		}()
		out, err := h.Execute(ctx, payload)
		resultCh <- result{output: out, err: err}
	}()
	select {
	case res := <-resultCh:
		return res.output, res.err
	case <-time.After(30 * time.Second):
		return nil, ErrTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runner) Simulate(ctx context.Context, h capability.Handler, payload map[string]any) (map[string]any, error) {
	return h.Simulate(ctx, payload)
}
