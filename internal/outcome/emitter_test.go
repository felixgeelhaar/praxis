package outcome_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/outcome"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func newLogger() *bolt.Logger {
	return bolt.New(bolt.NewJSONHandler(io.Discard))
}

func TestEmit_EnqueuesEnvelope(t *testing.T) {
	repos := memory.New()
	em := outcome.New(newLogger(), repos.Outbox, outcome.Config{URL: "http://example"})
	if err := em.Emit(context.Background(), domain.MnemosEvent{ActionID: "a1"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	batch, _ := repos.Outbox.NextBatch(context.Background(), 10, time.Now().Add(time.Hour))
	if len(batch) != 1 {
		t.Fatalf("outbox len=%d want 1", len(batch))
	}
	if batch[0].ActionID != "a1" {
		t.Errorf("ActionID=%s want a1", batch[0].ActionID)
	}
}

func TestDrain_DeliversToMnemos(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		var ev domain.MnemosEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("decode: %v", err)
		}
		if ev.ActionID != "a1" {
			t.Errorf("ActionID=%s want a1", ev.ActionID)
		}
		if r.Header.Get("Authorization") != "Bearer tk" {
			t.Errorf("missing bearer token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repos := memory.New()
	em := outcome.New(newLogger(), repos.Outbox, outcome.Config{
		URL: srv.URL, Token: "tk", PollInterval: 10 * time.Millisecond,
	})
	if err := em.Emit(context.Background(), domain.MnemosEvent{ActionID: "a1", Type: "praxis.action_completed"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := em.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("Mnemos called %d times, want 1", atomic.LoadInt32(&called))
	}
	delivered, failures := em.Stats()
	if delivered != 1 || failures != 0 {
		t.Errorf("delivered=%d failures=%d want 1/0", delivered, failures)
	}
	// Outbox should now mark delivered.
	batch, _ := repos.Outbox.NextBatch(context.Background(), 10, time.Now().Add(time.Hour))
	if len(batch) != 0 {
		t.Errorf("outbox not drained: %v", batch)
	}
}

func TestDrain_RetriesOn5xx_ThenFailsFastOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	repos := memory.New()
	em := outcome.New(newLogger(), repos.Outbox, outcome.Config{
		URL: srv.URL, MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond,
	})
	if err := em.Emit(context.Background(), domain.MnemosEvent{ActionID: "a"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := em.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("4xx should fail fast: calls=%d want 1", atomic.LoadInt32(&calls))
	}
	_, failures := em.Stats()
	if failures == 0 {
		t.Errorf("expected failure recorded")
	}
}

func TestDrain_RetriesOn503(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repos := memory.New()
	em := outcome.New(newLogger(), repos.Outbox, outcome.Config{
		URL: srv.URL, MaxAttempts: 5, InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond,
	})
	if err := em.Emit(context.Background(), domain.MnemosEvent{ActionID: "a"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := em.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if atomic.LoadInt32(&calls) < 3 {
		t.Errorf("expected ≥3 calls (retries), got %d", atomic.LoadInt32(&calls))
	}
	delivered, _ := em.Stats()
	if delivered != 1 {
		t.Errorf("delivered=%d want 1", delivered)
	}
}

func TestEmit_NoURL_StaysInOutbox(t *testing.T) {
	repos := memory.New()
	em := outcome.New(newLogger(), repos.Outbox, outcome.Config{URL: ""})
	if err := em.Emit(context.Background(), domain.MnemosEvent{ActionID: "a"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Drain returns no error but does not deliver.
	_ = em.Drain(context.Background())
	batch, _ := repos.Outbox.NextBatch(context.Background(), 10, time.Now().Add(time.Hour))
	if len(batch) != 1 {
		t.Errorf("expected 1 stranded envelope, got %d", len(batch))
	}
}
