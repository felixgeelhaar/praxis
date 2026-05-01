package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/audit"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
	"github.com/felixgeelhaar/praxis/internal/store/memory"
)

func seed(t *testing.T) ports.AuditRepo {
	t.Helper()
	r := memory.New().Audit
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	events := []domain.AuditEvent{
		{ID: "e2", ActionID: "a-1", Kind: "executed",
			Detail:    map[string]any{"capability": "send_email", "caller_type": "user", "caller_id": "u-1"},
			CreatedAt: base.Add(time.Second)},
		{ID: "e1", ActionID: "a-1", Kind: "received",
			Detail: map[string]any{"capability": "send_email", "caller_type": "user", "caller_id": "u-1",
				"password": "hunter2", "to": "alex@example.com"},
			CreatedAt: base},
	}
	for _, e := range events {
		if err := r.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return r
}

func TestExporter_JSON_OrderedByCreatedAtThenID(t *testing.T) {
	repo := seed(t)
	var buf bytes.Buffer
	if err := audit.NewExporter(repo, nil).Export(context.Background(), &buf, audit.FormatJSON, ports.AuditQuery{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	var out struct {
		Events []domain.AuditEvent `json:"events"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Events) != 2 {
		t.Fatalf("len=%d want 2", len(out.Events))
	}
	if out.Events[0].ID != "e1" || out.Events[1].ID != "e2" {
		t.Errorf("order=%s,%s want e1,e2", out.Events[0].ID, out.Events[1].ID)
	}
}

func TestExporter_CSV_ShapeAndHeader(t *testing.T) {
	repo := seed(t)
	var buf bytes.Buffer
	if err := audit.NewExporter(repo, nil).Export(context.Background(), &buf, audit.FormatCSV, ports.AuditQuery{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	body := buf.String()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("rows=%d want 3 (header + 2)", len(lines))
	}
	if !strings.HasPrefix(lines[0], "id,action_id,kind,capability,caller_type,caller_id,created_at,detail") {
		t.Errorf("header=%s", lines[0])
	}
}

func TestExporter_PIIRedaction(t *testing.T) {
	repo := seed(t)
	var buf bytes.Buffer
	if err := audit.NewExporter(repo, audit.NewDefaultRedactor()).
		Export(context.Background(), &buf, audit.FormatJSON, ports.AuditQuery{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, "hunter2") {
		t.Errorf("password leaked: %s", body)
	}
	if strings.Contains(body, "alex@example.com") {
		t.Errorf("email leaked: %s", body)
	}
	if !strings.Contains(body, "<redacted>") {
		t.Errorf("redaction not applied")
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in   string
		want audit.Format
		err  bool
	}{
		{"", audit.FormatJSON, false},
		{"json", audit.FormatJSON, false},
		{"JSON", audit.FormatJSON, false},
		{"csv", audit.FormatCSV, false},
		{"yaml", "", true},
	}
	for _, tt := range tests {
		got, err := audit.ParseFormat(tt.in)
		if (err != nil) != tt.err {
			t.Errorf("%q: err=%v want err=%v", tt.in, err, tt.err)
		}
		if !tt.err && got != tt.want {
			t.Errorf("%q: got %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestExporter_UnknownFormat(t *testing.T) {
	repo := seed(t)
	if err := audit.NewExporter(repo, nil).Export(context.Background(), io.Discard, "yaml", ports.AuditQuery{}); err == nil {
		t.Fatal("expected error for unknown format")
	}
}
