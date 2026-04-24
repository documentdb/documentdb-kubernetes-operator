package tlscerts

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"strings"
	"testing"
	"time"
)

func TestGenerateRejectsEmptySANs(t *testing.T) {
	if _, err := Generate(GenerateOptions{}); err == nil {
		t.Fatalf("expected error for empty SANs")
	}
}

func TestGenerateProducesVerifiableChain(t *testing.T) {
	b, err := Generate(GenerateOptions{
		CommonName:  "gw.test",
		DNSNames:    []string{"gw.test", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		Validity:    1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for name, pemBytes := range map[string][]byte{
		"ca.crt":  b.CACertPEM,
		"ca.key":  b.CAKeyPEM,
		"tls.crt": b.ServerCertPEM,
		"tls.key": b.ServerKeyPEM,
	} {
		if len(pemBytes) == 0 {
			t.Fatalf("%s empty", name)
		}
		if blk, _ := pem.Decode(pemBytes); blk == nil {
			t.Fatalf("%s not valid PEM", name)
		}
	}

	caBlock, _ := pem.Decode(b.CACertPEM)
	if caBlock == nil {
		t.Fatal("decode CA")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	if !caCert.IsCA {
		t.Fatal("CA.IsCA = false")
	}
	srvBlock, _ := pem.Decode(b.ServerCertPEM)
	srvCert, err := x509.ParseCertificate(srvBlock.Bytes)
	if err != nil {
		t.Fatalf("parse server: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := srvCert.Verify(x509.VerifyOptions{
		Roots:       pool,
		DNSName:     "gw.test",
		CurrentTime: time.Now(),
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !containsString(srvCert.DNSNames, "localhost") {
		t.Fatalf("missing localhost SAN: %v", srvCert.DNSNames)
	}
}

func TestGenerateDefaultValidity(t *testing.T) {
	b, err := Generate(GenerateOptions{DNSNames: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b.ServerCertPEM)
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.NotAfter.Sub(cert.NotBefore) < time.Hour {
		t.Fatalf("validity too short: %s", cert.NotAfter.Sub(cert.NotBefore))
	}
	if !strings.EqualFold(cert.Subject.CommonName, "documentdb-e2e") {
		t.Fatalf("unexpected CN: %s", cert.Subject.CommonName)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
