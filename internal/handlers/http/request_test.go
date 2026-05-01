package http_test

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	phttp "github.com/felixgeelhaar/praxis/internal/handlers/http"
)

func TestExecute_GET_HappyPath(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != "GET" {
			t.Errorf("method=%s", r.Method)
		}
		w.Header().Set("X-Trace", "abc")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	h := phttp.New(phttp.Config{})
	out, err := h.Execute(context.Background(), map[string]any{
		"method": "GET", "url": srv.URL,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["status_code"] != 200 {
		t.Errorf("status_code=%v want 200", out["status_code"])
	}
	if out["body"] != `{"ok":true}` {
		t.Errorf("body=%v", out["body"])
	}
	if h, ok := out["headers"].(map[string]any); !ok || h["X-Trace"] != "abc" {
		t.Errorf("X-Trace header missing: %v", out["headers"])
	}
}

func TestExecute_POST_JSON_SetsContentType(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Method != "POST" {
			t.Errorf("method=%s want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type=%q want application/json", ct)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["x"] != float64(1) {
			t.Errorf("body.x=%v want 1", body["x"])
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()

	h := phttp.New(phttp.Config{})
	out, err := h.Execute(context.Background(), map[string]any{
		"method": "POST", "url": srv.URL,
		"json": map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["status_code"] != 201 {
		t.Errorf("status_code=%v want 201", out["status_code"])
	}
}

func TestExecute_ForwardsIdempotencyKey(t *testing.T) {
	var seen int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.Header.Get("Idempotency-Key") == "abc-123" {
			atomic.StoreInt32(&seen, 1)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	h := phttp.New(phttp.Config{})
	if _, err := h.Execute(context.Background(), map[string]any{
		"method": "POST", "url": srv.URL, "idempotency_key": "abc-123",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if atomic.LoadInt32(&seen) != 1 {
		t.Errorf("Idempotency-Key not forwarded")
	}
}

func TestExecute_TruncatesLargeBody(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 4096))
	}))
	defer srv.Close()

	h := phttp.New(phttp.Config{MaxResponseBody: 100})
	out, err := h.Execute(context.Background(), map[string]any{"method": "GET", "url": srv.URL})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["truncated"] != true {
		t.Errorf("truncated=%v want true", out["truncated"])
	}
	if got := out["body"].(string); len(got) != 100 {
		t.Errorf("body len=%d want 100", len(got))
	}
}

func TestExecute_RejectsBadURL(t *testing.T) {
	h := phttp.New(phttp.Config{})
	_, err := h.Execute(context.Background(), map[string]any{
		"method": "GET", "url": "ftp://example.com/x",
	})
	if err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

func TestExecute_RejectsBadMethod(t *testing.T) {
	h := phttp.New(phttp.Config{})
	_, err := h.Execute(context.Background(), map[string]any{
		"method": "TRACE", "url": "https://example.com",
	})
	if err == nil {
		t.Fatal("expected error for unsupported method")
	}
}

func TestExecute_HonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	h := phttp.New(phttp.Config{Timeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := h.Execute(ctx, map[string]any{"method": "GET", "url": srv.URL})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSimulate(t *testing.T) {
	h := phttp.New(phttp.Config{})
	out, err := h.Simulate(context.Background(), map[string]any{
		"method": "POST", "url": "https://example.com/api",
	})
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v want true", out["simulated"])
	}
	if out["url"] != "https://example.com/api" {
		t.Errorf("url=%v", out["url"])
	}
}
