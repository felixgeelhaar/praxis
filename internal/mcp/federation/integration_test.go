//go:build integration

// Real-upstream integration test: spins up a full mcp-go server over
// HTTP and drives it through federation.Connect. Run with:
//
//	go test -tags=integration ./internal/mcp/federation/...
package federation_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	mcp "github.com/felixgeelhaar/mcp-go"
	"github.com/felixgeelhaar/praxis/internal/mcp/federation"
)

type echoInput struct {
	Message string `json:"message" jsonschema:"required,description=Text to echo"`
}

func startUpstream(t *testing.T) (string, func()) {
	t.Helper()
	srv := mcp.NewServer(mcp.ServerInfo{
		Name:    "praxis-fed-it",
		Version: "0.0.1",
		Capabilities: mcp.Capabilities{
			Tools: true,
		},
	})
	srv.Tool("echo").
		Description("Echo a message back to the caller").
		Handler(func(_ context.Context, in echoInput) (string, error) {
			return "echoed: " + in.Message, nil
		})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := mcp.ServeHTTP(ctx, srv, addr); err != nil &&
			!errors.Is(err, context.Canceled) {
			t.Logf("ServeHTTP exited: %v", err)
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	stop := func() {
		cancel()
		wg.Wait()
	}
	return "http://" + addr, stop
}

func TestFederation_RealUpstream_RoundTrip(t *testing.T) {
	url, stop := startUpstream(t)
	defer stop()

	conn, err := federation.Connect(context.Background(), federation.Upstream{
		Name: "real-upstream",
		URL:  url,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if len(conn.Tools) != 1 {
		t.Fatalf("Tools=%d want 1", len(conn.Tools))
	}
	if conn.Tools[0].Name != "echo" {
		t.Errorf("tool name=%q want echo", conn.Tools[0].Name)
	}

	res, err := conn.CallTool(context.Background(), "echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	got := fmt.Sprintf("%v", res)
	if !contains(got, "echoed: hi") {
		t.Errorf("CallTool result=%q want to contain 'echoed: hi'", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
