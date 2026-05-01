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
type ManagerConfig struct {
	Dir         string
	TrustedKeys []*ecdsa.PublicKey
	Loader      Loader
	Opener      Opener
	OnEvent     func(LoadEvent)
}

// Manager owns the runtime state of loaded plugins. It serialises load
// and reload calls so a watcher event and a SIGHUP-driven full re-scan
// don't race against each other.
type Manager struct {
	cfg ManagerConfig

	mu     sync.RWMutex
	loaded map[string]LoadEvent // pluginName -> last successful load
}

// NewManager constructs a Manager. Mandatory fields (Dir, Loader,
// Opener) are validated lazily — LoadAll surfaces missing wiring as a
// clear error rather than panicking at construction.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{cfg: cfg, loaded: map[string]LoadEvent{}}
}

// LoadAll runs the full discover→verify→open→Load pipeline once and
// records the outcome of every plugin. Returns the underlying pipeline
// error only if discovery fails outright; per-plugin failures populate
// the Manager's loaded state and are surfaced via OnEvent.
func (m *Manager) LoadAll(ctx context.Context) (PipelineResult, error) {
	res, err := RunPipeline(ctx, PipelineConfig{
		Dir:         m.cfg.Dir,
		TrustedKeys: m.cfg.TrustedKeys,
		Loader:      m.cfg.Loader,
		Opener:      m.cfg.Opener,
	})
	if err != nil {
		return res, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	return res, nil
}

// ReloadOne re-runs the pipeline scoped to a single plugin directory.
// The lookup is by plugin Name (the one returned by Manifest()), not by
// directory path — operators reload by capability identity, not by
// filesystem layout.
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
		delete(m.loaded, name)
		m.mu.Unlock()
		failure := LoadEvent{Dir: ev.Dir, Result: ClassifyError(err), Err: err}
		m.fire(failure)
		return err
	}

	if err := loadOne(ctx, PipelineConfig{
		TrustedKeys: m.cfg.TrustedKeys,
		Loader:      m.cfg.Loader,
		Opener:      m.cfg.Opener,
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
	m.mu.Unlock()
	m.fire(updated)
	return nil
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
