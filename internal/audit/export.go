package audit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/ports"
)

// Format selects the export wire format.
type Format string

const (
	FormatJSON Format = "json"
	FormatCSV  Format = "csv"
)

// Exporter renders audit events for SOC 2 / HIPAA / GDPR review.
//
// Output is stable: events are ordered by created_at ASC and then by event
// ID so a re-run on the same data produces a byte-identical dump (modulo
// JSON's map key order, which encoding/json does emit deterministically).
type Exporter struct {
	repo     ports.AuditRepo
	redactor *Redactor // optional PII redaction; nil = pass-through
}

// NewExporter constructs an Exporter. A nil redactor leaves audit detail
// untouched.
func NewExporter(repo ports.AuditRepo, redactor *Redactor) *Exporter {
	return &Exporter{repo: repo, redactor: redactor}
}

// Export writes events matching q to w in the supplied format.
func (e *Exporter) Export(ctx context.Context, w io.Writer, format Format, q ports.AuditQuery) error {
	events, err := e.repo.Search(ctx, q)
	if err != nil {
		return fmt.Errorf("audit export: search: %w", err)
	}
	if e.redactor != nil {
		for i := range events {
			events[i].Detail = e.redactor.Redact(events[i].Detail)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		}
		return events[i].ID < events[j].ID
	})
	switch format {
	case FormatJSON, "":
		return writeJSON(w, events)
	case FormatCSV:
		return writeCSV(w, events)
	default:
		return fmt.Errorf("audit export: unknown format %q", format)
	}
}

func writeJSON(w io.Writer, events []domain.AuditEvent) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep <redacted> literal so reviewers see the marker
	return enc.Encode(struct {
		ExportedAt time.Time           `json:"exported_at"`
		Events     []domain.AuditEvent `json:"events"`
	}{
		ExportedAt: time.Now().UTC(),
		Events:     events,
	})
}

func writeCSV(w io.Writer, events []domain.AuditEvent) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"id", "action_id", "kind", "capability", "caller_type", "caller_id", "created_at", "detail"}); err != nil {
		return err
	}
	for _, e := range events {
		cap, _ := e.Detail["capability"].(string)
		callerType, _ := e.Detail["caller_type"].(string)
		callerID, _ := e.Detail["caller_id"].(string)
		var detail string
		if e.Detail != nil {
			b, _ := json.Marshal(e.Detail)
			detail = string(b)
		}
		row := []string{
			e.ID, e.ActionID, e.Kind, cap, callerType, callerID,
			e.CreatedAt.UTC().Format(time.RFC3339Nano),
			detail,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	if err := cw.Error(); err != nil {
		return err
	}
	return nil
}

// ParseFormat normalises a user-supplied format string.
func ParseFormat(s string) (Format, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "json":
		return FormatJSON, nil
	case "csv":
		return FormatCSV, nil
	default:
		return "", fmt.Errorf("unsupported format %q (json|csv)", s)
	}
}
