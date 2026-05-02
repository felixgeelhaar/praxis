package plugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/felixgeelhaar/praxis/internal/capability"
)

// LoadEvent reports a single plugin's load outcome with enough metadata
// for /metrics labelling and `praxis plugins list` rendering. Phase 4.
type LoadEvent struct {
	Name        string
	Version     string
	ABI         string
	Dir         string
	ArtifactSHA string
	Result      string
	Err         error
}

// ManagerConfig parameters the plugin Manager. Loader, Opener, and Dir
// are required; TrustedKeys may be empty (the load pipeline will then
// reject every plugin via ErrNoTrustedKeys, which is the safe default).
//
// Unregister, when set, is invoked for every capability name the
// Manager needs to remove from the runtime registry on a plugin
// crash. The host wires this to capability.Registry.Unregister so
// the in-process registry stays consistent with the Manager's
// snapshot of loaded plugins.
type ManagerConfig struct {
	Dir         string
	TrustedKeys []*ecdsa.PublicKey
	Keyless     *KeylessVerifier
	Loader      Loader
	Opener      Opener
	Unregister  func(capName string)
	OnEvent     func(LoadEvent)
}

// Manager owns the runtime state of loaded plugins. It serialises load
// and reload calls so a watcher event and a SIGHUP-driven full re-scan
// don't race against each other.
type Manager struct {
	cfg ManagerConfig

	mu       sync.RWMutex
	loaded   map[string]LoadEvent           // pluginName -> last successful load
	wrapped  map[string][]*versionedHandler // pluginName -> active versioned wrappers
	retired  map[string][]*versionedHandler // pluginName -> retired wrappers awaiting drain
	capNames map[string][]string            // pluginName -> capability names for crash deregistration
	version  atomic.Uint64                  // monotonic per Manager
}

// NewManager constructs a Manager. Mandatory fields (Dir, Loader,
// Opener) are validated lazily — LoadAll surfaces missing wiring as a
// clear error rather than panicking at construction.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		cfg:      cfg,
		loaded:   map[string]LoadEvent{},
		wrapped:  map[string][]*versionedHandler{},
		retired:  map[string][]*versionedHandler{},
		capNames: map[string][]string{},
	}
}

// LoadAll runs the full discover→verify→open→Load pipeline once and
// records the outcome of every plugin. Returns the underlying pipeline
// error only if discovery fails outright; per-plugin failures populate
// the Manager's loaded state and are surfaced via OnEvent.
//
// Existing wrappers are retired before the new pipeline runs so the
// graceful drain semantics apply across full reloads — in-flight calls
// finish on the version they started with, and Drain(name) on the
// retired wrapper unblocks once they do.
func (m *Manager) LoadAll(ctx context.Context) (PipelineResult, error) {
	m.retireAll()

	staging := map[string][]*versionedHandler{}
	hooks := &LoadHooks{
		WrapHandler: func(manifest Manifest, h capability.Handler) capability.Handler {
			version := m.version.Add(1)
			wrapped, vh := wrapForVersioning(h, version)
			staging[manifest.Name] = append(staging[manifest.Name], vh)
			return wrapped
		},
		OnLoaded: m.afterLoad,
	}

	res, err := RunPipeline(ctx, PipelineConfig{
		Dir:         m.cfg.Dir,
		TrustedKeys: m.cfg.TrustedKeys,
		Keyless:     m.cfg.Keyless,
		Loader:      m.cfg.Loader,
		Opener:      m.cfg.Opener,
		LoadHooks:   hooks,
	})
	if err != nil {
		return res, err
	}
	m.mu.Lock()
	for _, p := range res.Loaded {
		ev := LoadEvent{
			Name:        p.Manifest.Name,
			Version:     p.Manifest.Version,
			ABI:         p.ABI,
			Dir:         p.Dir,
			ArtifactSHA: artifactSHA(p.Artifact),
			Result:      ResultSuccess,
		}
		m.loaded[ev.Name] = ev
		if v, ok := staging[ev.Name]; ok {
			m.wrapped[ev.Name] = v
		}
		m.fire(ev)
	}
	for _, e := range res.Errors {
		ev := LoadEvent{
			Dir:    e.Dir,
			Result: ClassifyError(e.Err),
			Err:    e.Err,
		}
		m.fire(ev)
	}
	m.mu.Unlock()
	return res, nil
}

// ReloadOne re-runs the pipeline scoped to a single plugin directory.
// The lookup is by plugin Name (the one returned by Manifest()), not
// by directory path — operators reload by capability identity, not by
// filesystem layout.
//
// The plugin's previous wrappers are retired before the new ones are
// registered so in-flight calls drain naturally; Drain(name) is the
// supported way to wait for that completion.
func (m *Manager) ReloadOne(ctx context.Context, name string) error {
	m.mu.RLock()
	ev, ok := m.loaded[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrPluginNotLoaded, name)
	}

	disc, err := loadManifest(ev.Dir)
	if err != nil {
		m.mu.Lock()
		m.retireLocked(name)
		delete(m.loaded, name)
		delete(m.wrapped, name)
		m.mu.Unlock()
		failure := LoadEvent{Dir: ev.Dir, Result: ClassifyError(err), Err: err}
		m.fire(failure)
		return err
	}

	staged := []*versionedHandler{}
	hooks := &LoadHooks{
		WrapHandler: func(_ Manifest, h capability.Handler) capability.Handler {
			version := m.version.Add(1)
			wrapped, vh := wrapForVersioning(h, version)
			staged = append(staged, vh)
			return wrapped
		},
		OnLoaded: m.afterLoad,
	}

	// Retire the previous wrappers BEFORE the new ones register so a
	// concurrent reader cannot see two active versions for the same
	// capability name.
	m.mu.Lock()
	m.retireLocked(name)
	m.mu.Unlock()

	if err := loadOne(ctx, PipelineConfig{
		TrustedKeys: m.cfg.TrustedKeys,
		Keyless:     m.cfg.Keyless,
		Loader:      m.cfg.Loader,
		Opener:      m.cfg.Opener,
		LoadHooks:   hooks,
	}, disc); err != nil {
		failure := LoadEvent{
			Name:   disc.Manifest.Name,
			Dir:    disc.Dir,
			Result: ClassifyError(err),
			Err:    err,
		}
		m.fire(failure)
		return err
	}

	updated := LoadEvent{
		Name:        disc.Manifest.Name,
		Version:     disc.Manifest.Version,
		ABI:         disc.ABI,
		Dir:         disc.Dir,
		ArtifactSHA: artifactSHA(disc.Artifact),
		Result:      ResultSuccess,
	}
	m.mu.Lock()
	m.loaded[updated.Name] = updated
	m.wrapped[updated.Name] = staged
	m.mu.Unlock()
	m.fire(updated)
	return nil
}

