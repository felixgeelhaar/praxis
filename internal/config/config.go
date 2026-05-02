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
	HTTPHost                   string
	HTTPPort                   int
	APIToken                   string
	APITokenFile               string // PRAXIS_API_TOKEN_FILE; SIGHUP-rotatable file containing the bearer token. Wins over APIToken.
	DBType                     string
	DBConn                     string
	MnemosURL                  string
	MnemosToken                string
	HandlerTimeout             time.Duration
	IdempotencyTTL             time.Duration
	PolicyMode                 string // allow | deny | rules
	OutboxBatchSize            int
	OutboxPollEvery            time.Duration
	PluginDir                  string        // PRAXIS_PLUGIN_DIR; empty disables plugin discovery
	PluginTrustedKeys          []string      // PRAXIS_PLUGIN_TRUSTED_KEYS; PEM ECDSA public keys for cosign-blob verification
	PluginFulcioRoots          []string      // PRAXIS_PLUGIN_FULCIO_ROOTS; PEM bundles trusted as Fulcio roots
	PluginFulcioSubjects       []string      // PRAXIS_PLUGIN_FULCIO_SUBJECTS; SAN globs allowed by trust policy
	PluginFulcioIssuer         string        // PRAXIS_PLUGIN_FULCIO_ISSUER; OIDC issuer required on the cert
	PluginStrict               bool          // PRAXIS_PLUGIN_STRICT=1; any plugin load error aborts startup
	PluginAutoreload           bool          // PRAXIS_PLUGIN_AUTORELOAD; default true. fsnotify-driven hot reload.
	OTLPEndpoint               string        // PRAXIS_OTLP_ENDPOINT; empty disables tracing
	OTLPProtocol               string        // PRAXIS_OTLP_PROTOCOL; grpc (default) or http
	OTLPInsecure               bool          // PRAXIS_OTLP_INSECURE; default false (TLS)
	TraceSample                float64       // PRAXIS_TRACE_SAMPLE; 0..1 sampling probability, default 1.0
	SchemaCompat               string        // PRAXIS_SCHEMA_COMPAT; off (default) | warn | strict
	MCPFederationConfigPath    string        // PRAXIS_MCP_FEDERATION_CONFIG; empty disables federation
	TLSCertFile                string        // PRAXIS_TLS_CERT_FILE; PEM-encoded cert chain. Empty disables TLS.
	TLSKeyFile                 string        // PRAXIS_TLS_KEY_FILE; PEM-encoded private key. Required when TLSCertFile set.
	MTLSClientCAFile           string        // PRAXIS_MTLS_CLIENT_CA_FILE; PEM CA bundle. When set, requires + verifies client certs.
	PluginOutOfProcess         bool          // PRAXIS_PLUGIN_OUT_OF_PROCESS=1; loads plugins via ProcessOpener (real isolation, kernel-enforced limits).
	PluginHostBinary           string        // PRAXIS_PLUGINHOST_BINARY; path to praxis-pluginhost. Required when PluginOutOfProcess is true.
	AuditRetentionInterval     time.Duration // PRAXIS_AUDIT_RETENTION_INTERVAL; cadence between sweeps
	AuditRetentionInitialDelay time.Duration // PRAXIS_AUDIT_RETENTION_INITIAL_DELAY; defer first sweep
	// AuditRetention maps OrgID to retention window. The empty key is the
	// default applied to events whose OrgID is unset. Configured via
	// PRAXIS_AUDIT_RETENTION as a comma-separated list of "orgID=duration"
	// pairs (use "*=duration" for the default). Phase 3 M3.3.
	AuditRetention map[string]time.Duration
}

