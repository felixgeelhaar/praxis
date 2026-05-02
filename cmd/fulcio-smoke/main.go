// Command fulcio-smoke drives plugin.VerifyKeyless against a
// real cosign-blob signed artefact. Used by the fulcio-smoke.yml
// workflow to validate the keyless trust pipeline end-to-end against
// production Sigstore infrastructure (Fulcio + GitHub OIDC).
//
// Inputs:
//
//	--artifact      path to the binary to verify
//	--fulcio-root   path to the Fulcio root PEM bundle
//	--subject-glob  allowed SAN glob (eg https://github.com/owner/*)
//	--issuer        OIDC issuer (eg https://token.actions.githubusercontent.com)
//
// Exit 0 on success; 1 with diagnostic on failure.
package main

import (
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"

	"github.com/felixgeelhaar/praxis/internal/plugin"
)

func main() {
	artifact := flag.String("artifact", "", "path to the artefact to verify")
	root := flag.String("fulcio-root", "", "path to a PEM bundle of trusted Fulcio roots")
	intermediate := flag.String("fulcio-intermediate", "", "optional path to a PEM bundle of Fulcio intermediates")
	subject := flag.String("subject-glob", "", "allowed SAN glob")
	issuer := flag.String("issuer", "", "OIDC issuer")
	flag.Parse()

	if *artifact == "" || *root == "" || *subject == "" || *issuer == "" {
		fmt.Fprintln(os.Stderr, "all flags required: --artifact --fulcio-root --subject-glob --issuer")
		os.Exit(2)
	}

	roots, err := plugin.LoadFulcioRoots([]string{*root})
	if err != nil {
		fmt.Fprintln(os.Stderr, "load roots:", err)
		os.Exit(1)
	}
	var intermediates []*x509.Certificate
	if *intermediate != "" {
		intermediates, err = plugin.LoadFulcioRoots([]string{*intermediate})
		if err != nil {
			fmt.Fprintln(os.Stderr, "load intermediates:", err)
			os.Exit(1)
		}
	}
	v := plugin.KeylessVerifier{
		FulcioRoots:   roots,
		Intermediates: intermediates,
		TrustedIdentities: []plugin.Identity{{
			SubjectGlob: *subject,
			Issuer:      *issuer,
		}},
	}
	if err := plugin.VerifyKeyless(plugin.Discovered{Artifact: *artifact}, v); err != nil {
		fmt.Fprintln(os.Stderr, "VerifyKeyless:", err)
		dumpCert(*artifact + plugin.CertificateExtension)
		os.Exit(1)
	}
	fmt.Println("OK: VerifyKeyless succeeded against real Fulcio cert")
}

func dumpCert(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stderr, "cert SANs (URI):", c.URIs)
	fmt.Fprintln(os.Stderr, "cert Subject:", c.Subject)
	fmt.Fprintln(os.Stderr, "cert NotBefore:", c.NotBefore)
	fmt.Fprintln(os.Stderr, "cert NotAfter:", c.NotAfter)
}
