// Package mcp exposes Praxis through the Model Context Protocol.
//
// The MCP surface shares the same Executor as the HTTP API — every
// invocation goes through the same policy, schema validation, idempotency,
// rate limit, and audit pipeline. There is no second code path; mcp-go is
// just another transport.
//
// Phase-3 M3.2 bootstrap: three universal tools are registered.
//
//   - list_capabilities  → maps to Executor.ListCapabilities (tool discovery
//     surface; clients can use it to discover what the server can do).
//   - execute            → maps to Executor.Execute.
//   - dry_run            → maps to Executor.DryRun.
//
// A future iteration (separate roady task) will register one MCP tool per
// capability so the schema is exposed natively per tool. The universal-
// tool design here is the smallest change that wires the protocol end to
// end and is enough for agent-go consumption today.
package mcp

import (
	"context"

	"github.com/felixgeelhaar/mcp-go"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

// Executor is the subset of executor.Executor used by the MCP surface.
// Defined as an interface so this package is unit-testable without a full
// executor wired up.
type Executor interface {
	ListCapabilities(ctx context.Context) ([]domain.Capability, error)
	Execute(ctx context.Context, action domain.Action) (domain.Result, error)
	DryRun(ctx context.Context, action domain.Action) (domain.Simulation, error)
}

// Info identifies the MCP server to clients.
type Info struct {
	Name    string
	Version string
}

// ExecuteInput is the body of the universal `execute` tool.
type ExecuteInput struct {
	Capability     string         `json:"capability"`
	Payload        map[string]any `json:"payload,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	CallerType     string         `json:"caller_type,omitempty"`
	CallerID       string         `json:"caller_id,omitempty"`
	Scope          []string       `json:"scope,omitempty"`
}

// ExecuteOutput is the result envelope returned to MCP clients.
type ExecuteOutput struct {
	ActionID   string         `json:"action_id"`
	Status     string         `json:"status"`
	Output     map[string]any `json:"output,omitempty"`
	ExternalID string         `json:"external_id,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
	ErrorMsg   string         `json:"error_message,omitempty"`
}

// DryRunOutput is the result envelope for `dry_run`.
type DryRunOutput struct {
	ActionID   string         `json:"action_id"`
	Decision   string         `json:"policy_decision"`
	Reason     string         `json:"policy_reason"`
	Preview    map[string]any `json:"preview,omitempty"`
	Reversible bool           `json:"reversible"`
}

// ListCapsOutput is the result envelope for `list_capabilities`.
type ListCapsOutput struct {
	Capabilities []domain.Capability `json:"capabilities"`
}

// Register attaches the universal tools to a fresh mcp-go server and
// returns it. The caller wires a transport (stdio/HTTP/etc.).
func Register(info Info, exec Executor, idGen func() string) *mcp.Server {
	srv := mcp.NewServer(mcp.ServerInfo{
		Name:    info.Name,
		Version: info.Version,
	})

	srv.Tool("list_capabilities").
		Description("List every capability this Praxis server knows how to execute. Returns the full descriptor so callers can validate input client-side.").
		Handler(func(ctx context.Context, _ struct{}) (ListCapsOutput, error) {
			caps, err := exec.ListCapabilities(ctx)
			if err != nil {
				return ListCapsOutput{}, err
			}
			return ListCapsOutput{Capabilities: caps}, nil
		})

	srv.Tool("execute").
		Description("Execute a registered capability under policy. Returns the action's result, including a stable external_id when the destination provides one. Set mode=async to receive a 202-equivalent (status=validated) and poll later via the HTTP /v1/actions/{id} endpoint.").
		Handler(func(ctx context.Context, in ExecuteInput) (ExecuteOutput, error) {
			a := actionFromInput(in, idGen)
			res, err := exec.Execute(ctx, a)
			out := ExecuteOutput{
				ActionID:   res.ActionID,
				Status:     string(res.Status),
				Output:     res.Output,
				ExternalID: res.ExternalID,
			}
			if res.Error != nil {
				out.ErrorCode = res.Error.Code
				out.ErrorMsg = res.Error.Message
			} else if err != nil {
				out.ErrorCode = "execute_error"
				out.ErrorMsg = err.Error()
			}
			return out, nil
		})

	srv.Tool("dry_run").
		Description("Simulate a capability invocation without contacting the destination. Returns the policy decision and a faithful preview when the capability is simulatable.").
		Handler(func(ctx context.Context, in ExecuteInput) (DryRunOutput, error) {
			a := actionFromInput(in, idGen)
			sim, err := exec.DryRun(ctx, a)
			if err != nil {
				return DryRunOutput{}, err
			}
			return DryRunOutput{
				ActionID:   sim.ActionID,
				Decision:   sim.PolicyDecision.Decision,
				Reason:     sim.PolicyDecision.Reason,
				Preview:    sim.Preview,
				Reversible: sim.Reversible,
			}, nil
		})

	return srv
}

// ServeStdio is the Phase-1 transport binding: stdio MCP, the canonical
// transport for local agent integration.
func ServeStdio(ctx context.Context, srv *mcp.Server) error {
	return mcp.ServeStdio(ctx, srv)
}

func actionFromInput(in ExecuteInput, idGen func() string) domain.Action {
	id := idGen()
	mode := domain.ActionMode(in.Mode)
	if mode == "" {
		mode = domain.ModeSync
	}
	caller := domain.CallerRef{Type: firstNonEmpty(in.CallerType, "mcp"), ID: in.CallerID}
	return domain.Action{
		ID:             id,
		Capability:     in.Capability,
		Payload:        in.Payload,
		Caller:         caller,
		Scope:          in.Scope,
		IdempotencyKey: firstNonEmpty(in.IdempotencyKey, id),
		Mode:           mode,
		Status:         domain.StatusPending,
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
