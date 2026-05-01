// Package store is the storage facade. It selects a backend driver from
// configuration and returns a fully-wired *ports.Repos.
package store

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
	"github.com/felixgeelhaar/praxis/internal/store/postgres"
	"github.com/felixgeelhaar/praxis/internal/store/sqlite"
)

// Config selects the backend and provides connection information.
type Config struct {
	Type string // memory | sqlite | postgres
	Conn string // backend-specific connection string
}

// FromEnv reads PRAXIS_DB_TYPE / PRAXIS_DB_CONN to build a Config. Falls back
// to the in-memory backend when nothing is set.
func FromEnv() Config {
	t := strings.ToLower(strings.TrimSpace(os.Getenv("PRAXIS_DB_TYPE")))
	if t == "" {
		t = "memory"
	}
	return Config{
		Type: t,
		Conn: os.Getenv("PRAXIS_DB_CONN"),
	}
}

// Open returns a fully-wired *ports.Repos for the requested backend. The
// caller is responsible for invoking repos.Close on shutdown.
func Open(ctx context.Context, logger *bolt.Logger, cfg Config) (*ports.Repos, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "memory":
		return memory.New(), nil
	case "sqlite":
		return sqlite.Open(ctx, logger, cfg.Conn)
	case "postgres", "postgresql":
		return postgres.Open(ctx, logger, cfg.Conn)
	default:
		return nil, fmt.Errorf("unknown PRAXIS_DB_TYPE: %s", cfg.Type)
	}
}
