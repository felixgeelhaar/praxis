package policy_test

import (
	"context"
	"io"
	"testing"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func newEngine(t *testing.T) *policy.Engine {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	return policy.New(logger, memory.New().Policy)
}

func TestEvaluate_DefaultAllow(t *testing.T) {
	e := newEngine(t)
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "allow" {
		t.Errorf("Decision=%s want allow", d.Decision)
	}
	if d.RuleID != "default_allow" {
		t.Errorf("RuleID=%s want default_allow", d.RuleID)
	}
}

func TestEvaluate_DenyRuleMatchesCapability(t *testing.T) {
	e := newEngine(t)
	if err := e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-x", Capability: "x", Decision: "deny", Reason: "blocked",
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "deny" {
		t.Errorf("Decision=%s want deny", d.Decision)
	}
	if d.RuleID != "deny-x" {
		t.Errorf("RuleID=%s want deny-x", d.RuleID)
	}
}

func TestEvaluate_RuleScopedToCallerType(t *testing.T) {
	e := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-user", Capability: "x", CallerType: "user", Decision: "deny",
	})
	d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Caller: domain.CallerRef{Type: "agent"},
	})
	if d.Decision != "allow" {
		t.Errorf("agent caller: Decision=%s want allow (rule targets user)", d.Decision)
	}
	d = e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Caller: domain.CallerRef{Type: "user"},
	})
	if d.Decision != "deny" {
		t.Errorf("user caller: Decision=%s want deny", d.Decision)
	}
}

func TestEvaluate_RuleScopedByScopeList(t *testing.T) {
	e := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-write", Capability: "x", Scope: []string{"write"}, Decision: "deny",
	})
	d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Scope: []string{"read"},
	})
	if d.Decision != "allow" {
		t.Errorf("read-only: Decision=%s want allow", d.Decision)
	}
	d = e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Scope: []string{"write"},
	})
	if d.Decision != "deny" {
		t.Errorf("write: Decision=%s want deny", d.Decision)
	}
}

func TestEvaluate_FirstMatchWins(t *testing.T) {
	e := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "r1", Capability: "x", Decision: "deny", Reason: "first"})
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "r2", Capability: "x", Decision: "allow", Reason: "second"})
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.RuleID == "" {
		t.Errorf("expected a rule match")
	}
}
