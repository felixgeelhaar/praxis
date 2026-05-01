package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/sqlite"
)

func TestSQLiteBackend(t *testing.T) {
	factory := func(t *testing.T) *ports.Repos {
		t.Helper()
		dir := t.TempDir()
		conn := "file:" + filepath.Join(dir, "praxis.db") + "?_pragma=foreign_keys(1)"
		repos, err := sqlite.Open(context.Background(), nil, conn)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = repos.Close() })
		return repos
	}
	// Inlined RunSuite — keep import surface minimal.
	runShared(t, factory)
}
