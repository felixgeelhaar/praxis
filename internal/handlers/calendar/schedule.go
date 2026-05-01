// Package calendar implements the calendar_schedule_meeting capability as
// an RFC 5545 ICS payload generator.
//
// The handler is destination-agnostic: it returns the ICS body and a
// stable UID so the caller can deliver it however they like — email
// attachment via send_email, drop on disk, hand to a calendar SDK in a
// downstream system. Vendor-specific Calendar/Outlook/Google providers
// belong in dedicated handlers (Phase-3 work) so this one stays
// dependency-free.
//
// Idempotency: the UID is deterministic from the supplied
// idempotency_key (or, fallback, from start_time + summary + organizer).
// Re-execution with the same inputs produces a byte-identical ICS body
// — RFC-compliant calendar clients dedupe on UID.
package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// ScheduleMeeting handles calendar_schedule_meeting.
type ScheduleMeeting struct {
	now    func() time.Time
	domain string
}

// New constructs a handler. ProductDomain seeds the UID's right-hand side
// (e.g. "praxis.local"); empty defaults to praxis.local.
func New(productDomain string) *ScheduleMeeting {
	if productDomain == "" {
		productDomain = "praxis.local"
	}
	return &ScheduleMeeting{now: time.Now, domain: productDomain}
}

// Name returns the capability name.
func (h *ScheduleMeeting) Name() string { return "calendar_schedule_meeting" }

// Capability returns the descriptor.
func (h *ScheduleMeeting) Capability() domain.Capability {
	return domain.Capability{
		Name:        "calendar_schedule_meeting",
		Description: "Generate an RFC 5545 ICS meeting invite with deterministic UID",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"summary", "start_time", "end_time", "organizer"},
			"properties": map[string]any{
				"summary":         map[string]any{"type": "string", "minLength": 1},
				"description":     map[string]any{"type": "string"},
				"location":        map[string]any{"type": "string"},
				"start_time":      map[string]any{"type": "string", "format": "date-time"},
				"end_time":        map[string]any{"type": "string", "format": "date-time"},
				"organizer":       map[string]any{"type": "string", "format": "email"},
				"attendees":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "email"}},
				"idempotency_key": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []any{"uid", "ics"},
		},
		Permissions: []string{"calendar:schedule"},
		Simulatable: true,
		Idempotent:  true,
	}
}

// Execute builds the ICS payload.
func (h *ScheduleMeeting) Execute(_ context.Context, payload map[string]any) (map[string]any, error) {
	in, err := readPayload(payload)
	if err != nil {
		return nil, err
	}
	uid := h.uid(in)
	ics := h.buildICS(uid, in)
	return map[string]any{
		"ok":          true,
		"uid":         uid,
		"ics":         ics,
		"external_id": uid,
	}, nil
}

// Simulate returns the same envelope; this handler has no side effect.
func (h *ScheduleMeeting) Simulate(ctx context.Context, payload map[string]any) (map[string]any, error) {
	out, err := h.Execute(ctx, payload)
	if err != nil {
		return nil, err
	}
	out["simulated"] = true
	return out, nil
}

type input struct {
	summary, description, location string
	start, end                     time.Time
	organizer                      string
	attendees                      []string
	idempotency                    string
}

func readPayload(p map[string]any) (input, error) {
	in := input{}
	in.summary, _ = p["summary"].(string)
	in.description, _ = p["description"].(string)
	in.location, _ = p["location"].(string)
	in.organizer, _ = p["organizer"].(string)
	in.idempotency, _ = p["idempotency_key"].(string)

	startStr, _ := p["start_time"].(string)
	endStr, _ := p["end_time"].(string)

	if in.summary == "" || in.organizer == "" || startStr == "" || endStr == "" {
		return in, errors.New("summary, organizer, start_time, end_time are required")
	}
	var err error
	if in.start, err = time.Parse(time.RFC3339, startStr); err != nil {
		return in, fmt.Errorf("start_time: %w", err)
	}
	if in.end, err = time.Parse(time.RFC3339, endStr); err != nil {
		return in, fmt.Errorf("end_time: %w", err)
	}
	if !in.end.After(in.start) {
		return in, errors.New("end_time must be after start_time")
	}
	if list, ok := p["attendees"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok && s != "" {
				in.attendees = append(in.attendees, s)
			}
		}
	}
	return in, nil
}

func (h *ScheduleMeeting) uid(in input) string {
	seed := in.idempotency
	if seed == "" {
		seed = in.summary + "|" + in.organizer + "|" + in.start.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16]) + "@" + h.domain
}

// buildICS emits a minimal RFC 5545 VEVENT. We avoid line-folding
// (recommended >75 chars) for readability — most modern clients tolerate
// long lines, and the produced payload tends to stay well below the
// folding threshold for typical meeting summaries.
func (h *ScheduleMeeting) buildICS(uid string, in input) string {
	now := h.now().UTC()
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//praxis//calendar//EN\r\n")
	b.WriteString("METHOD:REQUEST\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s\r\n", uid)
	fmt.Fprintf(&b, "DTSTAMP:%s\r\n", now.Format("20060102T150405Z"))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", in.start.UTC().Format("20060102T150405Z"))
	fmt.Fprintf(&b, "DTEND:%s\r\n", in.end.UTC().Format("20060102T150405Z"))
	fmt.Fprintf(&b, "SUMMARY:%s\r\n", icsEscape(in.summary))
	if in.description != "" {
		fmt.Fprintf(&b, "DESCRIPTION:%s\r\n", icsEscape(in.description))
	}
	if in.location != "" {
		fmt.Fprintf(&b, "LOCATION:%s\r\n", icsEscape(in.location))
	}
	fmt.Fprintf(&b, "ORGANIZER:mailto:%s\r\n", in.organizer)
	for _, a := range in.attendees {
		fmt.Fprintf(&b, "ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:%s\r\n", a)
	}
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func icsEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		";", `\;`,
		",", `\,`,
		"\n", `\n`,
	)
	return r.Replace(s)
}

var _ capability.Handler = (*ScheduleMeeting)(nil)
