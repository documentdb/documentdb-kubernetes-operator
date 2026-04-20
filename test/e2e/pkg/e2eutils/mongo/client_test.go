package mongo

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestBuildURI_Basic(t *testing.T) {
	t.Parallel()
	got, err := BuildURI(ClientOptions{
		Host: "gw.example", Port: "10260", User: "alice", Password: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "mongodb://alice:secret@gw.example:10260/?tls=false&authSource=admin"
	if got != want {
		t.Fatalf("uri mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestBuildURI_EscapesCreds(t *testing.T) {
	t.Parallel()
	got, err := BuildURI(ClientOptions{
		Host: "h", Port: "1", User: "a@b", Password: "p@ss:w/rd?&",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// '@', ':', '/', '?', '&' must all be percent-encoded so the driver
	// doesn't mis-parse the URI.
	for _, bad := range []string{"a@b:", "@ss:", "w/rd?", "?&@"} {
		if strings.Contains(got, bad) {
			t.Fatalf("uri must escape %q; got %s", bad, got)
		}
	}
	if !strings.Contains(got, "a%40b") {
		t.Fatalf("expected user to contain 'a%%40b'; got %s", got)
	}
	if !strings.Contains(got, "p%40ss%3Aw%2Frd%3F%26") {
		t.Fatalf("expected escaped password; got %s", got)
	}
}

func TestBuildURI_TLSFlag(t *testing.T) {
	t.Parallel()
	on, _ := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p", TLS: true})
	if !strings.Contains(on, "tls=true") {
		t.Fatalf("expected tls=true, got %s", on)
	}
	off, _ := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p", TLS: false})
	if !strings.Contains(off, "tls=false") {
		t.Fatalf("expected tls=false, got %s", off)
	}
}

func TestBuildURI_AuthDBOverride(t *testing.T) {
	t.Parallel()
	got, _ := BuildURI(ClientOptions{
		Host: "h", Port: "1", User: "u", Password: "p", AuthDB: "mydb",
	})
	if !strings.Contains(got, "authSource=mydb") {
		t.Fatalf("expected authSource=mydb; got %s", got)
	}
	def, _ := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p"})
	if !strings.Contains(def, "authSource=admin") {
		t.Fatalf("expected default authSource=admin; got %s", def)
	}
}

func TestBuildURI_MissingRequired(t *testing.T) {
	t.Parallel()
	cases := []ClientOptions{
		{Port: "1", User: "u"},
		{Host: "h", User: "u"},
		{Host: "h", Port: "1"},
	}
	for i, c := range cases {
		if _, err := BuildURI(c); err == nil {
			t.Fatalf("case %d: expected error for incomplete opts %+v", i, c)
		}
	}
}

// mintSelfSignedPEM returns a short-lived self-signed cert's PEM bytes.
// Used only to feed buildTLSConfig a PEM it can parse; we never need to
// serve TLS from it.
func mintSelfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestBuildTLSConfig_RootCAsTakesPriority(t *testing.T) {
	t.Parallel()
	pool := x509.NewCertPool()
	cfg, err := buildTLSConfig(ClientOptions{
		TLS:         true,
		RootCAs:     pool,
		CABundlePEM: []byte("ignored"),
		TLSInsecure: true,
		ServerName:  "localhost",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.RootCAs != pool {
		t.Fatal("RootCAs must be the supplied pool, not a parsed bundle")
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must not be set when RootCAs is supplied")
	}
	if cfg.ServerName != "localhost" {
		t.Fatalf("ServerName = %q, want localhost", cfg.ServerName)
	}
	if cfg.MinVersion != 0x0303 { // TLS 1.2
		t.Fatalf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
}

func TestBuildTLSConfig_CABundlePEMParsed(t *testing.T) {
	t.Parallel()
	pemBytes := mintSelfSignedPEM(t)
	cfg, err := buildTLSConfig(ClientOptions{
		TLS:         true,
		CABundlePEM: pemBytes,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("expected RootCAs parsed from PEM")
	}
}

func TestBuildTLSConfig_CABundlePEMInvalid(t *testing.T) {
	t.Parallel()
	if _, err := buildTLSConfig(ClientOptions{
		TLS:         true,
		CABundlePEM: []byte("not a real pem"),
	}); err == nil {
		t.Fatal("expected error for unparseable CABundlePEM")
	}
}

func TestBuildTLSConfig_Insecure(t *testing.T) {
	t.Parallel()
	cfg, err := buildTLSConfig(ClientOptions{TLS: true, TLSInsecure: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
}

func TestBuildTLSConfig_NilWhenNoHintsAndNoServerName(t *testing.T) {
	t.Parallel()
	cfg, err := buildTLSConfig(ClientOptions{TLS: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when no CA/insecure/ServerName supplied, got %+v", cfg)
	}
}

func TestBuildTLSConfig_ServerNameOnlyReturnsConfig(t *testing.T) {
	t.Parallel()
	cfg, err := buildTLSConfig(ClientOptions{TLS: true, ServerName: "gw.example"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || cfg.ServerName != "gw.example" {
		t.Fatalf("expected ServerName preserved, got %+v", cfg)
	}
	if cfg.RootCAs != nil || cfg.InsecureSkipVerify {
		t.Fatal("ServerName-only config must not set RootCAs or InsecureSkipVerify")
	}
}