// Drain blocks until every retired wrapper for the given plugin has
// completed its in-flight calls. New traffic, which already routes to
// the post-reload version, is unaffected. Returns immediately if the
// plugin has no retired wrappers (fresh load with no prior version).
//
// Drained wrappers are removed from the retired pool so a follow-up
// Drain call against the same name returns instantly rather than
// re-walking already-completed calls.
func (m *Manager) Drain(ctx context.Context, name string) error {
	m.mu.Lock()
	wrappers := m.retired[name]
	delete(m.retired, name)
	m.mu.Unlock()
	for _, vh := range wrappers {
		if err := vh.DrainCtx(ctx); err != nil {
			// Re-park the not-yet-drained wrappers so a subsequent
			// Drain (with a fresh ctx) can resume.
			m.mu.Lock()
			m.retired[name] = append(m.retired[name], vh)
			m.mu.Unlock()
			return err
		}
	}
	return nil
}

// afterLoad is invoked by LoadWithHooks once each plugin's
// registrations have reached the runtime registry. The Manager
// records the plugin's capability names for crash deregistration and,
// when the plugin exposes a Watch channel, spawns a goroutine that
// triggers crash recovery on terminal stream errors.
func (m *Manager) afterLoad(p Plugin, regs []Registration) {
	manifestName := p.Manifest().Name
	names := make([]string, 0, len(regs))
	for _, r := range regs {
		names = append(names, r.Capability.Name)
	}
	m.mu.Lock()
	m.capNames[manifestName] = names
	m.mu.Unlock()

	if w, ok := p.(Watchable); ok {
		go m.watchCrash(manifestName, w)
	}
}

// watchCrash blocks on the plugin's Watch channel and, when it fires,
// runs crash recovery: every capability the plugin contributed is
// deregistered and a LoadEvent with ResultCrashed is fired. The
// Manager's loaded snapshot drops the plugin so a subsequent
// ListCapabilities omits it until the operator reloads.
func (m *Manager) watchCrash(name string, w Watchable) {
	err, ok := <-w.Watch()
	if !ok || err == nil {
		return
	}
	m.mu.Lock()
	caps := append([]string(nil), m.capNames[name]...)
	delete(m.capNames, name)
	delete(m.loaded, name)
	// Move active wrappers into retired so any in-flight calls finish
	// on the dead handler (it'll surface the IPC error itself); the
	// retired bookkeeping prevents Drain from hanging forever later.
	if active, hasActive := m.wrapped[name]; hasActive {
		for _, vh := range active {
			vh.Retire()
			m.retired[name] = append(m.retired[name], vh)
		}
		delete(m.wrapped, name)
	}
	m.mu.Unlock()

	if m.cfg.Unregister != nil {
		for _, capName := range caps {
			m.cfg.Unregister(capName)
		}
	}
	m.fire(LoadEvent{Name: name, Result: ResultCrashed, Err: err})
}

// retireAll marks every currently-tracked wrapper retired. Called at
// the start of LoadAll so a full re-scan replaces the entire tracked
// set with the freshly-loaded one. Retired wrappers move into the
// retired pool so Drain can still find them.
func (m *Manager) retireAll() {
	m.mu.Lock()
	for name := range m.wrapped {
		m.retireLocked(name)
	}
	m.wrapped = map[string][]*versionedHandler{}
	m.mu.Unlock()
}

// retireLocked marks all current wrappers for `name` retired and
// moves them into the retired pool. Caller must hold m.mu.
func (m *Manager) retireLocked(name string) {
	for _, vh := range m.wrapped[name] {
		vh.Retire()
		m.retired[name] = append(m.retired[name], vh)
	}
}

// Snapshot returns the current set of loaded plugins, sorted by name
// for deterministic CLI rendering.
func (m *Manager) Snapshot() []LoadEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LoadEvent, 0, len(m.loaded))
	for _, ev := range m.loaded {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) fire(ev LoadEvent) {
	if m.cfg.OnEvent != nil {
		m.cfg.OnEvent(ev)
	}
}

// ErrPluginNotLoaded signals a reload request against a plugin name
// the Manager has no record of — typically a typo or a plugin that
// failed its initial load.
var ErrPluginNotLoaded = errors.New("plugin not loaded")

// artifactSHA returns the hex-encoded SHA-256 digest of the artefact
// file. Used by `praxis plugins list` so operators can confirm two
// hosts are running the same binary without diffing content. Returns
// the empty string on read errors so the snapshot stays well-formed.
func artifactSHA(path string) string {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
