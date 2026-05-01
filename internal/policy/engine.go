package policy

import (
	"context"
	"slices"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

type Engine struct {
	logger   *bolt.Logger
	repo     ports.PolicyRepo
	isGlobal bool
}

func New(logger *bolt.Logger, repo ports.PolicyRepo) *Engine {
	return &Engine{logger: logger, repo: repo}
}

func (e *Engine) Evaluate(ctx context.Context, action domain.Action) domain.PolicyDecision {
	e.logger.Info().Str("capability", action.Capability).Msg("policy evaluating")

	rules, err := e.repo.ListRules(ctx)
	if err != nil || len(rules) == 0 {
		e.logger.Info().Msg("no rules found, default allow")
		return domain.PolicyDecision{
			Decision:    "allow",
			RuleID:      "default_allow",
			Reason:      "no rules configured",
			EvaluatedAt: time.Now(),
		}
	}

	matched := e.findMatchingRule(rules, action)
	if matched != nil {
		return domain.PolicyDecision{
			Decision:    matched.Decision,
			RuleID:      matched.ID,
			Reason:      matched.Reason,
			Rule:        matched,
			EvaluatedAt: time.Now(),
		}
	}

	e.logger.Info().Msg("no matching rule, default allow")
	return domain.PolicyDecision{
		Decision:    "allow",
		RuleID:      "default_allow",
		Reason:      "no rule matched for capability/caller/scope",
		EvaluatedAt: time.Now(),
	}
}

func (e *Engine) findMatchingRule(rules []domain.PolicyRule, action domain.Action) *domain.PolicyRule {
	for _, rule := range rules {
		if rule.Capability != "" && rule.Capability != action.Capability {
			continue
		}
		if rule.CallerType != "" && rule.CallerType != action.Caller.Type {
			continue
		}
		if len(rule.Scope) > 0 && !e.hasMatchingScope(rule.Scope, action.Scope) {
			continue
		}
		e.logger.Info().Str("rule", rule.ID).Msg("rule matched")
		return &rule
	}
	return nil
}

func (e *Engine) hasMatchingScope(ruleScopes, actionScopes []string) bool {
	for _, rs := range ruleScopes {
		for _, as := range actionScopes {
			if rs == as {
				return true
			}
		}
	}
	return len(ruleScopes) == 0
}

func (e *Engine) SetRules(ctx context.Context, rules []domain.PolicyRule) error {
	e.repo.DeleteRule(ctx, "")
	for _, rule := range rules {
		if err := e.repo.UpsertRule(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) AddRule(ctx context.Context, rule domain.PolicyRule) error {
	return e.repo.UpsertRule(ctx, rule)
}

func (e *Engine) DeleteRule(ctx context.Context, ruleID string) error {
	return e.repo.DeleteRule(ctx, ruleID)
}

func (e *Engine) ListRules(ctx context.Context) ([]domain.PolicyRule, error) {
	return e.repo.ListRules(ctx)
}

func NewGlobal(logger *bolt.Logger) *Engine {
	return &Engine{logger: logger, isGlobal: true}
}

func (e *Engine) IsGlobalAllow() bool {
	return e.isGlobal
}

func (e *Engine) SetGlobal(allow bool) {
	e.isGlobal = allow
}

func MatchScopes(scopes []string, actionScopes []string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if slices.Contains(actionScopes, s) {
			return true
		}
	}
	return false
}