// Load reads configuration from the process environment, applying defaults
// when a variable is unset. It validates a small number of invariants and
// returns an error for malformed values.
func Load() (Config, error) {
	c := Config{
		HTTPHost:                   getEnv("PRAXIS_HTTP_HOST", "0.0.0.0"),
		HTTPPort:                   getInt("PRAXIS_HTTP_PORT", 8080),
		APIToken:                   os.Getenv("PRAXIS_API_TOKEN"),
		APITokenFile:               os.Getenv("PRAXIS_API_TOKEN_FILE"),
		DBType:                     strings.ToLower(getEnv("PRAXIS_DB_TYPE", "memory")),
		DBConn:                     os.Getenv("PRAXIS_DB_CONN"),
		MnemosURL:                  os.Getenv("PRAXIS_MNEMOS_URL"),
		MnemosToken:                os.Getenv("PRAXIS_MNEMOS_TOKEN"),
		HandlerTimeout:             getDur("PRAXIS_HANDLER_TIMEOUT", 30*time.Second),
		IdempotencyTTL:             getDur("PRAXIS_IDEMPOTENCY_TTL", 24*time.Hour),
		PolicyMode:                 strings.ToLower(getEnv("PRAXIS_POLICY_MODE", "allow")),
		OutboxBatchSize:            getInt("PRAXIS_OUTBOX_BATCH_SIZE", 32),
		OutboxPollEvery:            getDur("PRAXIS_OUTBOX_POLL_EVERY", 2*time.Second),
		PluginDir:                  os.Getenv("PRAXIS_PLUGIN_DIR"),
		PluginTrustedKeys:          parseList(os.Getenv("PRAXIS_PLUGIN_TRUSTED_KEYS")),
		PluginFulcioRoots:          parseList(os.Getenv("PRAXIS_PLUGIN_FULCIO_ROOTS")),
		PluginFulcioSubjects:       parseList(os.Getenv("PRAXIS_PLUGIN_FULCIO_SUBJECTS")),
		PluginFulcioIssuer:         os.Getenv("PRAXIS_PLUGIN_FULCIO_ISSUER"),
		PluginStrict:               parseBool(os.Getenv("PRAXIS_PLUGIN_STRICT")),
		PluginAutoreload:           parseBoolDefault(os.Getenv("PRAXIS_PLUGIN_AUTORELOAD"), true),
		AuditRetention:             parseRetention(os.Getenv("PRAXIS_AUDIT_RETENTION")),
		AuditRetentionInterval:     getDur("PRAXIS_AUDIT_RETENTION_INTERVAL", time.Hour),
		AuditRetentionInitialDelay: getDur("PRAXIS_AUDIT_RETENTION_INITIAL_DELAY", 5*time.Minute),
		OTLPEndpoint:               os.Getenv("PRAXIS_OTLP_ENDPOINT"),
		OTLPProtocol:               strings.ToLower(getEnv("PRAXIS_OTLP_PROTOCOL", "grpc")),
		OTLPInsecure:               parseBool(os.Getenv("PRAXIS_OTLP_INSECURE")),
		TraceSample:                getFloat("PRAXIS_TRACE_SAMPLE", 1.0),
		SchemaCompat:               strings.ToLower(getEnv("PRAXIS_SCHEMA_COMPAT", "off")),
		MCPFederationConfigPath:    os.Getenv("PRAXIS_MCP_FEDERATION_CONFIG"),
		TLSCertFile:                os.Getenv("PRAXIS_TLS_CERT_FILE"),
		TLSKeyFile:                 os.Getenv("PRAXIS_TLS_KEY_FILE"),
		MTLSClientCAFile:           os.Getenv("PRAXIS_MTLS_CLIENT_CA_FILE"),
		PluginOutOfProcess:         parseBool(os.Getenv("PRAXIS_PLUGIN_OUT_OF_PROCESS")),
		PluginHostBinary:           os.Getenv("PRAXIS_PLUGINHOST_BINARY"),
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
	if c.TLSCertFile != "" && c.TLSKeyFile == "" {
		return c, fmt.Errorf("PRAXIS_TLS_KEY_FILE: required when PRAXIS_TLS_CERT_FILE is set")
	}
	if c.TLSKeyFile != "" && c.TLSCertFile == "" {
		return c, fmt.Errorf("PRAXIS_TLS_CERT_FILE: required when PRAXIS_TLS_KEY_FILE is set")
	}
	if c.MTLSClientCAFile != "" && c.TLSCertFile == "" {
		return c, fmt.Errorf("PRAXIS_MTLS_CLIENT_CA_FILE: requires PRAXIS_TLS_CERT_FILE (mTLS implies TLS)")
	}
	if c.PluginOutOfProcess && c.PluginHostBinary == "" {
		return c, fmt.Errorf("PRAXIS_PLUGINHOST_BINARY: required when PRAXIS_PLUGIN_OUT_OF_PROCESS=1")
	}
	if len(c.PluginFulcioRoots) > 0 {
		if len(c.PluginFulcioSubjects) == 0 {
			return c, fmt.Errorf("PRAXIS_PLUGIN_FULCIO_SUBJECTS: required when PRAXIS_PLUGIN_FULCIO_ROOTS is set")
		}
		if c.PluginFulcioIssuer == "" {
			return c, fmt.Errorf("PRAXIS_PLUGIN_FULCIO_ISSUER: required when PRAXIS_PLUGIN_FULCIO_ROOTS is set")
		}
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

func getFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// parseBool reads a boolean env var. Truthy values: 1, true, yes, on
// (case-insensitive). Anything else (including empty) is false. Used
// for explicit opt-in flags where unknown values must default to off
// rather than panic.
func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseBoolDefault is parseBool with a configurable default for unset
// or unrecognised values. Used for opt-out flags where the default is
// "on" (PRAXIS_PLUGIN_AUTORELOAD).
func parseBoolDefault(raw string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// parseList splits a comma-separated env var into trimmed non-empty
// entries. Used by list-typed settings (PRAXIS_PLUGIN_TRUSTED_KEYS,
// future allow-lists). Returns nil for an empty input so callers can
// distinguish "unset" from "explicitly empty list".
func parseList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseRetention parses PRAXIS_AUDIT_RETENTION. Format is a comma-separated
// list of "key=duration" pairs where key is an OrgID or "*" for the
// default applied to events with no OrgID stamp.
//
//	"*=720h,org-x=2160h,org-y=0"
//
// A value of 0 (or "0s") opts a tenant out of retention sweeps. Malformed
// pairs are silently dropped — startup never fails on a typo, but the
// missing pair shows up as no purge for that tenant.
func parseRetention(raw string) map[string]time.Duration {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]time.Duration{}
	for _, item := range strings.Split(raw, ",") {
		eq := strings.IndexByte(item, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(item[:eq])
		val := strings.TrimSpace(item[eq+1:])
		if key == "*" {
			key = ""
		}
		d, err := time.ParseDuration(val)
		if err != nil {
			continue
		}
		out[key] = d
	}
	return out
}
