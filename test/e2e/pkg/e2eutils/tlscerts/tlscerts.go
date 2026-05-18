// Package tlscerts generates throwaway TLS material (CA + server
// certificate) suitable for DocumentDB E2E "Provided" mode tests.
//
// The generated artefacts are written into an in-memory struct whose
// PEM fields can be plugged directly into a Kubernetes
// kubernetes.io/tls Secret (tls.crt / tls.key) plus an optional
// ca.crt entry for clients that want to verify the chain.
//
// None of this material is secure: keys are 2048-bit RSA, validity
// windows are short, and no revocation story exists. It is only
// intended for tests.
package tlscerts

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// Bundle is the PEM-encoded material produced by Generate. The fields
// align with the canonical key names used by Kubernetes TLS secrets:
// tls.crt (ServerCertPEM), tls.key (ServerKeyPEM) and the optional
// ca.crt (CACertPEM).
type Bundle struct {
	CACertPEM     []byte
	CAKeyPEM      []byte
	ServerCertPEM []byte
	ServerKeyPEM  []byte
}

// GenerateOptions controls Generate. DNSNames and IPAddresses populate
// the server certificate's SANs; at least one entry is required so
// TLS clients performing hostname verification have something to
// match against. Validity defaults to 24 hours when zero.
type GenerateOptions struct {
	// CommonName is the server certificate's CN. Defaults to
	// "documentdb-e2e" when empty.
	CommonName string
	// DNSNames populates the SAN DNSNames field.
	DNSNames []string
	// IPAddresses populates the SAN IPAddresses field.
	IPAddresses []net.IP
	// Validity defaults to 24 hours when zero.
	Validity time.Duration
}

// Generate builds a self-signed CA and a server certificate signed by
// that CA. Both are returned as PEM-encoded bytes in Bundle.
func Generate(opts GenerateOptions) (*Bundle, error) {
	if len(opts.DNSNames) == 0 && len(opts.IPAddresses) == 0 {
		return nil, fmt.Errorf("tlscerts: at least one DNSName or IPAddress SAN is required")
	}
	validity := opts.Validity
	if validity == 0 {
		validity = 24 * time.Hour
	}
	cn := opts.CommonName
	if cn == "" {
		cn = "documentdb-e2e"
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("tlscerts: generate CA key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "documentdb-e2e-ca"},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tlscerts: sign CA: %w", err)
	}

	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("tlscerts: generate server key: %w", err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     append([]string(nil), opts.DNSNames...),
		IPAddresses:  append([]net.IP(nil), opts.IPAddresses...),
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caTmpl, &srvKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("tlscerts: sign server cert: %w", err)
	}

	return &Bundle{
		CACertPEM:     pemEncode("CERTIFICATE", caDER),
		CAKeyPEM:      pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey)),
		ServerCertPEM: pemEncode("CERTIFICATE", srvDER),
		ServerKeyPEM:  pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(srvKey)),
	}, nil
}

// pemEncode is a tiny wrapper so callers don't need to construct
// pem.Block literals at each call site.
func pemEncode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}
