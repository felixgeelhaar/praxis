// Package config holds environment-driven configuration for the Praxis
// runtime. All settings use the PRAXIS_<AREA>_<KEY> naming convention.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config aggregates all settings consumed by cmd/praxis.
type Config struct {
	HTTPHost        string
	HTTPPort        int
	APIToken        string
	DBType          string
	DBConn          string
	MnemosURL       string
	MnemosToken     string
	HandlerTimeout  time.Duration
	IdempotencyTTL  time.Duration
	PolicyMode      string // allow | deny | rules
	OutboxBatchSize int
	OutboxPollEvery time.Duration
	PluginDir       string // PRAXIS_PLUGIN_DIR; empty disables plugin discovery
}

// Load reads configuration from the process environment, applying defaults
// when a variable is unset. It validates a small number of invariants and
// returns an error for malformed values.
func Load() (Config, error) {
	c := Config{
		HTTPHost:        getEnv("PRAXIS_HTTP_HOST", "0.0.0.0"),
		HTTPPort:        getInt("PRAXIS_HTTP_PORT", 8080),
		APIToken:        os.Getenv("PRAXIS_API_TOKEN"),
		DBType:          strings.ToLower(getEnv("PRAXIS_DB_TYPE", "memory")),
		DBConn:          os.Getenv("PRAXIS_DB_CONN"),
		MnemosURL:       os.Getenv("PRAXIS_MNEMOS_URL"),
		MnemosToken:     os.Getenv("PRAXIS_MNEMOS_TOKEN"),
		HandlerTimeout:  getDur("PRAXIS_HANDLER_TIMEOUT", 30*time.Second),
		IdempotencyTTL:  getDur("PRAXIS_IDEMPOTENCY_TTL", 24*time.Hour),
		PolicyMode:      strings.ToLower(getEnv("PRAXIS_POLICY_MODE", "allow")),
		OutboxBatchSize: getInt("PRAXIS_OUTBOX_BATCH_SIZE", 32),
		OutboxPollEvery: getDur("PRAXIS_OUTBOX_POLL_EVERY", 2*time.Second),
		PluginDir:       os.Getenv("PRAXIS_PLUGIN_DIR"),
	}

	switch c.DBType {
	case "memory", "sqlite", "postgres", "postgresql":
	default:
		return c, fmt.Errorf("PRAXIS_DB_TYPE: unknown backend %q (memory|sqlite|postgres)", c.DBType)
	}
	if c.DBType == "postgres" || c.DBType == "postgresql" {
		if c.DBConn == "" {
			return c, fmt.Errorf("PRAXIS_DB_CONN: required when PRAXIS_DB_TYPE=postgres")
		}
	}
	switch c.PolicyMode {
	case "allow", "deny", "rules":
	default:
		return c, fmt.Errorf("PRAXIS_POLICY_MODE: unknown mode %q (allow|deny|rules)", c.PolicyMode)
	}
	return c, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getDur(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
