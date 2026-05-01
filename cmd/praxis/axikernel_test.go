package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/outcome"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

type echoHandler struct{}

func (echoHandler) Name() string { return "echo" }
func (echoHandler) Execute(_ context.Context, p map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true, "echo": p, "ts": "1.0"}, nil
}
func (echoHandler) Simulate(_ context.Context, p map[string]any) (map[string]any, error) {
	return map[string]any{"would_echo": p}, nil
}
func (echoHandler) Capability() domain.Capability {
	return domain.Capability{Name: "echo", Simulatable: true, Idempotent: true}
}

func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()
	reg := capability.New()
	_ = reg.Register(echoHandler{})
	pol := policy.New(logger, repos.Policy)
	idem := idempotency.New(repos.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	emitter := outcome.New(logger, repos.Outbox, outcome.Config{})

	exec := executor.New(logger, reg, pol, idem, runner, schema.New(),
		repos.Action, repos.Audit, emitter)

	auditSvc := audit.New(repos.Audit)

	mux := newMux(kernelDeps{
		logger: logger, exec: exec, registry: reg, repos: repos,
		auditSvc: auditSvc,
		emitter:  emitter, apiToken: token,
	}, &metrics{})

	return httptest.NewServer(mux)
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
}

func TestMetrics(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "praxis_actions_total") {
		t.Errorf("metrics missing praxis_actions_total: %s", body)
	}
}

func TestMetrics_AuditPurgeCounter(t *testing.T) {
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	repos := memory.New()
	reg := capability.New()
	pol := policy.New(logger, repos.Policy)
	idem := idempotency.New(repos.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	emitter := outcome.New(logger, repos.Outbox, outcome.Config{})
	exec := executor.New(logger, reg, pol, idem, runner, schema.New(),
		repos.Action, repos.Audit, emitter)
	auditSvc := audit.New(repos.Audit)
	m := &metrics{}
	m.addAuditPurge("org-a", "ok", 7)
	m.addAuditPurge("org-b", "error", 1)

	mux := newMux(kernelDeps{
		logger: logger, exec: exec, registry: reg, repos: repos,
		auditSvc: auditSvc, emitter: emitter,
	}, m)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	out := string(body)
	if !strings.Contains(out, `praxis_audit_purge_total{org_id="org-a",result="ok"} 7`) {
		t.Errorf("missing org-a counter: %s", out)
	}
	if !strings.Contains(out, `praxis_audit_purge_total{org_id="org-b",result="error"} 1`) {
		t.Errorf("missing org-b counter: %s", out)
	}
}

func TestCapabilities_AuthRequired(t *testing.T) {
	srv := newTestServer(t, "secret")
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/capabilities")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("no token: got %d want 401", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authed GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("authed: got %d want 200", resp.StatusCode)
	}
}

func TestExecute_HappyPath(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	body, _ := json.Marshal(domain.Action{
		Capability: "echo",
		Payload:    map[string]any{"hello": "world"},
		Caller:     domain.CallerRef{Type: "user"},
	})
	resp, err := http.Post(srv.URL+"/v1/actions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/actions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var res domain.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Status != domain.StatusSucceeded {
		t.Errorf("Status=%s want succeeded", res.Status)
	}
}

func TestExecute_UnknownCapability(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	body, _ := json.Marshal(domain.Action{
		Capability: "missing",
		Payload:    map[string]any{},
		Caller:     domain.CallerRef{Type: "user"},
	})
	resp, err := http.Post(srv.URL+"/v1/actions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestDryRun(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	body, _ := json.Marshal(domain.Action{
		ID: "act-dr", Capability: "echo", Payload: map[string]any{"x": 1},
		Caller: domain.CallerRef{Type: "user"},
	})
	resp, err := http.Post(srv.URL+"/v1/actions/act-dr/dry-run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST dry-run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
	var sim domain.Simulation
	_ = json.NewDecoder(resp.Body).Decode(&sim)
	if !sim.Reversible {
		t.Errorf("Reversible=false")
	}
}

func TestGetAction_NotFound(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/actions/missing")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}
}

func TestAuditSearch(t *testing.T) {
	srv := newTestServer(t, "")
	defer srv.Close()

	// Execute one action so audit has entries.
	body, _ := json.Marshal(domain.Action{
		Capability: "echo", Payload: map[string]any{}, Caller: domain.CallerRef{Type: "user"},
	})
	resp, err := http.Post(srv.URL+"/v1/actions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/v1/audit?capability=echo")
	if err != nil {
		t.Fatalf("GET /v1/audit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
	var out struct {
		Events []domain.AuditEvent `json:"events"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Events) == 0 {
		t.Errorf("expected events for capability=echo")
	}
}
