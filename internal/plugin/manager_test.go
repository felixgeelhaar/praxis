package plugin_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/plugin"
)

func setupManagerSandbox(t *testing.T) (*plugin.Manager, *capability.Registry, []*ecdsa.PublicKey, *fakeOpener, string) {
	t.Helper()
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "trusted")
	keys, err := plugin.LoadTrustedKeys([]string{pub})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	pluginDir := writeSignedPlugin(t, root, "p1", priv)

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{
			abi:      plugin.ABIVersion,
			manifest: plugin.Manifest{Name: "p1", Version: "1.0.0"},
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: "p1_cap"}, Handler: &fakeHandler{name: "p1_cap"}},
			},
		},
	}}

	mgr := plugin.NewManager(plugin.ManagerConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: reg},
		Opener:      opener,
	})
	return mgr, reg, keys, opener, root
}

func TestManager_LoadAllPopulatesSnapshot(t *testing.T) {
	mgr, reg, _, _, _ := setupManagerSandbox(t)

	if _, err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	snap := mgr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot=%+v want 1", snap)
	}
	if snap[0].Name != "p1" || snap[0].Version != "1.0.0" {
		t.Errorf("entry=%+v", snap[0])
	}
	if snap[0].ArtifactSHA == "" {
		t.Error("ArtifactSHA should be populated")
	}
	if _, err := reg.GetHandler("p1_cap"); err != nil {
		t.Errorf("registry missing capability: %v", err)
	}
}

func TestManager_ReloadOneSucceeds(t *testing.T) {
	mgr, _, _, _, _ := setupManagerSandbox(t)
	_, _ = mgr.LoadAll(context.Background())

	if err := mgr.ReloadOne(context.Background(), "p1"); err != nil {
		t.Fatalf("ReloadOne: %v", err)
	}
}

func TestManager_ReloadUnknownPluginFails(t *testing.T) {
	mgr, _, _, _, _ := setupManagerSandbox(t)
	_, _ = mgr.LoadAll(context.Background())

	if err := mgr.ReloadOne(context.Background(), "missing"); !errors.Is(err, plugin.ErrPluginNotLoaded) {
		t.Errorf("err=%v want ErrPluginNotLoaded", err)
	}
}

func TestManager_OnEventFiresForSuccessAndError(t *testing.T) {
	root := t.TempDir()
	signer, _ := genKeyPEM(t, root, "signer")
	_, otherPub := genKeyPEM(t, root, "other")
	keys, _ := plugin.LoadTrustedKeys([]string{otherPub})
	_ = writeSignedPlugin(t, root, "evil", signer) // signed with untrusted key

	var events []plugin.LoadEvent
	mgr := plugin.NewManager(plugin.ManagerConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: capability.New()},
		Opener:      &fakeOpener{plugins: map[string]plugin.Plugin{}},
		OnEvent:     func(ev plugin.LoadEvent) { events = append(events, ev) },
	})
	_, _ = mgr.LoadAll(context.Background())

	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	if events[0].Result != plugin.ResultSignature {
		t.Errorf("result=%s want %s", events[0].Result, plugin.ResultSignature)
	}
}

func TestManager_DrainBlocksUntilInFlightFinishes(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "trusted")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	pluginDir := writeSignedPlugin(t, root, "p1", priv)

	slow := &slowHandlerImpl{gate: make(chan struct{}), started: make(chan struct{}, 1)}

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{
			abi:      plugin.ABIVersion,
			manifest: plugin.Manifest{Name: "p1", Version: "1.0.0"},
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: "p1_slow"}, Handler: slow},
			},
		},
	}}

	mgr := plugin.NewManager(plugin.ManagerConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: reg},
		Opener:      opener,
	})
	if _, err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Start a slow Execute via the registry-resolved handler.
	h, err := reg.GetHandler("p1_slow")
	if err != nil {
		t.Fatalf("GetHandler: %v", err)
	}
	go func() {
		_, _ = h.Execute(context.Background(), nil)
	}()
	<-slow.started

	// Reload retires the old wrapper; Drain must block until the slow
	// call returns.
	if _, err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("reload LoadAll: %v", err)
	}

	drained := make(chan struct{})
	go func() {
		_ = mgr.Drain(context.Background(), "p1")
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("Drain returned before in-flight Execute finished")
	case <-time.After(40 * time.Millisecond):
	}
	close(slow.gate)
	select {
	case <-drained:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Drain did not return after Execute finished")
	}
}

