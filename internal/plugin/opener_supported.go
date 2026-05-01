//go:build linux || darwin

package plugin

import (
	"errors"
	"fmt"
	stdplugin "plugin"
)

// SymbolName is the exported variable plugin authors must define. The
// runtime looks up this symbol after dlopen and asserts its type to
// the Plugin interface.
const SymbolName = "Plugin"

// DefaultOpener loads a plugin artefact via Go's stdlib plugin package.
// Available on linux + darwin only; other platforms get the noop
// opener that always errors out (see opener_unsupported.go).
type DefaultOpener struct{}

// Open implements Opener.
func (DefaultOpener) Open(artefactPath string) (Plugin, error) {
	p, err := stdplugin.Open(artefactPath)
	if err != nil {
		return nil, fmt.Errorf("plugin.Open(%s): %w", artefactPath, err)
	}
	sym, err := p.Lookup(SymbolName)
	if err != nil {
		return nil, fmt.Errorf("symbol %q not found in %s: %w", SymbolName, artefactPath, err)
	}
	// Plugin authors export `var Plugin praxisplugin.Plugin = ...` so
	// the lookup yields a *Plugin (pointer to interface). Accept either
	// the value or the pointer.
	switch v := sym.(type) {
	case Plugin:
		return v, nil
	case *Plugin:
		if v == nil || *v == nil {
			return nil, errors.New("Plugin symbol is nil")
		}
		return *v, nil
	default:
		return nil, fmt.Errorf("symbol %q has type %T, expected praxis plugin.Plugin", SymbolName, sym)
	}
}
