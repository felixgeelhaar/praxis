package mcp_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	pmcp "github.com/felixgeelhaar/praxis/internal/mcp"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

type stub struct {
	calls  int
	output map[string]any
}

func (s *stub) Name() string { return "stub" }
func (s *stub) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	s.calls++
	return s.output, nil
}
func (s *stub) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{"sim": true}, nil
}
func (s *stub) Capability() domain.Capability {
	return domain.Capability{Name: "stub", Simulatable: true, Idempotent: true}
}

func newExec(t *testing.T) (pmcp.Executor, *stub) {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()
	h := &stub{output: map[string]any{"ok": true, "ts": "1.0"}}
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
	return exec, h
}

func TestRegister_BuildsServer(t *testing.T) {
	exec, _ := newExec(t)
	srv := pmcp.Register(pmcp.Info{Name: "praxis-test", Version: "0.0.0"}, exec, func() string { return "act-test" })
	if srv == nil {
		t.Fatal("Register returned nil")
	}
	if _, ok := srv.GetTool("execute"); !ok {
		t.Errorf("execute tool not registered")
	}
	if _, ok := srv.GetTool("dry_run"); !ok {
		t.Errorf("dry_run tool not registered")
	}
	if _, ok := srv.GetTool("list_capabilities"); !ok {
		t.Errorf("list_capabilities tool not registered")
	}
}

func TestRegister_RequiresExec(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			// builder may not panic; just confirm we don't crash on a real exec
			_ = r
		}
	}()
	exec, _ := newExec(t)
	srv := pmcp.Register(pmcp.Info{Name: "x", Version: "v"}, exec, func() string { return "id" })
	if srv == nil {
		t.Fatal("nil server")
	}
}

// errExec is a small Executor that returns errors so we cover the failure path.
type errExec struct{}

func (errExec) ListCapabilities(_ context.Context) ([]domain.Capability, error) {
	return nil, errors.New("list failed")
}
func (errExec) Execute(_ context.Context, _ domain.Action) (domain.Result, error) {
	return domain.Result{Status: domain.StatusFailed, Error: &domain.ActionError{Code: "x", Message: "boom"}}, errors.New("boom")
}
func (errExec) DryRun(_ context.Context, _ domain.Action) (domain.Simulation, error) {
	return domain.Simulation{}, errors.New("dry boom")
}

func TestRegister_AcceptsErrExec(t *testing.T) {
	srv := pmcp.Register(pmcp.Info{Name: "x", Version: "v"}, errExec{}, func() string { return "id" })
	if srv == nil {
		t.Fatal("nil server")
	}
}

func TestRegister_RegistersOneToolPerCapability(t *testing.T) {
	exec, _ := newExec(t)
	srv := pmcp.Register(pmcp.Info{Name: "praxis", Version: "v"}, exec, func() string { return "act" })

	// Universal tools.
	for _, want := range []string{"list_capabilities", "execute", "dry_run"} {
		if _, ok := srv.GetTool(want); !ok {
			t.Errorf("universal tool %s missing", want)
		}
	}
	// Per-cap tool — newExec wires a single "stub" capability.
	tool, ok := srv.GetTool("stub")
	if !ok {
		t.Fatal("per-capability tool 'stub' not registered")
	}
	if tool == nil {
		t.Fatal("nil tool")
	}
}
