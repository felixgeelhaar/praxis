package plugin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/praxis/internal/plugin"
)

// genKeyPEM generates a fresh P-256 ECDSA keypair and writes the public
// key as a PEM file under dir. Returns the private key (for signing in
// the test) and the path to the .pub file.
func genKeyPEM(t *testing.T, dir, name string) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	path := filepath.Join(dir, name+".pub")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}), 0o644); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	return priv, path
}

// signBlob signs blob the way `cosign sign-blob` does: SHA-256 digest,
// ECDSA-ASN1, base64-encoded.
func signBlob(t *testing.T, priv *ecdsa.PrivateKey, blob []byte) string {
	t.Helper()
	digest := sha256.Sum256(blob)
	der, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func setupSignedPlugin(t *testing.T, root, name string, priv *ecdsa.PrivateKey, tampered bool) plugin.Discovered {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	artefact := []byte("plugin-binary-bytes-" + name)
	artefactPath := filepath.Join(dir, "plugin.so")
	if err := os.WriteFile(artefactPath, artefact, 0o644); err != nil {
		t.Fatalf("write artefact: %v", err)
	}
	sig := signBlob(t, priv, artefact)
	if tampered {
		// Modify artefact after signing — sig should fail to verify.
		if err := os.WriteFile(artefactPath, []byte("tampered"), 0o644); err != nil {
			t.Fatalf("tamper: %v", err)
		}
	}
	if err := os.WriteFile(artefactPath+".sig", []byte(sig), 0o644); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	return plugin.Discovered{Dir: dir, Artifact: artefactPath}
}

func TestLoadTrustedKeys_ReadsPEMECDSA(t *testing.T) {
	dir := t.TempDir()
	_, path := genKeyPEM(t, dir, "k1")

	keys, err := plugin.LoadTrustedKeys([]string{path})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("keys=%d want 1", len(keys))
	}
}

func TestLoadTrustedKeys_RejectsMissingFile(t *testing.T) {
	if _, err := plugin.LoadTrustedKeys([]string{filepath.Join(t.TempDir(), "nope.pub")}); err == nil {
		t.Error("expected error for missing key file")
	}
}

func TestLoadTrustedKeys_RejectsNonECDSA(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.pub")
	if err := os.WriteFile(path, []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.LoadTrustedKeys([]string{path}); err == nil {
		t.Error("expected error for non-PEM input")
	}
}

func TestVerifyDiscovered_ValidSignatureAccepted(t *testing.T) {
	dir := t.TempDir()
	priv, pub := genKeyPEM(t, dir, "trusted")
	keys, err := plugin.LoadTrustedKeys([]string{pub})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	d := setupSignedPlugin(t, dir, "good", priv, false)
	if err := plugin.VerifyDiscovered(d, keys); err != nil {
		t.Errorf("VerifyDiscovered: %v", err)
	}
}

func TestVerifyDiscovered_WrongKeyRejected(t *testing.T) {
	dir := t.TempDir()
	signer, _ := genKeyPEM(t, dir, "signer")
	_, otherPub := genKeyPEM(t, dir, "other")
	keys, _ := plugin.LoadTrustedKeys([]string{otherPub})
	d := setupSignedPlugin(t, dir, "wrong-key", signer, false)

	if err := plugin.VerifyDiscovered(d, keys); !errors.Is(err, plugin.ErrSignatureInvalid) {
		t.Errorf("err=%v want ErrSignatureInvalid", err)
	}
}

func TestVerifyDiscovered_TamperedArtifactRejected(t *testing.T) {
	dir := t.TempDir()
	priv, pub := genKeyPEM(t, dir, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	d := setupSignedPlugin(t, dir, "tamper", priv, true)

	if err := plugin.VerifyDiscovered(d, keys); !errors.Is(err, plugin.ErrSignatureInvalid) {
		t.Errorf("err=%v want ErrSignatureInvalid", err)
	}
}

func TestVerifyDiscovered_MissingSignatureRejected(t *testing.T) {
	dir := t.TempDir()
	_, pub := genKeyPEM(t, dir, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})

	pluginDir := filepath.Join(dir, "no-sig")
	_ = os.MkdirAll(pluginDir, 0o755)
	artefact := filepath.Join(pluginDir, "plugin.so")
	_ = os.WriteFile(artefact, []byte("data"), 0o644)
	d := plugin.Discovered{Dir: pluginDir, Artifact: artefact}

	if err := plugin.VerifyDiscovered(d, keys); !errors.Is(err, plugin.ErrSignatureMissing) {
		t.Errorf("err=%v want ErrSignatureMissing", err)
	}
}

func TestVerifyDiscovered_NoTrustedKeysRejected(t *testing.T) {
	dir := t.TempDir()
	priv, _ := genKeyPEM(t, dir, "k")
	d := setupSignedPlugin(t, dir, "no-keys", priv, false)

	if err := plugin.VerifyDiscovered(d, nil); !errors.Is(err, plugin.ErrNoTrustedKeys) {
		t.Errorf("err=%v want ErrNoTrustedKeys", err)
	}
}

func TestVerifyDiscovered_AnyTrustedKeyMatches(t *testing.T) {
	dir := t.TempDir()
	priv1, pub1 := genKeyPEM(t, dir, "k1")
	_, pub2 := genKeyPEM(t, dir, "k2")
	keys, _ := plugin.LoadTrustedKeys([]string{pub2, pub1}) // signer is second
	d := setupSignedPlugin(t, dir, "multi", priv1, false)

	if err := plugin.VerifyDiscovered(d, keys); err != nil {
		t.Errorf("multi-key trust should accept: %v", err)
	}
}

func TestVerifyDiscovered_GarbageSignatureRejected(t *testing.T) {
	dir := t.TempDir()
	priv, pub := genKeyPEM(t, dir, "k")
	keys, _ := plugin.LoadTrustedKeys([]string{pub})
	d := setupSignedPlugin(t, dir, "garbage", priv, false)
	// Overwrite signature with junk.
	if err := os.WriteFile(d.Artifact+".sig", []byte("@@@ not base64 @@@"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plugin.VerifyDiscovered(d, keys); !errors.Is(err, plugin.ErrSignatureInvalid) {
		t.Errorf("err=%v want ErrSignatureInvalid", err)
	}
}
