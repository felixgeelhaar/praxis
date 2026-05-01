package slack_test

import (
	"context"
	"errors"
	"testing"

	slackgo "github.com/slack-go/slack"

	pslack "github.com/felixgeelhaar/praxis/internal/handlers/slack"
)

type fakeClient struct {
	postedChannel string
	options       []slackgo.MsgOption
	channelOut    string
	tsOut         string
	err           error
	calls         int
}

func (f *fakeClient) PostMessageContext(_ context.Context, channel string, options ...slackgo.MsgOption) (string, string, error) {
	f.calls++
	f.postedChannel = channel
	f.options = options
	return f.channelOut, f.tsOut, f.err
}

func TestExecute_Success(t *testing.T) {
	c := &fakeClient{channelOut: "C123", tsOut: "1234.567"}
	h := pslack.NewWithClient(c)
	out, err := h.Execute(context.Background(), map[string]any{
		"channel": "#general",
		"text":    "hi",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["ts"] != "1234.567" {
		t.Errorf("ts=%v want 1234.567", out["ts"])
	}
	if c.calls != 1 {
		t.Errorf("calls=%d want 1", c.calls)
	}
	if c.postedChannel != "#general" {
		t.Errorf("channel=%q want #general", c.postedChannel)
	}
}

func TestExecute_VendorError(t *testing.T) {
	h := pslack.NewWithClient(&fakeClient{err: errors.New("503 service unavailable")})
	_, err := h.Execute(context.Background(), map[string]any{
		"channel": "#x", "text": "hi",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecute_MissingFields(t *testing.T) {
	h := pslack.NewWithClient(&fakeClient{})
	_, err := h.Execute(context.Background(), map[string]any{"channel": "#x"})
	if err == nil {
		t.Fatal("expected text required error")
	}
}

func TestSimulate(t *testing.T) {
	h := pslack.NewWithClient(&fakeClient{})
	out, err := h.Simulate(context.Background(), map[string]any{
		"channel": "#x", "text": "hi",
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v want true", out["simulated"])
	}
}

func TestExecute_DegradedNoToken(t *testing.T) {
	h := pslack.New("") // no token, no client
	out, err := h.Execute(context.Background(), map[string]any{"channel": "#x", "text": "hi"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("expected simulated=true in degraded mode")
	}
}
