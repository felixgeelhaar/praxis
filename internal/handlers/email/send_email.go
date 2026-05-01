// Package email implements the send_email capability via SMTP.
//
// Idempotency is enforced at the destination via a deterministic Message-ID
// derived from the action's idempotency key. Resending the same action
// produces the same Message-ID, which RFC-compliant MTAs deduplicate.
package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// Config carries SMTP credentials.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Sender is the narrow SMTP interface this handler depends on.
type Sender func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// SendEmail handles the send_email capability.
type SendEmail struct {
	cfg    Config
	send   Sender
	now    func() time.Time
	domain string
}

// New constructs an SMTP send_email handler. With cfg.Host empty the
// handler runs in degraded mode (Execute returns a simulated success).
func New(cfg Config) *SendEmail {
	d := "praxis.local"
	if at := strings.Index(cfg.From, "@"); at != -1 {
		d = cfg.From[at+1:]
	}
	return &SendEmail{cfg: cfg, send: smtp.SendMail, now: time.Now, domain: d}
}

// NewWithSender is the test seam.
func NewWithSender(cfg Config, send Sender) *SendEmail {
	h := New(cfg)
	h.send = send
	return h
}

// Name returns the capability name.
func (h *SendEmail) Name() string { return "send_email" }

// Capability returns the descriptor used by the registry.
func (h *SendEmail) Capability() domain.Capability {
	return domain.Capability{
		Name:        "send_email",
		Description: "Send an email via SMTP with deterministic Message-ID",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"to", "subject", "body"},
			"properties": map[string]any{
				"to":      map[string]any{"type": "string"},
				"subject": map[string]any{"type": "string"},
				"body":    map[string]any{"type": "string"},
				"idempotency_key": map[string]any{"type": "string",
					"description": "Used to derive a stable Message-ID; defaults to action ID."},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []string{"ok", "message_id", "to"},
		},
		Permissions: []string{"email:send"},
		Simulatable: true,
		Idempotent:  true,
	}
}

// Execute sends the email.
func (h *SendEmail) Execute(_ context.Context, payload map[string]any) (map[string]any, error) {
	to, subject, body, idem, err := requireFields(payload)
	if err != nil {
		return nil, err
	}
	from := h.cfg.From
	if from == "" {
		from = h.cfg.Username
	}
	msgID := h.deterministicMessageID(idem, from, to, subject)

	if h.cfg.Host == "" {
		return map[string]any{
			"ok":         true,
			"simulated":  true,
			"message_id": msgID,
			"to":         to,
			"subject":    subject,
		}, nil
	}

	addr := net.JoinHostPort(h.cfg.Host, h.cfg.Port)
	auth := smtp.PlainAuth("", h.cfg.Username, h.cfg.Password, h.cfg.Host)
	body = buildEmail(msgID, from, to, subject, body, h.now())

	if err := h.send(addr, auth, from, []string{to}, []byte(body)); err != nil {
		return nil, fmt.Errorf("smtp send: %w", err)
	}

	return map[string]any{
		"ok":         true,
		"message_id": msgID,
		"to":         to,
		"subject":    subject,
	}, nil
}

// Simulate returns a faithful preview without contacting an MTA.
func (h *SendEmail) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	to, subject, body, idem, err := requireFields(payload)
	if err != nil {
		return nil, err
	}
	from := h.cfg.From
	if from == "" {
		from = h.cfg.Username
	}
	msgID := h.deterministicMessageID(idem, from, to, subject)
	return map[string]any{
		"simulated":  true,
		"message_id": msgID,
		"to":         to,
		"subject":    subject,
		"body":       body,
	}, nil
}

func (h *SendEmail) deterministicMessageID(idem, from, to, subject string) string {
	seed := strings.Join([]string{idem, from, to, subject}, "|")
	sum := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(sum[:16]), h.domain)
}

func buildEmail(messageID, from, to, subject, body string, t time.Time) string {
	return fmt.Sprintf(
		"Message-ID: %s\r\nFrom: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		messageID, from, to, subject, t.Format(time.RFC1123Z), body,
	)
}

func requireFields(payload map[string]any) (to, subject, body, idem string, err error) {
	to, _ = payload["to"].(string)
	subject, _ = payload["subject"].(string)
	body, _ = payload["body"].(string)
	idem, _ = payload["idempotency_key"].(string)
	if to == "" {
		return "", "", "", "", errors.New("to is required")
	}
	if subject == "" {
		return "", "", "", "", errors.New("subject is required")
	}
	if body == "" {
		return "", "", "", "", errors.New("body is required")
	}
	return to, subject, body, idem, nil
}

var _ capability.Handler = (*SendEmail)(nil)
