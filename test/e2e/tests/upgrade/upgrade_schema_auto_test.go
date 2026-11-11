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

// DocumentDB upgrade — schema, "auto" mode (single-step migration).
//
// This spec is the counterpart to upgrade_schema_test.go (two-phase mode).
// It exercises the other documented schemaVersion contract
// (documentdb_types.go): with spec.schemaVersion set to "auto", a single
// spec.documentDBVersion bump upgrades BOTH the binary and the extension
// schema in one step — the operator runs ALTER EXTENSION documentdb UPDATE
// automatically, with no separate finalize patch.
//
// The flow:
//
//  1. Create a DocumentDB pinned to the OLD version with spec.schemaVersion
//     set to "auto" and seed data. status.schemaVersion settles on OLD (the
//     binary and schema already agree, so "auto" is a no-op at first).
//  2. Upgrade the binary by patching spec.documentDBVersion to NEW. Because
//     schemaVersion is "auto", the operator must migrate the schema in the
//     same reconcile cycle — status.schemaVersion advances to NEW WITHOUT any
//     schemaVersion patch. Seeded data is retained.
//
// The absence of a separate finalize step (present in the two-phase spec) is
// the point of this test: it asserts the single-step path works end-to-end.
//
// Old/new versions come from the same env vars and defaults as the two-phase
// spec (E2E_UPGRADE_OLD_DOCUMENTDB_VERSION / _NEW_).
var _ = Describe("DocumentDB upgrade — schema (auto mode)",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-schema-auto"
			dbName   = "upgrade_schema_auto"
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

		It("migrates schema and binary in a single step when schemaVersion is auto, retaining data", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			By("creating a DocumentDB pinned to the old version with schemaVersion=auto")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			// Drive the version via documentDBVersion, not raw images.
			vars["DOCUMENTDB_IMAGE"] = ""
			vars["GATEWAY_IMAGE"] = ""
			vars["DOCUMENTDB_VERSION"] = oldVersion
			vars["SCHEMA_VERSION"] = "auto"

			dd, err := documentdb.Create(ctx, c, ns, ddName, documentdb.CreateOptions{
				Base:          "documentdb",
				Mixins:        []string{"documentdb_version", "schema_version"},
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

			// Single reused schema-version poller (caches last good read so a
			// transient API error can't fail a Consistently window).
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

			By("upgrading the binary via spec.documentDBVersion (schemaVersion stays auto)")
			fresh, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before version patch")
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *dbv1.DocumentDBSpec) {
				s.DocumentDBVersion = newVersion
			})).To(Succeed(), "patch DocumentDBVersion from %s to %s", oldVersion, newVersion)

			By("waiting for the operator to apply the new version and DocumentDB to be Ready")
			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(newVersion), "status.documentDBImage did not advance to version %s", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newVersion=%s", newVersion)

			By("verifying auto mode migrated the schema in a single step (no finalize patch)")
			// This is the single-step assertion: because schemaVersion is
			// "auto", the schema must advance to newVersion on its own — we
			// never set spec.schemaVersion. Contrast with the two-phase spec,
			// which requires an explicit finalize.
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(newVersion), "auto mode should migrate schema to %s in a single step", newVersion)

			By("verifying seeded data survived the single-step upgrade")
			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after single-step upgrade")
			DeferCleanup(func(ctx SpecContext) { _ = handle2.Close(ctx) })
			n, err := sharedmongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after single-step upgrade", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across single-step upgrade")
		})
	})
