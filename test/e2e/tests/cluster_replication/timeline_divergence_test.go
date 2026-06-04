package clusterreplication

// Tests for https://github.com/documentdb/documentdb-kubernetes-operator/issues/375
// "Multi-cloud failover: WAL timeline divergence after promotion leaves replica unrecoverable"
//
// These tests are designed to REPRODUCE the reported bugs and are expected to FAIL
// until the operator is fixed. The three sub-issues tested:
//
//  1. Rapid back-to-back failovers cause WAL timeline divergence on the demoted node,
//     leaving it unable to reconnect via WAL streaming.
//  2. After a successful promotion, the new primary's CNPG cluster retains a stale
//     promotionToken, causing CNPG to report "Cluster is unrecoverable".
//  3. spec.instancesPerNode is only applied to the primary CNPG cluster; the replica
//     always gets 1 instance regardless of the configured value.

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// ---------------------------------------------------------------------------
// Sub-issue 1 & 2: Rapid back-to-back failover + stale promotionToken
// ---------------------------------------------------------------------------

// PDescribe: skipped until issue #375 is fixed in the operator.
var _ = PDescribe("Issue #375: rapid back-to-back failover causes WAL timeline divergence",
	Ordered, ContinueOnFailure,
	Label(e2e.ClusterReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName = "td-primary"
			replicaName = "td-replica"
			testDB      = "timeline_divergence_db"
		)
		var (
			ctx           context.Context
			ns            string
			c             client.Client
			primaryHandle *emongo.Handle
			replicaHandle *emongo.Handle
		)

		BeforeAll(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			ns = namespaces.NamespaceForSpec(e2e.ClusterReplicationLabel + "-timeline")
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			By("creating the primary DocumentDB CR")
			primaryDD, err := documentdb.Create(ctx, c, ns, primaryName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars: mergeVars(baseVars(), replicationVars(
					primaryName, primaryName, replicaName,
				)),
			})
			Expect(err).ToNot(HaveOccurred(), "creating primary DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, primaryDD, 3*time.Minute)
			})

			By("waiting for the primary to become Ready")
			primaryKey := types.NamespacedName{Namespace: ns, Name: primaryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, primaryKey),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "primary should reach Ready")

			By("creating bridge ExternalName services for cross-CR DNS resolution")
			createReplicationBridgeServices(ctx, c, ns, primaryName, replicaName)

			By("creating the replica DocumentDB CR")
			replicaDD, err := documentdb.Create(ctx, c, ns, replicaName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars: mergeVars(baseVars(), replicationVars(
					primaryName, primaryName, replicaName,
				)),
			})
			Expect(err).ToNot(HaveOccurred(), "creating replica DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, replicaDD, 3*time.Minute)
			})

			By("waiting for the replica to become Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationReady),
				timeouts.PollInterval(timeouts.ClusterReplicationReady),
			).Should(Succeed(), "replica should reach Ready")
		})

		AfterAll(func() {
			if primaryHandle != nil {
				_ = primaryHandle.Client().Database(testDB).Drop(ctx)
				_ = primaryHandle.Close(ctx)
			}
			if replicaHandle != nil {
				_ = replicaHandle.Client().Database(testDB).Drop(ctx)
				_ = replicaHandle.Close(ctx)
			}
		})

		It("seeds data and verifies initial replication is healthy", func() {
			const coll = "td_seed"

			By("connecting to the primary and inserting seed data")
			var err error
			primaryHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(), "connect to primary gateway")

			docs := []interface{}{
				bson.M{"_id": "s1", "origin": "original-primary", "seq": 1},
				bson.M{"_id": "s2", "origin": "original-primary", "seq": 2},
			}
			_, err = primaryHandle.Client().Database(testDB).Collection(coll).InsertMany(ctx, docs)
			Expect(err).ToNot(HaveOccurred(), "seed primary")

			By("verifying data replicates to the replica")
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to replica gateway")

			Eventually(func(g Gomega) {
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(2)), "replica should have seed data")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			// Close handles before failover
			_ = primaryHandle.Close(ctx)
			primaryHandle = nil
			_ = replicaHandle.Close(ctx)
			replicaHandle = nil
		})

		It("first failover: promotes replica to primary", func() {
			By(fmt.Sprintf("patching both CRs to set primary=%s", replicaName))

			primaryDD := getDD(ctx, ns, primaryName)
			err := documentdb.PatchSpec(ctx, c, primaryDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch primary to demote")

			replicaDD := getDD(ctx, ns, replicaName)
			err = documentdb.PatchSpec(ctx, c, replicaDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch replica to promote")

			By("waiting for CNPG roles to swap")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, replicaName)
				g.Expect(cnpg).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).To(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"replica CNPG should be self-designated as primary",
				)
			},
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "replica should become primary")

			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, primaryName)
				g.Expect(cnpg).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).ToNot(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"original primary should no longer be self-designated",
				)
			},
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "original primary should become replica")
		})

		// Sub-issue 2: stale promotionToken
		// After a successful promotion, the new primary's CNPG cluster should
		// have an empty promotionToken. The bug is that the operator leaves the
		// token populated, causing CNPG to report "Cluster is unrecoverable /
		// Promotion token content is not correct".
		It("sub-issue 2: promotionToken should be cleared after successful promotion", func() {
			By("waiting for the new primary (replica CR) to reach Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "new primary should reach Ready")

			By("checking that promotionToken is cleared on the new primary's CNPG cluster")
			cnpg := findCNPGCluster(ctx, c, ns, replicaName)
			Expect(cnpg).ToNot(BeNil(), "new primary CNPG cluster should exist")
			Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
			Expect(cnpg.Spec.ReplicaCluster.PromotionToken).To(BeEmpty(),
				"promotionToken should be cleared after successful promotion, "+
					"but the operator leaves it populated (issue #375 sub-issue 2)")
		})

		// Sub-issue 1: rapid back-to-back failover
		// Immediately promote back to original primary WITHOUT waiting for
		// replication to be fully healthy. This triggers WAL timeline divergence
		// on the demoted node, making it unable to reconnect.
		It("second failover: promotes back to original primary immediately", func() {
			By(fmt.Sprintf("patching both CRs to set primary=%s (rapid switchback)", primaryName))

			// Promote back to original primary WITHOUT waiting for
			// replication health — this is the trigger for timeline divergence
			replicaDD := getDD(ctx, ns, replicaName)
			err := documentdb.PatchSpec(ctx, c, replicaDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = primaryName
			})
			Expect(err).ToNot(HaveOccurred(), "patch replica to demote (rapid switchback)")

			primaryDD := getDD(ctx, ns, primaryName)
			err = documentdb.PatchSpec(ctx, c, primaryDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = primaryName
			})
			Expect(err).ToNot(HaveOccurred(), "patch primary to promote (rapid switchback)")

			By("waiting for CNPG roles to swap back")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, primaryName)
				g.Expect(cnpg).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).To(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"original primary should be self-designated again",
				)
			},
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "original primary should become primary again")

			By("waiting for the restored primary to reach Ready")
			primaryKey := types.NamespacedName{Namespace: ns, Name: primaryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, primaryKey),
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "restored primary should reach Ready")
		})

		// After the rapid back-to-back failover, the demoted node (replicaName)
		// should be able to receive replication from the restored primary.
		// Issue #375: the WAL receiver fails with
		//   FATAL: requested starting point ... on timeline N is not in this server's history
		// and pg_stat_wal_receiver is empty, so replication is silently broken.
		It("sub-issue 1: replication should work after rapid back-to-back failover", func() {
			const coll = "td_after_switchback"

			By("connecting to the restored primary and writing new data")
			var err error
			primaryHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(), "connect to restored primary")

			docs := []interface{}{
				bson.M{"_id": "bb-1", "origin": "restored-primary", "phase": "after-switchback"},
				bson.M{"_id": "bb-2", "origin": "restored-primary", "phase": "after-switchback"},
				bson.M{"_id": "bb-3", "origin": "restored-primary", "phase": "after-switchback"},
			}
			_, err = primaryHandle.Client().Database(testDB).Collection(coll).InsertMany(ctx, docs)
			Expect(err).ToNot(HaveOccurred(), "write to restored primary")

			By("connecting to the demoted node (now a replica again)")
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to demoted node gateway")

			By("waiting for replication: data on restored primary should appear on the demoted node")
			// Issue #375: this will FAIL because the demoted node has WAL timeline
			// divergence and cannot reconnect to the new primary via streaming replication.
			// The WAL receiver exits with FATAL and pg_stat_wal_receiver is empty.
			Eventually(func(g Gomega) {
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(3)),
					"demoted node should replicate data from restored primary; "+
						"issue #375: WAL timeline divergence leaves it unable to reconnect")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			By("verifying document content on the demoted node")
			var doc bson.M
			err = replicaHandle.Client().Database(testDB).Collection(coll).
				FindOne(ctx, bson.M{"_id": "bb-1"}).Decode(&doc)
			Expect(err).ToNot(HaveOccurred(), "read from demoted node")
			Expect(doc["origin"]).To(Equal("restored-primary"))
			Expect(doc["phase"]).To(Equal("after-switchback"))
		})
	})

