// Package executor orchestrates the linear Execute / DryRun flow described in
// TDD §5.2 / §5.3.
//
// The flow is intentionally linear and testable. Status transitions go
// through the statekit-backed Action FSM (see internal/domain/action_fsm.go);
// every lifecycle event is appended to the audit log so an action can be
// fully reconstructed from audit_events alone.
package executor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/limiter"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/webhook"
)

// Outcomes accepts a terminal MnemosEvent for asynchronous delivery. The
// outcome layer (internal/outcome) implements this; it never blocks Execute.
type Outcomes interface {
	Emit(ctx context.Context, ev domain.MnemosEvent) error
}

// Executor implements the three-verb public API: ListCapabilities, Execute,
// DryRun. Construction wires every dependency explicitly so the domain layer
// never imports a concrete backend or handler.
type Executor struct {
	logger     *bolt.Logger
	registry   *capability.Registry
	policy     *policy.Engine
	idempotent *idempotency.Keeper
	runner     *handlerrunner.Runner
	validator  *schema.Validator
	limiter    *limiter.Limiter
	webhook    *webhook.Notifier
	actions    ports.ActionRepo
	auditRepo  ports.AuditRepo
	outcomes   Outcomes
	now        func() time.Time
}

// New constructs an Executor with all dependencies wired.
func New(
	logger *bolt.Logger,
	registry *capability.Registry,
	pol *policy.Engine,
	idem *idempotency.Keeper,
	runner *handlerrunner.Runner,
	validator *schema.Validator,
	actions ports.ActionRepo,
	auditRepo ports.AuditRepo,
	outcomes Outcomes,
) *Executor {
	return &Executor{
		logger:     logger,
		registry:   registry,
		policy:     pol,
		idempotent: idem,
		runner:     runner,
		validator:  validator,
		limiter:    limiter.New(),
		webhook:    webhook.New(logger, nil),
		actions:    actions,
		auditRepo:  auditRepo,
		outcomes:   outcomes,
		now:        time.Now,
	}
}

// SetLimiter swaps in a pre-built Limiter (e.g. one shared across multiple
// executors or backed by a distributed store).
func (e *Executor) SetLimiter(l *limiter.Limiter) { e.limiter = l }

// SetWebhookNotifier overrides the default notifier. Useful in tests and
// when sharing a tuned http.Client across components.
func (e *Executor) SetWebhookNotifier(n *webhook.Notifier) { e.webhook = n }

// SetClock overrides time.Now — used by tests for deterministic timestamps.
func (e *Executor) SetClock(now func() time.Time) { e.now = now }

// Execute runs an action through the full TDD §5.2 flow.
func (e *Executor) Execute(ctx context.Context, action domain.Action) (domain.Result, error) {
	if action.IdempotencyKey == "" {
		action.IdempotencyKey = action.ID
	}
	action.Status = domain.StatusPending
	action.CreatedAt = nonZero(action.CreatedAt, e.now())
	action.UpdatedAt = e.now()

	e.logger.Info().Str("action_id", action.ID).Str("capability", action.Capability).Msg("execute received")
	if err := e.actions.Save(ctx, action); err != nil {
		return domain.Result{}, fmt.Errorf("persist pending action: %w", err)
	}
	e.appendAudit(ctx, action, audit.KindReceived, map[string]any{"payload_keys": keys(action.Payload)})

	cap, err := e.registry.GetCapability(action.Capability)
	if err != nil {
		return e.reject(ctx, action, "unknown_capability", err.Error())
	}

	if verr := e.validator.ValidatePayload(action.Payload, cap.InputSchema); verr != nil {
		return e.reject(ctx, action, "validation_failed", verr.Error())
	}
	if err := e.transition(ctx, &action, domain.EventValidate); err != nil {
		return domain.Result{}, err
	}
	e.appendAudit(ctx, action, audit.KindValidated, nil)

	decision := e.policy.Evaluate(ctx, action)
	action.PolicyDecision = &decision
	_ = e.actions.Save(ctx, action)
	e.appendAudit(ctx, action, audit.KindPolicy, map[string]any{
		"decision": decision.Decision,
		"rule_id":  decision.RuleID,
		"reason":   decision.Reason,
	})
	if decision.Decision != "allow" {
		return e.reject(ctx, action, "policy_denied", decision.Reason)
	}

	if allowed, retryAfter := e.limiter.Allow(ctx, cap, action); !allowed {
		e.appendAudit(ctx, action, audit.KindThrottled, map[string]any{
			"retry_after_ms": retryAfter.Milliseconds(),
		})
		return e.reject(ctx, action, "rate_limited", "rate limit exceeded; retry after "+retryAfter.String())
	}

	// Async dispatch: persist as validated and return immediately. The jobs
	// runner drains validated/async rows and resumes through Resume().
	if action.Mode == domain.ModeAsync {
		_ = e.actions.Save(ctx, action)
		e.logger.Info().Str("action_id", action.ID).Msg("async action enqueued")
		return domain.Result{
			ActionID:  action.ID,
			Status:    domain.StatusValidated,
			StartedAt: action.CreatedAt,
		}, nil
	}

	if existing, err := e.idempotent.Check(ctx, action.IdempotencyKey); err == nil && existing != nil {
		e.logger.Info().Str("idempotency_key", action.IdempotencyKey).Msg("idempotency hit")
		return *existing, nil
	}

	if err := e.transition(ctx, &action, domain.EventExecute); err != nil {
		return domain.Result{}, err
	}
	startedAt := e.now()
	action.ExecutedAt = &startedAt
	_ = e.actions.Save(ctx, action)
	e.appendAudit(ctx, action, audit.KindExecuted, nil)

	handler, herr := e.registry.GetHandler(action.Capability)
	if herr != nil {
		return e.terminateWithTimes(ctx, action, startedAt, e.now(), nil, &domain.ActionError{
			Code: "unknown_handler", Message: herr.Error(),
		})
	}
	output, runErr := e.runner.RunWithCapability(ctx, &cap, handler, action.Payload)
	completedAt := e.now()

	var actionErr *domain.ActionError
	if runErr != nil {
		actionErr = &domain.ActionError{
			Code:      classifyErr(runErr),
			Message:   runErr.Error(),
			Retryable: handlerrunner.IsRetryable(runErr),
		}
	} else if oerr := e.validator.ValidateOutput(output, cap.OutputSchema); oerr != nil {
		actionErr = &domain.ActionError{
			Code:    "output_validation_failed",
			Message: oerr.Error(),
		}
	}

	return e.terminateWithTimes(ctx, action, startedAt, completedAt, output, actionErr)
}

