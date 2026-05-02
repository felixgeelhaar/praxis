// Package storetest is the shared backend contract suite for ports.Repos.
//
// Every storage backend (memory, sqlite, postgres) must pass RunSuite. The
// suite exercises the contracts of every repository port using only the
// interfaces — backend-specific behaviour belongs in backend-specific tests.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// RunSuite executes every contract test against the supplied repos.
//
// `factory` must produce a clean *ports.Repos for each subtest. The factory
// is responsible for migrations and cleanup — a backend that needs a fresh
// schema per test should provision one.
func RunSuite(t *testing.T, factory func(t *testing.T) *ports.Repos) {
	t.Helper()

	t.Run("CapabilityRepo", func(t *testing.T) { testCapabilityRepo(t, factory(t)) })
	t.Run("ActionRepo", func(t *testing.T) { testActionRepo(t, factory(t)) })
	t.Run("IdempotencyRepo", func(t *testing.T) { testIdempotencyRepo(t, factory(t)) })
	t.Run("AuditRepo", func(t *testing.T) { testAuditRepo(t, factory(t)) })
	t.Run("PolicyRepo", func(t *testing.T) { testPolicyRepo(t, factory(t)) })
	t.Run("OutboxRepo", func(t *testing.T) { testOutboxRepo(t, factory(t)) })
	t.Run("CapabilityHistoryRepo", func(t *testing.T) { testCapabilityHistoryRepo(t, factory(t)) })
}

func testCapabilityRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()

	c := domain.Capability{
		Name:         "send_message",
		Description:  "post to a channel",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Permissions:  []string{"send"},
		Simulatable:  true,
		Idempotent:   true,
	}

	if err := repos.Capability.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repos.Capability.Get(ctx, "send_message")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "send_message" {
		t.Errorf("Get.Name=%s want send_message", got.Name)
	}

	if _, err := repos.Capability.Get(ctx, "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Get(missing) err=%v want ErrNotFound", err)
	}

	list, err := repos.Capability.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len=%d want 1", len(list))
	}

	// upsert overwrites
	c.Description = "updated"
	if err := repos.Capability.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert overwrite: %v", err)
	}
	got, _ = repos.Capability.Get(ctx, "send_message")
	if got.Description != "updated" {
		t.Errorf("Description=%q want updated", got.Description)
	}
}

func testActionRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	a := domain.Action{
		ID:             "act-1",
		Capability:     "send_message",
		Payload:        map[string]any{"text": "hi"},
		Caller:         domain.CallerRef{Type: "user", ID: "u-1", Name: "Felix"},
		Scope:          []string{"send"},
		IdempotencyKey: "idem-1",
		Status:         domain.StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := repos.Action.Save(ctx, a); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repos.Action.Get(ctx, "act-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "act-1" || got.Capability != "send_message" {
		t.Errorf("Get returned unexpected action: %+v", got)
	}
	if got.Caller.Name != "Felix" {
		t.Errorf("CallerName=%q want Felix", got.Caller.Name)
	}

	if _, err := repos.Action.Get(ctx, "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Get(missing) err=%v want ErrNotFound", err)
	}

	if err := repos.Action.UpdateStatus(ctx, "act-1", domain.StatusValidated); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = repos.Action.Get(ctx, "act-1")
	if got.Status != domain.StatusValidated {
		t.Errorf("Status=%s want validated", got.Status)
	}

	res := domain.Result{
		ActionID:    "act-1",
		Status:      domain.StatusSucceeded,
		Output:      map[string]any{"ok": true},
		ExternalID:  "vendor-id-1",
		StartedAt:   now,
		CompletedAt: now.Add(time.Second),
	}
	if err := repos.Action.PutResult(ctx, "act-1", res); err != nil {
		t.Fatalf("PutResult: %v", err)
	}
	got, _ = repos.Action.Get(ctx, "act-1")
	if got.Status != domain.StatusSucceeded {
		t.Errorf("post-PutResult Status=%s want succeeded", got.Status)
	}
	if got.Result["ok"] != true {
		t.Errorf("Result.ok=%v want true", got.Result["ok"])
	}
}

func testIdempotencyRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()

	if _, err := repos.Idempotency.Lookup(ctx, "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Lookup(missing) err=%v want ErrNotFound", err)
	}

	res := domain.Result{
		ActionID: "act-1",
		Status:   domain.StatusSucceeded,
		Output:   map[string]any{"k": "v"},
	}
	if err := repos.Idempotency.Remember(ctx, "k1", res, 0); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	got, err := repos.Idempotency.Lookup(ctx, "k1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ActionID != "act-1" {
		t.Errorf("ActionID=%s want act-1", got.ActionID)
	}
	if got.Output["k"] != "v" {
		t.Errorf("Output.k=%v want v", got.Output["k"])
	}
}

func testAuditRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	events := []domain.AuditEvent{
		{ID: "e1", ActionID: "act-1", Kind: "received", Detail: map[string]any{"capability": "send_message", "caller_type": "user"}, CreatedAt: base},
		{ID: "e2", ActionID: "act-1", Kind: "validated", Detail: map[string]any{"capability": "send_message", "caller_type": "user"}, CreatedAt: base.Add(time.Second)},
		{ID: "e3", ActionID: "act-2", Kind: "received", Detail: map[string]any{"capability": "send_email", "caller_type": "agent"}, CreatedAt: base.Add(2 * time.Second)},
	}
	for _, e := range events {
		if err := repos.Audit.Append(ctx, e); err != nil {
			t.Fatalf("Append %s: %v", e.ID, err)
		}
	}

	listed, err := repos.Audit.ListForAction(ctx, "act-1")
	if err != nil {
		t.Fatalf("ListForAction: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListForAction len=%d want 2", len(listed))
	}
	if listed[0].ID != "e1" || listed[1].ID != "e2" {
		t.Errorf("ListForAction order=%s,%s want e1,e2", listed[0].ID, listed[1].ID)
	}

	results, err := repos.Audit.Search(ctx, ports.AuditQuery{Capability: "send_message"})
	if err != nil {
		t.Fatalf("Search capability: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Search capability len=%d want 2", len(results))
	}

	results, err = repos.Audit.Search(ctx, ports.AuditQuery{CallerType: "agent"})
	if err != nil {
		t.Fatalf("Search caller_type: %v", err)
	}
	if len(results) != 1 || results[0].ID != "e3" {
		t.Errorf("Search caller_type=%v want [e3]", results)
	}

	results, err = repos.Audit.Search(ctx, ports.AuditQuery{From: base.Add(2 * time.Second).Unix()})
	if err != nil {
		t.Fatalf("Search from: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Search from len=%d want 1", len(results))
	}
}

func testPolicyRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	rule := domain.PolicyRule{
		ID:         "r-1",
		Capability: "send_message",
		CallerType: "user",
		Scope:      []string{"send"},
		Decision:   "allow",
		Reason:     "phase 1",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := repos.Policy.UpsertRule(ctx, rule); err != nil {
		t.Fatalf("UpsertRule: %v", err)
	}

	rules, err := repos.Policy.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != "r-1" {
		t.Errorf("ListRules=%v want [r-1]", rules)
	}

	if err := repos.Policy.DeleteRule(ctx, "r-1"); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	rules, _ = repos.Policy.ListRules(ctx)
	if len(rules) != 0 {
		t.Errorf("after delete len=%d want 0", len(rules))
	}
}

func testOutboxRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	env := ports.OutboxEnvelope{
		ID:          "o-1",
		ActionID:    "act-1",
		Event:       domain.MnemosEvent{Type: "praxis.action_completed", ActionID: "act-1", Capability: "send_message", Status: "succeeded", Timestamp: now.Unix()},
		NextAttempt: now,
		CreatedAt:   now,
	}
	if err := repos.Outbox.Enqueue(ctx, env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	batch, err := repos.Outbox.NextBatch(ctx, 10, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if len(batch) != 1 || batch[0].ID != "o-1" {
		t.Fatalf("NextBatch=%v want [o-1]", batch)
	}

	if err := repos.Outbox.BumpAttempt(ctx, "o-1", now.Add(time.Minute), "boom"); err != nil {
		t.Fatalf("BumpAttempt: %v", err)
	}
	batch, _ = repos.Outbox.NextBatch(ctx, 10, now.Add(time.Second))
	if len(batch) != 0 {
		t.Errorf("after BumpAttempt to future len=%d want 0", len(batch))
	}
	batch, _ = repos.Outbox.NextBatch(ctx, 10, now.Add(2*time.Minute))
	if len(batch) != 1 {
		t.Errorf("after time advance len=%d want 1", len(batch))
	}
	if batch[0].Attempts != 1 {
		t.Errorf("Attempts=%d want 1", batch[0].Attempts)
	}
	if batch[0].LastError != "boom" {
		t.Errorf("LastError=%q want boom", batch[0].LastError)
	}

	if err := repos.Outbox.MarkDelivered(ctx, "o-1", now.Add(time.Hour)); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	batch, _ = repos.Outbox.NextBatch(ctx, 10, now.Add(time.Hour))
	if len(batch) != 0 {
		t.Errorf("after MarkDelivered len=%d want 0", len(batch))
	}
}

func testCapabilityHistoryRepo(t *testing.T, repos *ports.Repos) {
	t.Helper()
	if repos.CapabilityHistory == nil {
		t.Skip("CapabilityHistory repo not wired")
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	entry := domain.CapabilityHistoryEntry{
		ID:                "h-1",
		CapabilityName:    "send_message",
		RecordedAt:        now,
		PrevInputVersion:  "1",
		PrevOutputVersion: "1",
		NextInputVersion:  "2",
		NextOutputVersion: "1",
		Issues: []domain.CapabilityHistoryIssue{
			{Code: "field_removed", Field: "to", Message: "removed required field"},
		},
	}
	if err := repos.CapabilityHistory.Append(ctx, entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entry2 := entry
	entry2.ID = "h-2"
	entry2.RecordedAt = now.Add(time.Second)
	entry2.NextInputVersion = "3"
	entry2.Issues = []domain.CapabilityHistoryIssue{
		{Code: "type_changed", Field: "body", Message: "string -> int"},
	}
	if err := repos.CapabilityHistory.Append(ctx, entry2); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	got, err := repos.CapabilityHistory.ListForCapability(ctx, "send_message")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].ID != "h-1" || got[1].ID != "h-2" {
		t.Errorf("order=%s,%s want h-1,h-2", got[0].ID, got[1].ID)
	}
	if len(got[0].Issues) != 1 || got[0].Issues[0].Code != "field_removed" {
		t.Errorf("issues[0]=%+v", got[0].Issues)
	}
	if got[1].NextInputVersion != "3" {
		t.Errorf("NextInputVersion=%q want 3", got[1].NextInputVersion)
	}

	other, err := repos.CapabilityHistory.ListForCapability(ctx, "no_such_cap")
	if err != nil {
		t.Fatalf("List other: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("other len=%d want 0", len(other))
	}
}
