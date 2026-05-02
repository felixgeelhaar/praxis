// Package client is the public Go client for the Praxis HTTP API.
//
// The client mirrors the Mnemos / Chronos client patterns: typed methods,
// option-based construction (WithToken, WithLogger, WithRetry, WithHTTPClient),
// errors.As-friendly APIError, and concurrent-safe.
//
// Example:
//
//	c := client.New("http://praxis.local:8080",
//	    client.WithToken("secret"),
//	    client.WithLogger(logger))
//	res, err := c.Execute(ctx, action)
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/fortify/retry"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// Client talks to a Praxis HTTP API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	logger  *bolt.Logger
	retry   retry.Retry[*http.Response]
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithToken sets the bearer token sent on every request.
func WithToken(t string) Option { return func(c *Client) { c.token = t } }

// WithHTTPClient overrides the default *http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithLogger attaches a structured logger. Nil disables logging.
func WithLogger(l *bolt.Logger) Option { return func(c *Client) { c.logger = l } }

// WithRetry replaces the default retry policy. The default retries 5xx +
// 429 with exponential backoff and jitter and fails fast on 4xx.
func WithRetry(r retry.Retry[*http.Response]) Option { return func(c *Client) { c.retry = r } }

// New constructs a Client. baseURL must include the scheme; it is trimmed
// of any trailing slash.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
		//nolint:bodyclose // factory is constructing a retry strategy, not making an HTTP call.
		retry: retry.New[*http.Response](retry.Config{
			MaxAttempts:   3,
			InitialDelay:  200 * time.Millisecond,
			MaxDelay:      5 * time.Second,
			Multiplier:    2.0,
			BackoffPolicy: retry.BackoffExponential,
			Jitter:        true,
			IsRetryable:   defaultIsRetryable,
		}),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is returned for any non-2xx response. It satisfies errors.As so
// callers can branch on Status:
//
//	var apiErr *client.APIError
//	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden { ... }
type APIError struct {
	Status  int
	Message string
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Message == "" {
		return "praxis: HTTP " + strconv.Itoa(e.Status)
	}
	return "praxis: " + strconv.Itoa(e.Status) + " " + e.Message
}

// Retryable reports whether the error should be retried.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || (e.Status >= 500 && e.Status < 600)
}

// listResponse wraps the GET /v1/capabilities body.
type listResponse struct {
	Capabilities []domain.Capability `json:"capabilities"`
}

// ListCapabilities fetches the capability catalog.
func (c *Client) ListCapabilities(ctx context.Context) ([]domain.Capability, error) {
	var out listResponse
	if err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &out); err != nil {
		return nil, err
	}
	return out.Capabilities, nil
}

// GetCapability fetches a single capability by name.
func (c *Client) GetCapability(ctx context.Context, name string) (domain.Capability, error) {
	var capDesc domain.Capability
	err := c.do(ctx, http.MethodGet, "/v1/capabilities/"+name, nil, &capDesc)
	return capDesc, err
}

// Execute submits an action for synchronous execution.
func (c *Client) Execute(ctx context.Context, action domain.Action) (domain.Result, error) {
	var res domain.Result
	err := c.do(ctx, http.MethodPost, "/v1/actions", action, &res)
	return res, err
}

// DryRun submits an action for simulation. The action ID is reflected in
// the URL path so audit + replay treat the call as the same lifecycle.
func (c *Client) DryRun(ctx context.Context, action domain.Action) (domain.Simulation, error) {
	if action.ID == "" {
		return domain.Simulation{}, errors.New("praxis: DryRun requires Action.ID")
	}
	var sim domain.Simulation
	err := c.do(ctx, http.MethodPost, "/v1/actions/"+action.ID+"/dry-run", action, &sim)
	return sim, err
}

// GetAction fetches a stored action by ID.
func (c *Client) GetAction(ctx context.Context, id string) (domain.Action, error) {
	var a domain.Action
	err := c.do(ctx, http.MethodGet, "/v1/actions/"+id, nil, &a)
	return a, err
}

// --- internals ---

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	url := c.baseURL + path
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("praxis: encode body: %w", err)
		}
	}

	resp, err := c.retry.Do(ctx, func(ctx context.Context) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		r, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if r.StatusCode >= 400 {
			b, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			msg := strings.TrimSpace(string(b))
			return nil, &APIError{Status: r.StatusCode, Message: msg}
		}
		return r, nil
	})
	if err != nil {
		if c.logger != nil {
			c.logger.Error().Err(err).Str("method", method).Str("path", path).Msg("praxis client")
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("praxis: decode response: %w", err)
		}
	}
	return nil
}

func bodyReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return bytes.NewReader(b)
}

func defaultIsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Retryable()
	}
	// network/transport errors are retryable
	return true
}
