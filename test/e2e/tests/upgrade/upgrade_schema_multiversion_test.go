package upgrade

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
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

// DocumentDB upgrade — schema across MORE THAN ONE minor version.
//
// upgrade_schema_test.go covers exactly one old→new hop. These specs cover the
// two gaps deferred from #439, both of which only became meaningful once the
// operator gained an update-path preflight (#448):
//
//  1. "multi-minor jump" — a single upgrade that skips intermediate minors
//     (e.g. 0.110.0 → 0.113.0). The DocumentDB extension releases minors
//     roughly monthly, so a user upgrading quarterly is routinely 3+ minors
//     behind. This spec ratifies that such a jump is a SUPPORTED path: the
//     operator resolves the chained update scripts in one ALTER EXTENSION
//     UPDATE, the schema lands on the target, and data survives. If the
//     extension ever ships a release without its update script, the preflight
//     blocks the upgrade and this spec fails loudly — which is the point.
//
//  2. "sequential chain" — stepping through every version in the chain one at
//     a time (0.109.0 → 0.110.0 → 0.113.0 → …), asserting the schema advances
//     at each hop and the seeded data survives all of them cumulatively. This
//     is the conservative upgrade style, and it exercises repeated
//     rolling-restart + migrate cycles against a single volume.
//
// Both drive the upgrade through spec.documentDBVersion + spec.schemaVersion —
// the user-facing knobs — and read the versions from
// E2E_UPGRADE_DOCUMENTDB_VERSION_CHAIN (see documentDBVersionChain).
//
// These are the most expensive specs in the upgrade area (one cluster each,
// plus N rolling restarts), so they gate at the Lowest depth tier: they run in
// the full sweep (TEST_DEPTH=4) and are excluded from ordinary PR runs.
var _ = Describe("DocumentDB upgrade — schema across multiple minors",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.LowestLevelLabel,
	Serial, func() {
		const (
			dbName   = "upgrade_multiversion"
			collName = "seed"
		)

		var (
			chain  []string
			ctx    context.Context
			cancel context.CancelFunc
		)

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Lowest)
			skipUnlessUpgradeEnabled()
			chain = documentDBVersionChain()
			ctx, cancel = context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { cancel() })
		})

		// createAt provisions a fresh DocumentDB pinned to version, waits for
		// Ready, and returns its namespaced key. Cleanup is registered for the
		// caller. schemaVersion is left unset at creation (two-phase); each spec
		// sets it explicitly when it wants a migration.
		createAt := func(name, version string) types.NamespacedName {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			c := env.Client

			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(name, ns, "2Gi")
			// Drive the version via documentDBVersion, so the raw image fields
			// must stay empty for the mixin to take effect.
			vars["DOCUMENTDB_IMAGE"] = ""
			vars["GATEWAY_IMAGE"] = ""
			vars["DOCUMENTDB_VERSION"] = version

			dd, err := documentdb.Create(ctx, c, ns, name, documentdb.CreateOptions{
				Base:          "documentdb",
				Mixins:        []string{"documentdb_version"},
				Vars:          vars,
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred(), "create DocumentDB %s/%s at %s", ns, name, version)
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: name}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "DocumentDB did not reach Ready on %s", version)

			Eventually(schemaVersionGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Equal(version), "initial schema version should be %s", version)

			return key
		}

		// seedData writes the small dataset and returns once it is committed.
		seedData := func(ns, name string) {
			env := e2e.SuiteEnv()
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, name)
			Expect(err).NotTo(HaveOccurred(), "connect to DocumentDB gateway to seed")
			defer func() { _ = handle.Close(ctx) }()

			inserted, err := sharedmongo.Seed(ctx, handle.Client(), dbName, collName, seed.SmallDataset())
			Expect(err).NotTo(HaveOccurred(), "seed %s.%s", dbName, collName)
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
		}

		// expectSeedIntact reconnects and asserts the seeded document count is
		// unchanged — the data-survival half of every assertion below.
		expectSeedIntact := func(ns, name, afterWhat string) {
			env := e2e.SuiteEnv()
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, name)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway after %s", afterWhat)
			defer func() { _ = handle.Close(ctx) }()

			n, err := sharedmongo.Count(ctx, handle.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s after %s", dbName, collName, afterWhat)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across %s", afterWhat)
		}

		// upgradeTo patches both knobs in one step and waits for the binary
		// rollout, the schema migration, and Ready.
		upgradeTo := func(key types.NamespacedName, version string) {
			env := e2e.SuiteEnv()
			c := env.Client

			fresh, err := shareddb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before upgrade to %s", version)
			Expect(shareddb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.DocumentDBVersion = version
				s.SchemaVersion = version
			})).To(Succeed(), "patch DocumentDB to version %s", version)

			Eventually(statusDocumentDBImageGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(ContainSubstring(version), "status.documentDBImage did not advance to %s", version)

			Eventually(schemaVersionGetter(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(version), "schema did not migrate to %s", version)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB not Ready after upgrade to %s", version)
		}

		It("migrates in a single step when the upgrade skips more than one minor", func() {
			const ddName = "upgrade-multiminor"

			// Span the whole chain so the jump is as aggressive as the configured
			// versions allow.
			Expect(len(chain)).To(BeNumerically(">=", 2),
				"%s must list at least two versions", envDocumentDBVersionChain)
			from, to := chain[0], chain[len(chain)-1]
			if minorDistance(from, to) < 2 {
				Skip("configured version chain " + envOr(envDocumentDBVersionChain, defaultDocumentDBVersionChain) +
					" does not span more than one minor; nothing to prove")
			}

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			c := env.Client

			By("creating a DocumentDB pinned to " + from + " and seeding data")
			key := createAt(ddName, from)
			seedData(key.Namespace, ddName)

			By("jumping straight to " + to + ", skipping the intermediate minors")
			upgradeTo(key, to)

			By("verifying the operator did not report the jump as blocked")
			// A missing update script anywhere in the from→to range would leave
			// SchemaUpgradeBlocked=True and the schema behind; assert the
			// condition explicitly so a regression names the cause rather than
			// just timing out above.
			cond := schemaUpgradeBlockedGetter(ctx, c, key)()
			if cond != nil {
				Expect(string(cond.Status)).To(Equal("False"),
					"multi-minor jump %s → %s was blocked: %s", from, to, cond.Message)
			}

			By("verifying seeded data survived the multi-minor migration")
			expectSeedIntact(key.Namespace, ddName, "multi-minor jump "+from+" → "+to)
		})

		It("migrates step by step through every version in the chain", func() {
			const ddName = "upgrade-chain"

			// The single-hop case is already covered by upgrade_schema_test.go;
			// this spec only earns its cost with three or more versions.
			if len(chain) < 3 {
				Skip("configured version chain has fewer than 3 versions; " +
					"the single-hop case is covered by upgrade_schema_test.go")
			}

			By("creating a DocumentDB pinned to " + chain[0] + " and seeding data")
			key := createAt(ddName, chain[0])
			seedData(key.Namespace, ddName)

			for _, version := range chain[1:] {
				By("upgrading to " + version)
				upgradeTo(key, version)
				expectSeedIntact(key.Namespace, ddName, "upgrade to "+version)
			}
		})
	})
