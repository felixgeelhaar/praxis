// Package http implements the http_request capability — a generic HTTP
// adapter for one-off integrations the four cognitive systems may need
// before a dedicated handler exists for a vendor.
//
// Idempotency is forwarded as the Idempotency-Key request header (matching
// Stripe's RFC-draft-aligned convention). Servers that honour it will
// short-circuit duplicates; servers that don't simply ignore the header.
//
// Limits — kept tight on purpose:
//   - response body capped at 1 MiB by default (configurable via Config)
//   - default 30s timeout
//   - only http(s) URLs accepted; localhost/private addresses are not
//     blocked here (a Phase-3 SSRF policy belongs in the policy engine)
package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// Config tunes the HTTP handler.
type Config struct {
	Timeout         time.Duration
	MaxResponseBody int64
	Client          *nethttp.Client // injected for tests; defaults to a sane client
}

// Request handles the http_request capability.
type Request struct {
	cfg     Config
	client  *nethttp.Client
	maxBody int64
}

// New constructs a Request handler with sensible defaults.
func New(cfg Config) *Request {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxResponseBody <= 0 {
		cfg.MaxResponseBody = 1 << 20 // 1 MiB
	}
	c := cfg.Client
	if c == nil {
		c = &nethttp.Client{Timeout: cfg.Timeout}
	}
	return &Request{cfg: cfg, client: c, maxBody: cfg.MaxResponseBody}
}

// Name returns the capability name.
func (h *Request) Name() string { return "http_request" }

// Capability returns the descriptor used by the registry.
func (h *Request) Capability() domain.Capability {
	return domain.Capability{
		Name:        "http_request",
		Description: "Generic outbound HTTP request",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"method", "url"},
			"properties": map[string]any{
				"method":          map[string]any{"type": "string", "enum": []any{"GET", "POST", "PUT", "PATCH", "DELETE"}},
				"url":             map[string]any{"type": "string", "format": "uri"},
				"headers":         map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"body":            map[string]any{"type": "string"},
				"json":            map[string]any{"type": "object"},
				"idempotency_key": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"status_code"},
			"properties": map[string]any{
				"status_code": map[string]any{"type": "integer"},
				"headers":     map[string]any{"type": "object"},
				"body":        map[string]any{"type": "string"},
				"truncated":   map[string]any{"type": "boolean"},
			},
		},
		Permissions: []string{"http:request"},
		Simulatable: true,
		Idempotent:  false,
	}
}

// Execute issues the HTTP request.
func (h *Request) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	req, err := buildRequest(ctx, payload)
	if err != nil {
		return nil, err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, h.maxBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("http read body: %w", err)
	}
	truncated := false
	if int64(len(body)) > h.maxBody {
		body = body[:h.maxBody]
		truncated = true
	}

	headers := map[string]any{}
	for k, v := range resp.Header {
		if len(v) == 1 {
			headers[k] = v[0]
		} else {
			arr := make([]any, len(v))
			for i, s := range v {
				arr[i] = s
			}
			headers[k] = arr
		}
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"headers":     headers,
		"body":        string(body),
		"truncated":   truncated,
	}, nil
}

// Simulate returns a faithful preview without contacting the destination.
func (h *Request) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	method, urlStr, _, _, _, err := readPayload(payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"simulated": true,
		"method":    method,
		"url":       urlStr,
	}, nil
}

func buildRequest(ctx context.Context, payload map[string]any) (*nethttp.Request, error) {
	method, urlStr, headers, body, idem, err := readPayload(payload)
	if err != nil {
		return nil, err
	}

	req, err := nethttp.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("http build: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	return req, nil
}

func readPayload(payload map[string]any) (method, urlStr string, headers map[string]string, body io.Reader, idem string, err error) {
	method = strings.ToUpper(strings.TrimSpace(asString(payload["method"])))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return "", "", nil, nil, "", fmt.Errorf("http: unsupported method %q", method)
	}

	urlStr = asString(payload["url"])
	if urlStr == "" {
		return "", "", nil, nil, "", errors.New("http: url is required")
	}
	u, perr := url.Parse(urlStr)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", nil, nil, "", fmt.Errorf("http: invalid url %q", urlStr)
	}

	headers = map[string]string{}
	if h, ok := payload["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = asString(v)
		}
	}

	idem = asString(payload["idempotency_key"])

	if b, ok := payload["body"].(string); ok && b != "" {
		body = strings.NewReader(b)
	}
	if j, ok := payload["json"].(map[string]any); ok {
		buf := &bytes.Buffer{}
		// Best-effort JSON body. We use the minimal encoder here to avoid a
		// dependency cycle with encoding/json on every request path.
		raw, jerr := jsonMarshal(j)
		if jerr != nil {
			return "", "", nil, nil, "", fmt.Errorf("http: encode json body: %w", jerr)
		}
		buf.Write(raw)
		body = buf
		if _, exists := headers["Content-Type"]; !exists {
			headers["Content-Type"] = "application/json"
		}
	}

	return method, urlStr, headers, body, idem, nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

var _ capability.Handler = (*Request)(nil)
