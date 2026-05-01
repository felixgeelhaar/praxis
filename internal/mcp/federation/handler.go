package federation

import (
	"context"
	"fmt"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// federatedHandler proxies Execute and Simulate calls to an upstream
// MCP tool. Every call goes through Praxis's executor pipeline first
// (policy, schema, idempotency, audit, emit), so a federated tool
// inherits the same governance as a local handler — only the actual
// invocation is forwarded.
//
// Phase 5 federated MCP.
type federatedHandler struct {
	conn         *Connection
	toolName     string // upstream's local tool name
	capName      string // namespaced "<upstream>__<tool>" registry key
	upstreamName string
}

// Name implements capability.Handler. Returns the namespaced
// capability name so the registry's name → handler map stays in
// lock-step with the registered domain.Capability.Name.
func (h *federatedHandler) Name() string { return h.capName }

// Execute forwards to the upstream's CallTool. The result envelope
// from mcp-go is returned as-is in an "output" key so the executor's
// schema validator can verify it against the capability's
// OutputSchema (which mirrors the upstream's tool schema).
func (h *federatedHandler) Execute(ctx context.Context, payload map[string]any) (map[string]any, error) {
	res, err := h.conn.CallTool(ctx, h.toolName, payload)
	if err != nil {
		return nil, fmt.Errorf("federated tool %q on upstream %q: %w", h.toolName, h.upstreamName, err)
	}
	return map[string]any{
		"output":   res,
		"upstream": h.upstreamName,
	}, nil
}

// Simulate piggybacks on Execute. mcp-go has no separate dry-run RPC
// today; until upstream support lands, federated capabilities are
// flagged Simulatable=false in their descriptor so the executor's
// DryRun path returns a "not simulatable" preview without invoking
// this method.
func (h *federatedHandler) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return map[string]any{
		"note":     "federated upstream does not implement dry_run",
		"upstream": h.upstreamName,
	}, nil
}

// Capability implements capability.Describer so the registry's
// type-assertion picks up the upstream's input schema.
func (h *federatedHandler) Capability() domain.Capability {
	return domain.Capability{
		Name:        h.capName,
		Description: fmt.Sprintf("Federated tool %s from upstream %s", h.toolName, h.upstreamName),
		Simulatable: false,
		Idempotent:  false,
	}
}

// Registrations turns an active Connection into the [Registration]
// entries the runtime registry expects. Each upstream tool becomes
// one registration whose capability descriptor mirrors the upstream
// schema and whose handler routes through the federated client.
//
// The capability name is namespaced as "<upstream>__<tool>" so two
// upstreams advertising the same tool name don't collide. The
// upstream identity is also stamped into the capability's
// Description so /v1/capabilities discovery surfaces the origin.
func Registrations(conn *Connection) []Registration {
	out := make([]Registration, 0, len(conn.Tools))
	for _, tool := range conn.Tools {
		toolName := tool.Name
		capName := conn.UpstreamName + "__" + toolName
		out = append(out, Registration{
			Capability: domain.Capability{
				Name:        capName,
				Description: fmt.Sprintf("Federated MCP tool %s on upstream %s. %s", toolName, conn.UpstreamName, tool.Description),
				InputSchema: tool.InputSchema,
				Simulatable: false,
				Idempotent:  false,
			},
			Handler: &federatedHandler{
				conn:         conn,
				toolName:     toolName,
				capName:      capName,
				upstreamName: conn.UpstreamName,
			},
		})
	}
	return out
}

// Registration mirrors plugin.Registration locally so this package
// doesn't pull internal/plugin into its public surface for callers
// that only need federation. The bootstrap converts to plugin.Loader
// shape if needed.
type Registration struct {
	Capability domain.Capability
	Handler    capability.Handler
}
