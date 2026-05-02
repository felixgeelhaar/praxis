package plugin_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/capability"
	"github.com/felixgeelhaar/praxis/internal/domain"
	"github.com/felixgeelhaar/praxis/internal/plugin"
)

// fakeOpener simulates dlopen: it returns the Plugin instance keyed by
// plugin directory path. Tests pre-populate the map before calling Run.
type fakeOpener struct {
	plugins map[string]plugin.Plugin
	err     map[string]error
}

func (f *fakeOpener) Open(artefactPath string) (plugin.Plugin, error) {
	dir := filepath.Dir(artefactPath)
	if e, ok := f.err[dir]; ok {
		return nil, e
	}
	if p, ok := f.plugins[dir]; ok {
		return p, nil
	}
	return nil, errors.New("fakeOpener: no plugin registered for " + dir)
}

func writeSignedPlugin(t *testing.T, root, name string, priv *ecdsa.PrivateKey) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name":"` + name + `",
		"version":"1.0.0",
		"abi":"` + plugin.ABIVersion + `",
		"artifact":"plugin.so"
	}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	artefact := []byte(name + " bytes")
	artefactPath := filepath.Join(dir, "plugin.so")
	if err := os.WriteFile(artefactPath, artefact, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artefactPath+".sig", []byte(signBlob(t, priv, artefact)), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPipeline_LoadsSignedPluginIntoRegistry(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "trusted")
	keys, err := plugin.LoadTrustedKeys([]string{pub})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	pluginDir := writeSignedPlugin(t, root, "pagerduty", priv)

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{
			abi:      plugin.ABIVersion,
			manifest: plugin.Manifest{Name: "pagerduty", Version: "1.0.0"},
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: "pagerduty_create_incident"}, Handler: &fakeHandler{name: "pagerduty_create_incident"}},
			},
		},
	}}

	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir:         root,
		TrustedKeys: keys,
		Loader:      &registryLoader{reg: reg},
		Opener:      opener,
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(res.Loaded) != 1 || res.Loaded[0].Manifest.Name != "pagerduty" {
		t.Errorf("Loaded=%+v", res.Loaded)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors=%+v", res.Errors)
	}
	if _, err := reg.GetHandler("pagerduty_create_incident"); err != nil {
		t.Errorf("registry missing capability: %v", err)
	}
}

func TestPipeline_VerifyFailureSkipsLoad(t *testing.T) {
	root := t.TempDir()
	signer, _ := genKeyPEM(t, root, "signer")
	_, otherPub := genKeyPEM(t, root, "other")
	keys, _ := plugin.LoadTrustedKeys([]string{otherPub})
	pluginDir := writeSignedPlugin(t, root, "evil", signer)

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{abi: plugin.ABIVersion, caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "should_not_load"}, Handler: &fakeHandler{name: "should_not_load"}},
		}},
	}}

	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root, TrustedKeys: keys, Loader: &registryLoader{reg: reg}, Opener: opener,
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(res.Loaded) != 0 {
		t.Errorf("expected no load: %+v", res.Loaded)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrSignatureInvalid) {
		t.Errorf("Errors=%+v want ErrSignatureInvalid", res.Errors)
	}
	if _, err := reg.GetHandler("should_not_load"); err == nil {
		t.Error("evil plugin's capability leaked into registry")
	}
}

func TestPipeline_ABIMismatchSkipsLoad(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	pluginDir := writeSignedPlugin(t, root, "old", priv)

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{abi: "v0", caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "old_cap"}, Handler: &fakeHandler{name: "old_cap"}},
		}},
	}}

	res, _ := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root, TrustedKeys: keys, Loader: &registryLoader{reg: reg}, Opener: opener,
	})
	if len(res.Errors) != 1 {
		t.Fatalf("Errors=%+v", res.Errors)
	}
	var mm *plugin.ABIMismatchError
	if !errors.As(res.Errors[0].Err, &mm) {
		t.Errorf("err=%v want ABIMismatchError", res.Errors[0].Err)
	}
}

func TestPipeline_OpenerErrorSurfaced(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	pluginDir := writeSignedPlugin(t, root, "broken", priv)

	reg := capability.New()
	opener := &fakeOpener{err: map[string]error{
		pluginDir: errors.New("dlopen: not a Go plugin"),
	}}

	res, _ := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root, TrustedKeys: keys, Loader: &registryLoader{reg: reg}, Opener: opener,
	})
	if len(res.Loaded) != 0 {
		t.Errorf("expected no load on opener error: %+v", res.Loaded)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("Errors=%+v", res.Errors)
	}
}

func TestPipeline_OneBadPluginDoesNotStopOthers(t *testing.T) {
	root := t.TempDir()
	priv, pub := genKeyPEM(t, root, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	goodDir := writeSignedPlugin(t, root, "good", priv)
	otherSigner, _ := genKeyPEM(t, root, "other")
	_ = writeSignedPlugin(t, root, "evil", otherSigner) // signed with untrusted key

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		goodDir: &fakePlugin{abi: plugin.ABIVersion, caps: []plugin.Registration{
			{Capability: domain.Capability{Name: "good_cap"}, Handler: &fakeHandler{name: "good_cap"}},
		}},
	}}

	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root, TrustedKeys: keys, Loader: &registryLoader{reg: reg}, Opener: opener,
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(res.Loaded) != 1 {
		t.Errorf("Loaded=%d want 1", len(res.Loaded))
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors=%d want 1", len(res.Errors))
	}
	if _, err := reg.GetHandler("good_cap"); err != nil {
		t.Errorf("good_cap missing despite evil sibling: %v", err)
	}
}

