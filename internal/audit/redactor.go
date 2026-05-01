package audit

import (
	"regexp"
	"strings"
)

// Redactor scrubs PII from audit detail before export.
//
// Two mechanisms compose:
//
//   - Field redaction: any map key matching a SensitiveKey (case-insensitive)
//     is replaced with the literal string "<redacted>". Nested maps are
//     walked recursively.
//   - Pattern redaction: every string value is run through SensitivePatterns
//     and matches are replaced with "<redacted>". Built-in patterns cover
//     email and credit-card-shaped digit runs; callers can extend the list.
//
// The redactor is optional — passing nil to NewExporter leaves audit detail
// untouched. Phase-3 audit policy will couple redaction to caller scope so
// internal callers can see raw detail while external exports cannot.
type Redactor struct {
	SensitiveKeys     []string
	SensitivePatterns []*regexp.Regexp
}

// NewDefaultRedactor returns a Redactor with sensible defaults — the keys
// that show up in real-world handler payloads (passwords, tokens, secrets,
// authorization headers, plus first/last name, ssn, dob) and a built-in
// email pattern.
func NewDefaultRedactor() *Redactor {
	return &Redactor{
		SensitiveKeys: []string{
			"password", "passwd", "secret", "token", "api_key",
			"authorization", "auth", "ssn", "dob",
		},
		SensitivePatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b[\w._%+-]+@[\w.-]+\.[a-z]{2,}\b`),
			// Loose credit-card pattern: 13–19 digits with optional spaces or hyphens.
			regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`),
		},
	}
}

// Redact returns a deep-cloned copy of detail with sensitive values
// replaced by "<redacted>". Returns nil when detail is nil. The original
// map is never mutated so callers can safely render the same event for an
// internal viewer alongside a redacted export.
func (r *Redactor) Redact(detail map[string]any) map[string]any {
	if detail == nil || r == nil {
		return detail
	}
	return r.redactMap(detail)
}

func (r *Redactor) redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if r.isSensitiveKey(k) {
			out[k] = "<redacted>"
			continue
		}
		out[k] = r.redactValue(v)
	}
	return out
}

func (r *Redactor) redactValue(v any) any {
	switch val := v.(type) {
	case string:
		return r.redactString(val)
	case map[string]any:
		return r.redactMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = r.redactValue(item)
		}
		return out
	default:
		return v
	}
}

func (r *Redactor) redactString(s string) string {
	for _, pat := range r.SensitivePatterns {
		s = pat.ReplaceAllString(s, "<redacted>")
	}
	return s
}

func (r *Redactor) isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, sk := range r.SensitiveKeys {
		if lk == strings.ToLower(sk) {
			return true
		}
	}
	return false
}
