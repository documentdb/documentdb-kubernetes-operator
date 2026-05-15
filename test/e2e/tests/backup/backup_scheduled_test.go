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
	shareddoc "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB backup — scheduled CSI snapshots",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel, e2e.SlowLabel), e2e.MediumLevelLabel,
	func() {
		const (
			clusterName   = "backup-scheduled"
			scheduledName = "backup-scheduled-every-minute"
			// DocumentDB operator's ScheduledBackup uses
			// robfig/cron.ParseStandard (5-field crontab, no seconds
			// slot). See operator/src/internal/controller/
			// scheduledbackup_controller.go. "*/1 * * * *" means every
			// minute — same cadence the workflow uses.
			schedule = "*/1 * * * *"
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

		It("creates a child Backup that reaches Completed on the configured cadence", func() {
			dd, err := documentdb.Create(ctx, c, ns, clusterName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(clusterName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddoc.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: clusterName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			_, err = bkp.CreateScheduled(ctx, c, bkp.ScheduledBackupVars{
				Name:          scheduledName,
				Namespace:     ns,
				ClusterName:   clusterName,
				Schedule:      schedule,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(),
				"create ScheduledBackup %s/%s", ns, scheduledName)
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.DeleteScheduled(ctx, c, ns, scheduledName, 1*time.Minute)
			})

			// The cron fires once a minute, so the first child Backup
			// will appear within ~60s and then needs BackupComplete to
			// finish snapshotting. BackupComplete (10m) is ample for
			// both stages.
			child, err := bkp.WaitForFirstChildCompleted(
				ctx, c, ns, clusterName, timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"no completed child Backup observed for cluster %s/%s", ns, clusterName)
			Expect(child).NotTo(BeNil())
			Expect(string(child.Status.Phase)).To(Equal("completed"))

			// A VolumeSnapshot must back the child Backup — the whole
			// point of the scheduled path is that cron-driven runs use
			// the CSI backend identically to the on-demand path.
			snap, err := bkp.WaitForSnapshotForBackup(ctx, c, ns, child.Name,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"no ReadyToUse VolumeSnapshot observed for child Backup %s/%s", ns, child.Name)
			Expect(bkp.IsSnapshotReady(snap)).To(BeTrue())
		})
	})
