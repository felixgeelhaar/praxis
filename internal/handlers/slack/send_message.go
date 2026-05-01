// Package slack implements the send_message capability against the Slack
// Web API.
//
// Slack's chat.postMessage does not surface a server-side idempotency
// mechanism for outbound messages. The Praxis-layer IdempotencyKeeper
// short-circuits same-Action.ID re-executions before this handler runs;
// callers should also pass a stable IdempotencyKey on Action.
package slack

import (
	"context"
	"errors"
	"fmt"

	slackgo "github.com/slack-go/slack"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// PostMessageClient is the narrow Slack interface this handler depends on.
// Tests inject a fake; production wires *slackgo.Client.
type PostMessageClient interface {
	PostMessageContext(ctx context.Context, channel string, options ...slackgo.MsgOption) (string, string, error)
}

// SendMessage handles the send_message capability.
type SendMessage struct {
	client PostMessageClient
}

// New constructs a Slack send_message handler. When token is empty the
// handler runs in degraded mode: Execute returns a simulated success so
// developers can run end-to-end without credentials.
func New(token string) *SendMessage {
	if token == "" {
		return &SendMessage{}
	}
	return &SendMessage{client: slackgo.New(token)}
}

// NewWithClient is the test seam.
func NewWithClient(c PostMessageClient) *SendMessage {
	return &SendMessage{client: c}
}

// Name returns the capability name.
func (h *SendMessage) Name() string { return "send_message" }

// Capability returns the descriptor used by the registry and ListCapabilities.
func (h *SendMessage) Capability() domain.Capability {
	return domain.Capability{
		Name:        "send_message",
		Description: "Post a message to a Slack channel via the Web API",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"channel", "text"},
			"properties": map[string]any{
				"channel":    map[string]any{"type": "string"},
				"text":       map[string]any{"type": "string"},
				"thread_ts":  map[string]any{"type": "string"},
				"username":   map[string]any{"type": "string"},
				"icon_emoji": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []string{"ok", "ts", "channel"},
		},
		Permissions: []string{"slack:write"},
		Simulatable: true,
		Idempotent:  false,
	}
}

// Execute posts the message.
func (h *SendMessage) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	channel, text, err := requireFields(payload)
	if err != nil {
		return nil, err
	}
	if h.client == nil {
		// degraded mode: behave like Simulate
		return h.simulatedResponse(payload), nil
	}

	opts := []slackgo.MsgOption{slackgo.MsgOptionText(text, false)}
	if ts, ok := payload["thread_ts"].(string); ok && ts != "" {
		opts = append(opts, slackgo.MsgOptionTS(ts))
	}
	if user, ok := payload["username"].(string); ok && user != "" {
		opts = append(opts, slackgo.MsgOptionAsUser(false), slackgo.MsgOptionUsername(user))
	}
	if icon, ok := payload["icon_emoji"].(string); ok && icon != "" {
		opts = append(opts, slackgo.MsgOptionIconEmoji(icon))
	}

	channelID, ts, err := h.client.PostMessageContext(ctx, channel, opts...)
	if err != nil {
		return nil, fmt.Errorf("slack postMessage: %w", err)
	}
	return map[string]any{
		"ok":      true,
		"ts":      ts,
		"channel": channelID,
	}, nil
}

// Simulate returns a faithful preview without contacting Slack.
func (h *SendMessage) Simulate(_ context.Context, payload map[string]any) (map[string]any, error) {
	if _, _, err := requireFields(payload); err != nil {
		return nil, err
	}
	return h.simulatedResponse(payload), nil
}

func (h *SendMessage) simulatedResponse(payload map[string]any) map[string]any {
	channel, _ := payload["channel"].(string)
	text, _ := payload["text"].(string)
	return map[string]any{
		"ok":         true,
		"simulated":  true,
		"channel":    channel,
		"text":       text,
		"would_send": "dry-run preview",
	}
}

func requireFields(payload map[string]any) (channel, text string, err error) {
	channel, _ = payload["channel"].(string)
	text, _ = payload["text"].(string)
	if channel == "" {
		return "", "", errors.New("channel is required")
	}
	if text == "" {
		return "", "", errors.New("text is required")
	}
	return channel, text, nil
}

var _ capability.Handler = (*SendMessage)(nil)
