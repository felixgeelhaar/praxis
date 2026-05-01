package schema

import (
	"fmt"
	"sort"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

// CompatMode controls what happens when a re-registration introduces a
// breaking change. Phase 5 schema versioning.
type CompatMode string

const (
	// CompatStrict refuses the new registration with an error so the
	// runtime never silently swaps an incompatible capability into the
	// registry. Production deployments should run strict.
	CompatStrict CompatMode = "strict"

	// CompatWarn records the breaking change in the audit/metric path
	// but accepts the new registration. Useful during development when
	// schema iteration is expected.
	CompatWarn CompatMode = "warn"

	// CompatOff disables the checker entirely. Default to preserve
	// pre-Phase-5 behaviour for installations that have not opted in.
	CompatOff CompatMode = "off"
)

// Issue describes one breaking change discovered between a previous
// and new schema. Stable Code values let operators alert on a specific
// failure mode without parsing the human message.
type Issue struct {
	Code    string
	Field   string
	Message string
}

// Issue codes — stable strings for metric labels and alerts.
const (
	CodeRequiredFieldRemoved = "required_field_removed"
	CodeRequiredFieldAdded   = "required_field_added"
	CodeTypeChanged          = "type_changed"
	CodeEnumNarrowed         = "enum_narrowed"
)

// CheckCompat compares two Capability snapshots and returns the
// breaking-change issues. An empty result means the new capability is
// backward-compatible with the previous one. Both schemas are expected
// to be JSON Schema documents shaped as map[string]any (the form the
// validator operates on); any other shape returns an empty result so
// non-JSON-Schema capabilities skip the check rather than firing
// false positives.
func CheckCompat(prev, next domain.Capability) []Issue {
	var issues []Issue
	issues = append(issues, checkSchema("input", prev.InputSchema, next.InputSchema)...)
	issues = append(issues, checkSchema("output", prev.OutputSchema, next.OutputSchema)...)
	return issues
}

func checkSchema(prefix string, prev, next any) []Issue {
	pm, pok := prev.(map[string]any)
	nm, nok := next.(map[string]any)
	if !pok || !nok {
		return nil
	}

	var issues []Issue

	// Required fields: removed from old → existing payloads using
	// just-the-required fields might still validate, but the contract
	// signal is gone. Added in new → existing callers omitting them
	// will now fail.
	prevRequired := stringSet(extractStrings(pm["required"]))
	nextRequired := stringSet(extractStrings(nm["required"]))
	for f := range prevRequired {
		if !nextRequired[f] {
			issues = append(issues, Issue{
				Code:    CodeRequiredFieldRemoved,
				Field:   prefix + "." + f,
				Message: fmt.Sprintf("required field %q removed from %s schema", f, prefix),
			})
		}
	}
	for f := range nextRequired {
		if !prevRequired[f] {
			issues = append(issues, Issue{
				Code:    CodeRequiredFieldAdded,
				Field:   prefix + "." + f,
				Message: fmt.Sprintf("required field %q added to %s schema", f, prefix),
			})
		}
	}

	// Per-property checks: type changes and enum narrowing on shared
	// keys break payloads that already match the previous schema.
	pProps, _ := pm["properties"].(map[string]any)
	nProps, _ := nm["properties"].(map[string]any)
	for k, pv := range pProps {
		nv, ok := nProps[k]
		if !ok {
			continue
		}
		issues = append(issues, checkProperty(prefix+"."+k, pv, nv)...)
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Field != issues[j].Field {
			return issues[i].Field < issues[j].Field
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

func checkProperty(field string, prev, next any) []Issue {
	pm, pok := prev.(map[string]any)
	nm, nok := next.(map[string]any)
	if !pok || !nok {
		return nil
	}
	var out []Issue
	if pt, ok := pm["type"].(string); ok {
		if nt, ok := nm["type"].(string); ok && nt != pt {
			out = append(out, Issue{
				Code:    CodeTypeChanged,
				Field:   field,
				Message: fmt.Sprintf("type changed: %s → %s", pt, nt),
			})
		}
	}
	pEnum := stringSet(extractStrings(pm["enum"]))
	nEnum := stringSet(extractStrings(nm["enum"]))
	if len(pEnum) > 0 && len(nEnum) > 0 {
		for v := range pEnum {
			if !nEnum[v] {
				out = append(out, Issue{
					Code:    CodeEnumNarrowed,
					Field:   field,
					Message: fmt.Sprintf("enum narrowed: %q removed", v),
				})
			}
		}
	}
	return out
}

// extractStrings reads a JSON-shaped string-array field. JSON
// unmarshals []string as []any, so coerce element-wise.
func extractStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringSet(s []string) map[string]bool {
	out := make(map[string]bool, len(s))
	for _, v := range s {
		out[v] = true
	}
	return out
}
