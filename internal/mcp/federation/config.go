// Package federation lets a Praxis instance aggregate upstream MCP
// servers as Praxis capabilities. The agent talks to one Praxis
// instance and reaches every tool the operator has approved across
// multiple upstream MCP servers — without each tool reimplementing
// policy, audit, idempotency, or rate limiting.
//
// This file is the configuration parser; the upstream client + tool
// handler land in t-mcp-federation-client / t-mcp-federation-handler.
//
// Phase 5 federated MCP.
package federation

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape pointed at by PRAXIS_MCP_FEDERATION_CONFIG.
// Each Upstream entry adds one MCP server whose tools become Praxis
// capabilities once the federation client connects.
type Config struct {
	Upstreams []Upstream `yaml:"upstreams"`
}

// Upstream describes one upstream MCP server. Either URL (HTTP / SSE
// transport) or Command (stdio transport) must be set; configurations
// with both or neither fail validation.
//
// Allow restricts which of the upstream's tools become Praxis
// capabilities. Empty Allow means "every tool the upstream advertises";
// non-empty acts as an allowlist.
//
// Token is forwarded to the upstream as a Bearer authorization header
// for HTTP transports; ignored for stdio.
type Upstream struct {
	Name    string   `yaml:"name"`
	URL     string   `yaml:"url,omitempty"`
	Command []string `yaml:"command,omitempty"`
	Token   string   `yaml:"token,omitempty"`
	Allow   []string `yaml:"allow,omitempty"`

	// CABundle is a filesystem path to a PEM bundle the federation
	// client trusts when verifying the upstream's TLS certificate.
	// Empty means "use the host's default trust store." Ignored for
	// stdio transports.
	CABundle string `yaml:"ca_bundle,omitempty"`
	// InsecureSkipVerify disables TLS certificate verification.
	// Dangerous; only useful for local development against a private
	// MCP server with self-signed certs.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// LoadConfig parses the file at path into a Config. Returns
// ErrConfigPathEmpty when path is the empty string so the bootstrap
// can branch on "federation not configured" without inspecting fs
// errors.
func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, ErrConfigPathEmpty
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read federation config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse federation config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate enforces the structural rules the federation client
// depends on at startup. Returns the first violation rather than a
// list — startup errors are noisy enough as a single line, and the
// operator iterates one fix at a time.
func (c *Config) Validate() error {
	names := map[string]bool{}
	for i, u := range c.Upstreams {
		if strings.TrimSpace(u.Name) == "" {
			return fmt.Errorf("upstream[%d]: name required", i)
		}
		if names[u.Name] {
			return fmt.Errorf("upstream[%d]: duplicate name %q", i, u.Name)
		}
		names[u.Name] = true
		hasURL := strings.TrimSpace(u.URL) != ""
		hasCmd := len(u.Command) > 0
		switch {
		case hasURL && hasCmd:
			return fmt.Errorf("upstream %q: set exactly one of url or command", u.Name)
		case !hasURL && !hasCmd:
			return fmt.Errorf("upstream %q: must set url or command", u.Name)
		}
	}
	return nil
}

// ErrConfigPathEmpty signals that PRAXIS_MCP_FEDERATION_CONFIG was
// unset; the bootstrap treats this as "federation disabled" rather
// than a hard error. Other read/parse failures surface as wrapped
// errors so the operator sees the underlying cause.
var ErrConfigPathEmpty = errors.New("federation config path empty")
