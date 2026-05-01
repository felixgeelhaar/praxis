package plugin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/plugin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type slowHandler struct {
	delay time.Duration
}

func (slowHandler) Name() string { return "slow" }
func (h slowHandler) Execute(ctx context.Context, _ map[string]any) (map[string]any, error) {
	select {
	case <-time.After(h.delay):
		return map[string]any{"ok": true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (h slowHandler) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.Execute(ctx, p)
}

type httpHandler struct {
	url string
}

func (httpHandler) Name() string { return "http" }
func (h httpHandler) Execute(ctx context.Context, _ map[string]any) (map[string]any, error) {
	client := plugin.HTTPClient(ctx)
	if client == nil {
		client = http.DefaultClient
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return map[string]any{"status": resp.StatusCode}, nil
}
func (h httpHandler) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.Execute(ctx, p)
}

func TestSandboxed_CPUTimeoutAbortsExecute(t *testing.T) {
	h := plugin.Sandboxed(slowHandler{delay: 200 * time.Millisecond}, plugin.ResourceBudget{
		CPUTimeout: 20 * time.Millisecond,
	})
	start := time.Now()
	_, err := h.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("did not abort early: elapsed=%v", elapsed)
	}
}

func TestSandboxed_ZeroCPUTimeoutMeansUnlimited(t *testing.T) {
	h := plugin.Sandboxed(slowHandler{delay: 10 * time.Millisecond}, plugin.ResourceBudget{
		CPUTimeout: 0,
	})
	out, err := h.Execute(context.Background(), nil)
	if err != nil {
		t.Errorf("zero timeout should not abort: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("out=%v", out)
	}
}

func TestSandboxed_AllowedHostReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	h := plugin.Sandboxed(httpHandler{url: srv.URL}, plugin.ResourceBudget{
		AllowedHosts: []string{u.Hostname()},
	})
	out, err := h.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["status"] != http.StatusTeapot {
		t.Errorf("status=%v want 418", out["status"])
	}
}

func TestSandboxed_DeniedHostBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := plugin.Sandboxed(httpHandler{url: srv.URL}, plugin.ResourceBudget{
		AllowedHosts: []string{"only-this-host.example"},
	})
	_, err := h.Execute(context.Background(), nil)
	if !errors.Is(err, plugin.ErrEgressBlocked) {
		t.Errorf("err=%v want ErrEgressBlocked", err)
	}
}

func TestSandboxed_EmptyAllowedHostsMeansNoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	// Budget exists with no allowlist → block every outbound call.
	h := plugin.Sandboxed(httpHandler{url: srv.URL}, plugin.ResourceBudget{
		CPUTimeout: time.Second,
	})
	_, err := h.Execute(context.Background(), nil)
	if !errors.Is(err, plugin.ErrEgressBlocked) {
		t.Errorf("err=%v want ErrEgressBlocked", err)
	}
}

func TestSandboxed_WrapsSimulate(t *testing.T) {
	h := plugin.Sandboxed(slowHandler{delay: 200 * time.Millisecond}, plugin.ResourceBudget{
		CPUTimeout: 20 * time.Millisecond,
	})
	_, err := h.Simulate(context.Background(), nil)
	if err == nil {
		t.Fatal("expected timeout on Simulate")
	}
}

func TestSandboxed_PreservesName(t *testing.T) {
	h := plugin.Sandboxed(slowHandler{}, plugin.ResourceBudget{})
	if h.Name() != "slow" {
		t.Errorf("Name=%s want slow", h.Name())
	}
}

func TestHTTPClient_AbsentReturnsNil(t *testing.T) {
	if c := plugin.HTTPClient(context.Background()); c != nil {
		t.Errorf("expected nil client outside sandbox, got %v", c)
	}
}

func TestSandboxed_OutboundRequestCarriesTraceparent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(otel.GetTracerProvider())
	})

	var sawHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	h := plugin.Sandboxed(httpHandler{url: srv.URL}, plugin.ResourceBudget{
		AllowedHosts: []string{u.Hostname()},
	})

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "outer")
	defer span.End()

	if _, err := h.Execute(ctx, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sawHeader == "" {
		t.Error("server did not see Traceparent header on outbound request")
	}
}
