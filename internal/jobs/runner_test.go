package jobs_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/jobs"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

type stubHandler struct {
	calls  int
	output map[string]any
}

func (s *stubHandler) Name() string { return "stub" }
func (s *stubHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	s.calls++
	return s.output, nil
}
func (s *stubHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return s.output, nil
}
func (s *stubHandler) Capability() domain.Capability {
	return domain.Capability{Name: "stub", Simulatable: true, Idempotent: true}
}

func newWiring(t *testing.T) (*executor.Executor, *stubHandler, *ports.Repos) {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()
	h := &stubHandler{output: map[string]any{"ok": true, "ts": "1.0"}}
	reg := capability.New()
	_ = reg.Register(h)
	exec := executor.New(
		logger, reg,
		policy.New(logger, repos.Policy),
		idempotency.New(repos.Idempotency),
		handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1}),
		schema.New(),
		repos.Action, repos.Audit, nil,
	)
	return exec, h, repos
}

func TestRunner_DrainsAsyncQueue(t *testing.T) {
	exec, handler, repos := newWiring(t)

	a := domain.Action{
		ID:         "async-1",
		Capability: "stub",
		Mode:       domain.ModeAsync,
		Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
	}
	res, err := exec.Execute(context.Background(), a)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != domain.StatusValidated {
		t.Errorf("async submit Status=%s want validated", res.Status)
	}
	if handler.calls != 0 {
		t.Errorf("handler invoked synchronously (calls=%d)", handler.calls)
	}

	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	r := jobs.New(logger, repos.Action, exec, jobs.Config{BatchSize: 10})
	if err := r.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if handler.calls != 1 {
		t.Errorf("handler calls=%d want 1 (drained once)", handler.calls)
	}

	got, _ := repos.Action.Get(context.Background(), "async-1")
	if got.Status != domain.StatusSucceeded {
		t.Errorf("post-drain Status=%s want succeeded", got.Status)
	}
}

func TestRunner_RunStopsOnContextCancel(t *testing.T) {
	exec, _, repos := newWiring(t)
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	r := jobs.New(logger, repos.Action, exec, jobs.Config{PollInterval: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit on context cancel")
	}
}
