package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// Lifecycle is a reconstructed view of an action's history derived purely
// from audit_events. It is the canary for evidence completeness — every
// terminal state must be reachable from the audit log alone.
type Lifecycle struct {
	ActionID       string
	Capability     string
	CallerType     string
	CallerID       string
	Statuses       []domain.ActionStatus // ordered transitions inferred from audit kinds
	FinalStatus    domain.ActionStatus
	PolicyDecision string
	PolicyReason   string
	ExternalID     string
	ErrorCode      string
	ErrorMessage   string
}

// ErrIncompleteAudit signals that an action's audit log lacks the events
// needed to reach a terminal state. Used by the replay canary.
var ErrIncompleteAudit = errors.New("incomplete audit lifecycle")

// Replay reconstructs an action's lifecycle from audit_events alone using
// the kind taxonomy in kinds.go. It does not consult the actions table —
// the whole point is to verify the audit log is self-sufficient.
func Replay(ctx context.Context, repo ports.AuditRepo, actionID string) (Lifecycle, error) {
	events, err := repo.ListForAction(ctx, actionID)
	if err != nil {
		return Lifecycle{}, fmt.Errorf("audit list: %w", err)
	}
	if len(events) == 0 {
		return Lifecycle{}, fmt.Errorf("%w: no events for %s", ErrIncompleteAudit, actionID)
	}

	lc := Lifecycle{ActionID: actionID}
	for _, e := range events {
		applyEvent(&lc, e)
	}

	if !isTerminalStatus(lc.FinalStatus) {
		return lc, fmt.Errorf("%w: %s ended in non-terminal status %q", ErrIncompleteAudit, actionID, lc.FinalStatus)
	}
	return lc, nil
}

func applyEvent(lc *Lifecycle, e domain.AuditEvent) {
	if cap, ok := e.Detail["capability"].(string); ok && cap != "" {
		lc.Capability = cap
	}
	if ct, ok := e.Detail["caller_type"].(string); ok && ct != "" {
		lc.CallerType = ct
	}
	if cid, ok := e.Detail["caller_id"].(string); ok && cid != "" {
		lc.CallerID = cid
	}

	switch e.Kind {
	case KindReceived:
		lc.Statuses = append(lc.Statuses, domain.StatusPending)
	case KindValidated:
		lc.Statuses = append(lc.Statuses, domain.StatusValidated)
	case KindPolicy:
		if d, ok := e.Detail["decision"].(string); ok {
			lc.PolicyDecision = d
		}
		if r, ok := e.Detail["reason"].(string); ok {
			lc.PolicyReason = r
		}
	case KindExecuted:
		lc.Statuses = append(lc.Statuses, domain.StatusExecuting)
	case KindSucceeded:
		lc.Statuses = append(lc.Statuses, domain.StatusSucceeded)
		lc.FinalStatus = domain.StatusSucceeded
		if id, ok := e.Detail["external_id"].(string); ok {
			lc.ExternalID = id
		}
	case KindFailed:
		lc.Statuses = append(lc.Statuses, domain.StatusFailed)
		lc.FinalStatus = domain.StatusFailed
		if c, ok := e.Detail["code"].(string); ok {
			lc.ErrorCode = c
		}
		if m, ok := e.Detail["message"].(string); ok {
			lc.ErrorMessage = m
		}
	case KindSimulated:
		lc.Statuses = append(lc.Statuses, domain.StatusSimulated)
		lc.FinalStatus = domain.StatusSimulated
	case KindRejected:
		lc.Statuses = append(lc.Statuses, domain.StatusRejected)
		lc.FinalStatus = domain.StatusRejected
		if c, ok := e.Detail["code"].(string); ok {
			lc.ErrorCode = c
		}
		if r, ok := e.Detail["reason"].(string); ok {
			lc.ErrorMessage = r
		}
	}
}

func isTerminalStatus(s domain.ActionStatus) bool {
	switch s {
	case domain.StatusSucceeded, domain.StatusFailed, domain.StatusSimulated, domain.StatusRejected:
		return true
	}
	return false
}
