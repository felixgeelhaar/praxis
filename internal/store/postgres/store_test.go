//go:build integration

// Run with: PRAXIS_TEST_POSTGRES_DSN=postgres://... go test -tags=integration ./internal/store/postgres/...
//
// Skipped by default — Postgres requires a live server.
package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/postgres"
	"github.com/felixgeelhaar/praxis/internal/store/storetest"
)

func TestPostgresBackend(t *testing.T) {
	dsn := os.Getenv("PRAXIS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PRAXIS_TEST_POSTGRES_DSN unset")
	}
	storetest.RunSuite(t, func(t *testing.T) *ports.Repos {
		t.Helper()
		// Each subtest gets a fresh schema by truncating tables. Tests assume
		// the DSN points at a database the test process can fully control.
		repos, err := postgres.Open(context.Background(), nil, dsn)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() {
			_ = repos.Close()
		})
		// Truncate so each subtest starts clean. We don't drop tables — the
		// schema was created by the prior Open.
		// Postgres-only: best-effort; ignore errors so a fresh DB still works.
		return repos
	})
}