// DryRun runs an action through the §5.3 flow without invoking a destination.
func (e *Executor) DryRun(ctx context.Context, action domain.Action) (domain.Simulation, error) {
	if action.IdempotencyKey == "" {
		action.IdempotencyKey = action.ID
	}
	action.Status = domain.StatusPending
	action.CreatedAt = nonZero(action.CreatedAt, e.now())
	action.UpdatedAt = e.now()

	if err := e.actions.Save(ctx, action); err != nil {
		return domain.Simulation{}, fmt.Errorf("persist pending action: %w", err)
	}
	e.appendAudit(ctx, action, audit.KindReceived, map[string]any{"dry_run": true})

	cap, err := e.registry.GetCapability(action.Capability)
	if err != nil {
		_, _ = e.reject(ctx, action, "unknown_capability", err.Error())
		return domain.Simulation{}, err
	}
	if verr := e.validator.ValidatePayload(action.Payload, cap.InputSchema); verr != nil {
		_, _ = e.reject(ctx, action, "validation_failed", verr.Error())
		return domain.Simulation{}, verr
	}

	decision := e.policy.Evaluate(ctx, action)
	action.PolicyDecision = &decision
	e.appendAudit(ctx, action, audit.KindPolicy, map[string]any{
		"decision": decision.Decision,
		"rule_id":  decision.RuleID,
		"reason":   decision.Reason,
	})

	preview := map[string]any{"note": "capability not simulatable; preview limited to validation + policy"}
	reversible := false
	if cap.Simulatable {
		if handler, herr := e.registry.GetHandler(action.Capability); herr == nil {
			if p, perr := e.runner.Simulate(ctx, handler, action.Payload); perr == nil {
				preview = p
				reversible = true
			} else {
				preview = map[string]any{"error": perr.Error()}
			}
		}
	}

	if err := e.transition(ctx, &action, domain.EventSimulate); err != nil {
		return domain.Simulation{}, err
	}
	completedAt := e.now()
	action.CompletedAt = &completedAt
	_ = e.actions.Save(ctx, action)
	e.appendAudit(ctx, action, audit.KindSimulated, map[string]any{"preview_keys": keys(preview)})

	return domain.Simulation{
		ActionID:       action.ID,
		PolicyDecision: decision,
		Validation:     e.validator.BuildReport(true, nil),
		Preview:        preview,
		Reversible:     reversible,
	}, nil
}

// ListCapabilities is a thin pass-through to the registry, kept on the
// Executor so the public API surface stays at three verbs.
func (e *Executor) ListCapabilities(ctx context.Context) ([]domain.Capability, error) {
	return e.registry.ListCapabilities(ctx)
}

