// Package schema validates Action.Payload against Capability.InputSchema and
// handler output against Capability.OutputSchema.
//
// Phase 1 ships JSON Schema (Draft 2020-12) via santhosh-tekuri/jsonschema/v6.
// Schemas may be supplied as map[string]any (the canonical in-process form),
// or as raw JSON bytes. Compiled schemas are cached so repeat validations
// against the same descriptor are cheap.
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/felixgeelhaar/praxis/internal/domain"
)

var enPrinter = message.NewPrinter(language.English)

// ErrValidationFailed is the sentinel error returned for any validation
// failure. Callers can use errors.Is to branch.
var ErrValidationFailed = errors.New("validation failed")

// Validator compiles and caches JSON Schemas.
type Validator struct {
	mu    sync.RWMutex
	cache map[uint64]*jsonschema.Schema
}

// New constructs a Validator with an empty cache.
func New() *Validator {
	return &Validator{cache: make(map[uint64]*jsonschema.Schema)}
}

// ValidatePayload validates payload against schema. A nil schema is a
// permissive contract (no constraints).
func (v *Validator) ValidatePayload(payload map[string]any, schema any) error {
	return v.validate(payload, schema, "input")
}

// ValidateOutput validates handler output against schema.
func (v *Validator) ValidateOutput(output map[string]any, schema any) error {
	return v.validate(output, schema, "output")
}

func (v *Validator) validate(value map[string]any, schema any, kind string) error {
	if schema == nil {
		return nil
	}
	compiled, err := v.compile(schema)
	if err != nil {
		return fmt.Errorf("%w: %s schema invalid: %v", ErrValidationFailed, kind, err)
	}
	if compiled == nil {
		return nil
	}
	// jsonschema/v6 expects the value to be a JSON-decoded any; map[string]any
	// is exactly that, but the lib wants its own decoded form.
	v6val, err := jsonRoundtrip(value)
	if err != nil {
		return fmt.Errorf("%w: %s payload encode: %v", ErrValidationFailed, kind, err)
	}
	if err := compiled.Validate(v6val); err != nil {
		return fmt.Errorf("%w: %s: %s", ErrValidationFailed, kind, summariseError(err))
	}
	return nil
}

// compile turns the supplied schema descriptor into a *jsonschema.Schema.
// It accepts:
//   - map[string]any   (preferred, in-process form)
//   - []byte           (raw JSON)
//   - string           (raw JSON)
//   - *jsonschema.Schema (already-compiled, returned as-is)
//
// Anything else is rejected.
func (v *Validator) compile(s any) (*jsonschema.Schema, error) {
	if pre, ok := s.(*jsonschema.Schema); ok {
		return pre, nil
	}
	raw, err := schemaToBytes(s)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	key := fnvBytes(raw)

	v.mu.RLock()
	if c, ok := v.cache[key]; ok {
		v.mu.RUnlock()
		return c, nil
	}
	v.mu.RUnlock()

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, fmt.Errorf("add schema: %w", err)
	}
	compiled, err := c.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}

	v.mu.Lock()
	v.cache[key] = compiled
	v.mu.Unlock()
	return compiled, nil
}

func schemaToBytes(s any) ([]byte, error) {
	switch v := s.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	case map[string]any:
		return json.Marshal(v)
	default:
		// last-resort: try to marshal whatever it is.
		return json.Marshal(v)
	}
}

func jsonRoundtrip(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

func fnvBytes(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

// summariseError reduces a jsonschema validation error to a short, human-
// readable list. The library's default Error() prepends the resolved schema
// URL on the first line — useful when debugging the schema itself, noisy
// for end users. We walk the leaf causes and emit `<path>: <reason>` pairs
// derived from each kind's GoString.
func summariseError(err error) string {
	if err == nil {
		return ""
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	leaves := collectLeaves(ve, nil)
	if len(leaves) == 0 {
		return formatKind(ve)
	}
	parts := make([]string, 0, len(leaves))
	for _, l := range leaves {
		loc := strings.Join(l.InstanceLocation, "/")
		if loc == "" {
			loc = "(root)"
		}
		parts = append(parts, loc+": "+formatKind(l))
	}
	return strings.Join(parts, "; ")
}

func formatKind(ve *jsonschema.ValidationError) string {
	if ve == nil || ve.ErrorKind == nil {
		return "schema violation"
	}
	return ve.ErrorKind.LocalizedString(enPrinter)
}

func collectLeaves(ve *jsonschema.ValidationError, acc []*jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return append(acc, ve)
	}
	for _, c := range ve.Causes {
		acc = collectLeaves(c, acc)
	}
	return acc
}

// BuildReport packages a boolean + error list into the domain.ValidationReport
// type for Simulation results.
func (v *Validator) BuildReport(valid bool, errs []string) domain.ValidationReport {
	return domain.ValidationReport{Valid: valid, Errors: errs}
}
