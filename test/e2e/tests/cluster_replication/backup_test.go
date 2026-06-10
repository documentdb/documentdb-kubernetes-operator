package clusterreplication

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	bkp "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/backup"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	shareddoc "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// Cluster-replication backup tests verify that:
//  1. Backup on the primary DocumentDB completes successfully.
//  2. Backup on the replica DocumentDB is skipped (operator enforces
//     primary-only backup policy).
//  3. After failover, backup on the newly promoted primary succeeds.
var _ = Describe("DocumentDB cluster-replication backup",
	Label(e2e.ClusterReplicationLabel, e2e.NeedsCSISnapshotsLabel, e2e.BackupLabel),
	e2e.MediumLevelLabel, Ordered, func() {
		const (
			primaryName = "bk-primary"
			replicaName = "bk-replica"
		)
		var (
			ctx context.Context
			c   client.Client
			ns  string
		)

		BeforeAll(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			ns = namespaces.NamespaceForSpec("cluster-replication-backup")
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			By("creating the primary DocumentDB CR with CSI storage class")
			primaryDD, err := documentdb.Create(ctx, c, ns, primaryName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars: mergeVars(backupBaseVars(), replicationVars(
					primaryName, primaryName, replicaName,
				)),
			})
			Expect(err).ToNot(HaveOccurred(), "creating primary DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddoc.Delete(ctx, c, primaryDD, 3*time.Minute)
			})

			By("waiting for the primary to become Ready")
			primaryKey := types.NamespacedName{Namespace: ns, Name: primaryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, primaryKey),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "primary should reach Ready")

			By("creating bridge ExternalName services for cross-CR DNS resolution")
			createReplicationBridgeServices(ctx, c, ns, primaryName, replicaName)

			By("creating the replica DocumentDB CR with CSI storage class")
			replicaDD, err := documentdb.Create(ctx, c, ns, replicaName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars: mergeVars(backupBaseVars(), replicationVars(
					primaryName, primaryName, replicaName,
				)),
			})
			Expect(err).ToNot(HaveOccurred(), "creating replica DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddoc.Delete(ctx, c, replicaDD, 3*time.Minute)
			})

			By("waiting for the replica to become Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationReady),
				timeouts.PollInterval(timeouts.ClusterReplicationReady),
			).Should(Succeed(), "replica should reach Ready")
		})

		// -----------------------------------------------------------
		// Spec 1: Backup on the primary completes successfully.
		// -----------------------------------------------------------

		It("backup on the primary DocumentDB completes", func() {
			const backupName = "bk-primary-backup"

			By("creating a Backup CR for the primary DocumentDB")
			_, err := bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   primaryName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(), "create Backup CR %s/%s", ns, backupName)
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})

			By("waiting for the Backup CR to reach Completed")
			done, err := bkp.WaitForCompleted(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"Backup %s/%s did not reach Completed", ns, backupName)
			Expect(string(done.Status.Phase)).To(Equal("completed"))
		})

		// -----------------------------------------------------------
		// Spec 2: Backup on the replica is skipped.
		// The backup controller checks replicationContext.IsPrimary()
		// and sets the phase to "skipped" for non-primary clusters.
		// -----------------------------------------------------------

		It("backup on the replica DocumentDB is skipped", func() {
			const backupName = "bk-replica-backup"

			By("creating a Backup CR for the replica DocumentDB")
			_, err := bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   replicaName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(), "create Backup CR %s/%s", ns, backupName)
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})

			By("waiting for the Backup CR to reach the skipped phase")
			Eventually(func(g Gomega) {
				b, err := bkp.Get(ctx, c, ns, backupName)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(b.Status.Phase)).To(Equal("skipped"),
					"backup on replica should be skipped, got phase=%s", b.Status.Phase)
			},
				timeouts.For(timeouts.BackupComplete),
				timeouts.PollInterval(timeouts.BackupComplete),
			).Should(Succeed(), "backup on replica should reach skipped phase")
		})

		// -----------------------------------------------------------
		// Spec 3: After failover, backup on the new primary succeeds.
		// -----------------------------------------------------------

		It("after failover, backup on the promoted replica succeeds", func() {
			By(fmt.Sprintf("patching both CRs to promote %s as primary", replicaName))

			primaryDD := getDD(ctx, ns, primaryName)
			err := shareddoc.PatchSpec(ctx, c, primaryDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).NotTo(HaveOccurred(), "patch primary CR to demote")

			replicaDD := getDD(ctx, ns, replicaName)
			err = shareddoc.PatchSpec(ctx, c, replicaDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).NotTo(HaveOccurred(), "patch replica CR to promote")

			By("waiting for the promoted replica to become the designated primary")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, replicaName)
				g.Expect(cnpg).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).To(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"replica CNPG cluster should be self-designated as primary")
			},
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "replica should become designated primary")

			By("waiting for the promoted replica DocumentDB to reach Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "promoted replica should be Ready")

			const backupName = "bk-post-failover-backup"
			By("creating a Backup CR for the newly promoted primary")
			_, err = bkp.Create(ctx, c, bkp.BackupVars{
				Name:          backupName,
				Namespace:     ns,
				ClusterName:   replicaName,
				RetentionDays: 1,
			})
			Expect(err).NotTo(HaveOccurred(), "create Backup CR %s/%s", ns, backupName)
			DeferCleanup(func(ctx SpecContext) {
				_ = bkp.Delete(ctx, c, ns, backupName, 1*time.Minute)
			})

			By("waiting for the Backup CR to reach Completed")
			done, err := bkp.WaitForCompleted(ctx, c, ns, backupName,
				timeouts.For(timeouts.BackupComplete))
			Expect(err).NotTo(HaveOccurred(),
				"Backup %s/%s on promoted primary did not reach Completed", ns, backupName)
			Expect(string(done.Status.Phase)).To(Equal("completed"))
		})
	})
