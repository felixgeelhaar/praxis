package domain_test

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

func TestActionStatus_Transitions(t *testing.T) {
	tests := []struct {
		name   string
		from   domain.ActionStatus
		to     domain.ActionStatus
		wantOk bool
	}{
		{"pending to validated", domain.StatusPending, domain.StatusValidated, true},
		{"pending to rejected", domain.StatusPending, domain.StatusRejected, true},
		{"pending to simulated", domain.StatusPending, domain.StatusSimulated, true},
		{"validated to executing", domain.StatusValidated, domain.StatusExecuting, true},
		{"validated to simulated", domain.StatusValidated, domain.StatusSimulated, true},
		{"executing to succeeded", domain.StatusExecuting, domain.StatusSucceeded, true},
		{"executing to failed", domain.StatusExecuting, domain.StatusFailed, true},
		// Invalid transitions
		{"pending to executing", domain.StatusPending, domain.StatusExecuting, false},
		{"succeeded to failed", domain.StatusSucceeded, domain.StatusFailed, false},
		{"failed to succeeded", domain.StatusFailed, domain.StatusSucceeded, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := tt.from.CanTransitionTo(tt.to)
			if ok != tt.wantOk {
				t.Errorf("CanTransitionTo(%s -> %s) = %v, want %v", tt.from, tt.to, ok, tt.wantOk)
			}
		})
	}
}

func TestAction_New(t *testing.T) {
	now := time.Now()
	action := domain.Action{
		ID:             "test-123",
		Capability:     "send_message",
		Payload:        map[string]any{"channel": "#general", "text": "Hello"},
		Caller:         domain.CallerRef{Type: "user", ID: "user-1"},
		Scope:          []string{"write"},
		IdempotencyKey: "test-123",
		Status:         domain.StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if action.ID != "test-123" {
		t.Errorf("Action.ID = %s, want test-123", action.ID)
	}
	if action.Capability != "send_message" {
		t.Errorf("Action.Capability = %s, want send_message", action.Capability)
	}
	if action.Status != domain.StatusPending {
		t.Errorf("Action.Status = %s, want pending", action.Status)
	}
}

func TestResult_New(t *testing.T) {
	now := time.Now()
	result := domain.Result{
		ActionID:    "test-123",
		Status:      domain.StatusSucceeded,
		Output:      map[string]any{"ok": true, "ts": "123.456"},
		ExternalID:  "123.456",
		StartedAt:   now,
		CompletedAt: now,
	}

	if result.ActionID != "test-123" {
		t.Errorf("Result.ActionID = %s, want test-123", result.ActionID)
	}
	if result.Status != domain.StatusSucceeded {
		t.Errorf("Result.Status = %s, want succeeded", result.Status)
	}
	if result.ExternalID != "123.456" {
		t.Errorf("Result.ExternalID = %s, want 123.456", result.ExternalID)
	}
}

func TestPolicyDecision(t *testing.T) {
	decision := domain.PolicyDecision{
		Decision:    "allow",
		RuleID:      "global_allow",
		Reason:      "Phase 1: global allow",
		EvaluatedAt: time.Now(),
	}

	if decision.Decision != "allow" {
		t.Errorf("Decision = %s, want allow", decision.Decision)
	}
	if decision.RuleID != "global_allow" {
		t.Errorf("RuleID = %s, want global_allow", decision.RuleID)
	}
}

func TestCallerRef(t *testing.T) {
	tests := []struct {
		name     string
		caller   domain.CallerRef
		wantType string
		wantID   string
	}{
		{"nous", domain.CallerRef{Type: "nous", ID: "brain-1"}, "nous", "brain-1"},
		{"agent", domain.CallerRef{Type: "agent", ID: "agent-42"}, "agent", "agent-42"},
		{"user", domain.CallerRef{Type: "user", ID: "user-1"}, "user", "user-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.caller.Type != tt.wantType {
				t.Errorf("CallerRef.Type = %s, want %s", tt.caller.Type, tt.wantType)
			}
			if tt.caller.ID != tt.wantID {
				t.Errorf("CallerRef.ID = %s, want %s", tt.caller.ID, tt.wantID)
			}
		})
	}
}
