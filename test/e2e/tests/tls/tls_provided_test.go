package tls

import (
	"context"
	"crypto/x509"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/documentdb/documentdb-operator/test/e2e"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/tlscerts"
)

// Provided mode points the gateway at a user-supplied kubernetes.io/tls
// Secret that contains the full certificate chain and private key.
// This spec mints a throwaway CA + server cert with the tlscerts
// helper, materialises it as a Secret with the canonical data keys
// (tls.crt, tls.key, ca.crt), wires the DocumentDB CR at it, and
// verifies a TLS mongo connection succeeds while validating the server
// certificate against the locally generated CA.
//
// Because the client connects through a port-forward (SNI = 127.0.0.1),
// we explicitly override ServerName to "localhost" — one of the SANs
// baked into the issued server cert — so hostname verification passes.
// The invariants covered: (a) operator accepts the Provided Secret
// reference, (b) the gateway serves exactly the cert we handed it, and
// (c) the cert's chain validates against the CA bytes we planted.
var _ = Describe("DocumentDB TLS — provided",
	Label(e2e.TLSLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("uses a user-provided TLS secret", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "suite env not initialised")

			nsName := namespaces.NamespaceForSpec(e2e.TLSLabel)
			Expect(createIdempotent(ctx, env.Client,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}})).
				To(Succeed(), "create namespace %s", nsName)

			bundle, err := tlscerts.Generate(tlscerts.GenerateOptions{
				CommonName: "documentdb-e2e",
				DNSNames: []string{
					"localhost",
					"documentdb-service-" + tlsDocumentDBName,
					"documentdb-service-" + tlsDocumentDBName + "." + nsName + ".svc",
					"documentdb-service-" + tlsDocumentDBName + "." + nsName + ".svc.cluster.local",
				},
				IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
				Validity:    1 * time.Hour,
			})
			Expect(err).NotTo(HaveOccurred(), "generate TLS bundle")

			secretName := "tls-e2e-provided-cert"
			tlsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: nsName},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:              bundle.ServerCertPEM,
					corev1.TLSPrivateKeyKey:        bundle.ServerKeyPEM,
					corev1.ServiceAccountRootCAKey: bundle.CACertPEM, // "ca.crt"
				},
			}
			Expect(createIdempotent(ctx, env.Client, tlsSecret)).
				To(Succeed(), "create provided TLS secret")

			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_provided", map[string]string{
					"TLS_SECRET_NAME": secretName,
				})
			Expect(cluster.NamespaceName).To(Equal(nsName))

			host, port, stop := openGatewayForward(ctx, cluster.DD)
			defer stop()

			connectCtx, cancelConnect := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
			defer cancelConnect()

			pool := x509.NewCertPool()
			Expect(pool.AppendCertsFromPEM(bundle.CACertPEM)).
				To(BeTrue(), "parse self-minted CA PEM")

			client, err := mongohelper.NewClient(connectCtx, mongohelper.ClientOptions{
				Host:       host,
				Port:       port,
				User:       tlsCredentialUser,
				Password:   tlsCredentialPassword,
				TLS:        true,
				RootCAs:    pool,
				ServerName: "localhost", // matches a SAN in the issued server cert
			})
			Expect(err).NotTo(HaveOccurred(), "TLS connect with provided cert")
			defer func() { _ = client.Disconnect(ctx) }()

			Eventually(func() error {
				return mongohelper.Ping(connectCtx, client)
			}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "ping via provided cert should succeed under CA verification")
		})
	},
)
