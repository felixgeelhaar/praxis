package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// tokenLoader holds the current bearer token behind an atomic pointer
// so SIGHUP rotates the active token without restarting the process.
// The HTTP handler reads the pointer on every request.
//
// Phase 6: complements tlsLoader. PRAXIS_API_TOKEN remains the
// back-compat path for callers that pass the value directly via env;
// PRAXIS_API_TOKEN_FILE points at a file the operator rotates.
type tokenLoader struct {
	path    string
	static  string
	current atomic.Pointer[string]
}

// newTokenLoader prefers static when non-empty (env-var path) and
// otherwise reads from path. Returns nil, nil when both are empty so
// callers can branch on "auth disabled."
func newTokenLoader(static, path string) (*tokenLoader, error) {
	if static == "" && path == "" {
		return nil, nil
	}
	t := &tokenLoader{path: path, static: static}
	if err := t.Reload(); err != nil {
		return nil, err
	}
	return t, nil
}

// Reload re-reads the token from disk when path is set; otherwise
// re-applies the static value (no-op).
func (t *tokenLoader) Reload() error {
	if t.path == "" {
		v := t.static
		t.current.Store(&v)
		return nil
	}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return fmt.Errorf("read API token file %s: %w", t.path, err)
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return errors.New("API token file is empty")
	}
	t.current.Store(&v)
	return nil
}

// Token returns the current bearer token. Empty string means auth
// is disabled.
func (t *tokenLoader) Token() string {
	if t == nil {
		return ""
	}
	v := t.current.Load()
	if v == nil {
		return ""
	}
	return *v
}
