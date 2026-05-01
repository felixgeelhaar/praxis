package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func mustAppend(t *testing.T, repo ports.AuditRepo, e domain.AuditEvent) {
	t.Helper()
	if err := repo.Append(context.Background(), e); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestSearchForCaller_ScopesQueryToCallerOrg(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e1", ActionID: "a1", Kind: "received", OrgID: "org-a", CreatedAt: time.Unix(100, 0)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e2", ActionID: "a2", Kind: "received", OrgID: "org-b", CreatedAt: time.Unix(101, 0)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e3", ActionID: "a3", Kind: "received", OrgID: "", CreatedAt: time.Unix(102, 0)})

	svc := audit.New(repos.Audit)
	got, err := svc.SearchForCaller(context.Background(), ports.AuditQuery{}, domain.CallerRef{OrgID: "org-a"})
	if err != nil {
		t.Fatalf("SearchForCaller: %v", err)
	}
	if len(got) != 1 || got[0].OrgID != "org-a" {
		t.Errorf("org-a got=%+v want only e1", got)
	}
}

func TestSearchForCaller_RejectsCrossTenant(t *testing.T) {
	svc := audit.New(memory.New().Audit)
	_, err := svc.SearchForCaller(context.Background(),
		ports.AuditQuery{OrgID: "org-b"},
		domain.CallerRef{OrgID: "org-a"})
	if !errors.Is(err, audit.ErrCrossTenantAccess) {
		t.Errorf("err=%v want ErrCrossTenantAccess", err)
	}
}

func TestSearchForCaller_AnonymousSeesAll(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e1", OrgID: "org-a", CreatedAt: time.Unix(1, 0)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e2", OrgID: "org-b", CreatedAt: time.Unix(2, 0)})

	svc := audit.New(repos.Audit)
	got, err := svc.SearchForCaller(context.Background(), ports.AuditQuery{}, domain.CallerRef{})
	if err != nil {
		t.Fatalf("SearchForCaller: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("anon got=%d want 2", len(got))
	}
}

func TestListForActionByCaller_DeniesCrossTenant(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e1", ActionID: "a1", OrgID: "org-a", CreatedAt: time.Unix(1, 0)})

	svc := audit.New(repos.Audit)
	_, err := svc.ListForActionByCaller(context.Background(), "a1", domain.CallerRef{OrgID: "org-b"})
	if !errors.Is(err, audit.ErrCrossTenantAccess) {
		t.Errorf("err=%v want ErrCrossTenantAccess", err)
	}
}

func TestListForActionByCaller_AllowsSameTenant(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "e1", ActionID: "a1", OrgID: "org-a", CreatedAt: time.Unix(1, 0)})

	svc := audit.New(repos.Audit)
	got, err := svc.ListForActionByCaller(context.Background(), "a1", domain.CallerRef{OrgID: "org-a"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got=%d want 1", len(got))
	}
}

func TestPurge_DeletesScopedToOrg(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "old-a", OrgID: "org-a", CreatedAt: time.Unix(100, 0)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "old-b", OrgID: "org-b", CreatedAt: time.Unix(101, 0)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "new-a", OrgID: "org-a", CreatedAt: time.Unix(1000, 0)})

	svc := audit.New(repos.Audit)
	deleted, err := svc.Purge(context.Background(), "org-a", time.Unix(500, 0))
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d want 1", deleted)
	}

	// org-b's old row must still be there.
	all, _ := repos.Audit.Search(context.Background(), ports.AuditQuery{})
	ids := map[string]bool{}
	for _, e := range all {
		ids[e.ID] = true
	}
	if !ids["old-b"] || !ids["new-a"] || ids["old-a"] {
		t.Errorf("after purge ids=%v", ids)
	}
}

func TestPurgeExpired_AppliesPerOrgWindow(t *testing.T) {
	repos := memory.New()
	now := time.Unix(10_000, 0)

	// org-a: keep events newer than 100s.
	// org-b: keep events newer than 1000s.
	// no-org defaults: keep newer than 50s.
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "a-old", OrgID: "org-a", CreatedAt: now.Add(-200 * time.Second)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "a-new", OrgID: "org-a", CreatedAt: now.Add(-50 * time.Second)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "b-old", OrgID: "org-b", CreatedAt: now.Add(-1500 * time.Second)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "b-new", OrgID: "org-b", CreatedAt: now.Add(-500 * time.Second)})
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "anon-old", OrgID: "", CreatedAt: now.Add(-100 * time.Second)})

	svc := audit.New(repos.Audit).WithRetention(audit.RetentionPolicy{
		"":      50 * time.Second,
		"org-a": 100 * time.Second,
		"org-b": 1000 * time.Second,
	})

	deleted, err := svc.PurgeExpired(context.Background(), now)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	// Each org has exactly one expired row.
	if deleted["org-a"] != 1 || deleted["org-b"] != 1 || deleted[""] != 1 {
		t.Errorf("deleted=%v want each=1", deleted)
	}

	all, _ := repos.Audit.Search(context.Background(), ports.AuditQuery{})
	ids := map[string]bool{}
	for _, e := range all {
		ids[e.ID] = true
	}
	if !ids["a-new"] || !ids["b-new"] || ids["a-old"] || ids["b-old"] || ids["anon-old"] {
		t.Errorf("after purge ids=%v", ids)
	}
}

func TestPurgeExpired_ZeroDurationOptsOut(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "ancient", OrgID: "org-keep", CreatedAt: time.Unix(1, 0)})

	svc := audit.New(repos.Audit).WithRetention(audit.RetentionPolicy{"org-keep": 0})
	deleted, err := svc.PurgeExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if deleted["org-keep"] != 0 {
		t.Errorf("deleted=%d want 0 (retain forever)", deleted["org-keep"])
	}
}

func TestPurgeExpired_NoPolicyIsNoOp(t *testing.T) {
	repos := memory.New()
	mustAppend(t, repos.Audit, domain.AuditEvent{ID: "x", OrgID: "org-a", CreatedAt: time.Unix(1, 0)})
	svc := audit.New(repos.Audit)
	deleted, err := svc.PurgeExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted=%v want empty (no policy)", deleted)
	}
}
