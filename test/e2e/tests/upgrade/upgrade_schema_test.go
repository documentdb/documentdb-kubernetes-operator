package upgrade

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
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

// DocumentDB upgrade — schema (two-phase extension migration).
//
// This spec gates the safety of bumping the default DocumentDB version:
// it exercises the *irreversible* extension schema migration
// (ALTER EXTENSION documentdb UPDATE) that the image-only upgrade spec
// (upgrade_images_test.go) deliberately does NOT cover.
//
// It drives the upgrade through the user-facing knobs — spec.documentDBVersion
// (which sets the extension + gateway images together) and spec.schemaVersion —
// rather than raw spec.image.* overrides, mirroring how an operator user
// actually bumps a version.
//
// The flow mirrors the two-phase contract documented on spec.schemaVersion
// (documentdb_types.go):
//
//  1. Create a DocumentDB pinned to the OLD version with spec.schemaVersion
//     unset (two-phase mode) and seed data. status.schemaVersion settles on
//     the OLD semver.
//  2. Upgrade the binary by patching spec.documentDBVersion to NEW. The pods
//     roll, but because schemaVersion is unset the operator must NOT run
//     ALTER EXTENSION — status.schemaVersion stays at OLD, giving the
//     rollback-safe window. Seeded data is retained.
//  3. Finalize by setting spec.schemaVersion to NEW. The operator runs
//     ALTER EXTENSION UPDATE and status.schemaVersion advances to NEW.
//     Seeded data is still retained.
//
// The old→new versions are supplied by the caller via env vars
// (E2E_UPGRADE_OLD_DOCUMENTDB_VERSION / _NEW_), defaulting to the
// last-released pair so the spec runs on every e2e PR without extra
// wiring. The extension's installed schema semver matches the version
// string, so the same values drive documentDBVersion and assert
// status.schemaVersion.
var _ = Describe("DocumentDB upgrade — schema",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-schema"
			dbName   = "upgrade_schema"
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

		It("keeps the schema at the old version until finalized, then migrates and retains data", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			By("creating a DocumentDB pinned to the old version (schemaVersion unset → two-phase)")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			// Drive the version via documentDBVersion, not raw images: the
			// mixin sets spec.documentDBVersion, so the base image fields
			// must stay empty for it to take effect.
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

			// Single schema-version poller reused across every Eventually/
			// Consistently below: it caches the last good read so a transient
			// API error can't fail the Consistently window (see helper doc).
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
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.DocumentDBVersion = newVersion
			})).To(Succeed(), "patch DocumentDBVersion from %s to %s", oldVersion, newVersion)

			By("waiting for the operator to apply the new version and DocumentDB to be Ready")
			// status.documentDBImage is the extension image URI the
			// operator currently has applied; its tag carries the version.
			// This is a cleaner "binary upgraded" signal than scanning pod
			// containers (the extension ships as an ImageVolume, and the
			// PostgreSQL container keeps its own base image).
			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(newVersion), "status.documentDBImage did not advance to version %s", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newVersion=%s", newVersion)

			By("verifying two-phase mode did NOT migrate the schema (stays at old version)")
			// The binary now offers newVersion, but with schemaVersion unset
			// the operator must leave the installed schema at oldVersion. Hold
			// the assertion over a window so a late auto-migration would fail.
			Consistently(schemaVersion,
				30*time.Second, 5*time.Second,
			).Should(Equal(oldVersion),
				"schema must remain at %s until spec.schemaVersion is set (two-phase)", oldVersion)

			By("verifying seeded data survived the binary upgrade")
			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after binary upgrade")
			n, err := sharedmongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after binary upgrade", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across binary upgrade")
			Expect(handle2.Close(ctx)).To(Succeed())

			By("finalizing the schema by setting spec.schemaVersion to the new version")
			fresh2, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before schema finalize")
			Expect(shareddb.PatchSpec(ctx, c, fresh2, func(s *previewv1.DocumentDBSpec) {
				s.SchemaVersion = newVersion
			})).To(Succeed(), "patch DocumentDB schemaVersion to %s", newVersion)

			By("waiting for ALTER EXTENSION UPDATE to advance status.schemaVersion to the new version")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(newVersion), "schema did not migrate to %s after finalize", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB not Ready after schema migration to %s", newVersion)

			By("verifying seeded data survived the schema migration")
			handle3, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after schema migration")
			DeferCleanup(func(ctx SpecContext) { _ = handle3.Close(ctx) })
			n2, err := sharedmongo.Count(ctx, handle3.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after schema migration", dbName, collName)
			Expect(n2).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across schema migration")
		})
	})

// schemaVersionGetter returns a poll function reporting the DocumentDB's
// status.schemaVersion, for use with Eventually/Consistently. To tolerate
// transient API errors without failing a Consistently window, it caches the
// last successfully observed value and returns that (rather than "") on a fetch
// error — a real schema change is still caught because every successful read
// refreshes the cache. Instantiate it once per spec and reuse the same closure
// across all checks so the cache persists between them.
func schemaVersionGetter(ctx context.Context, c client.Client, key types.NamespacedName) func() string {
	var last string
	return func() string {
		dd, err := shareddb.Get(ctx, c, key)
		if err != nil {
			return last
		}
		last = dd.Status.SchemaVersion
		return last
	}
}

// statusDocumentDBImageGetter returns a poll function reporting the
// DocumentDB's status.documentDBImage (the extension image URI the
// operator currently has applied). Used with Eventually to detect that
// a spec.documentDBVersion bump has been reconciled. A fetch error is
// surfaced as an empty string so the matcher keeps polling.
func statusDocumentDBImageGetter(ctx context.Context, c client.Client, key types.NamespacedName) func() string {
	return func() string {
		dd, err := shareddb.Get(ctx, c, key)
		if err != nil {
			return ""
		}
		return dd.Status.DocumentDBImage
	}
}