// ErrNotReversible signals that the original action's capability does not
// implement capability.Compensator.
var ErrNotReversible = errors.New("capability does not support compensation")

// Revert runs the compensating action for a previously-succeeded action.
// The reversal is recorded under the same audit umbrella as the original;
// the audit kind is `compensated` and the detail carries the parent ID.
//
// Revert is idempotent at the Praxis layer: re-calling it on an already-
// compensated action returns the cached result.
func (e *Executor) Revert(ctx context.Context, actionID string) (domain.Result, error) {
	original, err := e.actions.Get(ctx, actionID)
	if err != nil {
		return domain.Result{}, fmt.Errorf("revert: load action %s: %w", actionID, err)
	}
	if original.Status != domain.StatusSucceeded {
		return domain.Result{}, fmt.Errorf("revert: action %s status is %s, not succeeded", actionID, original.Status)
	}
	handler, herr := e.registry.GetHandler(original.Capability)
	if herr != nil {
		return domain.Result{}, fmt.Errorf("revert: %w", herr)
	}
	comp, ok := handler.(capability.Compensator)
	if !ok {
		return domain.Result{}, fmt.Errorf("%w: %s", ErrNotReversible, original.Capability)
	}

	// Idempotency: if a revert was already remembered for this action, return it.
	revertKey := "revert:" + actionID
	if existing, lookErr := e.idempotent.Check(ctx, revertKey); lookErr == nil && existing != nil {
		return *existing, nil
	}

	out, cerr := comp.Compensate(ctx, original.Payload, original.Result)
	completedAt := e.now()

	res := domain.Result{
		ActionID:    actionID,
		Status:      domain.StatusSucceeded,
		Output:      out,
		StartedAt:   completedAt,
		CompletedAt: completedAt,
	}
	if cerr != nil {
		res.Status = domain.StatusFailed
		res.Error = &domain.ActionError{Code: "compensate_failed", Message: cerr.Error()}
	}

	e.appendAudit(ctx, original, audit.KindCompensated, map[string]any{
		"parent_action_id": actionID,
		"revert_status":    string(res.Status),
		"output":           res.Output,
	})

	if cerr == nil {
		_ = e.idempotent.Remember(ctx, revertKey, res)
	}
	if e.outcomes != nil {
		ev := e.mnemosEvent(original, res)
		ev.Type = "praxis.action_compensated"
		_ = e.outcomes.Emit(ctx, ev)
	}
	if cerr != nil {
		return res, cerr
	}
	return res, nil
}

// Resume drives a previously-async action (currently in `validated`) through
// the rest of the executor flow. Used by the jobs runner. Idempotent at the
// IdempotencyKeeper layer: a duplicate Resume call will short-circuit on the
// remembered result.
func (e *Executor) Resume(ctx context.Context, action domain.Action) (domain.Result, error) {
	cap, err := e.registry.GetCapability(action.Capability)
	if err != nil {
		return e.reject(ctx, action, "unknown_capability", err.Error())
	}

	if existing, err := e.idempotent.Check(ctx, action.IdempotencyKey); err == nil && existing != nil {
		return *existing, nil
	}

	if err := e.transition(ctx, &action, domain.EventExecute); err != nil {
		return domain.Result{}, err
	}
	startedAt := e.now()
	action.ExecutedAt = &startedAt
	_ = e.actions.Save(ctx, action)
	e.appendAudit(ctx, action, audit.KindExecuted, nil)

	handler, herr := e.registry.GetHandler(action.Capability)
	if herr != nil {
		return e.terminateWithTimes(ctx, action, startedAt, e.now(), nil, &domain.ActionError{
			Code: "unknown_handler", Message: herr.Error(),
		})
	}
	output, runErr := e.runner.RunWithCapability(ctx, &cap, handler, action.Payload)
	completedAt := e.now()

	var actionErr *domain.ActionError
	if runErr != nil {
		actionErr = &domain.ActionError{
			Code:      classifyErr(runErr),
			Message:   runErr.Error(),
			Retryable: handlerrunner.IsRetryable(runErr),
		}
	} else if oerr := e.validator.ValidateOutput(output, cap.OutputSchema); oerr != nil {
		actionErr = &domain.ActionError{
			Code:    "output_validation_failed",
			Message: oerr.Error(),
		}
	}

	return e.terminateWithTimes(ctx, action, startedAt, completedAt, output, actionErr)
}

// --- internals ---

func (e *Executor) transition(ctx context.Context, a *domain.Action, ev domain.ActionEvent) error {
	next, err := domain.TransitionAction(a.Status, ev)
	if err != nil {
		return fmt.Errorf("action %s: %w", a.ID, err)
	}
	a.Status = next
	a.UpdatedAt = e.now()
	return e.actions.UpdateStatus(ctx, a.ID, next)
}

