package config_test

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "")
	t.Setenv("PRAXIS_DB_CONN", "")
	t.Setenv("PRAXIS_HTTP_PORT", "")
	t.Setenv("PRAXIS_POLICY_MODE", "")

	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBType != "memory" {
		t.Errorf("DBType=%s want memory", c.DBType)
	}
	if c.HTTPPort != 8080 {
		t.Errorf("HTTPPort=%d want 8080", c.HTTPPort)
	}
	if c.PolicyMode != "allow" {
		t.Errorf("PolicyMode=%s want allow", c.PolicyMode)
	}
	if c.HandlerTimeout != 30*time.Second {
		t.Errorf("HandlerTimeout=%s want 30s", c.HandlerTimeout)
	}
}

func TestLoad_PostgresRequiresConn(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "postgres")
	t.Setenv("PRAXIS_DB_CONN", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for postgres without conn")
	}
}

func TestLoad_UnknownBackend(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "mongodb")
	t.Setenv("PRAXIS_DB_CONN", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestLoad_OverridesViaEnv(t *testing.T) {
	t.Setenv("PRAXIS_DB_TYPE", "sqlite")
	t.Setenv("PRAXIS_DB_CONN", "praxis.db")
	t.Setenv("PRAXIS_HTTP_PORT", "9999")
	t.Setenv("PRAXIS_POLICY_MODE", "deny")
	t.Setenv("PRAXIS_HANDLER_TIMEOUT", "5s")

	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBType != "sqlite" {
		t.Errorf("DBType=%s want sqlite", c.DBType)
	}
	if c.DBConn != "praxis.db" {
		t.Errorf("DBConn=%s want praxis.db", c.DBConn)
	}
	if c.HTTPPort != 9999 {
		t.Errorf("HTTPPort=%d want 9999", c.HTTPPort)
	}
	if c.PolicyMode != "deny" {
		t.Errorf("PolicyMode=%s want deny", c.PolicyMode)
	}
	if c.HandlerTimeout != 5*time.Second {
		t.Errorf("HandlerTimeout=%s want 5s", c.HandlerTimeout)
	}
}