// ---------------------------------------------------------------------------
// Sub-issue 3: instancesPerNode not honored on replica
// ---------------------------------------------------------------------------

// PDescribe: skipped until issue #375 is fixed in the operator.
var _ = PDescribe("Issue #375 sub-issue 3: instancesPerNode should be honored on replica",
	Ordered,
	Label(e2e.ClusterReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName      = "ip-primary"
			replicaName      = "ip-replica"
			desiredInstances = 3
		)
		var (
			ctx context.Context
			ns  string
			c   client.Client
		)

		BeforeAll(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			ns = namespaces.NamespaceForSpec(e2e.ClusterReplicationLabel + "-instances")
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := mergeVars(baseVars(), replicationVars(
				primaryName, primaryName, replicaName,
			))
			// Override INSTANCES to 3 to test instancesPerNode propagation
			vars["INSTANCES"] = fmt.Sprintf("%d", desiredInstances)

			By("creating the primary DocumentDB CR with instancesPerNode=3")
			primaryDD, err := documentdb.Create(ctx, c, ns, primaryName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars:   vars,
			})
			Expect(err).ToNot(HaveOccurred(), "creating primary DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, primaryDD, 3*time.Minute)
			})

			By("waiting for the primary to become Ready")
			primaryKey := types.NamespacedName{Namespace: ns, Name: primaryName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, primaryKey),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "primary should reach Ready")

			By("creating bridge services")
			createReplicationBridgeServices(ctx, c, ns, primaryName, replicaName)

			By("creating the replica DocumentDB CR with instancesPerNode=3")
			replicaDD, err := documentdb.Create(ctx, c, ns, replicaName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"cluster-replication"},
				Vars:   vars,
			})
			Expect(err).ToNot(HaveOccurred(), "creating replica DocumentDB")
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, replicaDD, 3*time.Minute)
			})

			By("waiting for the replica to become Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationReady),
				timeouts.PollInterval(timeouts.ClusterReplicationReady),
			).Should(Succeed(), "replica should reach Ready")
		})

		It("primary CNPG cluster should have the requested number of instances", func() {
			cnpg := findCNPGCluster(ctx, c, ns, primaryName)
			Expect(cnpg).ToNot(BeNil(), "primary CNPG cluster should exist")
			Expect(cnpg.Spec.Instances).To(Equal(desiredInstances),
				"primary CNPG cluster should have %d instances", desiredInstances)
		})

		It("replica CNPG cluster should have the requested number of instances", func() {
			// Issue #375 sub-issue 3: the replica always gets 1 instance regardless
			// of spec.instancesPerNode. The operator only applies instancesPerNode
			// to the primary CNPG cluster.
			cnpg := findCNPGCluster(ctx, c, ns, replicaName)
			Expect(cnpg).ToNot(BeNil(), "replica CNPG cluster should exist")
			Expect(cnpg.Spec.Instances).To(Equal(desiredInstances),
				"replica CNPG cluster should have %d instances (issue #375 sub-issue 3: "+
					"operator only applies instancesPerNode to primary)", desiredInstances)
		})
	})

// assertCNPGContinuousArchivingHealthy is a diagnostic helper that checks
// whether the CNPG cluster's ContinuousArchiving condition is True.
// This is a necessary (but not sufficient) indicator that WAL shipping
// is functioning; it does not verify streaming replication directly.
func assertCNPGContinuousArchivingHealthy(ctx context.Context, c client.Client, ns, ddName string) func(g Gomega) {
	return func(g Gomega) {
		cnpg := findCNPGCluster(ctx, c, ns, ddName)
		g.Expect(cnpg).ToNot(BeNil())

		// Check if the cluster is in a healthy state
		for _, cond := range cnpg.Status.Conditions {
			if cond.Type == string(cnpgv1.ConditionContinuousArchiving) {
				g.Expect(cond.Status).To(Equal("True"),
					"CNPG cluster %s should have healthy continuous archiving", ddName)
			}
		}
	}
}
