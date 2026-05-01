package audit_test

import (
	"testing"

	"github.com/felixgeelhaar/praxis/internal/audit"
)

func TestRedactor_NilDetailIsNoOp(t *testing.T) {
	r := audit.NewDefaultRedactor()
	if got := r.Redact(nil); got != nil {
		t.Errorf("nil detail should remain nil, got %v", got)
	}
}

func TestRedactor_ScrubsSensitiveKeys(t *testing.T) {
	r := audit.NewDefaultRedactor()
	got := r.Redact(map[string]any{
		"password":      "hunter2",
		"AUTHORIZATION": "Bearer xyz", // case-insensitive
		"username":      "alex",
	})
	if got["password"] != "<redacted>" {
		t.Errorf("password=%v", got["password"])
	}
	if got["AUTHORIZATION"] != "<redacted>" {
		t.Errorf("AUTHORIZATION=%v", got["AUTHORIZATION"])
	}
	if got["username"] != "alex" {
		t.Errorf("benign field redacted: username=%v", got["username"])
	}
}

func TestRedactor_ScrubsEmailPattern(t *testing.T) {
	r := audit.NewDefaultRedactor()
	got := r.Redact(map[string]any{"to": "alex@example.com", "note": "ping me at sam@x.io please"})
	if got["to"] != "<redacted>" {
		t.Errorf("to=%v", got["to"])
	}
	if note := got["note"].(string); !contains(note, "<redacted>") {
		t.Errorf("note=%q want pattern redaction", note)
	}
}

func TestRedactor_RecursiveMapsAndArrays(t *testing.T) {
	r := audit.NewDefaultRedactor()
	got := r.Redact(map[string]any{
		"caller": map[string]any{"token": "tk-secret"},
		"recipients": []any{
			map[string]any{"to": "alex@example.com"},
		},
	})
	caller := got["caller"].(map[string]any)
	if caller["token"] != "<redacted>" {
		t.Errorf("nested token=%v", caller["token"])
	}
	rec := got["recipients"].([]any)[0].(map[string]any)
	if rec["to"] != "<redacted>" {
		t.Errorf("array element email=%v", rec["to"])
	}
}

func TestRedactor_OriginalUnchanged(t *testing.T) {
	r := audit.NewDefaultRedactor()
	original := map[string]any{"password": "hunter2"}
	_ = r.Redact(original)
	if original["password"] != "hunter2" {
		t.Errorf("redactor mutated input: %v", original["password"])
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
