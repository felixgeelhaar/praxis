package plugin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/praxis/internal/plugin"
)

type testCA struct {
	rootKey  *ecdsa.PrivateKey
	rootCert *x509.Certificate
	rootDER  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen root key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Fulcio Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	return &testCA{rootKey: priv, rootCert: cert, rootDER: der}
}

// issueLeaf mints a Fulcio-style leaf cert with the given SAN URI and
// OIDC issuer extension, signed by the test root.
func (ca *testCA) issueLeaf(t *testing.T, sanURI, issuer string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen leaf key: %v", err)
	}
	u, err := url.Parse(sanURI)
	if err != nil {
		t.Fatalf("parse SAN: %v", err)
	}
	issuerExt, err := asn1.Marshal(issuer)
	if err != nil {
		t.Fatalf("marshal issuer: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "plugin-signer"},
		NotBefore:    time.Now().Add(-10 * time.Minute),
		NotAfter:     time.Now().Add(10 * time.Minute),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		URIs:         []*url.URL{u},
		ExtraExtensions: []pkix.Extension{
			{
				Id:    asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8},
				Value: issuerExt,
			},
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.rootCert, &priv.PublicKey, ca.rootKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert, priv
}

func writeArtifactWithKeyless(t *testing.T, dir string, payload []byte, leaf *x509.Certificate, leafKey *ecdsa.PrivateKey) string {
	t.Helper()
	artifact := filepath.Join(dir, "plugin.so")
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := os.WriteFile(artifact+plugin.SignatureExtension,
		[]byte(base64.StdEncoding.EncodeToString(sig)), 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	if err := os.WriteFile(artifact+plugin.CertificateExtension, pemCert, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return artifact
}

func TestVerifyKeyless_HappyPath(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("plugin payload"), leaf, leafKey)

	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{
			{
				SubjectGlob: "https://github.com/felixgeelhaar/*",
				Issuer:      "https://token.actions.githubusercontent.com",
			},
		},
	}
	if err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v); err != nil {
		t.Fatalf("VerifyKeyless: %v", err)
	}
}

func TestVerifyKeyless_NoFulcioRoots(t *testing.T) {
	if err := plugin.VerifyKeyless(plugin.Discovered{Artifact: "any"},
		plugin.KeylessVerifier{}); !errors.Is(err, plugin.ErrNoFulcioRoots) {
		t.Errorf("err=%v want ErrNoFulcioRoots", err)
	}
}

func TestVerifyKeyless_MissingCert(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	artifact := filepath.Join(dir, "plugin.so")
	if err := os.WriteFile(artifact, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	v := plugin.KeylessVerifier{
		FulcioRoots:       []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{SubjectGlob: "*"}},
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrCertificateMissing) {
		t.Errorf("err=%v want ErrCertificateMissing", err)
	}
}

func TestVerifyKeyless_UntrustedRoot(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)

	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{other.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrCertificateUntrusted) {
		t.Errorf("err=%v want ErrCertificateUntrusted", err)
	}
}

func TestVerifyKeyless_IdentityMismatch(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/attacker/evil/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)

	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrIdentityMismatch) {
		t.Errorf("err=%v want ErrIdentityMismatch", err)
	}
}

func TestVerifyKeyless_IssuerMismatch(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://accounts.google.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)

	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrIdentityMismatch) {
		t.Errorf("err=%v want ErrIdentityMismatch", err)
	}
}

func TestVerifyKeyless_TamperedArtefact(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)
	if err := os.WriteFile(artifact, []byte("TAMPERED"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrSignatureInvalid) {
		t.Errorf("err=%v want ErrSignatureInvalid", err)
	}
}

func TestVerifyKeyless_ExpiredCert(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)

	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
		Now: func() time.Time { return time.Now().Add(48 * time.Hour) },
	}
	err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v)
	if !errors.Is(err, plugin.ErrCertificateExpired) {
		t.Errorf("err=%v want ErrCertificateExpired", err)
	}
}

func TestLoadFulcioRoots_RoundTrip(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.rootDER}), 0o600); err != nil {
		t.Fatalf("write root: %v", err)
	}
	roots, err := plugin.LoadFulcioRoots([]string{path})
	if err != nil {
		t.Fatalf("LoadFulcioRoots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("len=%d want 1", len(roots))
	}
	if !roots[0].Equal(ca.rootCert) {
		t.Errorf("loaded root != original")
	}
}

func TestVerifyKeyless_AcceptsBase64Cert(t *testing.T) {
	ca := newTestCA(t)
	leaf, leafKey := ca.issueLeaf(t,
		"https://github.com/felixgeelhaar/praxis/.github/workflows/release.yml@refs/tags/v1.0.0",
		"https://token.actions.githubusercontent.com",
	)
	dir := t.TempDir()
	artifact := writeArtifactWithKeyless(t, dir, []byte("payload"), leaf, leafKey)
	// Replace the PEM cert with a base64-only DER (cosign 2.x format).
	if err := os.WriteFile(artifact+plugin.CertificateExtension,
		[]byte(base64.StdEncoding.EncodeToString(leaf.Raw)), 0o600); err != nil {
		t.Fatalf("rewrite cert: %v", err)
	}
	v := plugin.KeylessVerifier{
		FulcioRoots: []*x509.Certificate{ca.rootCert},
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: "https://github.com/felixgeelhaar/*",
			Issuer:      "https://token.actions.githubusercontent.com",
		}},
	}
	if err := plugin.VerifyKeyless(plugin.Discovered{Artifact: artifact}, v); err != nil {
		t.Errorf("VerifyKeyless on base64-DER cert: %v", err)
	}
}

func TestLoadFulcioRoots_BadPath(t *testing.T) {
	_, err := plugin.LoadFulcioRoots([]string{"/nonexistent/root.pem"})
	if err == nil {
		t.Error("expected error for missing path")
	}
}
