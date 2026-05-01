//go:build !linux && !darwin

package plugin

import "errors"

// SymbolName is exported on every platform so plugin authors get a
// stable name to reference; the loader never reaches it on unsupported
// platforms.
const SymbolName = "Plugin"

// DefaultOpener on unsupported platforms (windows, freebsd, etc.) always
// errors. Operators on those platforms must run plugins out-of-process
// once the praxis-pluginhost binary lands; until then plugin loading is
// disabled.
type DefaultOpener struct{}

// Open implements Opener.
func (DefaultOpener) Open(_ string) (Plugin, error) {
	return nil, errors.New("plugin loading is not supported on this platform; use the out-of-process loader")
}
