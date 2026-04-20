package tls

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/e2e"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// TLS-disabled mode corresponds to spec.tls.gateway.mode=Disabled.
// The gateway still listens but accepts plain-text mongo wire
// protocol. This spec verifies the happy-path: a freshly-created
// DocumentDB with TLS disabled accepts an unencrypted connection
// from the mongo driver.
var _ = Describe("DocumentDB TLS — disabled",
	Label(e2e.TLSLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("accepts plaintext mongo connections", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "suite env not initialised")

			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_disabled", nil)

			host, port, stop := openGatewayForward(ctx, cluster.DD)
			defer stop()

			connectCtx, cancelConnect := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
			defer cancelConnect()

			client, err := mongohelper.NewClient(connectCtx, mongohelper.ClientOptions{
				Host:     host,
				Port:     port,
				User:     tlsCredentialUser,
				Password: tlsCredentialPassword,
				TLS:      false,
			})
			Expect(err).NotTo(HaveOccurred(), "connect to gateway without TLS")
			defer func() { _ = client.Disconnect(ctx) }()

			Eventually(func() error {
				return mongohelper.Ping(connectCtx, client)
			}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "plaintext ping should succeed when TLS is disabled")
		})
	},
)
