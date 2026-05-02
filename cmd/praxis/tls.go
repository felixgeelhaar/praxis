package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
)

// tlsLoader holds the most recently loaded server certificate behind
// an atomic pointer so SIGHUP can swap a fresh cert into the running
// server without dropping connections. The TLSConfig.GetCertificate
// hook reads the pointer on every TLS handshake.
//
// Phase 6 mTLS.
type tlsLoader struct {
	certFile string
	keyFile  string
	caFile   string

	current atomic.Pointer[tls.Certificate]
}

func newTLSLoader(certFile, keyFile, caFile string) (*tlsLoader, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("tlsLoader: cert and key paths required")
	}
	t := &tlsLoader{certFile: certFile, keyFile: keyFile, caFile: caFile}
	if err := t.Reload(); err != nil {
		return nil, err
	}
	return t, nil
}

// Reload reads the cert and key from disk and atomically swaps them
// into the active pointer. Existing TLS handshakes complete with the
// previous cert; new handshakes pick up the new one.
func (t *tlsLoader) Reload() error {
	cert, err := tls.LoadX509KeyPair(t.certFile, t.keyFile)
	if err != nil {
		return fmt.Errorf("load TLS keypair from %s + %s: %w", t.certFile, t.keyFile, err)
	}
	t.current.Store(&cert)
	return nil
}

// Cert returns the current certificate. Used as the GetCertificate
// hook on tls.Config; the *tls.ClientHelloInfo argument is ignored
// because we do not select per-SNI today.
func (t *tlsLoader) Cert(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c := t.current.Load()
	if c == nil {
		return nil, errors.New("tlsLoader: no cert loaded")
	}
	return c, nil
}

// TLSConfig builds a *tls.Config wired to the loader's GetCertificate
// hook. When the loader's caFile is non-empty, mTLS is enabled:
// the client cert is required and verified against that CA bundle.
func (t *tlsLoader) TLSConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: t.Cert,
	}
	if t.caFile != "" {
		raw, err := os.ReadFile(t.caFile)
		if err != nil {
			return nil, fmt.Errorf("read mTLS client CA %s: %w", t.caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return nil, fmt.Errorf("mTLS client CA %s: no PEM certificates found", t.caFile)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
