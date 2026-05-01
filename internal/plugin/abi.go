// Package plugin defines the stable ABI for out-of-tree Praxis capability
// handlers (Phase 3 M3.1).
//
// A plugin is a Go module that exports a `Plugin` symbol implementing this
// package's Plugin interface. The Praxis runtime loads it via the standard
// plugin discovery flow (manifest.json + signature verification, both of
// which are layered on top of this ABI in follow-up tasks):
//
//  1. Plugin author writes a Go package exporting `var Plugin praxis.Plugin = ...`
//  2. The author signs the build with cosign and places the artefact +
//     manifest.json in $PRAXIS_PLUGIN_DIR.
//  3. Praxis verifies the signature against a configured root, loads the
//     symbol, and registers each Capability returned by Capabilities()
//     with the in-process registry.
//
// ABI version: v1. Breaking changes to this interface bump ABIVersion and
// the runtime refuses to load older plugins. Additive changes (new
// optional methods on the Plugin interface) are signalled by minor-version
// bumps and remain compatible.
package plugin

import (
	"context"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
)

// ABIVersion is the major version of the plugin ABI. Bump on any breaking
// change to Plugin or Handler.
const ABIVersion = "v1"

// Plugin is the symbol every plugin must export. The runtime calls
// Capabilities() at load time and registers each returned descriptor +
// handler with the registry.
type Plugin interface {
	// ABI reports the ABI version this plugin was built against. Plugins
	// MUST return ABIVersion (the constant in this package as compiled at
	// build time). The runtime refuses to load plugins whose ABI does not
	// match the runtime's ABIVersion.
	ABI() string

	// Manifest returns identity + provenance metadata. Authors set Name
	// and Version; the runtime validates Name uniqueness against already-
	// registered capabilities and surfaces Version in audit detail.
	Manifest() Manifest

	// Capabilities returns one or more (descriptor, handler) pairs for
	// registration. A plugin may expose multiple capabilities — convenient
	// when one vendor has several related actions (e.g. github_create_issue
	// + github_add_comment).
	Capabilities(ctx context.Context) ([]Registration, error)
}

// Manifest describes the plugin's identity and build provenance. Required
// fields: Name, Version. Optional fields surface in audit detail.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author,omitempty"`
	Description string `json:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
}

// Registration pairs a Capability descriptor with the handler that runs it.
// Returned by Plugin.Capabilities and consumed by the runtime registrar.
type Registration struct {
	Capability domain.Capability
	Handler    capability.Handler
}

// Loader is the runtime interface that registers a plugin's capabilities.
// Implemented by cmd/praxis at startup; tested in this package against a
// fake plugin to lock down the contract.
type Loader interface {
	Register(reg Registration) error
}

// Load is the canonical plugin-load helper. The runtime calls this once
// per discovered plugin. It validates the ABI version, runs Capabilities,
// and routes each Registration through the supplied Loader.
//
// If the plugin implements BudgetedPlugin, every handler in every
// Registration is wrapped in a Sandboxed wrapper that enforces the
// declared ResourceBudget. Plugins that don't opt in load unwrapped —
// existing untrusted-plugin-rejection happens upstream of Load (manifest
// validation + signature verification).
//
// Returning an error short-circuits load — a partial registration is
// rolled back by the caller (the runtime must not allow half-loaded
// plugins).
func Load(ctx context.Context, p Plugin, loader Loader) error {
	return LoadWithHooks(ctx, p, loader, nil)
}

// LoadHooks lets callers (the Manager) wrap handlers post-sandbox and
// post-budgeting. A nil LoadHooks reduces to the original Load
// behaviour. Phase 4 graceful rollover.
type LoadHooks struct {
	// WrapHandler runs against every Registration's handler after the
	// optional Sandboxed wrap. The plugin's Manifest is supplied so the
	// caller can record per-plugin state. Returning the same handler
	// unchanged is the no-op default.
	WrapHandler func(manifest Manifest, h capability.Handler) capability.Handler
}

// LoadWithHooks is Load with an optional WrapHandler hook. Used by the
// Manager to layer versioned-handler tracking on top of the existing
// sandbox wrapping without forking the load pipeline.
func LoadWithHooks(ctx context.Context, p Plugin, loader Loader, hooks *LoadHooks) error {
	if got := p.ABI(); got != ABIVersion {
		return &ABIMismatchError{Want: ABIVersion, Got: got}
	}
	regs, err := p.Capabilities(ctx)
	if err != nil {
		return err
	}
	if bp, ok := p.(BudgetedPlugin); ok {
		budget := bp.Budget()
		for i := range regs {
			regs[i].Handler = Sandboxed(regs[i].Handler, budget)
		}
	}
	if hooks != nil && hooks.WrapHandler != nil {
		manifest := p.Manifest()
		for i := range regs {
			regs[i].Handler = hooks.WrapHandler(manifest, regs[i].Handler)
		}
	}
	for _, r := range regs {
		if err := loader.Register(r); err != nil {
			return err
		}
	}
	return nil
}

// ABIMismatchError signals an ABI-version mismatch between runtime and plugin.
type ABIMismatchError struct {
	Want, Got string
}

// Error implements error.
func (e *ABIMismatchError) Error() string {
	return "praxis plugin ABI mismatch: runtime=" + e.Want + " plugin=" + e.Got
}
