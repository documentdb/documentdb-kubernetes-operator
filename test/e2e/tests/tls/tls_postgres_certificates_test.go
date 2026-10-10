package tls

import (
	"context"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/tlscerts"
)

var _ = Describe("DocumentDB TLS — PostgreSQL certificates",
	Label(e2e.TLSLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("passes spec.tls.postgres certificates to the backing CNPG Cluster", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "suite env not initialised")

			nsName := namespaces.NamespaceForSpec(e2e.TLSLabel)
			Expect(createIdempotent(ctx, env.Client,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}})).
				To(Succeed(), "create namespace %s", nsName)
			DeferCleanup(func(ctx SpecContext) {
				_ = env.Client.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}})
			})

			const (
				serverSecretName      = "tls-e2e-postgres-server"
				replicationSecretName = "tls-e2e-postgres-replication"
			)
			serverCertificate := generatePostgresCertificate(tlsDocumentDBName, []string{
				tlsDocumentDBName + "-rw",
				tlsDocumentDBName + "-rw." + nsName,
				tlsDocumentDBName + "-rw." + nsName + ".svc",
			})
			replicationCertificate := generatePostgresCertificate("streaming_replica", []string{"localhost"})

			createPostgresTLSSecret(ctx, env.Client, nsName, serverSecretName, serverCertificate)
			createPostgresTLSSecret(ctx, env.Client, nsName, replicationSecretName, replicationCertificate)

			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_postgres_certificates", map[string]string{
					"POSTGRES_REPLICATION_TLS_SECRET": replicationSecretName,
					"POSTGRES_CLIENT_CA_SECRET":       replicationSecretName,
					"POSTGRES_SERVER_TLS_SECRET":      serverSecretName,
					"POSTGRES_SERVER_CA_SECRET":       serverSecretName,
				})

			key := types.NamespacedName{Namespace: cluster.NamespaceName, Name: cluster.DD.Name}
			expected := &cnpgv1.CertificatesConfiguration{
				ReplicationTLSSecret: replicationSecretName,
				ClientCASecret:       replicationSecretName,
				ServerTLSSecret:      serverSecretName,
				ServerCASecret:       serverSecretName,
			}
			Eventually(func(g Gomega) {
				backingCluster := &cnpgv1.Cluster{}
				g.Expect(env.Client.Get(ctx, key, backingCluster)).To(Succeed())
				g.Expect(backingCluster.Spec.Certificates).To(Equal(expected))
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				Should(Succeed(), "CNPG Cluster must preserve spec.tls.postgres certificates")
		})

		It("preserves alternative DNS names when CNPG manages the server certificate", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()
			env := e2e.SuiteEnv()
			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_postgres_alt_dns_names", nil)
			Eventually(func(g Gomega) {
				backing := &cnpgv1.Cluster{}
				g.Expect(env.Client.Get(ctx, types.NamespacedName{
					Namespace: cluster.NamespaceName, Name: cluster.DD.Name,
				}, backing)).To(Succeed())
				g.Expect(backing.Spec.Certificates).NotTo(BeNil())
				g.Expect(backing.Spec.Certificates.ServerAltDNSNames).To(Equal([]string{"postgres-extra.example.test"}))
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				Should(Succeed(), "CNPG rejects serverAltDNSNames with a provided serverTLSSecret; test the managed-server case")
		})
	},
)

func generatePostgresCertificate(commonName string, dnsNames []string) *tlscerts.Bundle {
	GinkgoHelper()
	bundle, err := tlscerts.Generate(tlscerts.GenerateOptions{
		CommonName:  commonName,
		DNSNames:    dnsNames,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		Validity:    time.Hour,
	})
	Expect(err).NotTo(HaveOccurred(), "generate PostgreSQL certificate for %s", commonName)
	return bundle
}

func createPostgresTLSSecret(
	ctx context.Context,
	c client.Client,
	namespace, name string,
	bundle *tlscerts.Bundle,
) {
	GinkgoHelper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:              bundle.ServerCertPEM,
			corev1.TLSPrivateKeyKey:        bundle.ServerKeyPEM,
			corev1.ServiceAccountRootCAKey: bundle.CACertPEM,
		},
	}
	Expect(createIdempotent(ctx, c, secret)).To(Succeed(), "create PostgreSQL TLS secret %s/%s", namespace, name)
}
