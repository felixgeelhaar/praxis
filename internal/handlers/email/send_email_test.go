package email_test

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/handlers/email"
)

func TestExecute_Success_DeterministicMessageID(t *testing.T) {
	var captured []byte
	send := func(_ string, _ smtp.Auth, _ string, _ []string, msg []byte) error {
		captured = msg
		return nil
	}
	h := email.NewWithSender(email.Config{
		Host: "smtp.example.com", Port: "587", From: "ops@example.com",
	}, send)

	out1, err := h.Execute(context.Background(), map[string]any{
		"to": "u@x.com", "subject": "hi", "body": "hello",
		"idempotency_key": "key-1",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out2, err := h.Execute(context.Background(), map[string]any{
		"to": "u@x.com", "subject": "hi", "body": "hello",
		"idempotency_key": "key-1",
	})
	if err != nil {
		t.Fatalf("Execute2: %v", err)
	}
	if out1["message_id"] != out2["message_id"] {
		t.Errorf("Message-ID not deterministic: %v vs %v", out1["message_id"], out2["message_id"])
	}
	id, _ := out1["message_id"].(string)
	if !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID domain wrong: %s", id)
	}
	if !strings.Contains(string(captured), "Message-ID: "+id) {
		t.Errorf("captured payload missing Message-ID: %s", string(captured))
	}
}

func TestExecute_DiffKeysProduceDiffMessageID(t *testing.T) {
	send := func(string, smtp.Auth, string, []string, []byte) error { return nil }
	h := email.NewWithSender(email.Config{Host: "x", Port: "25", From: "a@b.c"}, send)
	a, _ := h.Execute(context.Background(), map[string]any{"to": "u@x.com", "subject": "s", "body": "b", "idempotency_key": "k1"})
	b, _ := h.Execute(context.Background(), map[string]any{"to": "u@x.com", "subject": "s", "body": "b", "idempotency_key": "k2"})
	if a["message_id"] == b["message_id"] {
		t.Errorf("different keys produced same Message-ID: %v", a["message_id"])
	}
}

func TestExecute_VendorError(t *testing.T) {
	send := func(string, smtp.Auth, string, []string, []byte) error { return errors.New("550 mailbox unavailable") }
	h := email.NewWithSender(email.Config{Host: "x", Port: "25", From: "a@b.c"}, send)
	_, err := h.Execute(context.Background(), map[string]any{"to": "u@x.com", "subject": "s", "body": "b"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecute_MissingFields(t *testing.T) {
	h := email.NewWithSender(email.Config{Host: "x", Port: "25", From: "a@b.c"}, nil)
	_, err := h.Execute(context.Background(), map[string]any{"to": "u@x.com"})
	if err == nil {
		t.Fatal("expected required-field error")
	}
}

func TestSimulate(t *testing.T) {
	h := email.New(email.Config{Host: "x", Port: "25", From: "a@b.c"})
	out, err := h.Simulate(context.Background(), map[string]any{
		"to": "u@x.com", "subject": "s", "body": "b",
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v want true", out["simulated"])
	}
}

func TestExecute_DegradedNoHost(t *testing.T) {
	h := email.New(email.Config{From: "a@b.c"})
	out, err := h.Execute(context.Background(), map[string]any{
		"to": "u@x.com", "subject": "s", "body": "b",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("expected simulated=true with no host")
	}
}
