package plugin

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// SignatureExtension is the suffix Praxis appends to a plugin artefact
// path to locate its detached signature. Keeping the convention identical
// to `cosign sign-blob --output-signature plugin.so.sig` so operators can
// sign with the upstream tool unchanged.
const SignatureExtension = ".sig"

// Sentinel errors. Callers branch on these to decide between "this load
// is forbidden, refuse" and "operator misconfiguration, fail loud".
var (
	ErrSignatureMissing = errors.New("plugin signature file not found")
	ErrSignatureInvalid = errors.New("plugin signature does not verify under any trusted key")
	ErrNoTrustedKeys    = errors.New("no trusted plugin keys configured")
)

// LoadTrustedKeys reads each PEM-encoded ECDSA P-256 public key from the
// given paths and returns the parsed bundle. Any unreadable file or
// non-ECDSA key fails the load — partial trust silently dropping a key
// would let an operator believe a stricter policy was active than really
// is.
func LoadTrustedKeys(paths []string) ([]*ecdsa.PublicKey, error) {
	out := make([]*ecdsa.PublicKey, 0, len(paths))
	for _, p := range paths {
		key, err := readECDSAPublicKey(p)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", p, err)
		}
		out = append(out, key)
	}
	return out, nil
}

func readECDSAPublicKey(path string) (*ecdsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("not PEM-encoded")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("expected ECDSA public key, got %T", pub)
	}
	return ec, nil
}

// VerifyDiscovered verifies the cosign-blob signature for a Discovered
// plugin against the configured trust bundle. The signature is expected
// at <artifact>.sig as base64-encoded ASN.1 DER over SHA-256(artifact),
// matching `cosign sign-blob` output exactly.
//
// Returns:
//   - nil on a successful verification.
//   - ErrNoTrustedKeys when keys is empty (fail-closed: never load
//     unsigned plugins by accident if trust isn't configured).
//   - ErrSignatureMissing if the .sig file is absent.
//   - ErrSignatureInvalid if no trusted key validates the signature
//     (covers wrong key, tampered artefact, malformed sig).
func VerifyDiscovered(d Discovered, keys []*ecdsa.PublicKey) error {
	if len(keys) == 0 {
		return ErrNoTrustedKeys
	}

	sigPath := d.Artifact + SignatureExtension
	sigRaw, err := os.ReadFile(sigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrSignatureMissing, sigPath)
		}
		return fmt.Errorf("read signature: %w", err)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid base64", ErrSignatureInvalid)
	}

	digest, err := sha256File(d.Artifact)
	if err != nil {
		return fmt.Errorf("hash artefact: %w", err)
	}

	for _, k := range keys {
		if ecdsa.VerifyASN1(k, digest, sigBytes) {
			return nil
		}
	}
	return ErrSignatureInvalid
}

func sha256File(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
