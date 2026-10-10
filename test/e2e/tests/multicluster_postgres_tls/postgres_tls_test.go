package multiclusterpostgrestls

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/tlscerts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("PostgreSQL certificates across independent Kubernetes clusters",
	Ordered, Label(e2e.TLSLabel, e2e.MultiClusterTLSLabel), e2e.MediumLevelLabel, func() {
		var primary, replica *member
		var serverCA, clientCA, replication, foreign *tlscerts.Bundle
		var primaryHost string
		var replicationApplicationName string

		AfterEach(func(ctx SpecContext) {
			if CurrentSpecReport().Failed() {
				collectDiagnostics(ctx)
			}
		})

		BeforeAll(func(ctx SpecContext) {
			Expect(GinkgoParallelProcess()).To(Equal(1), "multi-cluster TLS must run serially")
			// This dedicated acceptance package deliberately does not Skip at
			// shallower depths or missing prerequisites.
			ns := "e2e-pg-tls-" + e2e.RunID()
			if len(ns) > 63 {
				ns = ns[:63]
			}
			primary = loadMember(ctx, "E2E_PRIMARY_KUBECONFIG", "pg-primary", ns)
			replica = loadMember(ctx, "E2E_REPLICA_KUBECONFIG", "pg-replica", ns)
			Expect(primary.env.RestClientConfig.Host).NotTo(Equal(replica.env.RestClientConfig.Host),
				"primary and replica must use independent Kubernetes API servers")
			Expect(primary.nodeIP).NotTo(Equal(replica.nodeIP))
			primaryHost = clusterName(replica.name, primary.name) + "-rw." + ns + ".svc"
			replicaHost := clusterName(primary.name, replica.name) + "-rw." + ns + ".svc"
			primary.host, replica.host = primaryHost, replicaHost
			var err error
			opts := tlscerts.GenerateOptions{DNSNames: []string{"test-ca"}, Validity: 2 * time.Hour}
			serverCA, err = tlscerts.Generate(opts)
			Expect(err).NotTo(HaveOccurred())
			clientCA, err = tlscerts.Generate(opts)
			Expect(err).NotTo(HaveOccurred())
			foreign, err = tlscerts.Generate(opts)
			Expect(err).NotTo(HaveOccurred())
			replication = issue(clientCA, "streaming_replica", []string{"streaming_replica"}, x509.ExtKeyUsageClientAuth)
			primary.server = issue(serverCA, primary.name,
				[]string{primaryHost, primary.cluster + "-rw." + ns + ".svc"}, x509.ExtKeyUsageServerAuth)
			replica.server = issue(serverCA, replica.name,
				[]string{replicaHost, replica.cluster + "-rw." + ns + ".svc"}, x509.ExtKeyUsageServerAuth)
			primary.route(ctx, replica)
			replica.route(ctx, primary)
			primary.expose(ctx)
			replica.expose(ctx)
			primary.createDocumentDB(ctx, serverCA, clientCA, replication, primary.name, replica.name)
			primary.healthy(ctx)
			// The probe is in the replica Kubernetes cluster. Its successful
			// verify-full connection proves DNS and networking BEFORE bootstrap.
			replica.secret(ctx, serverCASecret, map[string][]byte{"ca.crt": serverCA.CACertPEM})
			replica.secret(ctx, clientSecret, leafData(replication))
			replica.createProbe(ctx, primary.cnpg(ctx).Status.Image)
			Eventually(func(g Gomega) {
				output, err := replica.probe(ctx, primaryHost)
				g.Expect(err).NotTo(HaveOccurred(), output)
				g.Expect(output).To(Equal("1"))
			}, budget, poll).Should(Succeed(), "cross-cluster DNS, reachability, and verify-full authentication")
			replica.createDocumentDB(ctx, serverCA, clientCA, replication, primary.name, replica.name)
			replica.healthy(ctx)
			primary.openGateway(ctx)
			replica.openGateway(ctx)
		}, NodeTimeout(25*time.Minute))

		assertStreaming := func(ctx context.Context, serial string) {
			GinkgoHelper()
			var observed string
			Eventually(func(g Gomega) {
				output, err := primary.sql(ctx, `SELECT s.ssl::text || '|' || s.client_dn || '|' || s.client_serial::text
					FROM pg_stat_replication r JOIN pg_stat_ssl s USING (pid)
					WHERE r.usename = 'streaming_replica' AND r.state = 'streaming'`)
				g.Expect(err).NotTo(HaveOccurred(), output)
				g.Expect(output).To(Equal("true|/CN=streaming_replica|" + serial))
				observed = output
			}, budget, poll).Should(Succeed(), "TLS streaming connection must present the intended client identity and serial")
			AddReportEntry("pg_stat_ssl streaming evidence", observed)
		}

		write := func(ctx context.Context, id string) bson.M {
			GinkgoHelper()
			doc := bson.M{"_id": id + "-" + e2e.RunID(), "message": "verified cross-cluster PostgreSQL TLS", "sequence": int32(42)}
			_, err := primary.gateway.Database("postgres_tls").Collection("documents").InsertOne(ctx, doc)
			Expect(err).NotTo(HaveOccurred())
			return doc
		}

		assertDocument := func(ctx context.Context, doc bson.M) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				var got bson.M
				err := replica.gateway.Database("postgres_tls").Collection("documents").
					FindOne(ctx, bson.M{"_id": doc["_id"]}).Decode(&got)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got).To(Equal(doc), "replica must preserve exact document contents")
			}, budget, poll).Should(Succeed())
		}

		assertServer := func(ctx context.Context, expected *tlscerts.Bundle) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				cert, err := primary.presentedCertificate(ctx, serverCA.CACertPEM, primaryHost)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cert.SerialNumber.String()).To(Equal(certificate(expected).SerialNumber.String()))
				g.Expect(cert.DNSNames).To(ContainElement(primaryHost))
			}, budget, poll).Should(Succeed(), "server must serve the expected certificate with chain and hostname verification")
		}

		It("preserves all PostgreSQL certificate fields and verifies live TLS replication", func(ctx SpecContext) {
			for _, m := range []*member{primary, replica} {
				cluster := m.cnpg(ctx)
				Expect(cluster.Spec.Certificates).To(Equal(&cnpgv1.CertificatesConfiguration{
					ServerCASecret: serverCASecret, ServerTLSSecret: serverSecret,
					ClientCASecret: clientCASecret, ReplicationTLSSecret: clientSecret,
				}))
				remote := 0
				for _, external := range cluster.Spec.ExternalClusters {
					if external.Name == cluster.Name {
						continue
					}
					remote++
					Expect(external.ConnectionParameters).To(HaveKeyWithValue("sslmode", "verify-full"))
					Expect(external.ConnectionParameters).To(HaveKeyWithValue("user", "streaming_replica"))
					Expect(external.SSLRootCert).To(Equal(selector(serverCASecret, "ca.crt")))
					Expect(external.SSLCert).To(Equal(selector(clientSecret, "tls.crt")))
					Expect(external.SSLKey).To(Equal(selector(clientSecret, "tls.key")))
				}
				Expect(remote).To(Equal(1))
			}
			assertServer(ctx, primary.server)
			assertStreaming(ctx, certificate(replication).SerialNumber.String())
			var err error
			replicationApplicationName, err = primary.sql(ctx,
				"SELECT application_name FROM pg_stat_replication WHERE usename='streaming_replica'")
			Expect(err).NotTo(HaveOccurred())
			Expect(replicationApplicationName).NotTo(BeEmpty())
			assertDocument(ctx, write(ctx, "positive"))
			AddReportEntry("certificate propagation", map[string]string{
				"primary_cluster": primary.cluster, "replica_cluster": replica.cluster,
				"server_ca": serverCASecret, "server_tls": serverSecret,
				"client_ca": clientCASecret, "replication_tls": clientSecret,
				"hostname": primaryHost, "sslmode": "verify-full",
			})
		}, SpecTimeout(10*time.Minute))

		// Each case starts with valid streaming, injects exactly one fault,
		// requires an explicit libpq failure AND a WAL-receiver TLS log,
		// proves writes are blocked, then restores and verifies catch-up.
		DescribeTable("rejects invalid trust and identity without replicating new writes",
			func(ctx SpecContext, fault string) {
				assertStreaming(ctx, certificate(replication).SerialNumber.String())
				start := time.Now().UTC().Format(time.RFC3339)
				restore := func(ctx context.Context) {
					replica.secret(ctx, serverCASecret, map[string][]byte{"ca.crt": serverCA.CACertPEM})
					replica.secret(ctx, clientSecret, leafData(replication))
					primary.secret(ctx, serverSecret, leafData(primary.server))
				}
				DeferCleanup(func(ctx SpecContext) { restore(ctx) })
				var expectedFailure string
				switch fault {
				case "server-ca":
					replica.secret(ctx, serverCASecret, map[string][]byte{"ca.crt": foreign.CACertPEM})
					expectedFailure = "certificate verify failed"
				case "hostname":
					invalid := issue(serverCA, primary.name, []string{"wrong-host.example.test"}, x509.ExtKeyUsageServerAuth)
					primary.secret(ctx, serverSecret, leafData(invalid))
					expectedFailure = "does not match host name"
				case "client-ca":
					invalid := issue(foreign, "streaming_replica", []string{"streaming_replica"}, x509.ExtKeyUsageClientAuth)
					replica.secret(ctx, clientSecret, leafData(invalid))
					expectedFailure = "unknown ca"
				default:
					Fail("unknown TLS fault: " + fault)
				}
				var verificationFailure string
				Eventually(func(g Gomega) {
					output, err := replica.probe(ctx, primaryHost)
					g.Expect(err).To(HaveOccurred())
					g.Expect(strings.ToLower(output)).To(ContainSubstring(expectedFailure),
						"a timeout or networking error is not proof of TLS rejection")
					verificationFailure = output
				}, budget, poll).Should(Succeed())
				AddReportEntry("explicit TLS rejection: "+fault, verificationFailure)
				// Existing sessions keep their negotiated TLS material. End the
				// session so the WAL receiver must authenticate the changed certs.
				output, err := primary.sql(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_replication WHERE usename='streaming_replica'")
				Expect(err).NotTo(HaveOccurred(), output)
				Eventually(func(g Gomega) {
					pod := replica.cnpg(ctx).Status.CurrentPrimary
					logs, err := replica.command(ctx, "logs", pod, "-c", "postgres", "--since-time="+start)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(strings.ToLower(logs)).To(ContainSubstring(expectedFailure),
						"WAL receiver must explicitly reject the TLS configuration")
					count, err := primary.sql(ctx, "SELECT count(*) FROM pg_stat_replication WHERE usename='streaming_replica'")
					g.Expect(err).NotTo(HaveOccurred(), count)
					g.Expect(count).To(Equal("0"))
				}, budget, poll).Should(Succeed())
				doc := write(ctx, fault)
				Consistently(func(g Gomega) {
					err := replica.gateway.Database("postgres_tls").Collection("documents").
						FindOne(ctx, bson.M{"_id": doc["_id"]}).Err()
					g.Expect(err).To(MatchError(driver.ErrNoDocuments), "replica must remain readable but not receive the new write")
					count, err := primary.sql(ctx, "SELECT count(*) FROM pg_stat_replication WHERE usename='streaming_replica'")
					g.Expect(err).NotTo(HaveOccurred(), count)
					g.Expect(count).To(Equal("0"))
				}, 20*time.Second, poll).Should(Succeed())
				restore(ctx)
				assertServer(ctx, primary.server)
				assertStreaming(ctx, certificate(replication).SerialNumber.String())
				assertDocument(ctx, doc)
			},
			Entry("untrusted server CA", "server-ca", SpecTimeout(15*time.Minute)),
			Entry("missing hostname SAN", "hostname", SpecTimeout(15*time.Minute)),
			Entry("untrusted replication client certificate", "client-ca", SpecTimeout(15*time.Minute)),
		)

		It("rotates server and replication client certificates under the existing CAs without manual repair", func(ctx SpecContext) {
			newServer := issue(serverCA, primary.name, certificate(primary.server).DNSNames, x509.ExtKeyUsageServerAuth)
			newReplicaServer := issue(serverCA, replica.name, certificate(replica.server).DNSNames, x509.ExtKeyUsageServerAuth)
			newClient := issue(clientCA, "streaming_replica", []string{"streaming_replica"}, x509.ExtKeyUsageClientAuth)
			for _, m := range []*member{primary, replica} {
				m.secret(ctx, clientSecret, leafData(newClient))
			}
			primary.secret(ctx, serverSecret, leafData(newServer))
			replica.secret(ctx, serverSecret, leafData(newReplicaServer))
			assertServer(ctx, newServer)
			Eventually(func(g Gomega) {
				cert, err := replica.presentedCertificate(ctx, serverCA.CACertPEM, replica.host)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cert.SerialNumber.String()).To(Equal(certificate(newReplicaServer).SerialNumber.String()))
			}, budget, poll).Should(Succeed())
			// No pod restart or pg_terminate_backend here: CNPG must reload the
			// Secrets and reconnect using the replacement client certificate.
			assertStreaming(ctx, certificate(newClient).SerialNumber.String())
			applicationName, err := primary.sql(ctx,
				"SELECT application_name FROM pg_stat_replication WHERE usename='streaming_replica'")
			Expect(err).NotTo(HaveOccurred())
			Expect(applicationName).To(Equal(replicationApplicationName), "rotation must preserve CNPG's synchronous standby identity")
			assertDocument(ctx, write(ctx, "rotation"))
			AddReportEntry("rotation", fmt.Sprintf("server serial=%s, client serial=%s",
				certificate(newServer).SerialNumber, certificate(newClient).SerialNumber))
		}, SpecTimeout(15*time.Minute))
	})

func selector(name, key string) *corev1.SecretKeySelector {
	return &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}
}
