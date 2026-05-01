package schema_test

import (
	"errors"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/schema"
)

var slackInputSchema = map[string]any{
	"type":     "object",
	"required": []any{"channel", "text"},
	"properties": map[string]any{
		"channel": map[string]any{"type": "string"},
		"text":    map[string]any{"type": "string", "minLength": 1},
	},
	"additionalProperties": true,
}

func TestValidatePayload_NilSchema_AlwaysPasses(t *testing.T) {
	v := schema.New()
	if err := v.ValidatePayload(map[string]any{"x": 1}, nil); err != nil {
		t.Errorf("nil schema rejected: %v", err)
	}
}

func TestValidatePayload_HappyPath(t *testing.T) {
	v := schema.New()
	if err := v.ValidatePayload(map[string]any{
		"channel": "#general", "text": "hi",
	}, slackInputSchema); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
}

func TestValidatePayload_MissingRequired(t *testing.T) {
	v := schema.New()
	err := v.ValidatePayload(map[string]any{"channel": "#general"}, slackInputSchema)
	if err == nil {
		t.Fatal("expected error for missing required text")
	}
	if !errors.Is(err, schema.ErrValidationFailed) {
		t.Errorf("not wrapped as ErrValidationFailed: %v", err)
	}
}

func TestValidatePayload_WrongType(t *testing.T) {
	v := schema.New()
	err := v.ValidatePayload(map[string]any{
		"channel": 42, "text": "hi",
	}, slackInputSchema)
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestValidatePayload_EmptyString(t *testing.T) {
	v := schema.New()
	err := v.ValidatePayload(map[string]any{
		"channel": "#x", "text": "",
	}, slackInputSchema)
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestValidate_BadSchema(t *testing.T) {
	v := schema.New()
	err := v.ValidatePayload(map[string]any{"x": 1}, map[string]any{
		"type": 123, // type must be string or array
	})
	if err == nil {
		t.Fatal("expected error for malformed schema")
	}
}

func TestValidate_AcceptsRawJSON(t *testing.T) {
	v := schema.New()
	raw := `{"type":"object","required":["x"],"properties":{"x":{"type":"integer"}}}`
	if err := v.ValidatePayload(map[string]any{"x": 1}, raw); err != nil {
		t.Errorf("raw JSON schema rejected: %v", err)
	}
	if err := v.ValidatePayload(map[string]any{"x": "no"}, raw); err == nil {
		t.Errorf("raw JSON schema should reject string")
	}
}

func TestValidate_CachesCompiledSchema(t *testing.T) {
	v := schema.New()
	for i := 0; i < 100; i++ {
		if err := v.ValidatePayload(map[string]any{
			"channel": "#x", "text": "hi",
		}, slackInputSchema); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
}

func TestValidateOutput(t *testing.T) {
	v := schema.New()
	outputSchema := map[string]any{
		"type":     "object",
		"required": []any{"ok"},
		"properties": map[string]any{
			"ok": map[string]any{"type": "boolean"},
		},
	}
	if err := v.ValidateOutput(map[string]any{"ok": true}, outputSchema); err != nil {
		t.Errorf("valid output rejected: %v", err)
	}
	if err := v.ValidateOutput(map[string]any{"ok": "yes"}, outputSchema); err == nil {
		t.Error("string ok should fail boolean check")
	}
}
