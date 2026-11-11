package upgrade

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	e2emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	sharedmongo "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

// DocumentDB upgrade — schema on a multi-instance (HA) cluster.
//
// The single-instance schema spec (upgrade_schema_test.go) pins
// INSTANCES=1, so it cannot observe how the two-phase migration behaves
// across a primary + replicas. This spec runs the same two-phase flow on
// a 3-instance cluster and adds two HA-specific guarantees:
//
//  1. Rollout health — the cluster returns to 3 ready instances after both
//     the binary roll and the schema finalize (CNPG rolls replicas first,
//     then the primary; we assert the observable end-state via
//     AssertInstanceCount rather than reimplementing CNPG's internal
//     rollout ordering).
//  2. Replica schema convergence — the operator computes
//     status.schemaVersion from the PRIMARY only, so this spec additionally
//     execs psql on a REPLICA pod to confirm the migrated extension version
//     propagated via WAL streaming replication.
//
// Old/new versions come from the same env vars/defaults as the other
// schema specs (E2E_UPGRADE_OLD_DOCUMENTDB_VERSION / _NEW_).
var _ = Describe("DocumentDB upgrade — schema (multi-instance HA)",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighestLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName    = "upgrade-schema-ha"
			dbName    = "upgrade_schema_ha"
			collName  = "seed"
			instances = 3
		)
		var (
			oldVersion string
			newVersion string
			ctx        context.Context
			cancel     context.CancelFunc
		)

		BeforeAll(func() {
			skipUnlessUpgradeEnabled()
			oldVersion = envOr(envOldDocumentDBVersion, defaultOldDocumentDBVersion)
			newVersion = envOr(envNewDocumentDBVersion, defaultNewDocumentDBVersion)
			if oldVersion == newVersion {
				Skip(envOldDocumentDBVersion + " and " + envNewDocumentDBVersion + " are identical; nothing to upgrade")
			}
		})

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Highest)
			ctx, cancel = context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { cancel() })
		})

		It("migrates the schema across a 3-instance cluster and propagates it to replicas", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			By("creating a 3-instance DocumentDB pinned to the old version (two-phase)")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			vars["DOCUMENTDB_IMAGE"] = ""
			vars["GATEWAY_IMAGE"] = ""
			vars["DOCUMENTDB_VERSION"] = oldVersion
			vars["INSTANCES"] = "3"

			dd, err := documentdb.Create(ctx, c, ns, ddName, documentdb.CreateOptions{
				Base:          "documentdb",
				Mixins:        []string{"documentdb_version"},
				Vars:          vars,
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred(), "create DocumentDB %s/%s", ns, ddName)
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: ddName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "DocumentDB did not reach Ready on oldVersion=%s", oldVersion)

			By("waiting for all 3 instances to become ready")
			Eventually(assertions.AssertInstanceCount(ctx, c, key, instances),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "cluster did not reach %d ready instances", instances)

			schemaVersion := schemaVersionGetter(ctx, c, key)

			By("waiting for status.schemaVersion to settle on the old version")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Equal(oldVersion), "initial schema version should be %s", oldVersion)

			By("confirming all replicas also report the old schema version before upgrade")
			Eventually(func() (string, error) {
				return replicaInstalledSchemaVersion(ctx, env, ns, ddName, instances-1)
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Equal(oldVersion), "replica should start at schema %s", oldVersion)

			By("seeding data on the old schema")
			docs := seed.SmallDataset()
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "connect to DocumentDB gateway on oldVersion")
			inserted, err := sharedmongo.Seed(ctx, handle.Client(), dbName, collName, docs)
			Expect(err).NotTo(HaveOccurred(), "seed %s.%s", dbName, collName)
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
			Expect(handle.Close(ctx)).To(Succeed())

			By("upgrading the binary via spec.documentDBVersion without setting schemaVersion")
			fresh, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before version patch")
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *dbv1.DocumentDBSpec) {
				s.DocumentDBVersion = newVersion
			})).To(Succeed(), "patch DocumentDBVersion from %s to %s", oldVersion, newVersion)

			By("waiting for the new binary to roll out and all 3 instances to be ready again")
			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(newVersion), "status.documentDBImage did not advance to version %s", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newVersion=%s", newVersion)
			Eventually(assertions.AssertInstanceCount(ctx, c, key, instances),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "cluster did not return to %d ready instances after binary roll", instances)

			By("verifying two-phase mode kept the schema at the old version across the HA roll")
			Consistently(schemaVersion,
				30*time.Second, 5*time.Second,
			).Should(Equal(oldVersion),
				"schema must remain at %s until spec.schemaVersion is set (two-phase)", oldVersion)

			By("finalizing the schema by setting spec.schemaVersion to the new version")
			fresh2, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before schema finalize")
			Expect(shareddb.PatchSpec(ctx, c, fresh2, func(s *dbv1.DocumentDBSpec) {
				s.SchemaVersion = newVersion
			})).To(Succeed(), "patch DocumentDB schemaVersion to %s", newVersion)

			By("waiting for status.schemaVersion (primary) to advance to the new version")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(newVersion), "schema did not migrate to %s after finalize", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB not Ready after schema migration to %s", newVersion)

			By("verifying the migrated schema propagated to all replicas via streaming replication")
			// The operator only runs ALTER EXTENSION on the primary and only
			// reads status.schemaVersion from the primary. Confirm the catalog
			// change reached every replica by reading pg_extension.extversion
			// on each of them.
			Eventually(func() (string, error) {
				return replicaInstalledSchemaVersion(ctx, env, ns, ddName, instances-1)
			}, timeouts.For(timeouts.DocumentDBUpgrade), timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(newVersion), "replica schema did not converge to %s after finalize", newVersion)

			By("verifying seeded data survived the HA schema migration")
			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after HA schema migration")
			DeferCleanup(func(ctx SpecContext) { _ = handle2.Close(ctx) })
			n, err := sharedmongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after HA schema migration", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across HA schema migration")
		})
	})
