package schema_test

import (
	"testing"

	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/schema"
)

func cap(input, output any) domain.Capability {
	return domain.Capability{Name: "c", InputSchema: input, OutputSchema: output}
}

func TestCheckCompat_NoChangesReturnsEmpty(t *testing.T) {
	s := map[string]any{"type": "object", "required": []any{"to"}}
	issues := schema.CheckCompat(cap(s, nil), cap(s, nil))
	if len(issues) != 0 {
		t.Errorf("issues=%+v want empty", issues)
	}
}

func TestCheckCompat_RequiredFieldRemoved(t *testing.T) {
	prev := map[string]any{"required": []any{"to", "body"}}
	next := map[string]any{"required": []any{"to"}}
	issues := schema.CheckCompat(cap(prev, nil), cap(next, nil))
	if len(issues) != 1 {
		t.Fatalf("issues=%+v want 1", issues)
	}
	if issues[0].Code != schema.CodeRequiredFieldRemoved {
		t.Errorf("code=%s", issues[0].Code)
	}
}

func TestCheckCompat_RequiredFieldAdded(t *testing.T) {
	prev := map[string]any{"required": []any{"to"}}
	next := map[string]any{"required": []any{"to", "subject"}}
	issues := schema.CheckCompat(cap(prev, nil), cap(next, nil))
	if len(issues) != 1 || issues[0].Code != schema.CodeRequiredFieldAdded {
		t.Errorf("issues=%+v want CodeRequiredFieldAdded", issues)
	}
}

func TestCheckCompat_TypeChanged(t *testing.T) {
	prev := map[string]any{"properties": map[string]any{
		"count": map[string]any{"type": "integer"},
	}}
	next := map[string]any{"properties": map[string]any{
		"count": map[string]any{"type": "string"},
	}}
	issues := schema.CheckCompat(cap(prev, nil), cap(next, nil))
	if len(issues) != 1 || issues[0].Code != schema.CodeTypeChanged {
		t.Errorf("issues=%+v want CodeTypeChanged", issues)
	}
}

func TestCheckCompat_EnumNarrowed(t *testing.T) {
	prev := map[string]any{"properties": map[string]any{
		"color": map[string]any{"enum": []any{"red", "green", "blue"}},
	}}
	next := map[string]any{"properties": map[string]any{
		"color": map[string]any{"enum": []any{"red", "green"}},
	}}
	issues := schema.CheckCompat(cap(prev, nil), cap(next, nil))
	if len(issues) != 1 || issues[0].Code != schema.CodeEnumNarrowed {
		t.Errorf("issues=%+v want CodeEnumNarrowed", issues)
	}
}

func TestCheckCompat_EnumWidenedIsFine(t *testing.T) {
	prev := map[string]any{"properties": map[string]any{
		"color": map[string]any{"enum": []any{"red"}},
	}}
	next := map[string]any{"properties": map[string]any{
		"color": map[string]any{"enum": []any{"red", "green", "blue"}},
	}}
	if issues := schema.CheckCompat(cap(prev, nil), cap(next, nil)); len(issues) != 0 {
		t.Errorf("widening should not flag: %+v", issues)
	}
}

func TestCheckCompat_OutputSchemaAlsoChecked(t *testing.T) {
	prev := map[string]any{"required": []any{"id"}}
	next := map[string]any{"required": []any{}}
	issues := schema.CheckCompat(cap(nil, prev), cap(nil, next))
	if len(issues) != 1 || issues[0].Field != "output.id" {
		t.Errorf("issues=%+v want output.id removed", issues)
	}
}

func TestCheckCompat_NonJSONSchemaSkipsSilently(t *testing.T) {
	// Non-map schemas (e.g. Go-struct registration) skip the check.
	if issues := schema.CheckCompat(cap("string-input", nil), cap(42, nil)); len(issues) != 0 {
		t.Errorf("non-JSON-Schema should skip: %+v", issues)
	}
}

func TestCheckCompat_MultipleIssuesSorted(t *testing.T) {
	prev := map[string]any{
		"required": []any{"a", "b"},
		"properties": map[string]any{
			"x": map[string]any{"type": "integer"},
		},
	}
	next := map[string]any{
		"required": []any{"c"},
		"properties": map[string]any{
			"x": map[string]any{"type": "string"},
		},
	}
	issues := schema.CheckCompat(cap(prev, nil), cap(next, nil))
	if len(issues) < 3 {
		t.Fatalf("expected ≥3 issues, got %+v", issues)
	}
	// Stable order: by field, then code.
	for i := 1; i < len(issues); i++ {
		if issues[i].Field < issues[i-1].Field {
			t.Errorf("issues not sorted: %+v", issues)
			break
		}
	}
}
