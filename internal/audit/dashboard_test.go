package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func seedDashboard(t *testing.T) ports.AuditRepo {
	t.Helper()
	r := memory.New().Audit
	now := time.Now()
	add := func(id, action, kind, cap, callerType string, extra map[string]any) {
		detail := map[string]any{"capability": cap, "caller_type": callerType, "caller_id": "u-1"}
		for k, v := range extra {
			detail[k] = v
		}
		_ = r.Append(context.Background(), domain.AuditEvent{
			ID: id, ActionID: action, Kind: kind, Detail: detail, CreatedAt: now,
		})
	}
	// Action 1: send_email succeeded, user
	add("e1a", "a1", audit.KindReceived, "send_email", "user", nil)
	add("e1b", "a1", audit.KindSucceeded, "send_email", "user", map[string]any{"external_id": "x"})
	// Action 2: send_email failed, user
	add("e2a", "a2", audit.KindReceived, "send_email", "user", nil)
	add("e2b", "a2", audit.KindFailed, "send_email", "user", map[string]any{"code": "vendor_500"})
	// Action 3: send_message rejected by policy, agent
	add("e3a", "a3", audit.KindReceived, "send_message", "agent", nil)
	add("e3b", "a3", audit.KindRejected, "send_message", "agent", map[string]any{"code": "policy_denied"})
	// Action 4: send_email throttled, user
	add("e4a", "a4", audit.KindReceived, "send_email", "user", nil)
	add("e4b", "a4", audit.KindThrottled, "send_email", "user", nil)
	add("e4c", "a4", audit.KindRejected, "send_email", "user", map[string]any{"code": "rate_limited"})
	// Action 5: dry_run simulated
	add("e5a", "a5", audit.KindReceived, "send_email", "user", nil)
	add("e5b", "a5", audit.KindSimulated, "send_email", "user", nil)
	return r
}

func TestBuildDashboard_Aggregates(t *testing.T) {
	repo := seedDashboard(t)
	d, err := audit.BuildDashboard(context.Background(), repo, ports.AuditQuery{})
	if err != nil {
		t.Fatalf("BuildDashboard: %v", err)
	}
	if d.TotalActions != 5 {
		t.Errorf("TotalActions=%d want 5", d.TotalActions)
	}
	if d.Succeeded != 1 {
		t.Errorf("Succeeded=%d want 1", d.Succeeded)
	}
	if d.Failed != 1 {
		t.Errorf("Failed=%d want 1", d.Failed)
	}
	if d.Simulated != 1 {
		t.Errorf("Simulated=%d want 1", d.Simulated)
	}
	if d.PolicyDenials != 1 {
		t.Errorf("PolicyDenials=%d want 1", d.PolicyDenials)
	}
	if d.Throttled != 1 {
		t.Errorf("Throttled=%d want 1", d.Throttled)
	}

	// send_email totals: 4 actions (a1, a2, a4, a5)
	var emailUsage audit.CapabilityUsage
	for _, c := range d.ByCapability {
		if c.Capability == "send_email" {
			emailUsage = c
		}
	}
	if emailUsage.Total != 4 {
		t.Errorf("send_email total=%d want 4", emailUsage.Total)
	}
	if rate := d.ErrorRatePerCap["send_email"]; rate != 0.25 {
		t.Errorf("send_email error rate=%f want 0.25", rate)
	}
}

func TestBuildDashboard_FilteredByCapability(t *testing.T) {
	repo := seedDashboard(t)
	d, err := audit.BuildDashboard(context.Background(), repo, ports.AuditQuery{Capability: "send_message"})
	if err != nil {
		t.Fatalf("BuildDashboard: %v", err)
	}
	if d.TotalActions != 1 {
		t.Errorf("TotalActions=%d want 1 (only send_message a3)", d.TotalActions)
	}
	if d.PolicyDenials != 1 {
		t.Errorf("PolicyDenials=%d want 1", d.PolicyDenials)
	}
}

func TestBuildDashboard_OrderedByTotal(t *testing.T) {
	repo := seedDashboard(t)
	d, _ := audit.BuildDashboard(context.Background(), repo, ports.AuditQuery{})
	if len(d.ByCapability) < 2 {
		t.Fatal("expected ≥2 capabilities")
	}
	if d.ByCapability[0].Total < d.ByCapability[1].Total {
		t.Errorf("ByCapability not sorted by Total desc: %+v", d.ByCapability)
	}
}
