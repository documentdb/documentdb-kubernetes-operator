// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mintSelfSignedPEM returns a short-lived self-signed cert's PEM bytes.
// Used only to feed buildTLSConfig a PEM it can parse; we never need to
// serve TLS from it.
func mintSelfSignedPEM() []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
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
	Expect(err).NotTo(HaveOccurred())
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

var _ = Describe("BuildURI", func() {
	It("renders the basic mongodb URI form", func() {
		got, err := BuildURI(ClientOptions{
			Host: "gw.example", Port: "10260", User: "alice", Password: "secret",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("mongodb://alice:secret@gw.example:10260/?tls=false&authSource=admin"))
	})

	It("percent-encodes credential metacharacters", func() {
		got, err := BuildURI(ClientOptions{
			Host: "h", Port: "1", User: "a@b", Password: "p@ss:w/rd?&",
		})
		Expect(err).NotTo(HaveOccurred())
		// '@', ':', '/', '?', '&' must all be percent-encoded so the driver
		// doesn't mis-parse the URI.
		for _, bad := range []string{"a@b:", "@ss:", "w/rd?", "?&@"} {
			Expect(strings.Contains(got, bad)).To(BeFalse(), "uri must escape %q; got %s", bad, got)
		}
		Expect(got).To(ContainSubstring("a%40b"))
		Expect(got).To(ContainSubstring("p%40ss%3Aw%2Frd%3F%26"))
	})

	It("propagates the TLS flag into the query string", func() {
		on, err := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p", TLS: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(on).To(ContainSubstring("tls=true"))

		off, err := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p", TLS: false})
		Expect(err).NotTo(HaveOccurred())
		Expect(off).To(ContainSubstring("tls=false"))
	})

	It("respects an authDB override and defaults to admin", func() {
		got, err := BuildURI(ClientOptions{
			Host: "h", Port: "1", User: "u", Password: "p", AuthDB: "mydb",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("authSource=mydb"))

		def, err := BuildURI(ClientOptions{Host: "h", Port: "1", User: "u", Password: "p"})
		Expect(err).NotTo(HaveOccurred())
		Expect(def).To(ContainSubstring("authSource=admin"))
	})

	DescribeTable("rejects incomplete options",
		func(opts ClientOptions) {
			_, err := BuildURI(opts)
			Expect(err).To(HaveOccurred())
		},
		Entry("missing host", ClientOptions{Port: "1", User: "u"}),
		Entry("missing port", ClientOptions{Host: "h", User: "u"}),
		Entry("missing user", ClientOptions{Host: "h", Port: "1"}),
	)
})

var _ = Describe("buildTLSConfig", func() {
	It("prefers an explicit RootCAs pool over CABundlePEM and TLSInsecure", func() {
		pool := x509.NewCertPool()
		cfg, err := buildTLSConfig(ClientOptions{
			TLS:         true,
			RootCAs:     pool,
			CABundlePEM: []byte("ignored"),
			TLSInsecure: true,
			ServerName:  "localhost",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.RootCAs).To(BeIdenticalTo(pool))
		Expect(cfg.InsecureSkipVerify).To(BeFalse())
		Expect(cfg.ServerName).To(Equal("localhost"))
		Expect(cfg.MinVersion).To(BeEquivalentTo(0x0303), "want TLS 1.2")
	})

	It("parses a CABundlePEM into a populated pool", func() {
		cfg, err := buildTLSConfig(ClientOptions{
			TLS:         true,
			CABundlePEM: mintSelfSignedPEM(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.RootCAs).NotTo(BeNil())
	})

	It("fails when CABundlePEM is malformed", func() {
		_, err := buildTLSConfig(ClientOptions{
			TLS:         true,
			CABundlePEM: []byte("not a real pem"),
		})
		Expect(err).To(HaveOccurred())
	})

	It("honours TLSInsecure when no CA material is supplied", func() {
		cfg, err := buildTLSConfig(ClientOptions{TLS: true, TLSInsecure: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.InsecureSkipVerify).To(BeTrue())
	})

	It("returns nil when no CA, insecure flag, or ServerName is supplied", func() {
		cfg, err := buildTLSConfig(ClientOptions{TLS: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(BeNil())
	})

	It("preserves a ServerName-only config without RootCAs or InsecureSkipVerify", func() {
		cfg, err := buildTLSConfig(ClientOptions{TLS: true, ServerName: "gw.example"})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.ServerName).To(Equal("gw.example"))
		Expect(cfg.RootCAs).To(BeNil())
		Expect(cfg.InsecureSkipVerify).To(BeFalse())
	})
})
