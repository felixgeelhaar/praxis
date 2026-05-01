// Package observability holds the OpenTelemetry wiring for Praxis.
// One package keeps tracer-provider lifecycle, exporter selection,
// and resource attributes in one place; the executor and HTTP layer
// stay vendor-neutral and call into otel.GetTracerProvider().
//
// Phase 5 OTel tracing.
package observability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// TracingConfig parameters the tracer-provider wiring. Empty Endpoint
// disables tracing — Init returns a no-op tracer + a no-op shutdown
// so callers can wire defer shutdown(ctx) unconditionally.
type TracingConfig struct {
	// Endpoint is the OTLP collector address. Empty disables tracing.
	Endpoint string

	// Protocol is "grpc" (default) or "http". Anything else fails
	// startup so a typo does not silently fall back to no tracing.
	Protocol string

	// Insecure disables TLS for the collector connection. Default is
	// secure (TLS); set to true only for local development collectors.
	Insecure bool

	// Sample is the trace sampling probability (0..1). Default 1.0
	// (every span sampled) for parity with the audit log; production
	// deployments under load should lower this.
	Sample float64

	// ServiceName is recorded as the service.name resource attribute.
	// Defaults to "praxis" when empty.
	ServiceName string

	// ServiceVersion is recorded as service.version. The CLI bootstrap
	// passes the build-time Version constant.
	ServiceVersion string

	// ExportTimeout caps the duration of one OTLP export call. Default
	// 5s; on-prem collectors with high latency may bump this.
	ExportTimeout time.Duration
}

// Tracer is the package-public alias so call sites do not import the
// otel/trace package directly.
type Tracer = trace.Tracer

// ShutdownFn flushes pending spans and shuts the provider down.
// Always safe to call: a no-op tracer returns a no-op ShutdownFn.
type ShutdownFn func(ctx context.Context) error

// Init configures the global tracer provider per the supplied
// TracingConfig and returns a tracer and a shutdown function. When
// the endpoint is empty, Init wires a noop tracer provider so call
// sites never branch on "is tracing enabled?" — they call
// otel.Tracer(...) the same way regardless.
func Init(ctx context.Context, cfg TracingConfig) (Tracer, ShutdownFn, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		))
		return otel.Tracer(serviceName(cfg)), noopShutdown, nil
	}

	exporter, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("otel exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName(cfg)),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("otel resource: %w", err)
	}

	sample := cfg.Sample
	if sample <= 0 {
		sample = 1.0
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(sample),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		// ForceFlush before Shutdown so any in-flight spans land.
		_ = tp.ForceFlush(ctx)
		return tp.Shutdown(ctx)
	}
	return tp.Tracer(serviceName(cfg)), shutdown, nil
}

func newExporter(ctx context.Context, cfg TracingConfig) (sdktrace.SpanExporter, error) {
	timeout := cfg.ExportTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Protocol)) {
	case "", "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTimeout(timeout),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithTimeout(timeout),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	default:
		return nil, fmt.Errorf("unknown OTLP protocol %q (want grpc|http)", cfg.Protocol)
	}
}

func serviceName(cfg TracingConfig) string {
	if cfg.ServiceName != "" {
		return cfg.ServiceName
	}
	return "praxis"
}

func noopShutdown(_ context.Context) error { return nil }

// ErrUnknownProtocol is returned by Init when Protocol is set to
// something other than "grpc" or "http".
var ErrUnknownProtocol = errors.New("unknown OTLP protocol")
