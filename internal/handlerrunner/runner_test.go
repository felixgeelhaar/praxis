package handlerrunner_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
)

type fakeHandler struct {
	name   string
	output map[string]any
	err    error
	calls  int32
	delay  time.Duration
	panic  bool
}

func (f *fakeHandler) Name() string { return f.name }
func (f *fakeHandler) Execute(ctx context.Context, _ map[string]any) (map[string]any, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.panic {
		panic("boom")
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.output, f.err
}
func (f *fakeHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return f.output, nil
}

func newRunner() *handlerrunner.Runner {
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	return handlerrunner.New(logger, handlerrunner.Config{
		MaxAttempts:  2,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
	})
}

func TestRun_Success(t *testing.T) {
	r := newRunner()
	h := &fakeHandler{name: "h", output: map[string]any{"ok": true}}
	out, err := r.Run(context.Background(), h, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("output=%v", out)
	}
}

func TestRun_PanicRecovered(t *testing.T) {
	r := newRunner()
	h := &fakeHandler{name: "h", panic: true}
	_, err := r.Run(context.Background(), h, nil)
	if err == nil {
		t.Fatal("expected error on panic")
	}
	if !errors.Is(err, handlerrunner.ErrPanic) {
		t.Errorf("err=%v want ErrPanic", err)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout sentinel", handlerrunner.ErrTimeout, true},
		{"panic sentinel", handlerrunner.ErrPanic, false},
		{"503 substring", errors.New("vendor 503 unavailable"), true},
		{"429 substring", errors.New("rate limited 429"), true},
		{"timeout substring", errors.New("read timeout"), true},
		{"4xx-shaped not retryable", errors.New("400 bad request"), false},
		{"opaque", errors.New("nope"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handlerrunner.IsRetryable(tt.err)
			if got != tt.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRun_RetriesOnRetryableError(t *testing.T) {
	r := newRunner()
	h := &fakeHandler{name: "h", err: errors.New("503 boom")}
	_, _ = r.Run(context.Background(), h, nil)
	if atomic.LoadInt32(&h.calls) < 2 {
		t.Errorf("calls=%d want ≥2 (retry expected)", h.calls)
	}
}

func TestRun_FailsFastOnNonRetryable(t *testing.T) {
	r := newRunner()
	h := &fakeHandler{name: "h", err: errors.New("400 bad request")}
	_, _ = r.Run(context.Background(), h, nil)
	if atomic.LoadInt32(&h.calls) != 1 {
		t.Errorf("calls=%d want 1 (fail-fast)", h.calls)
	}
}

func TestRunWithCapability_PerCapMaxAttempts(t *testing.T) {
	r := newRunner() // global MaxAttempts = 2
	h := &fakeHandler{name: "h", err: errors.New("503 boom")}
	capSpec := &domain.Capability{
		Name: "h",
		Retry: &domain.RetryConfig{
			MaxAttempts:  5,
			InitialDelay: int64(time.Millisecond),
			MaxDelay:     int64(5 * time.Millisecond),
			Multiplier:   2,
		},
	}
	_, _ = r.RunWithCapability(context.Background(), capSpec, h, nil)
	if got := atomic.LoadInt32(&h.calls); got < 5 {
		t.Errorf("calls=%d want ≥5 (per-cap MaxAttempts overrides global)", got)
	}
}

func TestRunWithCapability_NilFallsBackToGlobal(t *testing.T) {
	r := newRunner() // global MaxAttempts = 2
	h := &fakeHandler{name: "h", err: errors.New("503 boom")}
	_, _ = r.RunWithCapability(context.Background(), nil, h, nil)
	if got := atomic.LoadInt32(&h.calls); got != 2 {
		t.Errorf("calls=%d want 2 (global)", got)
	}
}

func TestSimulate(t *testing.T) {
	r := newRunner()
	h := &fakeHandler{name: "h", output: map[string]any{"sim": true}}
	out, err := r.Simulate(context.Background(), h, nil)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["sim"] != true {
		t.Errorf("Simulate output=%v", out)
	}
}
