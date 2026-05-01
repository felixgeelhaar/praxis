package calendar_test

import (
	"context"
	"strings"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/handlers/calendar"
)

func basePayload() map[string]any {
	return map[string]any{
		"summary":     "weekly sync",
		"description": "agenda: A; B; C",
		"location":    "https://meet.example.com/x",
		"start_time":  "2026-05-05T10:00:00Z",
		"end_time":    "2026-05-05T11:00:00Z",
		"organizer":   "ops@example.com",
		"attendees":   []any{"alex@example.com", "sam@example.com"},
	}
}

func TestExecute_BuildsICS(t *testing.T) {
	h := calendar.New("praxis.test")
	out, err := h.Execute(context.Background(), basePayload())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	ics, _ := out["ics"].(string)
	uid, _ := out["uid"].(string)
	for _, want := range []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "BEGIN:VEVENT",
		"DTSTART:20260505T100000Z", "DTEND:20260505T110000Z",
		"SUMMARY:weekly sync",
		"DESCRIPTION:agenda: A\\; B\\; C",
		"ORGANIZER:mailto:ops@example.com",
		"ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:alex@example.com",
		"END:VEVENT", "END:VCALENDAR",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("ICS missing %q\n%s", want, ics)
		}
	}
	if !strings.HasSuffix(uid, "@praxis.test") {
		t.Errorf("uid suffix=%s", uid)
	}
}

func TestExecute_DeterministicUIDFromIdempotencyKey(t *testing.T) {
	h := calendar.New("x")
	p1 := basePayload()
	p1["idempotency_key"] = "k-1"
	p2 := basePayload()
	p2["idempotency_key"] = "k-1"
	o1, _ := h.Execute(context.Background(), p1)
	o2, _ := h.Execute(context.Background(), p2)
	if o1["uid"] != o2["uid"] {
		t.Errorf("UID not deterministic: %v vs %v", o1["uid"], o2["uid"])
	}
}

func TestExecute_DifferentKeysProduceDifferentUIDs(t *testing.T) {
	h := calendar.New("x")
	p1 := basePayload()
	p1["idempotency_key"] = "k-1"
	p2 := basePayload()
	p2["idempotency_key"] = "k-2"
	o1, _ := h.Execute(context.Background(), p1)
	o2, _ := h.Execute(context.Background(), p2)
	if o1["uid"] == o2["uid"] {
		t.Errorf("expected different UIDs")
	}
}

func TestExecute_RequiresFields(t *testing.T) {
	h := calendar.New("")
	_, err := h.Execute(context.Background(), map[string]any{"summary": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecute_RejectsEndBeforeStart(t *testing.T) {
	h := calendar.New("")
	p := basePayload()
	p["end_time"] = "2026-05-05T09:00:00Z" // before start
	_, err := h.Execute(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for end_time before start_time")
	}
}

func TestExecute_RejectsBadDate(t *testing.T) {
	h := calendar.New("")
	p := basePayload()
	p["start_time"] = "not-a-date"
	_, err := h.Execute(context.Background(), p)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSimulate_FlagsSimulated(t *testing.T) {
	h := calendar.New("x")
	out, err := h.Simulate(context.Background(), basePayload())
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if out["simulated"] != true {
		t.Errorf("simulated=%v want true", out["simulated"])
	}
	if _, ok := out["ics"].(string); !ok {
		t.Errorf("ics missing in simulate output")
	}
}
