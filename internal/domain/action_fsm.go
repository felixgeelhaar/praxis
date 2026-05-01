package domain

import (
	"errors"
	"fmt"
	"sync"

	"github.com/felixgeelhaar/statekit"
)

// ActionEvent drives a transition in the Action lifecycle FSM.
type ActionEvent string

const (
	EventValidate ActionEvent = "VALIDATE"
	EventReject   ActionEvent = "REJECT"
	EventSimulate ActionEvent = "SIMULATE"
	EventExecute  ActionEvent = "EXECUTE"
	EventSucceed  ActionEvent = "SUCCEED"
	EventFail     ActionEvent = "FAIL"
)

// ErrInvalidTransition is returned when an event does not produce a transition
// from the current state.
var ErrInvalidTransition = errors.New("invalid action transition")

// ActionMachineID is the statekit machine identifier for the Action lifecycle.
const ActionMachineID = "praxis.action"

// actionTransition describes a single edge in the FSM.
type actionTransition struct {
	From  ActionStatus
	Event ActionEvent
	To    ActionStatus
}

// actionTransitions is the canonical Action lifecycle table. It is the single
// source of truth for both stateless transition checks and the statekit
// machine config built in actionMachine().
//
//	pending  --VALIDATE-->  validated
//	pending  --REJECT---->  rejected   (final)
//	pending  --SIMULATE-->  simulated  (final)
//	validated --EXECUTE--> executing
//	validated --SIMULATE-> simulated   (final)
//	executing --SUCCEED--> succeeded   (final)
//	executing --FAIL---->  failed      (final)
var actionTransitions = []actionTransition{
	{StatusPending, EventValidate, StatusValidated},
	{StatusPending, EventReject, StatusRejected},
	{StatusPending, EventSimulate, StatusSimulated},
	{StatusValidated, EventExecute, StatusExecuting},
	{StatusValidated, EventSimulate, StatusSimulated},
	{StatusExecuting, EventSucceed, StatusSucceeded},
	{StatusExecuting, EventFail, StatusFailed},
}

// actionFinalStates lists the terminal states (no outgoing edges).
var actionFinalStates = []ActionStatus{
	StatusSucceeded, StatusFailed, StatusSimulated, StatusRejected,
}

type actionCtx struct{}

var (
	actionMachineOnce sync.Once
	actionMachineErr  error
	actionMachineCfg  *statekit.MachineConfig[actionCtx]
)

// ActionMachine returns the statekit machine config for the Action lifecycle.
// Built once and shared. Useful for visualization, event sourcing, or
// constructing per-action interpreters in higher layers.
func ActionMachine() (*statekit.MachineConfig[actionCtx], error) {
	actionMachineOnce.Do(func() {
		actionMachineCfg, actionMachineErr = buildActionMachine()
	})
	return actionMachineCfg, actionMachineErr
}

func buildActionMachine() (*statekit.MachineConfig[actionCtx], error) {
	b := statekit.NewMachine[actionCtx](ActionMachineID).
		WithInitial(statekit.StateID(StatusPending))

	// Group transitions by source state for the fluent builder.
	bySource := map[ActionStatus][]actionTransition{}
	for _, t := range actionTransitions {
		bySource[t.From] = append(bySource[t.From], t)
	}

	for _, src := range []ActionStatus{StatusPending, StatusValidated, StatusExecuting} {
		sb := b.State(statekit.StateID(src))
		edges := bySource[src]
		for i, e := range edges {
			tb := sb.On(statekit.EventType(e.Event)).Target(statekit.StateID(e.To))
			if i == len(edges)-1 {
				b = tb.Done()
			} else {
				sb = tb.End()
			}
		}
	}

	for i, fs := range actionFinalStates {
		sb := b.State(statekit.StateID(fs)).Final()
		if i == len(actionFinalStates)-1 {
			b = sb.Done()
		} else {
			b = sb.Done()
		}
	}

	cfg, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("build action machine: %w", err)
	}
	return cfg, nil
}

// TransitionAction returns the next ActionStatus given the current status and
// an event, or ErrInvalidTransition if no transition is defined.
func TransitionAction(current ActionStatus, event ActionEvent) (ActionStatus, error) {
	for _, t := range actionTransitions {
		if t.From == current && t.Event == event {
			return t.To, nil
		}
	}
	return current, fmt.Errorf("%w: %s --%s--> ?", ErrInvalidTransition, current, event)
}

// CanTransition reports whether an Action in `from` can move to `to` via any
// declared event.
func CanTransition(from, to ActionStatus) bool {
	for _, t := range actionTransitions {
		if t.From == from && t.To == to {
			return true
		}
	}
	return false
}

// IsFinal reports whether s is a terminal state.
func IsFinal(s ActionStatus) bool {
	for _, f := range actionFinalStates {
		if f == s {
			return true
		}
	}
	return false
}

// CanTransitionTo mirrors the previous hand-rolled API for ergonomic call
// sites; backed by the FSM table above.
func (s ActionStatus) CanTransitionTo(next ActionStatus) bool {
	return CanTransition(s, next)
}
