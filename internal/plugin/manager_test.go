package plugin_test

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"path/filepath"
	"testing"

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
