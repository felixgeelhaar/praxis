package policy_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func newEngine(t *testing.T) (*policy.Engine, ports.PolicyRepo) {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repo := memory.New().Policy
	return policy.New(logger, repo), repo
}

func TestEvaluate_DefaultAllow_NoRules(t *testing.T) {
	e, _ := newEngine(t)
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "allow" || d.RuleID != "default_allow" {
		t.Errorf("d=%+v want default_allow", d)
	}
}

func TestEvaluate_DefaultDenyMode_NoRules(t *testing.T) {
	e, _ := newEngine(t)
	e.SetMode(policy.ModeDeny)
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "deny" || d.RuleID != "default_deny" {
		t.Errorf("d=%+v want default_deny", d)
	}
}

func TestEvaluate_RulesModeAliasesDeny(t *testing.T) {
	e, _ := newEngine(t)
	e.SetMode(policy.ModeRules)
	if e.Mode() != policy.ModeDeny {
		t.Errorf("ModeRules should alias ModeDeny, got %s", e.Mode())
	}
}

func TestEvaluate_DenyRuleMatchesCapability(t *testing.T) {
	e, _ := newEngine(t)
	if err := e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-x", Capability: "x", Decision: "deny", Reason: "blocked",
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "deny" || d.RuleID != "deny-x" {
		t.Errorf("d=%+v want deny-x", d)
	}
}

func TestEvaluate_RuleScopedToCallerType(t *testing.T) {
	e, _ := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-user", Capability: "x", CallerType: "user", Decision: "deny",
	})
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Caller: domain.CallerRef{Type: "agent"},
	}); d.Decision != "allow" {
		t.Errorf("agent caller: got %s want allow", d.Decision)
	}
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Caller: domain.CallerRef{Type: "user"},
	}); d.Decision != "deny" {
		t.Errorf("user caller: got %s want deny", d.Decision)
	}
}

func TestEvaluate_TenantScopeHierarchy(t *testing.T) {
	e, _ := newEngine(t)
	// org-scoped deny
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-org-x", Capability: "send", OrgID: "org-x", Decision: "deny",
	})
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "send", Caller: domain.CallerRef{Type: "user", OrgID: "org-other"},
	}); d.Decision != "allow" {
		t.Errorf("other org: %s want allow", d.Decision)
	}
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "send", Caller: domain.CallerRef{Type: "user", OrgID: "org-x"},
	}); d.Decision != "deny" {
		t.Errorf("org-x: %s want deny", d.Decision)
	}
}

func TestEvaluate_TeamScopeHierarchy(t *testing.T) {
	e, _ := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-team", Capability: "send", OrgID: "org-x", TeamID: "team-y", Decision: "deny",
	})
	// Same org, different team: allow
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "send", Caller: domain.CallerRef{OrgID: "org-x", TeamID: "team-z"},
	}); d.Decision != "allow" {
		t.Errorf("other team: %s want allow", d.Decision)
	}
	// Same org + team: deny
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "send", Caller: domain.CallerRef{OrgID: "org-x", TeamID: "team-y"},
	}); d.Decision != "deny" {
		t.Errorf("matching team: %s want deny", d.Decision)
	}
}

func TestEvaluate_FirstMatchWins_ByPriority(t *testing.T) {
	e, _ := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "z-allow", Capability: "x", Decision: "allow", Priority: 100})
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "a-deny", Capability: "x", Decision: "deny", Priority: 1})

	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.RuleID != "a-deny" {
		t.Errorf("RuleID=%s want a-deny (lowest priority wins)", d.RuleID)
	}
	if d.Decision != "deny" {
		t.Errorf("Decision=%s want deny", d.Decision)
	}
}

func TestEvaluate_FirstMatchWins_TieBreakOnID(t *testing.T) {
	e, _ := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "rule-b-allow", Capability: "x", Decision: "allow"})
	_ = e.AddRule(context.Background(), domain.PolicyRule{ID: "rule-a-deny", Capability: "x", Decision: "deny"})

	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.RuleID != "rule-a-deny" {
		t.Errorf("RuleID=%s want rule-a-deny (alphabetic tie-break)", d.RuleID)
	}
}

func TestEvaluate_RepoErrorClosesGate(t *testing.T) {
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	e := policy.New(logger, &flakyRepo{err: errors.New("boom")})
	d := e.Evaluate(context.Background(), domain.Action{Capability: "x"})
	if d.Decision != "deny" || d.RuleID != "rules_unavailable" {
		t.Errorf("d=%+v want deny/rules_unavailable on repo error", d)
	}
}

func TestEvaluate_ScopeOverlapMatch(t *testing.T) {
	e, _ := newEngine(t)
	_ = e.AddRule(context.Background(), domain.PolicyRule{
		ID: "deny-write", Capability: "x", Scope: []string{"write"}, Decision: "deny",
	})
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Scope: []string{"read"},
	}); d.Decision != "allow" {
		t.Errorf("read-only caller: %s want allow", d.Decision)
	}
	if d := e.Evaluate(context.Background(), domain.Action{
		Capability: "x", Scope: []string{"read", "write"},
	}); d.Decision != "deny" {
		t.Errorf("write caller: %s want deny", d.Decision)
	}
}

// flakyRepo simulates a repo failure so we can test fail-closed behaviour.
type flakyRepo struct{ err error }

func (f *flakyRepo) ListRules(_ context.Context) ([]domain.PolicyRule, error) {
	return nil, f.err
}
func (*flakyRepo) UpsertRule(_ context.Context, _ domain.PolicyRule) error { return nil }
func (*flakyRepo) DeleteRule(_ context.Context, _ string) error            { return nil }
