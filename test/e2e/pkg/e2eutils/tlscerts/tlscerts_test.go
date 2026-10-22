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

func TestIssueRotationAndUsages(t *testing.T) {
	ca, err := Generate(GenerateOptions{DNSNames: []string{"ca.test"}})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CACertPEM)
	var previous string
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		for range 2 {
			bundle, err := Issue(ca, GenerateOptions{
				CommonName: "streaming_replica", DNSNames: []string{"postgres.test"},
				ExtKeyUsage: []x509.ExtKeyUsage{usage},
			})
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(bundle.ServerCertPEM)
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if cert.SerialNumber.String() == previous {
				t.Fatal("rotation reused the certificate serial")
			}
			previous = cert.SerialNumber.String()
			if _, err := cert.Verify(x509.VerifyOptions{
				Roots: pool, DNSName: "postgres.test", KeyUsages: []x509.ExtKeyUsage{usage},
			}); err != nil {
				t.Fatal(err)
			}
			other := x509.ExtKeyUsageServerAuth
			if usage == other {
				other = x509.ExtKeyUsageClientAuth
			}
			if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{other}}); err == nil {
				t.Fatal("leaf unexpectedly permits the other authentication usage")
			}
			if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: "wrong.test", KeyUsages: []x509.ExtKeyUsage{usage}}); err == nil {
				t.Fatal("leaf unexpectedly verifies the wrong hostname")
			}
		}
	}
}

func TestIssueRejectsInvalidCA(t *testing.T) {
	valid, err := Generate(GenerateOptions{DNSNames: []string{"ca.test"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := Generate(GenerateOptions{DNSNames: []string{"other.test"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ca := range []*Bundle{
		nil, {}, {CACertPEM: []byte("invalid"), CAKeyPEM: valid.CAKeyPEM},
		{CACertPEM: valid.CACertPEM, CAKeyPEM: []byte("invalid")},
		{CACertPEM: valid.ServerCertPEM, CAKeyPEM: valid.ServerKeyPEM},
		{CACertPEM: valid.CACertPEM, CAKeyPEM: other.CAKeyPEM},
	} {
		if _, err := Issue(ca, GenerateOptions{DNSNames: []string{"server.test"}}); err == nil {
			t.Fatal("expected error for invalid signing CA")
		}
	}
}
