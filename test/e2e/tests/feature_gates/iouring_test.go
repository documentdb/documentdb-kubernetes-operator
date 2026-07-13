package feature_gates

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	sharedmongo "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

// backingCluster fetches the CNPG Cluster that backs the given
// DocumentDB. The Cluster name equals the DocumentDB name for single-
// cluster deployments (see the lifecycle deploy spec).
func backingCluster(ctx context.Context, c client.Client, dd *previewv1.DocumentDB) (*cnpgv1.Cluster, error) {
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: dd.Namespace, Name: dd.Name}, cluster); err != nil {
		return nil, fmt.Errorf("get CNPG Cluster %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	return cluster, nil
}

// DocumentDB feature-gates / io_uring.
//
// The operator translates `spec.featureGates.IOUring=true` into two
// changes on the underlying CNPG Cluster (see operator/src/internal/
// cnpg/cnpg_cluster.go and pg_defaults.go):
//  1. postgresql.parameters["io_method"] = "io_uring", enabling
//     PostgreSQL 18 asynchronous I/O; and
//  2. a Localhost seccomp profile on the postgres container that
//     re-allows the io_uring_setup/enter/register syscalls which
//     CNPG's default RuntimeDefault profile strips.
//
// Without (2), postgres FATALs at startup ("could not setup io_uring
// queue: Operation not permitted") and the cluster never reaches
// Healthy. So the fact that setupFreshCluster's WaitHealthy returns IS
// the end-to-end proof that the seccomp relaxation works on the target
// kernel — there is no need (and no harness helper) to exec `SHOW
// io_method` inside the pod.
//
// This spec is gated behind the needs-iouring capability label AND a
// runtime E2E_IOURING=1 opt-in, because a default kind/CI node lacks
// both the io_uring-capable kernel and the documentdb-iouring Localhost
// seccomp profile; running it there would crashloop postgres rather
// than skip cleanly. The disabled-gate translation is already covered
// by the operator unit tests, so this spec focuses on the high-value
// enabled-and-healthy path that only an end-to-end environment can
// exercise.
var _ = Describe("DocumentDB feature-gates — io_uring",
	Label(e2e.FeatureLabel, e2e.NeedsIOUringLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			if os.Getenv("E2E_IOURING") != "1" {
				Skip("io_uring spec requires E2E_IOURING=1 and a cluster whose nodes carry " +
					"the documentdb-iouring Localhost seccomp profile on an io_uring-capable kernel")
			}
		})

		It("sets io_method=io_uring with a relaxed seccomp profile and stays healthy", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil(), "SuiteEnv must be initialized")
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			DeferCleanup(cancel)

			// Reaching past setupFreshCluster means the cluster
			// became Healthy with io_uring enabled — i.e. postgres
			// started under the relaxed seccomp profile without the
			// "Operation not permitted" crashloop.
			dd, cleanup := setupFreshCluster(ctx, c, "ft-iouring", []string{"feature_iouring"}, nil)
			DeferCleanup(cleanup)

			cluster, err := backingCluster(ctx, c, dd)
			Expect(err).ToNot(HaveOccurred())

			Expect(cluster.Spec.PostgresConfiguration.Parameters).To(
				HaveKeyWithValue("io_method", "io_uring"),
				"IOUring gate must set io_method=io_uring on the CNPG Cluster")

			Expect(cluster.Spec.SeccompProfile).ToNot(BeNil(),
				"IOUring gate must set a Localhost seccomp profile on the CNPG Cluster")
			Expect(cluster.Spec.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeLocalhost),
				"IOUring seccomp profile must be Localhost, not RuntimeDefault/Unconfined")
			Expect(cluster.Spec.SeccompProfile.LocalhostProfile).ToNot(BeNil(),
				"Localhost seccomp profile must reference a profile path")
			Expect(*cluster.Spec.SeccompProfile.LocalhostProfile).To(
				HaveSuffix("documentdb-iouring.json"),
				"Localhost profile should point at the documentdb-iouring profile")

			// Data-plane smoke: prove the gateway still answers on
			// the wire with io_uring active, so "Healthy" cannot mask
			// a postgres that is up but unable to serve queries.
			h, err := mongohelper.NewFromDocumentDB(ctx, env, dd.Namespace, dd.Name)
			Expect(err).ToNot(HaveOccurred(), "connect mongo to io_uring DocumentDB")
			DeferCleanup(func(ctx SpecContext) { _ = h.Close(ctx) })
			Expect(sharedmongo.Ping(ctx, h.Client())).To(Succeed(),
				"ping io_uring DocumentDB gateway")
		})
	})
