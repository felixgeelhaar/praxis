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
// Per-capability tool registration is layered on top of the universal
// tools: at boot, Register iterates Executor.ListCapabilities and adds
// one MCP tool per capability whose name is the capability name. The
// per-cap tool's description embeds the capability's JSON Schema so MCP
// clients (e.g. agent-go agents) see one tool per Praxis capability —
// idiomatic from the agent's perspective — while still flowing through
// the same executor pipeline (policy, schema, idempotency, audit).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/felixgeelhaar/mcp-go"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/felixgeelhaar/praxis/internal/mcp"

func tracer() trace.Tracer { return otel.Tracer(tracerName) }

// Executor is the subset of executor.Executor used by the MCP surface.
// Defined as an interface so this package is unit-testable without a full
// executor wired up.
type Executor interface {
	ListCapabilities(ctx context.Context) ([]domain.Capability, error)
	ListCapabilitiesForCaller(ctx context.Context, caller domain.CallerRef) ([]domain.Capability, error)
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
	OrgID          string         `json:"org_id,omitempty"`  // tenant scope; routes private capabilities (Phase 4)
	TeamID         string         `json:"team_id,omitempty"` // tenant scope; (Phase 4)
	Scope          []string       `json:"scope,omitempty"`
}

// ListCapsInput is the optional body of the universal `list_capabilities`
// tool. Empty input returns the global view; OrgID/TeamID scope the
// returned set to the caller's tenant-private capabilities plus the
// globals (Phase 4 M3.3).
type ListCapsInput struct {
	OrgID  string `json:"org_id,omitempty"`
	TeamID string `json:"team_id,omitempty"`
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
		Description("List every capability this Praxis server knows how to execute. Returns the full descriptor so callers can validate input client-side. Optional org_id/team_id scope the result to the caller's tenant-private capabilities plus the globals.").
		Handler(func(ctx context.Context, in ListCapsInput) (ListCapsOutput, error) {
			ctx, span := tracer().Start(ctx, "mcp.list_capabilities",
				trace.WithAttributes(
					attribute.String("praxis.org.id", in.OrgID),
					attribute.String("praxis.team.id", in.TeamID),
				))
			defer span.End()
			caller := domain.CallerRef{OrgID: in.OrgID, TeamID: in.TeamID}
			caps, err := exec.ListCapabilitiesForCaller(ctx, caller)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				return ListCapsOutput{}, err
			}
			span.SetAttributes(attribute.Int("praxis.capabilities.count", len(caps)))
			return ListCapsOutput{Capabilities: caps}, nil
		})

	srv.Tool("execute").
		Description("Execute a registered capability under policy. Returns the action's result, including a stable external_id when the destination provides one. Set mode=async to receive a 202-equivalent (status=validated) and poll later via the HTTP /v1/actions/{id} endpoint.").
		Handler(func(ctx context.Context, in ExecuteInput) (ExecuteOutput, error) {
			ctx, span := tracer().Start(ctx, "mcp.execute",
				trace.WithAttributes(attribute.String("praxis.capability", in.Capability)))
			defer span.End()
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
				span.SetStatus(codes.Error, res.Error.Message)
			} else if err != nil {
				out.ErrorCode = "execute_error"
				out.ErrorMsg = err.Error()
				span.SetStatus(codes.Error, err.Error())
			}
			return out, nil
		})

	srv.Tool("dry_run").
		Description("Simulate a capability invocation without contacting the destination. Returns the policy decision and a faithful preview when the capability is simulatable.").
		Handler(func(ctx context.Context, in ExecuteInput) (DryRunOutput, error) {
			ctx, span := tracer().Start(ctx, "mcp.dry_run",
				trace.WithAttributes(attribute.String("praxis.capability", in.Capability)))
			defer span.End()
			a := actionFromInput(in, idGen)
			sim, err := exec.DryRun(ctx, a)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
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

	registerPerCapability(srv, exec, idGen)
	return srv
}

// CapInput is the per-capability tool input. Each capability's MCP tool
// shape is `{"payload": {...cap-specific JSON Schema...}, "idempotency_key"?,
// "mode"?, "scope"?}`. The cap-specific schema is conveyed via the tool's
// description because mcp-go derives the wire schema from this struct's
// reflection — a dynamic per-call schema is not yet supported by mcp-go.
type CapInput struct {
	Payload        map[string]any `json:"payload,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	CallerType     string         `json:"caller_type,omitempty"`
	CallerID       string         `json:"caller_id,omitempty"`
	OrgID          string         `json:"org_id,omitempty"`
	TeamID         string         `json:"team_id,omitempty"`
	Scope          []string       `json:"scope,omitempty"`
}

// registerPerCapability adds one MCP tool per Praxis capability. Failures
// to enumerate are non-fatal — the universal `execute` and `dry_run` tools
// already give clients a working surface.
func registerPerCapability(srv *mcp.Server, exec Executor, idGen func() string) {
	caps, err := exec.ListCapabilities(context.Background())
	if err != nil || len(caps) == 0 {
		return
	}
	for _, c := range caps {
		c := c // closure capture
		desc := buildCapDescription(c)
		srv.Tool(c.Name).
			Description(desc).
			Handler(func(ctx context.Context, in CapInput) (ExecuteOutput, error) {
				ctx, span := tracer().Start(ctx, "mcp.tool."+c.Name,
					trace.WithAttributes(attribute.String("praxis.capability", c.Name)))
				defer span.End()
				a := actionFromCapInput(c.Name, in, idGen)
				res, eerr := exec.Execute(ctx, a)
				out := ExecuteOutput{
					ActionID:   res.ActionID,
					Status:     string(res.Status),
					Output:     res.Output,
					ExternalID: res.ExternalID,
				}
				if res.Error != nil {
					out.ErrorCode = res.Error.Code
					out.ErrorMsg = res.Error.Message
					span.SetStatus(codes.Error, res.Error.Message)
				} else if eerr != nil {
					out.ErrorCode = "execute_error"
					out.ErrorMsg = eerr.Error()
					span.SetStatus(codes.Error, eerr.Error())
				}
				return out, nil
			})
	}
}

// buildCapDescription embeds the capability's JSON Schema and metadata in
// the tool description so MCP clients can render and validate the payload
// shape locally.
func buildCapDescription(c domain.Capability) string {
	desc := c.Description
	if desc == "" {
		desc = c.Name
	}
	if c.InputSchema != nil {
		if b, err := json.MarshalIndent(c.InputSchema, "", "  "); err == nil {
			desc += "\n\n## Input Payload Schema\n\n```json\n" + string(b) + "\n```"
		}
	}
	if len(c.Permissions) > 0 {
		desc += fmt.Sprintf("\n\n**Permissions required:** %v", c.Permissions)
	}
	if c.Simulatable {
		desc += "\n\nSupports dry_run."
	}
	if c.Idempotent {
		desc += " Idempotent at destination."
	}
	return desc
}

func actionFromCapInput(capName string, in CapInput, idGen func() string) domain.Action {
	id := idGen()
	mode := domain.ActionMode(in.Mode)
	if mode == "" {
		mode = domain.ModeSync
	}
	return domain.Action{
		ID:         id,
		Capability: capName,
		Payload:    in.Payload,
		Caller: domain.CallerRef{
			Type:   firstNonEmpty(in.CallerType, "mcp"),
			ID:     in.CallerID,
			OrgID:  in.OrgID,
			TeamID: in.TeamID,
		},
		Scope:          in.Scope,
		IdempotencyKey: firstNonEmpty(in.IdempotencyKey, id),
		Mode:           mode,
		Status:         domain.StatusPending,
	}
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
	caller := domain.CallerRef{
		Type:   firstNonEmpty(in.CallerType, "mcp"),
		ID:     in.CallerID,
		OrgID:  in.OrgID,
		TeamID: in.TeamID,
	}
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
