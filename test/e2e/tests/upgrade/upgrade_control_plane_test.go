package upgrade

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/helmop"
	e2emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// DocumentDB upgrade — control plane: uninstalls the operator, installs
// a previously-released chart, deploys a DocumentDB, seeds data, then
// upgrades the chart to the PR's built chart and verifies the operator
// is healthy and the seeded data survived the bounce.
//
// Residual risk: the "previous-released chart" is NOT pinned in code.
// It must be supplied by the caller via E2E_UPGRADE_PREVIOUS_CHART and
// E2E_UPGRADE_PREVIOUS_VERSION (e.g. the chart published on
// GitHub Releases). Hard-coding "latest" here would break every time a
// new release is cut, so the spec fail-closed skips when unset.
var _ = Describe("DocumentDB upgrade — control plane",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-cp"
			dbName   = "upgrade_cp"
			collName = "seed"
		)
		var (
			releaseName    string
			operatorNs     string
			previousChart  string
			previousVer    string
			currentChart   string
			currentVer     string
			operatorCtx    context.Context
			operatorCancel context.CancelFunc
		)

		BeforeAll(func() {
			skipUnlessUpgradeEnabled()
			releaseName = envOr(envReleaseName, defaultReleaseName)
			operatorNs = envOr(envOperatorNamespace, defaultOperatorNamespace)
			previousChart = requireEnv(envPreviousChart,
				"chart ref to the previous released operator chart (e.g. documentdb/documentdb-operator or a local tgz)")
			previousVer = requireEnv(envPreviousVersion,
				"semver of the previous released chart; see GitHub Releases or the published Helm index")
			currentChart = requireEnv(envCurrentChart,
				"chart ref to the PR's built chart (path to the unpacked chart dir or packaged tgz)")
			currentVer = envOr(envCurrentVersion, "")
		})

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.High)
			operatorCtx, operatorCancel = context.WithTimeout(context.Background(), controlPlaneUpgradeTimeout)
			DeferCleanup(func() { operatorCancel() })
		})

		It("upgrades operator from previous released chart to current and retains data", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			c := env.Client

			By("uninstalling any pre-existing operator release (idempotent)")
			Expect(helmop.Uninstall(operatorCtx, releaseName, operatorNs)).To(Succeed())

			By("installing the previous released operator chart")
			Expect(helmop.Install(operatorCtx, releaseName, operatorNs, previousChart, previousVer, nil)).
				To(Succeed(), "install previous chart %s@%s", previousChart, previousVer)
			Expect(helmop.WaitOperatorReady(operatorCtx, env, operatorNs, 3*time.Minute)).To(Succeed())

			By("creating a DocumentDB on the previous operator")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(operatorCtx, c, ns)
			createCredentialSecret(operatorCtx, c, ns)

			dd, err := documentdb.Create(operatorCtx, c, ns, ddName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(ddName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred(), "create DocumentDB %s/%s", ns, ddName)
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: ddName}
			Eventually(assertions.AssertDocumentDBReady(operatorCtx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "DocumentDB did not reach Ready under previous operator")

			By("seeding data on the previous operator")
			docs := seed.SmallDataset()
			handle, err := e2emongo.NewFromDocumentDB(operatorCtx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "connect to DocumentDB gateway")
			inserted, err := e2emongo.Seed(operatorCtx, handle.Client(), dbName, collName, docs)
			Expect(err).NotTo(HaveOccurred(), "seed %s.%s", dbName, collName)
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
			// Explicit close before the helm upgrade: the port-forward
			// goroutine must not outlive the operator bounce.
			Expect(handle.Close(operatorCtx)).To(Succeed())

			By("upgrading the chart to the PR's built version")
			Expect(helmop.Upgrade(operatorCtx, releaseName, operatorNs, currentChart, currentVer, nil)).
				To(Succeed(), "upgrade to current chart %s@%s", currentChart, currentVer)
			Expect(helmop.WaitOperatorReady(operatorCtx, env, operatorNs, 5*time.Minute)).To(Succeed())

			By("verifying the DocumentDB CR is still reconciled by the new operator")
			Eventually(assertions.AssertDocumentDBReady(operatorCtx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready after operator upgrade")

			By("verifying seeded data survived the operator bounce")
			handle2, err := e2emongo.NewFromDocumentDB(operatorCtx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway")
			DeferCleanup(func(ctx SpecContext) { _ = handle2.Close(ctx) })
			n, err := e2emongo.Count(operatorCtx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across operator upgrade")
		})
	})
