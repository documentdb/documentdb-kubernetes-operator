package backup

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	bkp "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/backup"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB backup — on-demand CSI snapshot",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel), e2e.MediumLevelLabel,
	func() {
		const (
			clusterName = "backup-ondemand"
			backupName  = "backup-ondemand-1"
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

		It("takes a CSI volume snapshot and marks the Backup CR Completed", func() {
			dd, err := documentdb.Create(ctx, c, ns, clusterName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(clusterName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dd, 3*time.Minute)
			})

			// 1. Source cluster healthy before we ask for a backup.
			key := types.NamespacedName{Namespace: ns, Name: clusterName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// 2. Request an on-demand Backup. Rendering and applying
			// this CR is all it takes to trigger the operator path
			// that the workflow exercises end-to-end.
			_, err = bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   clusterName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(), "create Backup CR %s/%s", ns, backupName)
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})

			// 3. Wait for the Backup CR itself to go Completed. This
			// is the operator-visible signal that the CSI snapshot
			// finished and the backup metadata was persisted.
			done, err := bkp.WaitForCompleted(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"Backup %s/%s did not reach Completed", ns, backupName)
			Expect(string(done.Status.Phase)).To(Equal("completed"))

			// 4. Assert a VolumeSnapshot tagged for this Backup
			// reached ReadyToUse. This is what distinguishes the CSI
			// path from any other backup strategy.
			snap, err := bkp.WaitForSnapshotForBackup(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"no ReadyToUse VolumeSnapshot observed for Backup %s/%s", ns, backupName)
			Expect(snap).NotTo(BeNil())
			Expect(bkp.IsSnapshotReady(snap)).To(BeTrue(),
				"expected VolumeSnapshot %s to report ReadyToUse=true", snap.Name)
		})
	})
