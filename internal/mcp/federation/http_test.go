package federation_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/mcp/federation"
)

// fakeMCPServer answers JSON-RPC over POST /mcp matching the wire
// format mcp-go's transport.HTTP serves. Just enough surface for
// initialize + tools/list to drive federation.Connect.
type fakeMCPServer struct {
	mu      sync.Mutex
	headers http.Header
	tools   []map[string]any
}

func (f *fakeMCPServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.headers = r.Header.Clone()
		f.mu.Unlock()

		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "fake", "version": "1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			result = map[string]any{"tools": f.tools}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		})
	}
}

func TestConnect_HTTPHappyPath(t *testing.T) {
	srv := &fakeMCPServer{
		tools: []map[string]any{
			{"name": "echo", "description": "echoes input"},
			{"name": "ping", "description": "ping"},
		},
	}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	conn, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "fake", URL: ts.URL,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if len(conn.Tools) != 2 {
		t.Errorf("Tools=%d want 2", len(conn.Tools))
	}
}

func TestConnect_HTTPBearerForwarded(t *testing.T) {
	srv := &fakeMCPServer{tools: []map[string]any{}}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	conn, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "auth", URL: ts.URL, Token: "s3cret",
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	srv.mu.Lock()
	got := srv.headers.Get("Authorization")
	srv.mu.Unlock()
	if got != "Bearer s3cret" {
		t.Errorf("Authorization=%q want Bearer s3cret", got)
	}
}

func TestConnect_HTTPAllowFiltersTools(t *testing.T) {
	srv := &fakeMCPServer{
		tools: []map[string]any{
			{"name": "echo"}, {"name": "ping"}, {"name": "secret"},
		},
	}
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	conn, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "filtered", URL: ts.URL, Allow: []string{"echo", "ping"},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if len(conn.Tools) != 2 {
		t.Errorf("Tools=%d want 2", len(conn.Tools))
	}
	for _, tl := range conn.Tools {
		if tl.Name == "secret" {
			t.Errorf("disallowed tool surfaced: %s", tl.Name)
		}
	}
}

func TestConnect_HTTPInsecureSkipVerify(t *testing.T) {
	srv := &fakeMCPServer{tools: []map[string]any{}}
	ts := httptest.NewTLSServer(srv.handler())
	defer ts.Close()

	conn, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "tls", URL: ts.URL, InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()
}

func TestConnect_HTTPDefaultPoolRejectsSelfSigned(t *testing.T) {
	srv := &fakeMCPServer{tools: []map[string]any{}}
	ts := httptest.NewTLSServer(srv.handler())
	defer ts.Close()

	_, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "tls-pinned", URL: ts.URL,
	})
	if err == nil {
		t.Fatal("expected TLS verification failure")
	}
	if !strings.Contains(err.Error(), "x509") && !strings.Contains(err.Error(), "tls") &&
		!strings.Contains(err.Error(), "certificate") {
		t.Errorf("err=%v want TLS-related failure", err)
	}
}
