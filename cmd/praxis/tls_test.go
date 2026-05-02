package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genSelfSigned writes a freshly generated ECDSA P-256 self-signed
// cert + key pair to dir and returns the paths. Used by the TLS
// loader tests so we don't carry binary fixtures.
func genSelfSigned(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")

	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPem, 0o644); err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPem, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestTLSLoader_NewLoadsCertOnConstruction(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSigned(t, dir, "server")
	loader, err := newTLSLoader(certPath, keyPath, "")
	if err != nil {
		t.Fatalf("newTLSLoader: %v", err)
	}
	cert, err := loader.Cert(nil)
	if err != nil {
		t.Fatalf("Cert: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Error("loaded cert has no DER bytes")
	}
}

func TestTLSLoader_RequiresCertAndKey(t *testing.T) {
	if _, err := newTLSLoader("", "", ""); err == nil {
		t.Error("empty cert + key should error")
	}
}

func TestTLSLoader_FailsOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := newTLSLoader(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"), ""); err == nil {
		t.Error("missing files should error")
	}
}

func TestTLSLoader_ReloadSwapsCertAtomically(t *testing.T) {
	dir := t.TempDir()
	cert1, key1 := genSelfSigned(t, dir, "v1")
	loader, err := newTLSLoader(cert1, key1, "")
	if err != nil {
		t.Fatalf("newTLSLoader: %v", err)
	}
	first, _ := loader.Cert(nil)

	// Overwrite the same paths with a fresh keypair.
	cert2, key2 := genSelfSigned(t, dir, "v2")
	if err := os.Rename(cert2, cert1); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(key2, key1); err != nil {
		t.Fatal(err)
	}
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	second, _ := loader.Cert(nil)
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Error("Reload did not swap the cert")
	}
}

func TestTLSLoader_TLSConfig_TLSOnlyByDefault(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSigned(t, dir, "server")
	loader, _ := newTLSLoader(certPath, keyPath, "")
	cfg, err := loader.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if cfg.ClientAuth != tls.NoClientCert {
		t.Errorf("ClientAuth=%v want NoClientCert when no CA file", cfg.ClientAuth)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion=%x", cfg.MinVersion)
	}
}

func TestTLSLoader_TLSConfig_MTLSRequiresClientCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSigned(t, dir, "server")
	caPath, _ := genSelfSigned(t, dir, "client-ca")

	loader, err := newTLSLoader(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("newTLSLoader: %v", err)
	}
	cfg, err := loader.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth=%v want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs nil despite CA file set")
	}
}

func TestTLSLoader_TLSConfig_FailsOnBadCAFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := genSelfSigned(t, dir, "server")
	bogus := filepath.Join(dir, "not-pem.txt")
	_ = os.WriteFile(bogus, []byte("not a certificate"), 0o644)

	loader, err := newTLSLoader(certPath, keyPath, bogus)
	if err != nil {
		t.Fatalf("newTLSLoader: %v", err)
	}
	if _, err := loader.TLSConfig(); err == nil {
		t.Error("non-PEM CA file should error")
	}
}
