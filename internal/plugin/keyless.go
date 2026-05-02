package plugin

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CertificateExtension is the suffix Praxis appends to a plugin
// artefact path to locate the keyless certificate emitted by Fulcio.
// `cosign sign-blob --output-certificate plugin.so.cert` produces this
// file unchanged.
const CertificateExtension = ".cert"

// Sentinel errors. Operators branch on these to log precise reasons
// in strict-mode failures.
var (
	ErrCertificateMissing   = errors.New("plugin certificate file not found")
	ErrCertificateUntrusted = errors.New("plugin certificate did not chain to a trusted Fulcio root")
	ErrCertificateExpired   = errors.New("plugin certificate expired or not yet valid")
	ErrIdentityMismatch     = errors.New("plugin certificate identity not in trust policy")
	ErrNoFulcioRoots        = errors.New("no Fulcio roots configured for keyless verification")
)

// Sigstore Fulcio issues short-lived certificates that encode the
// signing identity as SAN URIs and the OIDC issuer as a custom X.509
// extension. The OIDs below are stable across cosign / Fulcio releases.
//
//	1.3.6.1.4.1.57264.1.1 -- legacy OIDC Issuer
//	1.3.6.1.4.1.57264.1.8 -- OIDC Issuer V2 (RFC 5280 IA5String wrapped)
var (
	oidOIDCIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidOIDCIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
)

// Identity is one allowed (subject, issuer) pair. SubjectGlob accepts
// a literal SAN value or a single-asterisk suffix wildcard
// ("https://github.com/felixgeelhaar/*"). Issuer is matched literally
// against the OIDC issuer extension.
type Identity struct {
	SubjectGlob string
	Issuer      string
}

// KeylessVerifier holds the trust policy for Fulcio-issued plugin
// signatures. Zero value is unusable: callers must populate at least
// FulcioRoots and TrustedIdentities before VerifyKeyless will succeed.
//
// FulcioRoots is the operator's pinned root bundle (typically the
// production Sigstore root + any private Fulcio instance). Intermediates
// is optional — `cosign sign-blob` already attaches the chain inside
// the leaf cert when present.
//
// TrustedIdentities is the allowlist that gates which build identities
// may produce loadable plugins. Any cert whose (SAN, issuer) pair fails
// to match every entry is rejected with ErrIdentityMismatch.
//
// Now is injected so tests can pin a deterministic clock; production
// callers leave it nil for time.Now.
type KeylessVerifier struct {
	FulcioRoots       []*x509.Certificate
	Intermediates     []*x509.Certificate
	TrustedIdentities []Identity
	Now               func() time.Time
}

// LoadFulcioRoots reads PEM-encoded certificates (one or many per file)
// from each path. Mixed PEM bundles are supported so operators can
// drop the standard Sigstore `root.pem` in unchanged.
func LoadFulcioRoots(paths []string) ([]*x509.Certificate, error) {
	out := []*x509.Certificate{}
	for _, p := range paths {
		certs, err := readCertificates(p)
		if err != nil {
			return nil, fmt.Errorf("fulcio root %s: %w", p, err)
		}
		out = append(out, certs...)
	}
	return out, nil
}

func readCertificates(path string) ([]*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var certs []*x509.Certificate
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM CERTIFICATE blocks found")
	}
	return certs, nil
}

// VerifyKeyless validates that the discovered plugin was signed under
// a Fulcio-issued certificate that:
//
//  1. chains to a trusted root,
//  2. was valid at signing time,
//  3. encodes a (SAN, issuer) pair on the operator's allowlist, and
//  4. produced an ECDSA signature that verifies over SHA-256(artifact).
//
// The artefact's signature still lives at <artifact>.sig (cosign
// sign-blob format), and the certificate at <artifact>.cert. Returns
// nil only when every check passes.
func VerifyKeyless(d Discovered, v KeylessVerifier) error {
	if len(v.FulcioRoots) == 0 {
		return ErrNoFulcioRoots
	}

	cert, err := loadLeafCertificate(d.Artifact)
	if err != nil {
		return err
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	if err := chainToFulcio(cert, v.Intermediates, v.FulcioRoots, now()); err != nil {
		return err
	}

	if err := matchIdentity(cert, v.TrustedIdentities); err != nil {
		return err
	}

	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: leaf public key is not ECDSA", ErrSignatureInvalid)
	}
	return verifyArtefactSignature(d.Artifact, []*ecdsa.PublicKey{pub})
}

