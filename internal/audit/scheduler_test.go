package audit_test

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/bolt"
	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func newScheduler(t *testing.T, svc *audit.Service, cfg audit.SchedulerConfig) *audit.Scheduler {
	t.Helper()
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	return audit.NewScheduler(svc, logger, cfg)
}

func TestScheduler_RunOncePurgesExpired(t *testing.T) {
	repos := memory.New()
	now := time.Unix(10_000, 0)
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "old", OrgID: "org-a", CreatedAt: now.Add(-2 * time.Hour)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "new", OrgID: "org-a", CreatedAt: now.Add(-10 * time.Minute)})

	svc := audit.New(repos.Audit).WithRetention(audit.RetentionPolicy{
		"org-a": time.Hour,
	})
	sched := newScheduler(t, svc, audit.SchedulerConfig{Now: func() time.Time { return now }})

	purged := map[string]int64{}
	sched.OnPurge = func(orgID string, deleted int64, _ error) {
		purged[orgID] += deleted
	}
	sched.RunOnce(context.Background())

	if purged["org-a"] != 1 {
		t.Errorf("purged[org-a]=%d want 1", purged["org-a"])
	}
}

func TestScheduler_RunRespectsContextCancellation(t *testing.T) {
	repos := memory.New()
	svc := audit.New(repos.Audit)
	sched := newScheduler(t, svc, audit.SchedulerConfig{
		InitialDelay: 10 * time.Millisecond,
		Interval:     10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestScheduler_RunFiresAfterInitialDelay(t *testing.T) {
	repos := memory.New()
	now := time.Unix(10_000, 0)
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "expired", OrgID: "org-a", CreatedAt: now.Add(-2 * time.Hour)})

	svc := audit.New(repos.Audit).WithRetention(audit.RetentionPolicy{
		"org-a": time.Hour,
	})
	var swept int32
	sched := newScheduler(t, svc, audit.SchedulerConfig{
		InitialDelay: 20 * time.Millisecond,
		Interval:     10 * time.Second, // long enough that only the initial fires
		Now:          func() time.Time { return now },
	})
	sched.OnPurge = func(_ string, _ int64, _ error) { atomic.AddInt32(&swept, 1) }

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go sched.Run(ctx)

	// Wait for initial delay + buffer.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) && atomic.LoadInt32(&swept) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&swept) == 0 {
		t.Error("expected at least one sweep after InitialDelay")
	}
}

func TestScheduler_DefaultsApplied(t *testing.T) {
	svc := audit.New(memory.New().Audit)
	logger := bolt.New(bolt.NewJSONHandler(io.Discard))
	// Construct via NewScheduler to confirm defaults don't panic and
	// RunOnce works without explicit Now.
	sched := audit.NewScheduler(svc, logger, audit.SchedulerConfig{})
	sched.RunOnce(context.Background())
}
