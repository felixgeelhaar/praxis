package audit_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

type stubHandler struct {
	output, sim map[string]any
	err         error
}

func (s *stubHandler) Name() string { return "test" }
func (s *stubHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	return s.output, s.err
}
func (s *stubHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return s.sim, nil
}

func newExec(t *testing.T, h *stubHandler, denyRule *domain.PolicyRule) (*executor.Executor, *ports.Repos) {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()
	registry := capability.New()
	registry.Register(h)
	pol := policy.New(logger, repos.Policy)
	if denyRule != nil {
		_ = pol.AddRule(context.Background(), *denyRule)
	}
	return executor.New(
		logger,
		registry,
		pol,
		idempotency.New(repos.Idempotency),
		handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1}),
		schema.New(),
		repos.Action,
		repos.Audit,
		nil,
	), repos
}

func TestReplay_Succeeded(t *testing.T) {
	exec, repos := newExec(t, &stubHandler{
		output: map[string]any{"ts": "1.2", "ok": true},
	}, nil)
	a := domain.Action{
		ID:         "act-ok",
		Capability: "test",
		Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
	}
	if _, err := exec.Execute(context.Background(), a); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lc, err := audit.Replay(context.Background(), repos.Audit, "act-ok")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lc.FinalStatus != domain.StatusSucceeded {
		t.Errorf("FinalStatus=%s want succeeded", lc.FinalStatus)
	}
	if lc.ExternalID != "1.2" {
		t.Errorf("ExternalID=%q want 1.2", lc.ExternalID)
	}
	if lc.Capability != "test" {
		t.Errorf("Capability=%s want test", lc.Capability)
	}
	if lc.PolicyDecision != "allow" {
		t.Errorf("PolicyDecision=%s want allow", lc.PolicyDecision)
	}
}

func TestReplay_Failed(t *testing.T) {
	exec, repos := newExec(t, &stubHandler{err: errors.New("vendor 503 boom")}, nil)
	if _, err := exec.Execute(context.Background(), domain.Action{
		ID: "act-fail", Capability: "test", Caller: domain.CallerRef{Type: "agent"},
	}); err == nil {
		t.Fatal("expected error")
	}

	lc, err := audit.Replay(context.Background(), repos.Audit, "act-fail")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lc.FinalStatus != domain.StatusFailed {
		t.Errorf("FinalStatus=%s want failed", lc.FinalStatus)
	}
	if lc.ErrorCode == "" {
		t.Errorf("ErrorCode empty, want classified code")
	}
}

func TestReplay_Rejected_PolicyDeny(t *testing.T) {
	exec, repos := newExec(t, &stubHandler{}, &domain.PolicyRule{
		ID: "deny", Capability: "test", Decision: "deny", Reason: "no go",
	})
	if _, err := exec.Execute(context.Background(), domain.Action{
		ID: "act-deny", Capability: "test", Caller: domain.CallerRef{Type: "user"},
	}); err == nil {
		t.Fatal("expected policy deny")
	}

	lc, err := audit.Replay(context.Background(), repos.Audit, "act-deny")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lc.FinalStatus != domain.StatusRejected {
		t.Errorf("FinalStatus=%s want rejected", lc.FinalStatus)
	}
	if lc.PolicyDecision != "deny" {
		t.Errorf("PolicyDecision=%s want deny", lc.PolicyDecision)
	}
	if lc.ErrorCode != "policy_denied" {
		t.Errorf("ErrorCode=%s want policy_denied", lc.ErrorCode)
	}
}

func TestReplay_Rejected_UnknownCapability(t *testing.T) {
	exec, repos := newExec(t, &stubHandler{}, nil)
	if _, err := exec.Execute(context.Background(), domain.Action{
		ID: "act-unknown", Capability: "missing", Caller: domain.CallerRef{Type: "user"},
	}); err == nil {
		t.Fatal("expected unknown capability error")
	}
	lc, err := audit.Replay(context.Background(), repos.Audit, "act-unknown")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lc.FinalStatus != domain.StatusRejected {
		t.Errorf("FinalStatus=%s want rejected", lc.FinalStatus)
	}
	if lc.ErrorCode != "unknown_capability" {
		t.Errorf("ErrorCode=%s want unknown_capability", lc.ErrorCode)
	}
}

func TestReplay_Simulated(t *testing.T) {
	exec, repos := newExec(t, &stubHandler{sim: map[string]any{"would": "send"}}, nil)
	if _, err := exec.DryRun(context.Background(), domain.Action{
		ID: "act-sim", Capability: "test", Caller: domain.CallerRef{Type: "user"},
	}); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	lc, err := audit.Replay(context.Background(), repos.Audit, "act-sim")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lc.FinalStatus != domain.StatusSimulated {
		t.Errorf("FinalStatus=%s want simulated", lc.FinalStatus)
	}
}

func TestReplay_NoEvents(t *testing.T) {
	repos := memory.New()
	_, err := audit.Replay(context.Background(), repos.Audit, "missing")
	if err == nil {
		t.Fatal("expected error for missing action")
	}
	if !errors.Is(err, audit.ErrIncompleteAudit) {
		t.Errorf("expected ErrIncompleteAudit, got %v", err)
	}
}