func TestPipeline_NoPluginDirIsNoOp(t *testing.T) {
	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: "", Loader: &registryLoader{reg: capability.New()},
	})
	if err != nil {
		t.Fatalf("RunPipeline empty dir: %v", err)
	}
	if len(res.Loaded) != 0 || len(res.Errors) != 0 {
		t.Errorf("expected no-op: %+v", res)
	}
}

func TestPipeline_DiscoveryErrorAborts(t *testing.T) {
	_, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir:    "/nonexistent/praxis-plugins-dir-test",
		Loader: &registryLoader{reg: capability.New()},
	})
	if err == nil {
		t.Error("expected error for missing plugin dir")
	}
}

func TestClassifyError_KnownSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, plugin.ResultSuccess},
		{"signature invalid", plugin.ErrSignatureInvalid, plugin.ResultSignature},
		{"signature missing", plugin.ErrSignatureMissing, plugin.ResultSignature},
		{"no trusted keys", plugin.ErrNoTrustedKeys, plugin.ResultNoTrustedKeys},
		{"manifest invalid", plugin.ErrManifestInvalid, plugin.ResultManifest},
		{"manifest missing", plugin.ErrManifestMissing, plugin.ResultManifestMiss},
		{"artifact missing", plugin.ErrArtifactMissing, plugin.ResultArtifact},
		{"unsafe artifact", plugin.ErrUnsafeArtifact, plugin.ResultUnsafeArtifact},
		{"duplicate name", plugin.ErrDuplicateName, plugin.ResultDuplicate},
		{"abi mismatch", &plugin.ABIMismatchError{Want: "v1", Got: "v0"}, plugin.ResultABIMismatch},
		{"unknown wrapped", errors.New("anything else"), plugin.ResultLoad},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := plugin.ClassifyError(tc.err); got != tc.want {
				t.Errorf("ClassifyError(%v)=%s want %s", tc.err, got, tc.want)
			}
		})
	}
}

// registryLoader bridges *capability.Registry into plugin.Loader.
type registryLoader struct{ reg *capability.Registry }

func (r *registryLoader) Register(reg plugin.Registration) error {
	return r.reg.Register(reg.Handler)
}

func TestPipeline_KeylessVerificationDispatch(t *testing.T) {
	root := t.TempDir()
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)

	pluginDir := filepath.Join(root, "pagerduty")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"pagerduty","version":"1.0.0","abi":"` + plugin.ABIVersion + `","artifact":"plugin.so"}`
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = writeArtifactWithKeyless(t, pluginDir, []byte("pagerduty bytes"), leaf, leafKey)
	// writeArtifactWithKeyless used a fixed filename; rename to "plugin.so" used by manifest.
	if err := os.Rename(filepath.Join(pluginDir, "plugin.so"), filepath.Join(pluginDir, "plugin.so")); err != nil {
		t.Fatal(err)
	}

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{
		pluginDir: &fakePlugin{
			abi:      plugin.ABIVersion,
			manifest: plugin.Manifest{Name: "pagerduty", Version: "1.0.0"},
			caps: []plugin.Registration{
				{Capability: domain.Capability{Name: "pagerduty_create_incident"}, Handler: &fakeHandler{name: "pagerduty_create_incident"}},
			},
		},
	}}

	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root,
		// TrustedKeys deliberately empty: keyless path must engage on its own.
		Keyless: &plugin.KeylessVerifier{
			FulcioRoots: []*x509.Certificate{ca.rootCert},
			TrustedIdentities: []plugin.Identity{{
				SubjectGlob: "https://github.com/felixgeelhaar/*",
				Issuer:      "https://token.actions.githubusercontent.com",
			}},
		},
		Loader: &registryLoader{reg: reg},
		Opener: opener,
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("Errors=%+v", res.Errors)
	}
	if len(res.Loaded) != 1 {
		t.Errorf("Loaded=%+v", res.Loaded)
	}
}

func TestPipeline_KeylessIdentityMismatchRejected(t *testing.T) {
	root := t.TempDir()
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/attacker/evil/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)

	pluginDir := filepath.Join(root, "evil")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"evil","version":"1.0.0","abi":"` + plugin.ABIVersion + `","artifact":"plugin.so"}`
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = writeArtifactWithKeyless(t, pluginDir, []byte("evil bytes"), leaf, leafKey)

	reg := capability.New()
	opener := &fakeOpener{plugins: map[string]plugin.Plugin{}}

	res, err := plugin.RunPipeline(context.Background(), plugin.PipelineConfig{
		Dir: root,
		Keyless: &plugin.KeylessVerifier{
			FulcioRoots: []*x509.Certificate{ca.rootCert},
			TrustedIdentities: []plugin.Identity{{
				SubjectGlob: "https://github.com/felixgeelhaar/*",
				Issuer:      "https://token.actions.githubusercontent.com",
			}},
		},
		Loader: &registryLoader{reg: reg},
		Opener: opener,
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(res.Loaded) != 0 {
		t.Errorf("Loaded=%+v want empty", res.Loaded)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0].Err, plugin.ErrIdentityMismatch) {
		t.Errorf("Errors=%+v want ErrIdentityMismatch", res.Errors)
	}
}
