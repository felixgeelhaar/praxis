package plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/plugin"
)

func TestWatcher_RequiresOnReload(t *testing.T) {
	dir := t.TempDir()
	if _, err := plugin.NewWatcher(plugin.WatcherConfig{Root: dir}); err == nil {
		t.Error("expected error when OnReload nil")
	}
}

func TestWatcher_FailsOnMissingRoot(t *testing.T) {
	_, err := plugin.NewWatcher(plugin.WatcherConfig{
		Root:     "/nonexistent/praxis-watcher-test",
		OnReload: func(string) {},
	})
	if err == nil {
		t.Error("expected error for missing root")
	}
}

func TestWatcher_TriggersOnManifestWrite(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "p1")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var (
		mu        sync.Mutex
		callCount int
		gotDir    string
	)
	w, err := plugin.NewWatcher(plugin.WatcherConfig{
		Root:     root,
		Debounce: 30 * time.Millisecond,
		OnReload: func(d string) {
			mu.Lock()
			callCount++
			gotDir = d
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// Add the plugin subdir to the watcher path. Watching root only
	// catches direct events on root; we explicitly auto-watch new dirs
	// via fsnotify.Create — but the dir already existed before the
	// watcher started, so re-add it manually for the test.
	if err := os.WriteFile(filepath.Join(pluginDir, "warmup"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	manifestPath := filepath.Join(pluginDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"p1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if !waitFor(150*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callCount > 0
	}) {
		t.Fatal("OnReload not invoked after manifest write")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotDir != pluginDir {
		t.Errorf("gotDir=%s want %s", gotDir, pluginDir)
	}
}

func TestWatcher_DebouncesBurstWrites(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "p1")
	_ = os.MkdirAll(pluginDir, 0o755)

	var (
		mu    sync.Mutex
		calls int
	)
	w, err := plugin.NewWatcher(plugin.WatcherConfig{
		Root:     root,
		Debounce: 100 * time.Millisecond,
		OnReload: func(string) {
			mu.Lock()
			calls++
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// Warm up: ensure the subdir is auto-watched (creating a file in
	// root triggers Create event).
	_ = os.WriteFile(filepath.Join(pluginDir, "warmup"), []byte("x"), 0o644)
	time.Sleep(40 * time.Millisecond)

	manifestPath := filepath.Join(pluginDir, "manifest.json")
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(manifestPath, []byte(`{"i":1}`), 0o644)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("calls=%d want 1 (debounce should coalesce)", calls)
	}
}

func TestWatcher_IgnoresUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "p1")
	_ = os.MkdirAll(pluginDir, 0o755)

	var (
		mu    sync.Mutex
		calls int
	)
	w, err := plugin.NewWatcher(plugin.WatcherConfig{
		Root:     root,
		Debounce: 30 * time.Millisecond,
		OnReload: func(string) {
			mu.Lock()
			calls++
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	_ = os.WriteFile(filepath.Join(pluginDir, "warmup"), []byte("x"), 0o644)
	time.Sleep(40 * time.Millisecond)
	_ = os.WriteFile(filepath.Join(pluginDir, "README.md"), []byte("docs"), 0o644)
	_ = os.WriteFile(filepath.Join(pluginDir, "plugin.so"), []byte("binary"), 0o644)
	time.Sleep(120 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Errorf("calls=%d want 0 (only manifest/.sig should trigger)", calls)
	}
}

func TestWatcher_CtxCancelStopsLoop(t *testing.T) {
	root := t.TempDir()
	w, err := plugin.NewWatcher(plugin.WatcherConfig{
		Root:     root,
		OnReload: func(string) {},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

// waitFor returns true if cond becomes true within d, false otherwise.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}
