package backup

import (
	"context"
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

var _ = Describe("DocumentDB restore — recovery.backup (CSI snapshot)",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel, e2e.SlowLabel), e2e.MediumLevelLabel,
	func() {
		const (
			sourceName   = "restore-src"
			backupName   = "restore-src-backup"
			recoveryName = "restore-dst"
			dbName       = "restore_db"
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

		It("restores a new DocumentDB from a prior on-demand backup and sees the seeded data", func() {
			// 1. Source cluster + seeded data.
			src, err := documentdb.Create(ctx, c, ns, sourceName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(sourceName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, src, 3*time.Minute)
			})
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

			// 2. On-demand Backup → Completed.
			_, err = bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   sourceName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})
			_, err = bkp.WaitForCompleted(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"source backup %s/%s did not complete", ns, backupName)

			// 3. Recovery DocumentDB sourced from that Backup name.
			dst := createRecoveryDocumentDB(ctx, c, ns, recoveryName,
				"recovery_from_backup.yaml.template",
				map[string]string{"BACKUP_NAME": backupName})
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dst, 3*time.Minute)
			})
			dstKey := types.NamespacedName{Namespace: ns, Name: recoveryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, dstKey),
				timeouts.For(timeouts.RestoreComplete),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "recovery DocumentDB %s/%s did not become ready", ns, recoveryName)

			// 4. Data survived the restore.
			rh, err := emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, recoveryName)
			Expect(err).NotTo(HaveOccurred(), "connect to recovery DocumentDB")
			DeferCleanup(func(ctx SpecContext) { _ = rh.Close(ctx) })
			n, err := emongo.Count(ctx, rh.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"restored cluster should contain the full seeded dataset")
		})
	})
