package executor_test

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/executor"
	"github.com/felixgeelhaar/praxis/internal/handlerrunner"
	"github.com/felixgeelhaar/praxis/internal/idempotency"
	"github.com/felixgeelhaar/praxis/internal/policy"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/schema"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

// benchExec wires a full Executor against the supplied repos. The
// handler is a no-op map[string]any returner; the bench measures the
// executor's own pipeline cost (validate, policy, idempotency, audit,
// emit) rather than vendor latency.
func benchExec(b *testing.B, repos *ports.Repos) (*executor.Executor, *capability.Registry) {
	b.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	reg := capability.New()
	if err := reg.Register(&fakeHandler{
		name:   "bench",
		output: map[string]any{"ts": "1.0", "ok": true},
	}); err != nil {
		b.Fatalf("register: %v", err)
	}
	pol := policy.New(logger, repos.Policy)
	idem := idempotency.New(repos.Idempotency)
	runner := handlerrunner.New(logger, handlerrunner.Config{MaxAttempts: 1})
	exec := executor.New(logger, reg, pol, idem, runner, schema.New(),
		repos.Action, repos.Audit, nil)
	exec.SetClock(time.Now)
	return exec, reg
}

// BenchmarkExecute_Memory measures the executor hot path against the
// in-memory backend. This is the floor: any backend cost shows up as
// the delta between this bench and BenchmarkExecute_Sqlite /
// BenchmarkExecute_Postgres.
func BenchmarkExecute_Memory(b *testing.B) {
	repos := memory.New()
	exec, _ := benchExec(b, repos)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := domain.Action{
			ID:         "bench-" + strconv.Itoa(i),
			Capability: "bench",
			Payload:    map[string]any{"i": i},
			Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
		}
		if _, err := exec.Execute(ctx, a); err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// BenchmarkExecute_Memory_Parallel measures concurrent throughput on
// the in-memory backend. The target is 5k actions/sec on a developer
// laptop (per the f-perf-benchmarks SLOs).
func BenchmarkExecute_Memory_Parallel(b *testing.B) {
	repos := memory.New()
	exec, _ := benchExec(b, repos)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			a := domain.Action{
				ID:         fmt.Sprintf("bench-%p-%d", pb, i),
				Capability: "bench",
				Payload:    map[string]any{"i": i},
				Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
			}
			if _, err := exec.Execute(ctx, a); err != nil {
				b.Fatalf("Execute: %v", err)
			}
			i++
		}
	})
}

// BenchmarkDryRun_Memory measures the simulate path. Dry-runs avoid
// the idempotency write and outcome emit but still pay validation
// and policy.
func BenchmarkDryRun_Memory(b *testing.B) {
	repos := memory.New()
	exec, _ := benchExec(b, repos)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := domain.Action{
			ID:         "bench-dry-" + strconv.Itoa(i),
			Capability: "bench",
			Payload:    map[string]any{"i": i},
			Caller:     domain.CallerRef{Type: "user", ID: "u-1"},
		}
		if _, err := exec.DryRun(ctx, a); err != nil {
			b.Fatalf("DryRun: %v", err)
		}
	}
}