// watchablePlugin is a fake Plugin that implements plugin.Watchable so
// the Manager spawns a crash watcher. fail() pushes an error on the
// channel to simulate a child-process crash.
type watchablePlugin struct {
	*fakePlugin
	crashCh chan error
}

func newWatchablePlugin(p *fakePlugin) *watchablePlugin {
	return &watchablePlugin{fakePlugin: p, crashCh: make(chan error, 1)}
}

func (w *watchablePlugin) Watch() <-chan error { return w.crashCh }

func (w *watchablePlugin) fail(err error) { w.crashCh <- err }

func TestManager_CrashRecoveryUnregistersCaps(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "trusted")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	pluginDir := writeSignedPlugin(t, root, "p1", priv)

	wp := newWatchablePlugin(&fakePlugin{
		abi:      plugin.ABIVersion,
		manifest: plugin.Manifest{Name: "p1", Version: "1"},
		caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "p1_a"}, Handler: &fakeHandler{name: "p1_a"}},
			{Capability: domain.Capability{Name: "p1_b"}, Handler: &fakeHandler{name: "p1_b"}},
		},
	})

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{pluginDir: wp}}

	var (
		eventsMu sync.Mutex
		events   []plugin.LoadEvent
	)
	mgr := plugin.NewManager(plugin.ManagerConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: reg},
		Opener:      opener,
		Unregister:  reg.Unregister,
		OnEvent: func(ev plugin.LoadEvent) {
			eventsMu.Lock()
			events = append(events, ev)
			eventsMu.Unlock()
		},
	})
	if _, err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Both caps should be registered initially.
	if _, err := reg.GetHandler("p1_a"); err != nil {
		t.Fatalf("p1_a missing pre-crash: %v", err)
	}

	// Trigger crash and wait for watcher to deregister.
	wp.fail(errors.New("EOF"))
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := reg.GetHandler("p1_a"); err != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := reg.GetHandler("p1_a"); err == nil {
		t.Error("p1_a still registered after crash")
	}
	if _, err := reg.GetHandler("p1_b"); err == nil {
		t.Error("p1_b still registered after crash")
	}

	// Snapshot must drop the crashed plugin.
	for _, p := range mgr.Snapshot() {
		if p.Name == "p1" {
			t.Errorf("crashed plugin still in Snapshot: %+v", p)
		}
	}

	// One LoadEvent with ResultCrashed should have been fired.
	eventsMu.Lock()
	defer eventsMu.Unlock()
	var sawCrash bool
	for _, ev := range events {
		if ev.Result == plugin.ResultCrashed && ev.Name == "p1" {
			sawCrash = true
		}
	}
	if !sawCrash {
		t.Errorf("no crashed event fired; got=%+v", events)
	}
}

type slowHandlerImpl struct {
	gate    chan struct{}
	started chan struct{}
}

func (slowHandlerImpl) Name() string { return "p1_slow" }
func (h *slowHandlerImpl) Execute(_ context.Context, _ map[string]any) (map[string]any, error) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.gate
	return map[string]any{"ok": true}, nil
}
func (h *slowHandlerImpl) Simulate(ctx context.Context, p map[string]any) (map[string]any, error) {
	return h.Execute(ctx, p)
}

func TestManager_SnapshotIsSorted(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "trusted")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	dirA := writeSignedPlugin(t, root, "alpha", priv)
	dirZ := writeSignedPlugin(t, root, "zeta", priv)
	dirM := writeSignedPlugin(t, root, "mu", priv)

	mkPlugin := func(name string) *fakePlugin {
		return &fakePlugin{
			abi:      plugin.ABIVersion,
			manifest: plugin.Manifest{Name: name, Version: "1"},
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: name + "_cap"}, Handler: &fakeHandler{name: name + "_cap"}},
			},
		}
	}
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		dirA: mkPlugin("alpha"),
		dirZ: mkPlugin("zeta"),
		dirM: mkPlugin("mu"),
	}}

	mgr := plugin.NewManager(plugin.ManagerConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: capability.New()},
		Opener:      opener,
	})
	if _, err := mgr.LoadAll(context.Background()); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	snap := mgr.Snapshot()
	want := []string{"alpha", "mu", "zeta"}
	for i, ev := range snap {
		if ev.Name != want[i] {
			t.Errorf("snap[%d]=%s want %s (path=%s)", i, ev.Name, want[i], filepath.Base(ev.Dir))
		}
	}
}
