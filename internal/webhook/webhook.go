// Package webhook delivers terminal-action notifications to caller-supplied
// URLs. The payload is HMAC-SHA256 signed when the caller registered a
// secret on Action.CallbackSecret; otherwise the body is sent unsigned.
//
// The Notifier is invoked by the executor (and the jobs runner) once an
// action reaches a terminal status. Delivery is best-effort; failures are
// logged via bolt — at-least-once retry should be layered via the outbox
// in a follow-up. Phase-2 ships the synchronous send so the contract and
// signature shape are nailed down first.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

// SignatureHeader is the request header carrying the HMAC-SHA256 hex
// digest of the raw body. Mirrors the convention used by GitHub
// (X-Hub-Signature-256) but keeps the prefix Praxis-specific.
const SignatureHeader = "X-Praxis-Signature"

// EventHeader names the type of event being delivered.
const EventHeader = "X-Praxis-Event"

// Notifier posts terminal-action results to caller URLs.
type Notifier struct {
	logger *bolt.Logger
	client *http.Client
}

// New constructs a Notifier with the supplied HTTP client. A nil client
// gets a 5-second-timeout default.
func New(logger *bolt.Logger, client *http.Client) *Notifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Notifier{logger: logger, client: client}
}

// Payload is the request body delivered to the callback URL.
type Payload struct {
	Type        string         `json:"type"`
	ActionID    string         `json:"action_id"`
	Capability  string         `json:"capability"`
	Status      string         `json:"status"`
	Output      map[string]any `json:"output,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	Error       *errorPayload  `json:"error,omitempty"`
	CompletedAt time.Time      `json:"completed_at"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Notify posts a webhook for a terminal action. Returns nil when the
// destination accepted (2xx); otherwise returns an error so the caller can
// retry. Actions without a CallbackURL are a no-op.
func (n *Notifier) Notify(ctx context.Context, action domain.Action, result domain.Result) error {
	if action.CallbackURL == "" {
		return nil
	}

	pl := Payload{
		Type:        "praxis.action_completed",
		ActionID:    action.ID,
		Capability:  action.Capability,
		Status:      string(result.Status),
		Output:      result.Output,
		ExternalID:  result.ExternalID,
		CompletedAt: result.CompletedAt,
	}
	if result.Error != nil {
		pl.Error = &errorPayload{Code: result.Error.Code, Message: result.Error.Message}
	}

	body, err := json.Marshal(pl)
	if err != nil {
		return fmt.Errorf("webhook: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(EventHeader, pl.Type)
	if action.CallbackSecret != "" {
		req.Header.Set(SignatureHeader, "sha256="+sign(action.CallbackSecret, body))
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return nil
}

// sign returns the lowercase hex HMAC-SHA256 digest of body keyed by secret.
// Exported as Verify on the receive side via package-level Verify().
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether the supplied signature header value matches the
// HMAC-SHA256 of body keyed by secret. Header values are expected in the
// `sha256=<hex>` form to match the producer.
func Verify(secret string, body []byte, header string) bool {
	const prefix = "sha256="
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	want, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}
