package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/store"
)

func TestOpen_Memory(t *testing.T) {
	repos, err := store.Open(context.Background(), nil, store.Config{Type: "memory"})
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	if repos == nil || repos.Action == nil {
		t.Errorf("nil repos")
	}
	if repos.Close != nil {
		_ = repos.Close()
	}
}

func TestOpen_Sqlite(t *testing.T) {
	dir := t.TempDir()
	conn := "file:" + filepath.Join(dir, "p.db")
	repos, err := store.Open(context.Background(), nil, store.Config{Type: "sqlite", Conn: conn})
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	if repos.Action == nil {
		t.Errorf("nil Action repo")
	}
	_ = repos.Close()
}

func TestOpen_Default_IsMemory(t *testing.T) {
	repos, err := store.Open(context.Background(), nil, store.Config{})
	if err != nil {
		t.Fatalf("Open empty: %v", err)
	}
	if repos.Action == nil {
		t.Errorf("nil Action repo")
	}
	_ = repos.Close()
}

func TestOpen_UnknownBackend(t *testing.T) {
	_, err := store.Open(context.Background(), nil, store.Config{Type: "mongo"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "sqlite")
	t.Setenv("PRAXIS_DB_CONN", "praxis.db")
	cfg := store.FromEnv()
	if cfg.Type != "sqlite" {
		t.Errorf("Type=%s want sqlite", cfg.Type)
	}
	if cfg.Conn != "praxis.db" {
		t.Errorf("Conn=%s want praxis.db", cfg.Conn)
	}
}
