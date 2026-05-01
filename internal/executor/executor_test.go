package executor_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

type fakeHandler struct {
	name      string
	output    map[string]any
	err       error
	simulate  map[string]any
	simErr    error
	callCount int
}

func (h *fakeHandler) Name() string { return h.name }

func (h *fakeHandler) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	h.callCount++
	return h.output, h.err
}

func (h *fakeHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return h.simulate, h.simErr
}

type capturingOutcomes struct {
	events []domain.MnemosEvent
}

func (c *capturingOutcomes) Emit(_ context.Context, ev domain.MnemosEvent) error {
	c.events = append(c.events, ev)
	return nil
}

type harness struct {
	exec     *executor.Executor
	repos    *ports.Repos
	handler  *fakeHandler
	outcomes *capturingOutcomes
}

func newHarness(t *testing.T, h *fakeHandler) *harness {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()

	registry := capability.New()
	registry.Register(h)

	pol := policy.New(logger, repos.Policy)
	idem := idempotency.New(repos.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	validator := schema.New()
	outcomes := &capturingOutcomes{}

	exec := executor.New(logger, registry, pol, idem, runner, validator,
		repos.Action, repos.Audit, outcomes)
	exec.SetClock(deterministicClock())

	return &harness{exec: exec, repos: repos, handler: h, outcomes: outcomes}
}

func deterministicClock() func() time.Time {
	t := time.Unix(0, 0).UTC()
	return func() time.Time {
		t = t.Add(time.Microsecond)
		return t
	}
}

func newAction(id string) domain.Action {
	return domain.Action{
		ID:         id,
		Capability: "test_handler",
		Payload:    map[string]any{"text": "hi"},
		Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
	}
}

func auditKinds(t *testing.T, h *harness, actionID string) []string {
	t.Helper()
	events, err := h.repos.Audit.ListForAction(context.Background(), actionID)
	if err != nil {
		t.Fatalf("ListForAction(%s): %v", actionID, err)
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

// --- branch coverage ---

func TestExecute_Success(t *testing.T) {
	h := newHarness(t, &fakeHandler{
		name:   "test_handler",
		output: map[string]any{"ts": "1234.5678", "ok": true},
	})
	res, err := h.exec.Execute(context.Background(), newAction("a1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != domain.StatusSucceeded {
		t.Errorf("Status=%s want succeeded", res.Status)
	}
	if res.ExternalID != "1234.5678" {
		t.Errorf("ExternalID=%q want 1234.5678", res.ExternalID)
	}
	if h.handler.callCount != 1 {
		t.Errorf("handler called %d times, want 1", h.handler.callCount)
	}
	if len(h.outcomes.events) != 1 {
		t.Fatalf("outcomes len=%d want 1", len(h.outcomes.events))
	}
	if h.outcomes.events[0].Status != "succeeded" {
		t.Errorf("outcome.Status=%s want succeeded", h.outcomes.events[0].Status)
	}

	want := []string{audit.KindReceived, audit.KindValidated, audit.KindPolicy, audit.KindExecuted, audit.KindSucceeded}
	if !containsSequence(auditKinds(t, h, "a1"), want) {
		t.Errorf("audit kinds=%v want sequence %v", auditKinds(t, h, "a1"), want)
	}
}

func TestExecute_HandlerFailure(t *testing.T) {
	h := newHarness(t, &fakeHandler{
		name: "test_handler",
		err:  errors.New("vendor 503 service unavailable"),
	})
	res, err := h.exec.Execute(context.Background(), newAction("a2"))
	if err == nil {
		t.Fatal("expected error")
	}
	if res.Status != domain.StatusFailed {
		t.Errorf("Status=%s want failed", res.Status)
	}
	if res.Error == nil {
		t.Fatal("Result.Error nil")
	}
	if !res.Error.Retryable {
		t.Errorf("expected Retryable=true for 5xx-shaped error")
	}

	want := []string{audit.KindReceived, audit.KindValidated, audit.KindPolicy, audit.KindExecuted, audit.KindFailed}
	if !containsSequence(auditKinds(t, h, "a2"), want) {
		t.Errorf("audit kinds=%v missing failure sequence", auditKinds(t, h, "a2"))
	}
}

func TestExecute_UnknownCapability(t *testing.T) {
	h := newHarness(t, &fakeHandler{name: "test_handler"})
	a := newAction("a3")
	a.Capability = "nope"
	_, err := h.exec.Execute(context.Background(), a)
	if err == nil {
		t.Fatal("expected error")
	}
	kinds := auditKinds(t, h, "a3")
	for _, want := range []string{audit.KindReceived, audit.KindRejected} {
		if !contains(kinds, want) {
			t.Errorf("missing %s in %v", want, kinds)
		}
	}
}

func TestExecute_IdempotencyHit(t *testing.T) {
	h := newHarness(t, &fakeHandler{
		name:   "test_handler",
		output: map[string]any{"ts": "1.0", "ok": true},
	})
	a := newAction("a4")
	a.IdempotencyKey = "shared-key"
	if _, err := h.exec.Execute(context.Background(), a); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if h.handler.callCount != 1 {
		t.Fatalf("first run callCount=%d want 1", h.handler.callCount)
	}

	a2 := newAction("a4-retry")
	a2.IdempotencyKey = "shared-key"
	res, err := h.exec.Execute(context.Background(), a2)
	if err != nil {
		t.Fatalf("retry Execute: %v", err)
	}
	if res.ActionID != "a4" {
		t.Errorf("retry returned ActionID=%s want a4 (cached)", res.ActionID)
	}
	if h.handler.callCount != 1 {
		t.Errorf("handler called %d times, want 1 (idempotency hit)", h.handler.callCount)
	}
}

func TestExecute_PolicyDeny(t *testing.T) {
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	mem := memory.New()
	handler := &fakeHandler{name: "test_handler"}
	registry := capability.New()
	registry.Register(handler)

	pol := policy.New(logger, mem.Policy)
	if err := pol.AddRule(context.Background(), domain.PolicyRule{
		ID:         "deny-all",
		Capability: "test_handler",
		Decision:   "deny",
		Reason:     "blocked",
	}); err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	idem := idempotency.New(mem.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	validator := schema.New()
	outcomes := &capturingOutcomes{}

	exec := executor.New(logger, registry, pol, idem, runner, validator,
		mem.Action, mem.Audit, outcomes)

	_, err := exec.Execute(context.Background(), newAction("a5"))
	if err == nil {
		t.Fatal("expected policy deny error")
	}
	events, _ := mem.Audit.ListForAction(context.Background(), "a5")
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	for _, want := range []string{audit.KindPolicy, audit.KindRejected} {
		if !contains(kinds, want) {
			t.Errorf("missing %s in %v", want, kinds)
		}
	}
	if handler.callCount != 0 {
		t.Errorf("handler called %d times on policy deny, want 0", handler.callCount)
	}
}

func TestExecute_RateLimited(t *testing.T) {
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	mem := memory.New()

	handler := &fakeHandler{name: "throttled", output: map[string]any{"ok": true}}

	// Wrap a handler whose Capability descriptor declares a tiny rate limit.
	rlHandler := &rateLimitedHandler{fakeHandler: handler}

	registry := capability.New()
	_ = registry.Register(rlHandler)
	pol := policy.New(logger, mem.Policy)
	idem := idempotency.New(mem.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	outcomes := &capturingOutcomes{}

	exec := executor.New(logger, registry, pol, idem, runner, schema.New(),
		mem.Action, mem.Audit, outcomes)

	a := newAction("rl-1")
	a.Capability = "throttled"

	if _, err := exec.Execute(context.Background(), a); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	a2 := newAction("rl-2")
	a2.Capability = "throttled"
	a2.Caller = a.Caller // same caller key
	if _, err := exec.Execute(context.Background(), a2); err == nil {
		t.Fatal("expected rate_limited error on second call")
	}

	events, _ := mem.Audit.ListForAction(context.Background(), "rl-2")
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if !contains(kinds, audit.KindThrottled) {
		t.Errorf("missing %s in %v", audit.KindThrottled, kinds)
	}
	if !contains(kinds, audit.KindRejected) {
		t.Errorf("missing rejected in %v", kinds)
	}
	if handler.callCount != 1 {
		t.Errorf("handler invoked %d times, want 1 (second call should be throttled)", handler.callCount)
	}
}

type rateLimitedHandler struct{ *fakeHandler }

func (h *rateLimitedHandler) Capability() domain.Capability {
	return domain.Capability{
		Name:        "throttled",
		Simulatable: true,
		Idempotent:  true,
		RateLimit:   &domain.RateLimitConfig{Rate: 1, Burst: 1, Interval: int64(time.Second)},
	}
}

func TestDryRun_Simulatable(t *testing.T) {
	h := newHarness(t, &fakeHandler{
		name:     "test_handler",
		simulate: map[string]any{"would_send": "hi"},
	})
	sim, err := h.exec.DryRun(context.Background(), newAction("a6"))
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !sim.Reversible {
		t.Errorf("Reversible=false want true for simulatable capability")
	}
	if sim.Preview["would_send"] != "hi" {
		t.Errorf("Preview=%v want would_send=hi", sim.Preview)
	}
	if h.handler.callCount != 0 {
		t.Errorf("Execute called during DryRun (callCount=%d)", h.handler.callCount)
	}
	if !contains(auditKinds(t, h, "a6"), audit.KindSimulated) {
		t.Errorf("missing simulated kind in %v", auditKinds(t, h, "a6"))
	}

	// action persisted with status simulated
	got, err := h.repos.Action.Get(context.Background(), "a6")
	if err != nil {
		t.Fatalf("Get a6: %v", err)
	}
	if got.Status != domain.StatusSimulated {
		t.Errorf("Action.Status=%s want simulated", got.Status)
	}
}

// --- helpers ---

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func containsSequence(haystack, needle []string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		ok := true
		for j, n := range needle {
			if haystack[i+j] != n {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
