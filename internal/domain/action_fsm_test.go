package domain_test

import (
	"errors"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

func TestTransitionAction_Allowed(t *testing.T) {
	tests := []struct {
		name  string
		from  domain.ActionStatus
		event domain.ActionEvent
		want  domain.ActionStatus
	}{
		{"pending validate -> validated", domain.StatusPending, domain.EventValidate, domain.StatusValidated},
		{"pending reject -> rejected", domain.StatusPending, domain.EventReject, domain.StatusRejected},
		{"pending simulate -> simulated", domain.StatusPending, domain.EventSimulate, domain.StatusSimulated},
		{"validated execute -> executing", domain.StatusValidated, domain.EventExecute, domain.StatusExecuting},
		{"validated simulate -> simulated", domain.StatusValidated, domain.EventSimulate, domain.StatusSimulated},
		{"executing succeed -> succeeded", domain.StatusExecuting, domain.EventSucceed, domain.StatusSucceeded},
		{"executing fail -> failed", domain.StatusExecuting, domain.EventFail, domain.StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.TransitionAction(tt.from, tt.event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("TransitionAction(%s,%s) = %s, want %s", tt.from, tt.event, got, tt.want)
			}
		})
	}
}

func TestTransitionAction_Forbidden(t *testing.T) {
	tests := []struct {
		name  string
		from  domain.ActionStatus
		event domain.ActionEvent
	}{
		{"pending execute", domain.StatusPending, domain.EventExecute},
		{"pending succeed", domain.StatusPending, domain.EventSucceed},
		{"pending fail", domain.StatusPending, domain.EventFail},
		{"validated reject", domain.StatusValidated, domain.EventReject},
		{"validated succeed", domain.StatusValidated, domain.EventSucceed},
		{"executing validate", domain.StatusExecuting, domain.EventValidate},
		{"executing simulate", domain.StatusExecuting, domain.EventSimulate},
		{"succeeded fail", domain.StatusSucceeded, domain.EventFail},
		{"failed succeed", domain.StatusFailed, domain.EventSucceed},
		{"simulated execute", domain.StatusSimulated, domain.EventExecute},
		{"rejected validate", domain.StatusRejected, domain.EventValidate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.TransitionAction(tt.from, tt.event)
			if err == nil {
				t.Fatalf("expected error for %s --%s-->", tt.from, tt.event)
			}
			if !errors.Is(err, domain.ErrInvalidTransition) {
				t.Errorf("expected ErrInvalidTransition, got %v", err)
			}
		})
	}
}

func TestIsFinal(t *testing.T) {
	finals := []domain.ActionStatus{
		domain.StatusSucceeded, domain.StatusFailed,
		domain.StatusSimulated, domain.StatusRejected,
	}
	nonfinals := []domain.ActionStatus{
		domain.StatusPending, domain.StatusValidated, domain.StatusExecuting,
	}
	for _, s := range finals {
		if !domain.IsFinal(s) {
			t.Errorf("IsFinal(%s) = false, want true", s)
		}
	}
	for _, s := range nonfinals {
		if domain.IsFinal(s) {
			t.Errorf("IsFinal(%s) = true, want false", s)
		}
	}
}

func TestActionMachine_Builds(t *testing.T) {
	m, err := domain.ActionMachine()
	if err != nil {
		t.Fatalf("ActionMachine() error: %v", err)
	}
	if m == nil {
		t.Fatal("ActionMachine() returned nil")
	}
}

func TestCanTransition(t *testing.T) {
	if !domain.CanTransition(domain.StatusPending, domain.StatusValidated) {
		t.Error("pending -> validated should be allowed")
	}
	if domain.CanTransition(domain.StatusSucceeded, domain.StatusFailed) {
		t.Error("succeeded -> failed should not be allowed")
	}
}
