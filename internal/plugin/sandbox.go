package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ErrEgressBlocked signals that a sandboxed plugin attempted to reach a
// host outside its declared AllowedHosts. The wrapped handler surfaces
// this error directly so audit detail records the blocked destination.
var ErrEgressBlocked = errors.New("plugin egress blocked: host not in allowlist")

// ResourceBudget caps what a plugin handler may consume during a single
// Execute or Simulate call. Plugins opt in by implementing
// BudgetedPlugin; unknown plugins run unsandboxed.
//
// Phase 3 M3.1 enforces:
//
//   - CPUTimeout: real, via context.WithTimeout. Zero disables the cap.
//   - AllowedHosts: real, via a runtime-supplied http.Client whose
//     transport rejects unlisted destinations. Plugins that bypass the
//     supplied client (e.g. instantiate their own net.Dialer) defeat
//     this layer; the contract is "use plugin.HTTPClient(ctx) for any
//     outbound call."
//   - MaxMemoryBytes: NOT enforced in-process. Go's runtime does not
//     expose per-handler accounting and the global allocator is shared
//     with the rest of Praxis. The field is reserved so plugin authors
//     can declare their intended ceiling today; the out-of-process
//     loader (future task) will hold it to that limit.
type ResourceBudget struct {
	CPUTimeout     time.Duration
	MaxMemoryBytes uint64
	AllowedHosts   []string
}

// BudgetedPlugin is the optional interface a plugin implements to declare
// its resource budget. The loader detects this via type assertion so the
// stable Plugin interface (ABI v1) does not break for plugins that don't
// opt in.
type BudgetedPlugin interface {
	Plugin
	Budget() ResourceBudget
}

// Sandboxed wraps a handler so each Execute and Simulate call runs under
// the supplied budget. The wrapper is always-on: if the caller invokes
// Sandboxed, the handler is sandboxed. Use this only when the budget was
// explicitly declared (e.g. plugin author opted in via BudgetedPlugin).
func Sandboxed(inner capability.Handler, budget ResourceBudget) capability.Handler {
	return &sandboxedHandler{inner: inner, budget: budget}
}

type sandboxedHandler struct {
	inner  capability.Handler
	budget ResourceBudget
}

func (s *sandboxedHandler) Name() string { return s.inner.Name() }

func (s *sandboxedHandler) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	return s.run(ctx, payload, s.inner.Execute)
}

func (s *sandboxedHandler) Simulate(ctx context.Context, payload map[string]any) (map[string]any, error) {
	return s.run(ctx, payload, s.inner.Simulate)
}

func (s *sandboxedHandler) run(
	ctx context.Context,
	payload map[string]any,
	fn func(context.Context, map[string]any) (map[string]any, error),
) (map[string]any, error) {
	if s.budget.CPUTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.budget.CPUTimeout)
		defer cancel()
	}
	ctx = withHTTPClient(ctx, s.buildClient())
	return fn(ctx, payload)
}

func (s *sandboxedHandler) buildClient() *http.Client {
	allowed := make(map[string]struct{}, len(s.budget.AllowedHosts))
	for _, h := range s.budget.AllowedHosts {
		allowed[h] = struct{}{}
	}
	// Wrap inside-out: hostFilter rejects denied destinations BEFORE
	// the request hits the network; otelhttp wraps that result so the
	// outbound call gets traceparent propagation, request/response
	// span attrs, and child-span linkage to the executor's
	// handler.<cap> span. Order matters — putting otelhttp inside
	// hostFilter would still create the span for blocked requests,
	// which misrepresents what was attempted.
	return &http.Client{
		Transport: otelhttp.NewTransport(&hostFilterTransport{
			base:    http.DefaultTransport,
			allowed: allowed,
		}),
	}
}

// hostFilterTransport is an http.RoundTripper that rejects requests
// whose target host is not in the allowlist. The lookup uses the URL's
// Hostname() (no port, no userinfo) so an entry of "api.example.com"
// matches https://api.example.com:443/v1/foo.
type hostFilterTransport struct {
	base    http.RoundTripper
	allowed map[string]struct{}
}

func (t *hostFilterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if _, ok := t.allowed[host]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrEgressBlocked, host)
	}
	return t.base.RoundTrip(req)
}

type httpClientKey struct{}

// HTTPClient retrieves the sandbox-supplied http.Client from ctx. Plugin
// authors call this for every outbound HTTP request — the returned
// client honours the plugin's AllowedHosts policy. Returns nil if the
// context carries no sandbox client (handler is not sandboxed).
func HTTPClient(ctx context.Context) *http.Client {
	c, _ := ctx.Value(httpClientKey{}).(*http.Client)
	return c
}

func withHTTPClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, httpClientKey{}, c)
}