func loadLeafCertificate(artifact string) (*x509.Certificate, error) {
	certPath := artifact + CertificateExtension
	raw, err := os.ReadFile(certPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrCertificateMissing, filepath.Clean(certPath))
		}
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	if cert, perr := parsePEMCertificate(raw); perr == nil {
		return cert, nil
	}
	// cosign sign-blob's --output-certificate output is base64-
	// encoded raw DER on some 2.x builds (no PEM headers). Try that
	// before giving up.
	if cert, derr := parseBase64Certificate(raw); derr == nil {
		return cert, nil
	}
	return nil, fmt.Errorf("%w: certificate is neither PEM nor base64-encoded DER", ErrCertificateUntrusted)
}

func parsePEMCertificate(raw []byte) (*x509.Certificate, error) {
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errors.New("no PEM CERTIFICATE block")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
}

func parseBase64Certificate(raw []byte) (*x509.Certificate, error) {
	trimmed := bytes.TrimSpace(raw)
	decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
	if err != nil {
		return nil, err
	}
	// cosign 2.x base64-encodes the *PEM block*, not raw DER. Try
	// parsing the decoded bytes as PEM first, then fall back to
	// treating them as DER.
	if cert, perr := parsePEMCertificate(decoded); perr == nil {
		return cert, nil
	}
	return x509.ParseCertificate(decoded)
}

func chainToFulcio(leaf *x509.Certificate, intermediates, roots []*x509.Certificate, now time.Time) error {
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("%w: now=%s notBefore=%s notAfter=%s",
			ErrCertificateExpired, now.UTC().Format(time.RFC3339),
			leaf.NotBefore.UTC().Format(time.RFC3339),
			leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	rootPool := x509.NewCertPool()
	for _, r := range roots {
		rootPool.AddCert(r)
	}
	intPool := x509.NewCertPool()
	for _, i := range intermediates {
		intPool.AddCert(i)
	}
	opts := x509.VerifyOptions{
		Roots:         rootPool,
		Intermediates: intPool,
		CurrentTime:   now,
		// Fulcio leaves only carry CodeSigning EKU. Allow any so we
		// don't have to special-case private Fulcios that omit it.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("%w: %v", ErrCertificateUntrusted, err)
	}
	return nil
}

func matchIdentity(cert *x509.Certificate, allowed []Identity) error {
	if len(allowed) == 0 {
		// Empty policy = deny all. Forces operators to declare intent.
		return fmt.Errorf("%w: trust policy is empty", ErrIdentityMismatch)
	}
	subjects := certSubjects(cert)
	issuer := oidcIssuer(cert)
	for _, id := range allowed {
		if id.Issuer != "" && id.Issuer != issuer {
			continue
		}
		for _, s := range subjects {
			if matchSubjectGlob(id.SubjectGlob, s) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: subjects=%v issuer=%q", ErrIdentityMismatch, subjects, issuer)
}

func certSubjects(cert *x509.Certificate) []string {
	out := make([]string, 0, len(cert.URIs)+len(cert.EmailAddresses)+len(cert.DNSNames))
	for _, u := range cert.URIs {
		out = append(out, u.String())
	}
	out = append(out, cert.EmailAddresses...)
	out = append(out, cert.DNSNames...)
	return out
}

func oidcIssuer(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		switch {
		case ext.Id.Equal(oidOIDCIssuerV2):
			var s string
			if _, err := asn1.Unmarshal(ext.Value, &s); err == nil && s != "" {
				return s
			}
		case ext.Id.Equal(oidOIDCIssuerV1):
			return string(ext.Value)
		}
	}
	return ""
}

func matchSubjectGlob(glob, subject string) bool {
	if glob == "" {
		return false
	}
	if glob == subject {
		return true
	}
	if strings.HasSuffix(glob, "*") {
		return strings.HasPrefix(subject, strings.TrimSuffix(glob, "*"))
	}
	return false
}

// verifyArtefactSignature reuses the cosign-blob signature path; Fulcio
// signing differs only in *where* the public key comes from.
func verifyArtefactSignature(artifact string, keys []*ecdsa.PublicKey) error {
	d := Discovered{Artifact: artifact}
	return VerifyDiscovered(d, keys)
}
