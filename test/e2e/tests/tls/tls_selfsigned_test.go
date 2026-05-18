package tls

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	ddbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// Self-signed mode corresponds to spec.tls.gateway.mode=SelfSigned.
// The operator mints an in-cluster CA and server certificate and
// projects them into a per-DocumentDB Secret. Clients outside the
// cluster can't practically obtain that CA, so the spec connects
// with InsecureSkipVerify=true — the goal here is to prove that
// enabling TLS doesn't break the happy path, not to validate the
// chain.
var _ = Describe("DocumentDB TLS — self-signed",
	Label(e2e.TLSLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("deploys with self-signed certs and accepts TLS connections", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "suite env not initialised")

			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_selfsigned", nil)

			// Wait for the operator-published TLS status to name a
			// secret and advertise Ready. The secret name is chosen
			// by the operator; we don't assert a specific value — we
			// only fetch whatever the status reports.
			key := types.NamespacedName{Namespace: cluster.NamespaceName, Name: cluster.DD.Name}
			Eventually(func(g Gomega) string {
				dd, err := ddbutil.Get(ctx, env.Client, key)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(dd.Status.TLS).NotTo(BeNil(), "status.tls not populated yet")
				g.Expect(dd.Status.TLS.Ready).To(BeTrue(), "status.tls.ready false")
				return dd.Status.TLS.SecretName
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				ShouldNot(BeEmpty(), "operator did not publish TLS secret name")

			// Assert the projected secret looks like a TLS secret.
			Eventually(func() error {
				dd, err := ddbutil.Get(ctx, env.Client, key)
				if err != nil {
					return err
				}
				return assertions.AssertTLSSecretReady(ctx, env.Client,
					cluster.NamespaceName, dd.Status.TLS.SecretName)()
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				Should(Succeed())

			host, port, stop := openGatewayForward(ctx, cluster.DD)
			defer stop()

			connectCtx, cancelConnect := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
			defer cancelConnect()

			client, err := mongohelper.NewClient(connectCtx, mongohelper.ClientOptions{
				Host:        host,
				Port:        port,
				User:        tlsCredentialUser,
				Password:    tlsCredentialPassword,
				TLS:         true,
				TLSInsecure: true,
			})
			Expect(err).NotTo(HaveOccurred(), "connect with insecure TLS")
			defer func() { _ = client.Disconnect(ctx) }()

			Eventually(func() error {
				return mongohelper.Ping(connectCtx, client)
			}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "TLS ping with insecure verify should succeed")
		})
	},
)
