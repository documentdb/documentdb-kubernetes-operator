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

var _ = Describe("DocumentDB backup — expired backup cleanup",
	Label(e2e.BackupLabel, e2e.NeedsCSISnapshotsLabel), e2e.MediumLevelLabel,
	func() {
		const (
			clusterName = "backup-expire"
			backupName  = "backup-expire-1"
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

		It("deletes the Backup CR once status.expiredAt is in the past", func() {
			dd, err := documentdb.Create(ctx, c, ns, clusterName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          baseVars(clusterName, ns, "2Gi"),
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dd, 3*time.Minute)
			})

			// 1. Source cluster healthy so the backup can actually run.
			key := types.NamespacedName{Namespace: ns, Name: clusterName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// 2. Create an on-demand Backup CR and wait for Completed
			// before fast-forwarding expiration. Marking an in-flight
			// backup expired would race the snapshot controller and
			// leak CNPG state, and the retentionDays contract is
			// defined against completed backups anyway.
			_, err = bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   clusterName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(), "create Backup CR %s/%s", ns, backupName)
			// The expired-cleanup path makes the Backup disappear on
			// its own; bkp.Delete already swallows IsNotFound so this
			// cleanup stays correct even after the spec succeeds.
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})
			_, err = bkp.WaitForCompleted(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"Backup %s/%s did not reach Completed", ns, backupName)

			// 3. Fast-forward expiration via the status subresource.
			// The next reconcile sees IsExpired() == true and deletes
			// the CR — this is the behaviour the retired
			// test-backup-and-restore workflow used to cover.
			Expect(bkp.MarkExpired(ctx, c, ns, backupName, time.Now().Add(-1*time.Hour))).
				To(Succeed(), "patch status.expiredAt on Backup %s/%s", ns, backupName)

			// 4. Operator must garbage-collect the Backup CR. The
			// status patch from MarkExpired fires a watch event that
			// the controller picks up within seconds, so 3 minutes is
			// comfortable headroom even on a loaded CI worker — and
			// well clear of the 1-minute periodic requeue cadence in
			// backup_controller.go.
			Expect(bkp.WaitForBackupDeleted(ctx, c, ns, backupName, 3*time.Minute)).
				To(Succeed(), "Backup %s/%s was not garbage-collected after expiration", ns, backupName)
		})
	})
