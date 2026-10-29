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

// DocumentDB upgrade — schema, rollback within the safe window.
//
// Two-phase mode (spec.schemaVersion unset) exists precisely to give a
// rollback-safe window: after the binary is bumped but BEFORE the schema is
// finalized, the installed extension schema still sits at the OLD version, so
// the binary can be reverted without hitting the irreversible ALTER EXTENSION
// migration. The image-rollback webhook only blocks downgrades below the
// *installed schema* (documentdb_webhook.go: validateImageRollback), so a
// downgrade back to the old binary is permitted while the schema is still old.
//
// The flow:
//
//  1. Create a DocumentDB pinned to the OLD version with spec.schemaVersion
//     unset (two-phase) and seed data. status.schemaVersion settles on OLD.
//  2. Upgrade the binary to NEW via spec.documentDBVersion. The schema stays
//     at OLD (two-phase); data is retained.
//  3. Roll the binary back to OLD via spec.documentDBVersion — BEFORE any
//     finalize. Because the installed schema is still OLD, the webhook admits
//     the downgrade. Assert DocumentDB returns to Ready on the old binary, the
//     schema is still OLD, and the seeded data survived the down-hop.
//
// This is the safe-window guarantee documented on spec.schemaVersion; the
// negative case (rollback blocked AFTER finalize) is covered by the webhook
// unit tests.
var _ = Describe("DocumentDB upgrade — schema (rollback within safe window)",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-schema-rollback"
			dbName   = "upgrade_schema_rollback"
			collName = "seed"
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
			e2e.SkipUnlessLevel(e2e.High)
			ctx, cancel = context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { cancel() })
		})

		It("allows rolling the binary back to the old version before finalize, retaining data", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			By("creating a DocumentDB pinned to the old version (schemaVersion unset → two-phase)")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			vars["DOCUMENTDB_IMAGE"] = ""
			vars["GATEWAY_IMAGE"] = ""
			vars["DOCUMENTDB_VERSION"] = oldVersion

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

			schemaVersion := schemaVersionGetter(ctx, c, key)

			By("waiting for status.schemaVersion to settle on the old version")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Equal(oldVersion), "initial schema version should be %s", oldVersion)

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

			By("waiting for the new binary to be applied and DocumentDB to be Ready")
			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(newVersion), "status.documentDBImage did not advance to version %s", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newVersion=%s", newVersion)

			By("confirming two-phase mode kept the schema at the old version (rollback window open)")
			Consistently(schemaVersion,
				30*time.Second, 5*time.Second,
			).Should(Equal(oldVersion),
				"schema must remain at %s while schemaVersion is unset (rollback-safe window)", oldVersion)

			By("rolling the binary back to the old version before finalize")
			// The installed schema is still oldVersion, so the image-rollback
			// webhook admits this downgrade. Reverting after a finalize would
			// instead be rejected (covered by webhook unit tests).
			fresh2, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before rollback patch")
			Expect(shareddb.PatchSpec(ctx, c, fresh2, func(s *dbv1.DocumentDBSpec) {
				s.DocumentDBVersion = oldVersion
			})).To(Succeed(), "rollback DocumentDBVersion from %s back to %s", newVersion, oldVersion)

			By("waiting for the old binary to be re-applied and DocumentDB to be Ready")
			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(oldVersion), "status.documentDBImage did not roll back to version %s", oldVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready after rollback to oldVersion=%s", oldVersion)

			By("verifying the schema is still at the old version after rollback")
			Consistently(schemaVersion,
				30*time.Second, 5*time.Second,
			).Should(Equal(oldVersion),
				"schema must still be %s after rolling the binary back", oldVersion)

			By("verifying seeded data survived the binary rollback")
			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after rollback")
			DeferCleanup(func(ctx SpecContext) { _ = handle2.Close(ctx) })
			n, err := sharedmongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after rollback", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across binary rollback")
		})
	})
