// Package policy evaluates an Action against the configured ruleset and
// returns a PolicyDecision. Phase 1 shipped a permissive default-allow;
// Phase 2 adds explicit scoped rules with first-match-wins ordering and a
// default-deny mode.
//
// Modes:
//
//	"allow" — no rule matched ⇒ allow (Phase-1 behaviour, lowest friction)
//	"deny"  — no rule matched ⇒ deny  (production-safe default)
//	"rules" — alias for "deny" with explicit semantics
//
// Rule matching is first-match-wins. Order is determined by the optional
// PolicyRule.Priority (lower runs first); rules with equal priority break
// ties on ID. Every evaluation records the matched rule on the action.
package policy

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// Mode controls what a missing-rule evaluation returns.
type Mode string

const (
	ModeAllow Mode = "allow"
	ModeDeny  Mode = "deny"
	ModeRules Mode = "rules" // alias for deny — kept for env-var clarity
)

// Engine evaluates actions against a PolicyRepo.
type Engine struct {
	logger *bolt.Logger
	repo   ports.PolicyRepo
	mode   Mode
}

// New returns an engine in default-allow mode (Phase-1 compatible).
func New(logger *bolt.Logger, repo ports.PolicyRepo) *Engine {
	return &Engine{logger: logger, repo: repo, mode: ModeAllow}
}

// SetMode switches the no-match outcome. Unknown values are coerced to
// ModeAllow so a misconfigured env var fails open in development; callers
// that want fail-closed should validate at config-load time (config.Load
// already does so).
func (e *Engine) SetMode(m Mode) {
	switch m {
	case ModeDeny, ModeRules:
		e.mode = ModeDeny
	default:
		e.mode = ModeAllow
	}
}

// Mode returns the current default behaviour for unmatched actions.
func (e *Engine) Mode() Mode { return e.mode }

// Evaluate walks the ordered ruleset and returns the first matching
// decision, or the mode-default when no rule matches.
func (e *Engine) Evaluate(ctx context.Context, action domain.Action) domain.PolicyDecision {
	now := time.Now()
	rules, err := e.repo.ListRules(ctx)
	if err != nil {
		e.logger.Error().Err(err).Msg("policy: list rules failed")
		// In any mode, a repo failure is a deny — never let an outage
		// open the gate.
		return domain.PolicyDecision{
			Decision:    "deny",
			RuleID:      "rules_unavailable",
			Reason:      "policy repository unreachable",
			EvaluatedAt: now,
		}
	}

	rules = sortRules(rules)
	for _, r := range rules {
		if matches(r, action) {
			rule := r
			e.logger.Info().
				Str("rule_id", r.ID).
				Str("decision", r.Decision).
				Str("capability", action.Capability).
				Msg("policy: rule matched")
			return domain.PolicyDecision{
				Decision:    r.Decision,
				RuleID:      r.ID,
				Reason:      r.Reason,
				Rule:        &rule,
				EvaluatedAt: now,
			}
		}
	}

	// No rule matched — apply the mode default.
	if e.mode == ModeDeny {
		return domain.PolicyDecision{
			Decision:    "deny",
			RuleID:      "default_deny",
			Reason:      "default-deny mode: no matching rule",
			EvaluatedAt: now,
		}
	}
	return domain.PolicyDecision{
		Decision:    "allow",
		RuleID:      "default_allow",
		Reason:      "default-allow mode: no matching rule",
		EvaluatedAt: now,
	}
}

// AddRule inserts (or updates) a rule.
func (e *Engine) AddRule(ctx context.Context, rule domain.PolicyRule) error {
	return e.repo.UpsertRule(ctx, rule)
}

// DeleteRule removes a rule by ID.
func (e *Engine) DeleteRule(ctx context.Context, ruleID string) error {
	return e.repo.DeleteRule(ctx, ruleID)
}

// ListRules returns all rules unsorted (callers that care about order use
// Evaluate, which sorts internally).
func (e *Engine) ListRules(ctx context.Context) ([]domain.PolicyRule, error) {
	return e.repo.ListRules(ctx)
}

// matches reports whether rule applies to action.
//
//   - empty Capability matches any capability
//   - empty CallerType matches any caller
//   - non-empty Scope requires at least one overlap with the action's scope
func matches(r domain.PolicyRule, a domain.Action) bool {
	if r.Capability != "" && r.Capability != a.Capability {
		return false
	}
	if r.CallerType != "" && !strings.EqualFold(r.CallerType, a.Caller.Type) {
		return false
	}
	if len(r.Scope) > 0 {
		hit := false
		for _, s := range r.Scope {
			if slices.Contains(a.Scope, s) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// sortRules orders rules by (Priority asc, ID asc). Rules without a
// Priority column (Phase-1 schema) tie on Priority=0 and fall back to ID.
func sortRules(rules []domain.PolicyRule) []domain.PolicyRule {
	out := make([]domain.PolicyRule, len(rules))
	copy(out, rules)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}
