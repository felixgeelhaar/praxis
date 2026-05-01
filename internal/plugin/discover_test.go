package plugin_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/plugin"
)

// writePlugin creates a plugin directory under root with a manifest.json and
// (optionally) an artefact file. Returns the plugin directory path.
func writePlugin(t *testing.T, root, name, manifestJSON string, artefact string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if manifestJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	if artefact != "" {
		if err := os.WriteFile(filepath.Join(dir, artefact), []byte("artefact"), 0o644); err != nil {
			t.Fatalf("write artefact: %v", err)
		}
	}
	return dir
}

func validManifest(name string) string {
	return `{
		"name": "` + name + `",
		"version": "1.0.0",
		"abi": "` + plugin.ABIVersion + `",
		"artifact": "plugin.so"
	}`
}

func TestDiscover_NonExistentDir(t *testing.T) {
	res, err := plugin.Discover(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatalf("expected error for missing dir, got %+v", res)
	}
}

func TestDiscover_DirIsFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Discover(f); err == nil {
		t.Fatal("expected error when root is a file")
	}
}

func TestDiscover_EmptyDir(t *testing.T) {
	res, err := plugin.Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 0 || len(res.Errors) != 0 {
		t.Errorf("empty dir: got %+v", res)
	}
}

func TestDiscover_ValidPlugin(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "pagerduty", validManifest("pagerduty"), "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", res.Errors)
	}
	if len(res.Plugins) != 1 {
		t.Fatalf("plugins=%d want 1", len(res.Plugins))
	}
	p := res.Plugins[0]
	if p.Dir != dir {
		t.Errorf("Dir=%s want %s", p.Dir, dir)
	}
	if p.Manifest.Name != "pagerduty" {
		t.Errorf("Name=%s", p.Manifest.Name)
	}
	if p.Manifest.Version != "1.0.0" {
		t.Errorf("Version=%s", p.Manifest.Version)
	}
	if p.ABI != plugin.ABIVersion {
		t.Errorf("ABI=%s want %s", p.ABI, plugin.ABIVersion)
	}
	if p.Artifact != filepath.Join(dir, "plugin.so") {
		t.Errorf("Artifact=%s", p.Artifact)
	}
}

func TestDiscover_MissingManifest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "noman", "", "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 0 {
		t.Errorf("expected no plugins, got %+v", res.Plugins)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("errors=%d want 1", len(res.Errors))
	}
	if !errors.Is(res.Errors[0].Err, plugin.ErrManifestMissing) {
		t.Errorf("err=%v want ErrManifestMissing", res.Errors[0].Err)
	}
}

func TestDiscover_MalformedManifest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "broken", "{ not valid json", "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 0 {
		t.Errorf("expected no plugins, got %+v", res.Plugins)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("errors=%d want 1", len(res.Errors))
	}
	if !errors.Is(res.Errors[0].Err, plugin.ErrManifestInvalid) {
		t.Errorf("err=%v want ErrManifestInvalid", res.Errors[0].Err)
	}
}

func TestDiscover_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing name", `{"version":"1.0.0","abi":"v1","artifact":"plugin.so"}`},
		{"missing version", `{"name":"x","abi":"v1","artifact":"plugin.so"}`},
		{"missing abi", `{"name":"x","version":"1.0.0","artifact":"plugin.so"}`},
		{"missing artifact", `{"name":"x","version":"1.0.0","abi":"v1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePlugin(t, root, "p", tc.json, "plugin.so")
			res, err := plugin.Discover(root)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(res.Errors) != 1 {
				t.Fatalf("errors=%d want 1: %+v", len(res.Errors), res.Errors)
			}
			if !errors.Is(res.Errors[0].Err, plugin.ErrManifestInvalid) {
				t.Errorf("err=%v want ErrManifestInvalid", res.Errors[0].Err)
			}
		})
	}
}

func TestDiscover_PathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "evil", `{
		"name":"evil","version":"1.0.0","abi":"`+plugin.ABIVersion+`","artifact":"../escape.so"
	}`, "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 0 {
		t.Errorf("expected no plugins, got %+v", res.Plugins)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrUnsafeArtifact) {
		t.Errorf("errors=%+v want ErrUnsafeArtifact", res.Errors)
	}
}

func TestDiscover_AbsoluteArtifactRejected(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "absolute", `{
		"name":"absolute","version":"1.0.0","abi":"`+plugin.ABIVersion+`","artifact":"/etc/passwd"
	}`, "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrUnsafeArtifact) {
		t.Errorf("errors=%+v want ErrUnsafeArtifact", res.Errors)
	}
}

func TestDiscover_ArtifactMissing(t *testing.T) {
	root := t.TempDir()
	// manifest declares plugin.so but artefact not written.
	writePlugin(t, root, "no-artefact", validManifest("no-artefact"), "")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrArtifactMissing) {
		t.Errorf("errors=%+v want ErrArtifactMissing", res.Errors)
	}
}

func TestDiscover_NonDirEntryIgnored(t *testing.T) {
	root := t.TempDir()
	// File at root level - not a plugin dir, should be skipped silently.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, root, "real", validManifest("real"), "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected errors: %+v", res.Errors)
	}
	if len(res.Plugins) != 1 || res.Plugins[0].Manifest.Name != "real" {
		t.Errorf("plugins=%+v", res.Plugins)
	}
}

func TestDiscover_MultiplePluginsMixed(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "good-1", validManifest("good-1"), "plugin.so")
	writePlugin(t, root, "good-2", validManifest("good-2"), "plugin.so")
	writePlugin(t, root, "broken", "{ bad", "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 2 {
		t.Errorf("plugins=%d want 2: %+v", len(res.Plugins), res.Plugins)
	}
	if len(res.Errors) != 1 {
		t.Errorf("errors=%d want 1: %+v", len(res.Errors), res.Errors)
	}
}

func TestDiscover_DuplicateNamesReported(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "first", `{
		"name":"dup","version":"1.0.0","abi":"`+plugin.ABIVersion+`","artifact":"plugin.so"
	}`, "plugin.so")
	writePlugin(t, root, "second", `{
		"name":"dup","version":"2.0.0","abi":"`+plugin.ABIVersion+`","artifact":"plugin.so"
	}`, "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// First wins, second surfaces ErrDuplicateName.
	if len(res.Plugins) != 1 {
		t.Errorf("plugins=%d want 1", len(res.Plugins))
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrDuplicateName) {
		t.Errorf("errors=%+v want ErrDuplicateName", res.Errors)
	}
}

func TestDiscover_DeterministicOrdering(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "zeta", validManifest("zeta"), "plugin.so")
	writePlugin(t, root, "alpha", validManifest("alpha"), "plugin.so")
	writePlugin(t, root, "mu", validManifest("mu"), "plugin.so")

	res, err := plugin.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Plugins) != 3 {
		t.Fatalf("plugins=%d want 3", len(res.Plugins))
	}
	got := []string{res.Plugins[0].Manifest.Name, res.Plugins[1].Manifest.Name, res.Plugins[2].Manifest.Name}
	want := []string{"alpha", "mu", "zeta"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("ordering=%v want %v", got, want)
			break
		}
	}
}
