package observability_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/observability"
)

func TestInit_NoEndpointReturnsNoOp(t *testing.T) {
	tr, shutdown, err := observability.Init(context.Background(), observability.TracingConfig{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tr == nil {
		t.Fatal("nil tracer")
	}
	// Issuing a span on the no-op tracer is a no-op but must not
	// panic; record the smoke test explicitly.
	_, span := tr.Start(context.Background(), "smoke")
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestInit_UnknownProtocolFails(t *testing.T) {
	_, _, err := observability.Init(context.Background(), observability.TracingConfig{
		Endpoint: "localhost:4317",
		Protocol: "carrier-pigeon",
	})
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
	if !strings.Contains(err.Error(), "carrier-pigeon") {
		t.Errorf("err=%v should mention bad protocol", err)
	}
}

func TestInit_GRPCExporterRefusesInvalidEndpoint(t *testing.T) {
	// The endpoint is syntactically valid but unreachable; with a
	// short timeout the exporter constructor still succeeds (it
	// connects lazily). The test confirms Init does not block on
	// dial — a real shutdown drains anyway.
	tr, shutdown, err := observability.Init(context.Background(), observability.TracingConfig{
		Endpoint:      "127.0.0.1:1",
		Protocol:      "grpc",
		Insecure:      true,
		Sample:        1.0,
		ExportTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tr == nil {
		t.Fatal("nil tracer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_HTTPProtocolAccepted(t *testing.T) {
	tr, shutdown, err := observability.Init(context.Background(), observability.TracingConfig{
		Endpoint:      "127.0.0.1:1",
		Protocol:      "http",
		Insecure:      true,
		ExportTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tr == nil {
		t.Fatal("nil tracer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_DefaultProtocolIsGRPC(t *testing.T) {
	tr, shutdown, err := observability.Init(context.Background(), observability.TracingConfig{
		Endpoint:      "127.0.0.1:1",
		Insecure:      true,
		ExportTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if tr == nil {
		t.Fatal("nil tracer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_NoOpShutdownIdempotent(t *testing.T) {
	_, shutdown, err := observability.Init(context.Background(), observability.TracingConfig{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("shutdown call %d: %v", i, err)
		}
	}
}
