package backup

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	bkp "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/backup"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB restore — recovery.persistentVolume (retained PV)",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel,
		e2e.SlowLabel, e2e.DestructiveLabel),
	e2e.MediumLevelLabel,
	func() {
		const (
			sourceName   = "pv-recovery-src"
			recoveryName = "pv-recovery-dst"
			dbName       = "pv_recovery_db"
			collName     = "testCollection"
		)
		var (
			ctx context.Context
			ns  string
			c   client.Client
		)

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			skipUnlessCSISnapshotsUsable(ctx, c)
			ns = namespaces.NamespaceForSpec(e2e.BackupLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)
		})

		It("adopts a retained PV from a deleted source cluster and rebuilds DocumentDB from it", func() {
			// 1. Bring up the source cluster and seed data that must
			// survive the source-cluster deletion via the retained PV.
			src, err := documentdb.Create(ctx, c, ns, sourceName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(sourceName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred())
			srcKey := types.NamespacedName{Namespace: ns, Name: sourceName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, srcKey),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			h, err := emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, sourceName)
			Expect(err).NotTo(HaveOccurred(), "connect to source DocumentDB")
			inserted, err := emongo.Seed(ctx, h.Client(), dbName, collName, seed.SmallDataset())
			Expect(err).NotTo(HaveOccurred())
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
			Expect(h.Close(ctx)).To(Succeed())

			// 2. Delete the source cluster. Default reclaimPolicy is
			// Retain (per the DocumentDB CRD default), so the PV
			// persists with the data blocks intact.
			Expect(documentdb.Delete(ctx, c, src, 3*time.Minute)).To(Succeed())

			// 3. Discover the retained PV that belonged to the now-
			// deleted source cluster. FindRetainedPV matches both
			// CNPG and DocumentDB cluster labels as well as a
			// claim-name prefix, which is how the reference workflow
			// locates the same volume.
			pv, err := bkp.FindRetainedPV(ctx, c, ns, sourceName)
			Expect(err).NotTo(HaveOccurred(),
				"no retained PV found for deleted source cluster %s/%s", ns, sourceName)
			Expect(pv).NotTo(BeNil())

			// 4. Create the recovery DocumentDB that points at that
			// PV's name. The operator should rehydrate the data,
			// including creating and then cleaning up a temp PVC
			// named <name>-pv-recovery-temp.
			dst := createRecoveryDocumentDB(ctx, c, ns, recoveryName,
				"recovery_from_pv.yaml.template",
				map[string]string{"PV_NAME": pv.Name})
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dst, 3*time.Minute)
			})
			dstKey := types.NamespacedName{Namespace: ns, Name: recoveryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, dstKey),
				timeouts.For(timeouts.RestoreComplete),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "recovery DocumentDB %s/%s did not become ready", ns, recoveryName)

			// 5. Temp PVC used during the adoption dance must be gone.
			tempPVC := fmt.Sprintf("%s-pv-recovery-temp", recoveryName)
			Expect(bkp.WaitForPVCDeleted(ctx, c, ns, tempPVC, 3*time.Minute)).
				To(Succeed(), "temp adoption PVC %s/%s should be cleaned up", ns, tempPVC)

			// 6. Data that was seeded into the deleted source is
			// visible through the new cluster — the whole point of
			// the PV-recovery scenario.
			rh, err := emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, recoveryName)
			Expect(err).NotTo(HaveOccurred(), "connect to recovery DocumentDB")
			DeferCleanup(func(ctx SpecContext) { _ = rh.Close(ctx) })
			n, err := emongo.Count(ctx, rh.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"PV-recovered cluster should contain the full seeded dataset")
		})
	})
