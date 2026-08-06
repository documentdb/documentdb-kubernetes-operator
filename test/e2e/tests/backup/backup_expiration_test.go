package backup

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	bkp "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/backup"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB backup — expiration cleanup",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel), e2e.MediumLevelLabel,
	func() {
		const clusterName = "backup-expire"
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
			// A completed backup marked expired would otherwise race the
			// snapshot controller, so every spec here starts from a live
			// cluster and lets createCompletedBackup settle the snapshot.
			provisionReadyCluster(ctx, c, ns, clusterName)
		})

		It("garbage-collects the Backup CR and its VolumeSnapshot once status.expiredAt is in the past", func() {
			const backupName = "backup-expire-1"
			createCompletedBackup(ctx, c, ns, clusterName, backupName)

			// A completed CSI backup must have produced a ready
			// VolumeSnapshot. Waiting on ReadyToUse (rather than a bare
			// list) removes any dependence on CNPG's snapshot-vs-status
			// ordering and pins the snapshot identity we expect the
			// operator's cascading GC (Backup → CNPG Backup →
			// VolumeSnapshot) to reclaim — a leaked snapshot is the more
			// user-visible failure than a lingering CR.
			snap, err := bkp.WaitForSnapshotForBackup(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"no ready VolumeSnapshot for completed Backup %s/%s", ns, backupName)
			Expect(snap).NotTo(BeNil())

			// Fast-forward expiration via the status subresource. The
			// next reconcile sees IsExpired() == true and deletes the CR.
			// This must use the status subresource: Backup declares
			// +kubebuilder:subresource:status, so a plain Patch is a
			// silent no-op (see MarkExpired).
			Expect(bkp.MarkExpired(ctx, c, ns, backupName, time.Now().Add(-1*time.Hour))).
				To(Succeed(), "patch status.expiredAt on Backup %s/%s", ns, backupName)

			// The status patch fires a watch event, so deletion is
			// event-driven — 3 minutes is comfortable headroom over the
			// 1-minute periodic requeue in backup_controller.go.
			Expect(bkp.WaitForBackupDeleted(ctx, c, ns, backupName, 3*time.Minute)).
				To(Succeed(), "Backup %s/%s was not garbage-collected after expiration", ns, backupName)
			Expect(bkp.WaitForSnapshotsDeletedForBackup(ctx, c, ns, backupName, 3*time.Minute)).
				To(Succeed(), "VolumeSnapshot for Backup %s/%s leaked after expiration", ns, backupName)
		})

		It("retains a Backup CR whose status.expiredAt is still in the future", func() {
			const backupName = "backup-expire-2"
			createCompletedBackup(ctx, c, ns, clusterName, backupName)

			// The retention guard is only meaningful if expiredAt is
			// actually in the future — otherwise "not deleted" proves
			// nothing. createCompletedBackup uses RetentionDays=1, so
			// assert the controller populated expiredAt ~1 day out.
			b, err := bkp.Get(ctx, c, ns, backupName)
			Expect(err).NotTo(HaveOccurred())
			Expect(b.Status.ExpiredAt).NotTo(BeNil(),
				"Backup %s/%s has no status.expiredAt; retention premise is vacuous", ns, backupName)
			Expect(b.Status.ExpiredAt.Time).To(BeTemporally(">", time.Now()),
				"status.expiredAt must be in the future for the retention guard to mean anything")

			snap, err := bkp.WaitForSnapshotForBackup(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"no ready VolumeSnapshot for completed Backup %s/%s", ns, backupName)

			// Neither the Backup CR nor its snapshot may disappear while
			// unexpired. This guards user data against a regression that
			// inverts IsExpired() or makes cleanup over-aggressive. We
			// never call MarkExpired — both must simply persist across
			// several reconcile passes (status writes fire watch events,
			// so a broken controller would react well within 45s).
			Consistently(func() error {
				if _, err := bkp.Get(ctx, c, ns, backupName); err != nil {
					return err
				}
				return c.Get(ctx, client.ObjectKey{Namespace: ns, Name: snap.Name}, &snapshotv1.VolumeSnapshot{})
			}, 45*time.Second, bkp.DefaultPollInterval).
				Should(Succeed(), "Backup %s/%s or its VolumeSnapshot was deleted before expiry", ns, backupName)
		})
	})
