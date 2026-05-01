package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/felixgeelhaar/bolt"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/webhook"
)

func TestNotify_NoCallbackURL_NoOp(t *testing.T) {
	n := webhook.New(bolt.New(bolt.NewJSONHandler(io.Discard)), nil)
	err := n.Notify(context.Background(), domain.Action{ID: "a"}, domain.Result{})
	if err != nil {
		t.Errorf("expected no-op nil, got %v", err)
	}
}

func TestNotify_PostsAndSigns(t *testing.T) {
	var captured struct {
		body      []byte
		signature string
		event     string
		method    string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured.body = b
		captured.signature = r.Header.Get(webhook.SignatureHeader)
		captured.event = r.Header.Get(webhook.EventHeader)
		captured.method = r.Method
		w.WriteHeader(204)
	}))
	defer srv.Close()

	n := webhook.New(bolt.New(bolt.NewJSONHandler(io.Discard)), nil)
	err := n.Notify(context.Background(),
		domain.Action{
			ID: "a-1", Capability: "send_email",
			CallbackURL: srv.URL, CallbackSecret: "shh",
		},
		domain.Result{ActionID: "a-1", Status: domain.StatusSucceeded, ExternalID: "ext-1"},
	)
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if captured.method != "POST" {
		t.Errorf("method=%s", captured.method)
	}
	if captured.event != "praxis.action_completed" {
		t.Errorf("event=%s", captured.event)
	}
	if !webhook.Verify("shh", captured.body, captured.signature) {
		t.Errorf("HMAC verify failed: sig=%q body=%q", captured.signature, captured.body)
	}
	var pl webhook.Payload
	if err := json.Unmarshal(captured.body, &pl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pl.ActionID != "a-1" || pl.ExternalID != "ext-1" {
		t.Errorf("payload mismatch: %+v", pl)
	}
}

func TestNotify_Non2xxFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	n := webhook.New(bolt.New(bolt.NewJSONHandler(io.Discard)), nil)
	err := n.Notify(context.Background(),
		domain.Action{ID: "a", CallbackURL: srv.URL},
		domain.Result{Status: domain.StatusFailed},
	)
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
}

func TestVerify_RejectsBadSignature(t *testing.T) {
	body := []byte(`{"x":1}`)
	if webhook.Verify("shh", body, "sha256=deadbeef") {
		t.Errorf("expected bad signature to fail")
	}
	if webhook.Verify("shh", body, "wrong-prefix") {
		t.Errorf("expected wrong-prefix to fail")
	}
}
