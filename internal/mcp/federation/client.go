package federation

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/felixgeelhaar/mcp-go/client"
)

// ErrURLTransportUnsupported is retained for backwards-compatible
// callers that switch on it. As of Phase 6 t-fed-http-transport the
// federation client supports HTTP upstreams via mcp-go v1.10's
// HTTPTransport, so this sentinel is no longer returned for URL
// upstreams.
//
// Deprecated: HTTP federation is now supported. Kept only so external
// callers that imported the symbol do not fail to compile.
var ErrURLTransportUnsupported = errors.New("federation upstream URL transport not yet supported (stdio only)")

// Tool mirrors mcp-go's Tool but lives in this package so callers
// don't need a transitive import. Phase 5 federated MCP.
type Tool struct {
	Name        string
	Description string
	InputSchema any
}

// Connection wraps a live mcp-go Client + the metadata the federation
// supervisor needs to route Execute calls back through it. Close
// releases the underlying transport (stdio: SIGTERM the subprocess).
type Connection struct {
	UpstreamName string
	Tools        []Tool

	client    *client.Client
	transport client.Transport

	// closed receives the terminal error from the dispatcher when the
	// transport breaks. Buffered + non-blocking so a fast failure does
	// not deadlock when no observer attaches.
	closed chan error
}

// CallTool forwards an Execute payload to the upstream tool. Used by
// the federation handler (t-mcp-federation-handler) when the executor
// dispatches through a federated capability.
func (c *Connection) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	res, err := c.client.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Watch returns a channel that fires once the transport breaks.
// Mirrors plugin.Watchable so the federation supervisor can use the
// existing crash-recovery pattern. The channel is buffered so the
// dispatcher's failure path never blocks.
func (c *Connection) Watch() <-chan error { return c.closed }

// Close releases the upstream connection. Idempotent.
func (c *Connection) Close() error {
	if c.transport != nil {
		return c.transport.Close()
	}
	return nil
}

// Connect dials an upstream MCP server and runs the initialize +
// list-tools handshake. The Allow allowlist filters the returned tool
// set; an empty Allow means "every tool the upstream advertises."
//
// Both stdio (Command) and HTTP (URL) transports are supported. HTTP
// upstreams forward the upstream's Token as a Bearer header and
// verify the server certificate against CABundle (or the host trust
// store when CABundle is empty). InsecureSkipVerify disables that
// check for local development.
func Connect(ctx context.Context, upstream Upstream) (*Connection, error) {
	if upstream.URL != "" && len(upstream.Command) > 0 {
		return nil, fmt.Errorf("upstream %q: set exactly one of url or command", upstream.Name)
	}
	if upstream.URL == "" && len(upstream.Command) == 0 {
		return nil, fmt.Errorf("upstream %q: url or command required", upstream.Name)
	}

	transport, err := dialTransport(upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", upstream.Name, err)
	}

	cli := client.New(transport)
	if _, err := cli.Initialize(ctx); err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("upstream %q: initialize: %w", upstream.Name, err)
	}

	tools, err := cli.ListTools(ctx)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("upstream %q: list tools: %w", upstream.Name, err)
	}

	allow := stringSet(upstream.Allow)
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if len(allow) > 0 && !allow[t.Name] {
			continue
		}
		out = append(out, Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	return &Connection{
		UpstreamName: upstream.Name,
		Tools:        out,
		client:       cli,
		transport:    transport,
		closed:       make(chan error, 1),
	}, nil
}

// NewConnectionForTest returns a Connection without a real
// transport. The supervisor + manager tests use this to drive the
// reconnect / deregister paths without spawning real subprocesses.
// Production callers must use Connect.
func NewConnectionForTest(upstreamName string, tools []Tool) *Connection {
	return &Connection{
		UpstreamName: upstreamName,
		Tools:        tools,
		closed:       make(chan error, 1),
	}
}

// TriggerCloseForTest pushes an error onto the connection's Watch
// channel, simulating a transport failure. Tests use this to drive
// the supervisor's disconnect path deterministically.
func TriggerCloseForTest(c *Connection, err error) {
	select {
	case c.closed <- err:
	default:
	}
}

// dialTransport selects between stdio and HTTP transports based on
// the upstream's URL / Command fields. Validation already enforces
// exactly one is set.
func dialTransport(u Upstream) (client.Transport, error) {
	if u.URL != "" {
		opts := []client.HTTPTransportOption{}
		if u.Token != "" {
			opts = append(opts, client.WithBearerToken(u.Token))
		}
		if u.CABundle != "" {
			pool, err := loadCAPool(u.CABundle)
			if err != nil {
				return nil, fmt.Errorf("ca bundle: %w", err)
			}
			opts = append(opts, client.WithCABundle(pool))
		}
		if u.InsecureSkipVerify {
			opts = append(opts, client.WithInsecureSkipVerify())
		}
		t, err := client.NewHTTPTransport(u.URL, opts...)
		if err != nil {
			return nil, fmt.Errorf("http transport: %w", err)
		}
		return t, nil
	}
	t, err := client.NewStdioTransport(u.Command[0], u.Command[1:]...)
	if err != nil {
		return nil, fmt.Errorf("stdio transport: %w", err)
	}
	return t, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("no PEM certificates parsed from %s", path)
	}
	return pool, nil
}

func stringSet(s []string) map[string]bool {
	out := make(map[string]bool, len(s))
	for _, v := range s {
		out[v] = true
	}
	return out
}
