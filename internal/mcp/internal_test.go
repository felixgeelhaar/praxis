package mcp

import (
	"testing"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

func TestActionFromInput_DefaultsAndOverrides(t *testing.T) {
	in := ExecuteInput{
		Capability:     "send_message",
		Payload:        map[string]any{"text": "hi"},
		IdempotencyKey: "",
		Mode:           "",
		CallerType:     "",
		CallerID:       "u-1",
		OrgID:          "org-x",
		TeamID:         "eng",
		Scope:          []string{"write"},
	}
	a := actionFromInput(in, func() string { return "act-42" })

	if a.ID != "act-42" {
		t.Errorf("ID=%s", a.ID)
	}
	if a.Capability != "send_message" {
		t.Errorf("Capability=%s", a.Capability)
	}
	if a.IdempotencyKey != "act-42" {
		t.Errorf("IdempotencyKey defaults to ID, got %s", a.IdempotencyKey)
	}
	if a.Mode != domain.ModeSync {
		t.Errorf("Mode defaults to sync, got %s", a.Mode)
	}
	if a.Caller.Type != "mcp" {
		t.Errorf("Caller.Type defaults to mcp, got %s", a.Caller.Type)
	}
	if a.Caller.OrgID != "org-x" || a.Caller.TeamID != "eng" {
		t.Errorf("Caller scope=%+v", a.Caller)
	}
	if a.Status != domain.StatusPending {
		t.Errorf("Status=%s", a.Status)
	}
}

func TestActionFromInput_RespectsExplicitMode(t *testing.T) {
	a := actionFromInput(ExecuteInput{
		Capability: "send",
		Mode:       "async",
	}, func() string { return "x" })
	if a.Mode != domain.ModeAsync {
		t.Errorf("Mode=%s want async", a.Mode)
	}
}

func TestActionFromInput_RespectsExplicitIdempotencyKey(t *testing.T) {
	a := actionFromInput(ExecuteInput{
		Capability:     "send",
		IdempotencyKey: "client-supplied",
	}, func() string { return "act" })
	if a.IdempotencyKey != "client-supplied" {
		t.Errorf("IdempotencyKey=%s want client-supplied", a.IdempotencyKey)
	}
}

func TestActionFromInput_RespectsExplicitCallerType(t *testing.T) {
	a := actionFromInput(ExecuteInput{
		Capability: "send",
		CallerType: "api",
	}, func() string { return "x" })
	if a.Caller.Type != "api" {
		t.Errorf("Caller.Type=%s want api", a.Caller.Type)
	}
}

func TestActionFromCapInput_NamespacedCap(t *testing.T) {
	a := actionFromCapInput("send_message", CapInput{
		Payload: map[string]any{"text": "hi"},
		OrgID:   "org-y",
	}, func() string { return "act-7" })
	if a.Capability != "send_message" {
		t.Errorf("Capability=%s", a.Capability)
	}
	if a.Caller.OrgID != "org-y" {
		t.Errorf("OrgID=%s", a.Caller.OrgID)
	}
	if a.Caller.Type != "mcp" {
		t.Errorf("Caller.Type=%s", a.Caller.Type)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "", "third"}, "third"},
		{[]string{"first", "", "third"}, "first"},
		{[]string{"", "second", "third"}, "second"},
		{[]string{}, ""},
		{[]string{"", ""}, ""},
	}
	for i, c := range cases {
		if got := firstNonEmpty(c.in...); got != c.want {
			t.Errorf("case %d: firstNonEmpty(%v)=%q want %q", i, c.in, got, c.want)
		}
	}
}

func TestBuildCapDescription_FullDescriptor(t *testing.T) {
	c := domain.Capability{
		Name:         "echo",
		Description:  "Echoes input.",
		InputSchema:  map[string]any{"type": "object"},
		Permissions:  []string{"echo:write"},
		Simulatable:  true,
		Idempotent:   true,
	}
	desc := buildCapDescription(c)
	if !contains(desc, "Echoes input.") {
		t.Error("description missing original description")
	}
	if !contains(desc, "Input Payload Schema") {
		t.Error("description missing schema header")
	}
	if !contains(desc, "Permissions required") {
		t.Error("description missing permissions")
	}
	if !contains(desc, "Supports dry_run") {
		t.Error("description missing simulatable note")
	}
	if !contains(desc, "Idempotent at destination") {
		t.Error("description missing idempotent note")
	}
}

func TestBuildCapDescription_EmptyDescriptionFallsBackToName(t *testing.T) {
	c := domain.Capability{Name: "noop"}
	desc := buildCapDescription(c)
	if !contains(desc, "noop") {
		t.Errorf("empty description should fall back to name; got %q", desc)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
