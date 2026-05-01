package plugin_test

import (
	"context"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/plugin/ipc"
)

// BenchmarkProcessOpener_Execute measures the per-call IPC overhead
// of routing an Execute through the out-of-process loader. Uses an
// in-process pipe pair via SpawnFn so the bench isolates the codec +
// dispatch + JSON marshalling cost from any real subprocess scheduling.
//
// The delta between this and BenchmarkExecute_Memory in
// internal/executor is the IPC tax operators pay for true memory
// isolation. Phase 5: published target is < 200µs/op median on a
// developer laptop.
func BenchmarkProcessOpener_Execute(b *testing.B) {
	child := &fakeChild{
		manifest: ipc.ManifestResult{Name: "bench", Version: "1.0.0"},
		caps: []ipc.CapabilityDescriptor{
			{Name: "bench_cap", Simulatable: true, Idempotent: true},
		},
		exec: func(_ string, payload map[string]any) (map[string]any, error) {
			return map[string]any{"echoed": payload["i"]}, nil
		},
	}
	op := newProcessOpenerForBench(b, child)
	p, err := op.Open("/tmp/bench.so")
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	regs, err := p.Capabilities(context.Background())
	if err != nil {
		b.Fatalf("Capabilities: %v", err)
	}
	h := regs[0].Handler

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Execute(context.Background(), map[string]any{"i": i}); err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// BenchmarkProcessOpener_Execute_Parallel measures concurrent
// throughput so contention on the codec write mutex (a known
// bottleneck) shows up.
func BenchmarkProcessOpener_Execute_Parallel(b *testing.B) {
	child := &fakeChild{
		manifest: ipc.ManifestResult{Name: "bench", Version: "1.0.0"},
		caps: []ipc.CapabilityDescriptor{
			{Name: "bench_cap"},
		},
		exec: func(_ string, payload map[string]any) (map[string]any, error) {
			return map[string]any{"echoed": payload["i"]}, nil
		},
	}
	op := newProcessOpenerForBench(b, child)
	p, _ := op.Open("/tmp/bench.so")
	regs, _ := p.Capabilities(context.Background())
	h := regs[0].Handler

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, err := h.Execute(context.Background(), map[string]any{"i": i}); err != nil {
				b.Fatalf("Execute: %v", err)
			}
			i++
		}
	})
}
