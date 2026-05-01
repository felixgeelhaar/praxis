package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func TestKeeper_RememberLookup_RoundTrip(t *testing.T) {
	repos := memory.New()
	k := idempotency.New(repos.Idempotency)
	res := domain.Result{ActionID: "a-1", Status: domain.StatusSucceeded, Output: map[string]any{"ok": true}}
	if err := k.Remember(context.Background(), "key-1", res); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	got, err := k.Check(context.Background(), "key-1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.ActionID != "a-1" {
		t.Errorf("ActionID=%s want a-1", got.ActionID)
	}
}

func TestKeeper_Lookup_MissingReturnsErrNotFound(t *testing.T) {
	k := idempotency.New(memory.New().Idempotency)
	_, err := k.Check(context.Background(), "missing")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}
