package feature_gates

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
)

// walLevelFor reads the CNPG Cluster that backs the given DocumentDB and
// returns the value of its postgresql.parameters["wal_level"]. Empty
// string means the operator did not set the key (CNPG default applies,
// which is the "replica" level that disables logical decoding — i.e.
// change streams). Any error from the client is surfaced verbatim.
func walLevelFor(ctx context.Context, c client.Client, dd *previewv1.DocumentDB) (string, error) {
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: dd.Namespace, Name: dd.Name}, cluster); err != nil {
		return "", fmt.Errorf("get CNPG Cluster %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	if cluster.Spec.PostgresConfiguration.Parameters == nil {
		return "", nil
	}
	return cluster.Spec.PostgresConfiguration.Parameters["wal_level"], nil
}

// DocumentDB feature-gates / change streams.
//
// The operator translates `spec.featureGates.ChangeStreams=true` into
// `wal_level=logical` on the underlying CNPG Cluster (see
// operator/src/internal/cnpg/cnpg_cluster.go). When the gate is off (or
// unset), the operator does not force a wal_level override, so CNPG's
// default ("replica") applies and change streams over the Mongo wire
// protocol are not supported by the DocumentDB extension.
//
// We assert the observable operator contract — the CNPG Cluster's
// postgresql.parameters — because:
//  1. It is image-independent: the protocol-level change-stream
//     behaviour is only available in the "-changestream" DocumentDB
//     image variants, which are not guaranteed to be loaded in every
//     e2e environment;
//  2. It is what the operator code actually controls.
//
// A future expansion can layer a best-effort mongo `Watch` call on top
// once the suite standardises on change-stream-capable images.
var _ = Describe("DocumentDB feature-gates — change streams",
	Label(e2e.FeatureLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		DescribeTable("wal_level reflects ChangeStreams gate",
			func(enabled, expectLogical bool) {
				env := e2e.SuiteEnv()
				Expect(env).ToNot(BeNil(), "SuiteEnv must be initialized")
				c := env.Client

				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
				DeferCleanup(cancel)

				name := "ft-cs-on"
				mixin := "feature_changestreams"
				if !enabled {
					name = "ft-cs-off"
					// Omit the mixin; the base template has no
					// featureGates block, so the gate is implicitly
					// disabled.
					mixin = ""
				}
				mixins := []string{}
				if mixin != "" {
					mixins = append(mixins, mixin)
				}
				dd, cleanup := setupFreshCluster(ctx, c, name, mixins, nil)
				DeferCleanup(cleanup)

				walLevel, err := walLevelFor(ctx, c, dd)
				Expect(err).ToNot(HaveOccurred())

				if expectLogical {
					Expect(walLevel).To(Equal("logical"),
						"enabled gate must drive wal_level=logical")
				} else {
					Expect(walLevel).ToNot(Equal("logical"),
						"disabled gate must leave wal_level off of logical; got %q", walLevel)
				}
			},
			Entry("enabled → wal_level=logical", true, true),
			Entry("disabled → wal_level not forced to logical", false, false),
		)
	})