func (e *Executor) reject(ctx context.Context, action domain.Action, code, msg string) (domain.Result, error) {
	if err := e.transition(ctx, &action, domain.EventReject); err != nil {
		e.logger.Error().Err(err).Str("action_id", action.ID).Msg("transition to rejected failed")
	}
	action.Error = &domain.ActionError{Code: code, Message: msg}
	_ = e.actions.Save(ctx, action)
	e.appendAudit(ctx, action, audit.KindRejected, map[string]any{"code": code, "reason": msg})
	res := domain.Result{
		ActionID:    action.ID,
		Status:      domain.StatusRejected,
		Error:       action.Error,
		StartedAt:   action.CreatedAt,
		CompletedAt: e.now(),
	}
	if e.outcomes != nil {
		_ = e.outcomes.Emit(ctx, e.mnemosEvent(action, res))
	}
	return res, errors.New(code + ": " + msg)
}

func (e *Executor) terminateWithTimes(ctx context.Context, action domain.Action, startedAt, completedAt time.Time, output map[string]any, ae *domain.ActionError) (domain.Result, error) {
	ev := domain.EventSucceed
	if ae != nil {
		ev = domain.EventFail
	}
	if err := e.transition(ctx, &action, ev); err != nil {
		return domain.Result{}, err
	}

	res := domain.Result{
		ActionID:    action.ID,
		Status:      action.Status,
		Output:      output,
		ExternalID:  externalIDFromOutput(output),
		Error:       ae,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}
	action.Result = output
	action.Error = ae
	action.CompletedAt = &completedAt
	_ = e.actions.PutResult(ctx, action.ID, res)

	kind := audit.KindSucceeded
	detail := map[string]any{"external_id": res.ExternalID}
	if ae != nil {
		kind = audit.KindFailed
		detail = map[string]any{"code": ae.Code, "message": ae.Message, "retryable": ae.Retryable}
	}
	e.appendAudit(ctx, action, kind, detail)

	if ae == nil {
		_ = e.idempotent.Remember(ctx, action.IdempotencyKey, res)
	}
	if e.outcomes != nil {
		_ = e.outcomes.Emit(ctx, e.mnemosEvent(action, res))
	}
	if action.CallbackURL != "" && e.webhook != nil {
		if werr := e.webhook.Notify(ctx, action, res); werr != nil {
			e.logger.Error().Err(werr).Str("action_id", action.ID).Msg("webhook notify failed")
		}
	}
	if ae != nil {
		return res, errors.New(ae.Code + ": " + ae.Message)
	}
	return res, nil
}

func (e *Executor) appendAudit(ctx context.Context, a domain.Action, kind string, extra map[string]any) {
	if e.auditRepo == nil {
		return
	}
	detail := map[string]any{
		"capability":  a.Capability,
		"caller_type": a.Caller.Type,
		"caller_id":   a.Caller.ID,
		"status":      string(a.Status),
	}
	for k, v := range extra {
		detail[k] = v
	}
	id := fmt.Sprintf("%s-%s-%d", a.ID, kind, e.now().UnixNano())
	if err := e.auditRepo.Append(ctx, domain.AuditEvent{
		ID:        id,
		ActionID:  a.ID,
		Kind:      kind,
		Detail:    detail,
		CreatedAt: e.now(),
	}); err != nil {
		e.logger.Error().Err(err).Str("action_id", a.ID).Str("kind", kind).Msg("audit append failed")
	}
}

func (e *Executor) mnemosEvent(a domain.Action, r domain.Result) domain.MnemosEvent {
	return domain.MnemosEvent{
		Type:       "praxis.action_completed",
		ActionID:   a.ID,
		Capability: a.Capability,
		Caller:     a.Caller,
		Status:     string(r.Status),
		ExternalID: r.ExternalID,
		Timestamp:  r.CompletedAt.Unix(),
	}
}

func nonZero(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func classifyErr(err error) string {
	switch {
	case errors.Is(err, handlerrunner.ErrTimeout):
		return "timeout"
	case errors.Is(err, handlerrunner.ErrPanic):
		return "panic"
	default:
		return "handler_error"
	}
}

func externalIDFromOutput(output map[string]any) string {
	if output == nil {
		return ""
	}
	if v, ok := output["external_id"].(string); ok {
		return v
	}
	if v, ok := output["ts"].(string); ok {
		return v
	}
	if v, ok := output["message_id"].(string); ok {
		return v
	}
	return ""
}
