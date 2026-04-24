package exposure

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// reserveFreePort opens and immediately closes a TCP listener on :0 so
// the kernel picks an unused ephemeral port. There is an inherent TOCTOU
// window between the close and the subsequent bind inside port-forward,
// but for a single-threaded ginkgo run it is adequate.
func reserveFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve free port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// DocumentDB exposure — ClusterIP.
//
// Verifies:
//  1. spec.exposeViaService.serviceType=ClusterIP round-trips into the
//     API server unchanged;
//  2. the gateway Service the operator creates is of type ClusterIP;
//  3. a cluster-internal connection (via port-forward) can ping the
//     gateway — i.e. the Service is actually wired to Ready gateway pods.
var _ = Describe("DocumentDB exposure — ClusterIP",
	Label(e2e.ExposureLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("routes cluster-internal traffic to the gateway", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			DeferCleanup(cancel)

			dd, cleanup := setupFreshCluster(ctx, c, "expose-clusterip",
				[]string{"exposure_clusterip"}, nil)
			DeferCleanup(cleanup)

			// 1. Spec round-trip.
			Expect(dd.Spec.ExposeViaService.ServiceType).To(Equal("ClusterIP"))

			// 2. Service type is ClusterIP.
			svcName := portforward.GatewayServiceName(dd)
			Eventually(assertions.AssertServiceType(ctx, c, dd.Namespace, svcName, corev1.ServiceTypeClusterIP),
				timeouts.For(timeouts.ServiceReady), timeouts.PollInterval(timeouts.ServiceReady)).
				Should(Succeed())

			// 3. Cluster-internal connection works.
			localPort, err := reserveFreePort()
			Expect(err).ToNot(HaveOccurred())
			stop, err := portforward.Open(ctx, env, dd, localPort)
			Expect(err).ToNot(HaveOccurred(), "open port-forward")
			DeferCleanup(stop)

			var pingErr error
			Eventually(func() error {
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				defer pingCancel()
				cli, err := mongo.NewClient(pingCtx, mongo.ClientOptions{
					Host:     "127.0.0.1",
					Port:     strconv.Itoa(localPort),
					User:     credUser,
					Password: credPassword,
					TLS:      false,
				})
				if err != nil {
					pingErr = err
					return err
				}
				defer func() { _ = cli.Disconnect(context.Background()) }()
				pingErr = mongo.Ping(pingCtx, cli)
				return pingErr
			}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "mongo ping through ClusterIP port-forward: %v", pingErr)
		})
	})
