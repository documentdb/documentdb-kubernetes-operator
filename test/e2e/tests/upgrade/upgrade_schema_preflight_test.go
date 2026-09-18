package upgrade

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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

// DocumentDB upgrade — schema migration failure (no update path).
//
// This is the third gap deferred from #439: "ALTER EXTENSION UPDATE fails".
// It was deferred because, before the preflight landed (#448), the failure was
// only reachable by shipping a doctored extension image with a hole in its
// update-script chain — and the resulting behavior was an un-ratified raw
// PostgreSQL error that re-fired on every reconcile.
//
// With the preflight in place the failure is reachable deterministically with
// stock images: request a schema version that is <= the binary version (so the
// validating webhook admits it) and > the installed version (so the operator
// actually plans a migration), but which the extension never released and
// therefore has no documentdb--<from>--<to>.sql script for. A patch-level
// version such as 0.109.999 satisfies all three at once.
//
// The contract being pinned:
//
//   - The operator does NOT fire ALTER EXTENSION UPDATE.
//   - status.conditions gains SchemaUpgradeBlocked=True with reason
//     NoUpdatePath, naming both versions.
//   - status.schemaVersion does not move — no partial migration.
//   - The cluster stays Ready and serving; data is intact. In particular the
//     operator does not crash-loop the reconcile: we hold the assertions over
//     a window rather than sampling once.
//   - Correcting spec.schemaVersion to a real version clears the condition and
//     completes the migration, so the block is recoverable, not terminal.
var _ = Describe("DocumentDB upgrade — schema migration blocked",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.LowLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-blocked"
			dbName   = "upgrade_blocked"
			collName = "seed"

			// A patch release the documentdb extension has never shipped. It is
			// deliberately derived from oldVersion's minor so it sorts above the
			// installed schema but below the new binary — see the file comment.
			// .999 rather than a low number so the spec cannot silently invert if
			// upstream ever ships more patch releases on an old minor.
			unreachableSchemaSuffix = ".999"
		)

		var (
			oldVersion string
			newVersion string
			ctx        context.Context
			cancel     context.CancelFunc
			ns         string
			key        types.NamespacedName
		)

		BeforeAll(func() {
			// Gate before any cluster work: BeforeAll runs ahead of BeforeEach.
			e2e.SkipUnlessLevel(e2e.Low)
			skipUnlessUpgradeEnabled()
			oldVersion = envOr(envOldDocumentDBVersion, defaultOldDocumentDBVersion)
			newVersion = envOr(envNewDocumentDBVersion, defaultNewDocumentDBVersion)
			if oldVersion == newVersion {
				Skip(envOldDocumentDBVersion + " and " + envNewDocumentDBVersion +
					" are identical; there is no upgrade to block")
			}
			// The unreachable target is built from oldVersion's major.minor plus a high
			// patch, so it only stays below the binary version when the two versions
			// differ by at least one minor. On a same-minor pair the validating webhook
			// would reject the patch and the spec would fail for the wrong reason.
			if compareMajorMinor(newVersion, oldVersion) <= 0 {
				Skip(envOldDocumentDBVersion + " and " + envNewDocumentDBVersion +
					" share a major.minor; cannot construct an unreachable patch version " +
					"that the webhook will still admit")
			}

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			c := env.Client

			setupCtx, setupCancel := context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { setupCancel() })

			By("creating a DocumentDB pinned to the old version (schemaVersion unset → two-phase)")
			ns = namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(setupCtx, c, ns)
			createCredentialSecret(setupCtx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			vars["DOCUMENTDB_IMAGE"] = ""
			vars["GATEWAY_IMAGE"] = ""
			vars["DOCUMENTDB_VERSION"] = oldVersion

			dd, err := documentdb.Create(setupCtx, c, ns, ddName, documentdb.CreateOptions{
				Base:          "documentdb",
				Mixins:        []string{"documentdb_version"},
				Vars:          vars,
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred(), "create DocumentDB %s/%s", ns, ddName)
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key = types.NamespacedName{Namespace: ns, Name: ddName}
			Eventually(assertions.AssertDocumentDBReady(setupCtx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "DocumentDB did not reach Ready on oldVersion=%s", oldVersion)
		})

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Low)
			ctx, cancel = context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { cancel() })
		})

		It("blocks the migration with an actionable condition instead of failing the ALTER", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			schemaVersion := schemaVersionGetter(ctx, c, key)
			blocked := schemaUpgradeBlockedGetter(ctx, c, key)

			By("waiting for status.schemaVersion to settle on the old version")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Equal(oldVersion), "initial schema version should be %s", oldVersion)

			By("seeding data on the old schema")
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "connect to DocumentDB gateway on oldVersion")
			inserted, err := sharedmongo.Seed(ctx, handle.Client(), dbName, collName, seed.SmallDataset())
			Expect(err).NotTo(HaveOccurred(), "seed %s.%s", dbName, collName)
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
			Expect(handle.Close(ctx)).To(Succeed())

			By("upgrading the binary to the new version, leaving the schema in two-phase")
			fresh, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before binary upgrade")
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.DocumentDBVersion = newVersion
			})).To(Succeed(), "patch DocumentDBVersion from %s to %s", oldVersion, newVersion)

			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(newVersion), "status.documentDBImage did not advance to %s", newVersion)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newVersion=%s", newVersion)

			// e.g. old "0.109.0" → unreachable "0.109.7": above the installed
			// schema, below the new binary, and never released — so no
			// documentdb--0.109-0--0.109-7.sql exists.
			unreachable := majorMinorOf(oldVersion) + unreachableSchemaSuffix

			By("requesting an unreachable schema version " + unreachable)
			fresh2, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before unreachable schema patch")
			Expect(shareddb.PatchSpec(ctx, c, fresh2, func(s *previewv1.DocumentDBSpec) {
				s.SchemaVersion = unreachable
			})).To(Succeed(), "patch schemaVersion to unreachable %s", unreachable)

			By("waiting for the operator to report SchemaUpgradeBlocked/NoUpdatePath")
			Eventually(blocked,
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).ShouldNot(BeNil(), "operator never set the %s condition", previewv1.ConditionSchemaUpgradeBlocked)

			Eventually(func() metav1.ConditionStatus {
				cond := blocked()
				if cond == nil {
					return metav1.ConditionUnknown
				}
				return cond.Status
			}, timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(metav1.ConditionTrue), "schema upgrade should be reported as blocked")

			cond := blocked()
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(previewv1.ReasonNoUpdatePath))
			Expect(cond.Message).To(ContainSubstring(oldVersion),
				"blocked message should name the installed schema version")
			Expect(cond.Message).To(ContainSubstring(unreachable),
				"blocked message should name the requested target version")

			By("verifying the schema did not move and the block is stable (no crash-loop)")
			// Holding the window matters: the pre-preflight behavior was to fire
			// ALTER EXTENSION on every reconcile and error out each time. A
			// stable schema across the window is the observable proof that the
			// operator stopped cleanly instead of retrying.
			Consistently(schemaVersion,
				60*time.Second, 5*time.Second,
			).Should(Equal(oldVersion),
				"schema must stay at %s while the upgrade is blocked", oldVersion)

			Consistently(func() metav1.ConditionStatus {
				c := blocked()
				if c == nil {
					return metav1.ConditionUnknown
				}
				return c.Status
			}, 60*time.Second, 5*time.Second,
			).Should(Equal(metav1.ConditionTrue), "blocked condition should not flap")

			By("verifying the cluster stayed Ready and the data is intact")
			Expect(assertions.AssertDocumentDBReady(ctx, c, key)()).To(Succeed(),
				"DocumentDB should remain Ready while a schema upgrade is blocked")

			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway while blocked")
			n, err := sharedmongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s while blocked", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed while the upgrade was blocked")
			Expect(handle2.Close(ctx)).To(Succeed())
		})

		It("recovers and completes the migration once a reachable version is requested", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			schemaVersion := schemaVersionGetter(ctx, c, key)
			blocked := schemaUpgradeBlockedGetter(ctx, c, key)

			By("correcting spec.schemaVersion to the real new version")
			fresh, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before recovery patch")
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.SchemaVersion = newVersion
			})).To(Succeed(), "patch schemaVersion to %s", newVersion)

			By("waiting for the migration to complete")
			Eventually(schemaVersion,
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(newVersion), "schema did not migrate to %s after correcting the target", newVersion)

			By("verifying the blocked condition cleared")
			Eventually(func() metav1.ConditionStatus {
				cond := blocked()
				if cond == nil {
					return metav1.ConditionUnknown
				}
				return cond.Status
			}, timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(metav1.ConditionFalse), "blocked condition should clear after a successful migration")

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB not Ready after recovering the schema migration")

			By("verifying seeded data survived the blocked-then-recovered cycle")
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after recovery")
			DeferCleanup(func(ctx SpecContext) { _ = handle.Close(ctx) })
			n, err := sharedmongo.Count(ctx, handle.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after recovery", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across the blocked-then-recovered migration")
		})
	})
