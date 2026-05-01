package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatcherConfig parameters the plugin-directory watcher. Root is the
// directory to watch (PRAXIS_PLUGIN_DIR); OnReload is invoked with the
// affected plugin directory after a debounce window. Debounce coalesces
// bursts of FS events (a manifest rewrite often produces several writes
// per second) into a single reload.
type WatcherConfig struct {
	Root     string
	OnReload func(pluginDir string)
	Debounce time.Duration
}

// Watcher monitors PRAXIS_PLUGIN_DIR and triggers OnReload when a
// plugin's manifest.json or .sig changes. Sub-second event bursts are
// coalesced via Debounce so a single edit doesn't cause five reloads.
//
// The watcher is opt-in: callers control startup (no goroutine starts
// implicitly) and pass the cancel context that ends the loop.
type Watcher struct {
	cfg WatcherConfig
	w   *fsnotify.Watcher

	mu     sync.Mutex
	timers map[string]*time.Timer // pluginDir -> debounce timer
}

// NewWatcher constructs a Watcher rooted at cfg.Root. Returns an error
// if fsnotify cannot watch the directory (root missing, permissions,
// platform unsupported). Debounce defaults to 200ms.
func NewWatcher(cfg WatcherConfig) (*Watcher, error) {
	if cfg.OnReload == nil {
		return nil, errors.New("watcher: OnReload required")
	}
	if cfg.Debounce == 0 {
		cfg.Debounce = 200 * time.Millisecond
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(cfg.Root); err != nil {
		_ = w.Close()
		return nil, err
	}
	// fsnotify is non-recursive on linux/darwin; explicitly add every
	// existing plugin subdirectory so manifest writes inside them
	// trigger events. Newly created subdirs are auto-watched in
	// handleEvent below.
	if entries, derr := os.ReadDir(cfg.Root); derr == nil {
		for _, e := range entries {
			if e.IsDir() {
				_ = w.Add(filepath.Join(cfg.Root, e.Name()))
			}
		}
	}
	return &Watcher{cfg: cfg, w: w, timers: map[string]*time.Timer{}}, nil
}

// Run blocks until ctx is cancelled, dispatching plugin reload events.
// New plugin subdirectories created at runtime are auto-watched so the
// operator can drop a fresh plugin into the directory and have it
// loaded without a process restart.
func (w *Watcher) Run(ctx context.Context) {
	defer func() { _ = w.w.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
			// Errors from fsnotify are non-fatal — the watcher keeps
			// running; the next event will re-trigger reload logic.
		}
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	// Auto-watch newly created plugin subdirectories so plugins can be
	// added at runtime.
	if ev.Op&fsnotify.Create != 0 {
		_ = w.w.Add(ev.Name)
	}

	// Reload triggers: manifest.json or *.sig changes inside a plugin
	// subdir. Other files (the artefact itself, README, etc.) do not
	// trigger reload — manifest+signature is the integrity boundary.
	base := filepath.Base(ev.Name)
	if base != ManifestFilename && !strings.HasSuffix(base, SignatureExtension) {
		return
	}
	pluginDir := filepath.Dir(ev.Name)
	if pluginDir == w.cfg.Root || pluginDir == "" {
		return
	}
	w.scheduleReload(pluginDir)
}

func (w *Watcher) scheduleReload(pluginDir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.timers[pluginDir]; ok {
		t.Reset(w.cfg.Debounce)
		return
	}
	w.timers[pluginDir] = time.AfterFunc(w.cfg.Debounce, func() {
		w.mu.Lock()
		delete(w.timers, pluginDir)
		w.mu.Unlock()
		w.cfg.OnReload(pluginDir)
	})
}
