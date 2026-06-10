package clusterreplication

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
	"go.mongodb.org/mongo-driver/v2/bson"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	shareddoc "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	sharedmongo "github.com/documentdb/documentdb-operator/test/shared/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// This file combines data-replication validation and failover-promotion tests
// into a single Ordered Describe block that shares one DocumentDB primary/replica
// pair, saving ~5 min of cluster provisioning time per run.
//
// Test order:
//   Phase 1 – CNPG configuration & data replication (non-destructive)
//   Phase 2 – Failover promotion & post-failover verification (mutates roles)

var _ = Describe("DocumentDB cluster replication — data replication & failover",
	Ordered,
	Label(e2e.ClusterReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName = "cr-primary"
			replicaName = "cr-replica"
			testDB      = "cluster_repl_db"
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
			ns = namespaces.NamespaceForSpec(e2e.ClusterReplicationLabel)
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
				_ = shareddoc.Delete(ctx, c, replicaDD, 3*time.Minute)
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

		// ---------------------------------------------------------------
		// Phase 1: CNPG configuration & data replication
		// ---------------------------------------------------------------

		It("verifies CNPG cluster configuration for both primary and replica", func() {
			By("verifying primary CNPG Cluster has correct ReplicaCluster config")
			primaryCNPG := findCNPGCluster(ctx, c, ns, primaryName)
			Expect(primaryCNPG).ToNot(BeNil(), "CNPG Cluster for primary should exist")
			Expect(primaryCNPG.Spec.ReplicaCluster).ToNot(BeNil(),
				"primary CNPG Cluster should have ReplicaCluster config")
			Expect(primaryCNPG.Spec.ReplicaCluster.Primary).To(
				Equal(primaryCNPG.Spec.ReplicaCluster.Self),
				"primary CNPG Cluster should be self-designated as primary")
			Expect(primaryCNPG.Spec.ExternalClusters).ToNot(BeEmpty(),
				"primary CNPG Cluster should have ExternalClusters")

			By("verifying replica CNPG Cluster has correct ReplicaCluster config")
			replicaCNPG := findCNPGCluster(ctx, c, ns, replicaName)
			Expect(replicaCNPG).ToNot(BeNil(), "CNPG Cluster for replica should exist")
			Expect(replicaCNPG.Spec.ReplicaCluster).ToNot(BeNil(),
				"replica CNPG Cluster should have ReplicaCluster config")
			Expect(replicaCNPG.Spec.ReplicaCluster.Primary).ToNot(
				Equal(replicaCNPG.Spec.ReplicaCluster.Self),
				"replica CNPG Cluster primary should differ from self")
			Expect(replicaCNPG.Spec.Bootstrap).ToNot(BeNil(),
				"replica CNPG Cluster should have Bootstrap config")
			Expect(replicaCNPG.Spec.Bootstrap.PgBaseBackup).ToNot(BeNil(),
				"replica should bootstrap via pg_basebackup")
			expectedSource := cnpgClusterName(replicaName, primaryName)
			Expect(replicaCNPG.Spec.Bootstrap.PgBaseBackup.Source).To(
				Equal(expectedSource),
				"replica pg_basebackup source should reference the primary")
			Expect(replicaCNPG.Spec.ExternalClusters).ToNot(BeEmpty(),
				"replica CNPG Cluster should have ExternalClusters")
		})

		It("replicates inserted documents from primary to replica", func() {
			const coll = "repl_smoke"

			By("connecting to both gateways")
			var err error
			primaryHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(), "connect to primary gateway")
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to replica gateway")

			docs := seed.SmallDataset()
			By(fmt.Sprintf("seeding %d documents into primary %s.%s", len(docs), testDB, coll))
			n, err := sharedmongo.Seed(ctx, primaryHandle.Client(), testDB, coll, docs)
			Expect(err).ToNot(HaveOccurred(), "seed primary")
			Expect(n).To(Equal(seed.SmallDatasetSize), "all documents should be accepted")

			By("verifying documents appear on the primary")
			cnt, err := sharedmongo.Count(ctx, primaryHandle.Client(), testDB, coll, nil)
			Expect(err).ToNot(HaveOccurred(), "count on primary")
			Expect(cnt).To(Equal(int64(seed.SmallDatasetSize)),
				"primary should have all seeded documents")

			By("waiting for documents to replicate to the replica")
			Eventually(func(g Gomega) {
				cnt, err := sharedmongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred(), "count on replica")
				g.Expect(cnt).To(Equal(int64(seed.SmallDatasetSize)),
					"replica should have all replicated documents")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed(), "data should replicate from primary to replica")
		})

		It("replicates document content faithfully", func() {
			const coll = "repl_content"
			doc := bson.M{
				"_id":     "repl-check-1",
				"message": "hello from primary",
				"value":   42,
			}

			By("inserting a document with known content into primary")
			_, err := primaryHandle.Client().Database(testDB).Collection(coll).InsertOne(ctx, doc)
			Expect(err).ToNot(HaveOccurred(), "insert on primary")

			By("waiting for the document to appear on the replica with correct content")
			Eventually(func(g Gomega) {
				var got bson.M
				err := replicaHandle.Client().Database(testDB).Collection(coll).
					FindOne(ctx, bson.M{"_id": "repl-check-1"}).Decode(&got)
				g.Expect(err).ToNot(HaveOccurred(), "find on replica")
				g.Expect(got["message"]).To(Equal("hello from primary"))
				g.Expect(got["value"]).To(Equal(int32(42)))
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed(), "document content should replicate faithfully")
		})

		It("replicates updates from primary to replica", func() {
			const coll = "repl_updates"

			By("inserting a document on the primary")
			_, err := primaryHandle.Client().Database(testDB).Collection(coll).
				InsertOne(ctx, bson.M{"_id": "upd-1", "status": "created"})
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the insert to replicate")
			Eventually(func(g Gomega) {
				cnt, err := sharedmongo.Count(ctx, replicaHandle.Client(), testDB, coll,
					bson.M{"_id": "upd-1"})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(1)))
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			By("updating the document on the primary")
			_, err = primaryHandle.Client().Database(testDB).Collection(coll).
				UpdateOne(ctx, bson.M{"_id": "upd-1"},
					bson.M{"$set": bson.M{"status": "updated"}})
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the update to replicate")
			Eventually(func(g Gomega) {
				var got bson.M
				err := replicaHandle.Client().Database(testDB).Collection(coll).
					FindOne(ctx, bson.M{"_id": "upd-1"}).Decode(&got)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(got["status"]).To(Equal("updated"),
					"replica should reflect the updated value")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed(), "updates should replicate from primary to replica")
		})

		// ---------------------------------------------------------------
		// Phase 2: Failover promotion & post-failover verification
		// ---------------------------------------------------------------

		It("pre-failover: seeds failover-specific data and verifies replication", func() {
			const coll = "fo_data"

			By("inserting seed documents into the primary")
			docs := []interface{}{
				bson.M{"_id": "fo-1", "origin": "original-primary", "seq": 1},
				bson.M{"_id": "fo-2", "origin": "original-primary", "seq": 2},
				bson.M{"_id": "fo-3", "origin": "original-primary", "seq": 3},
			}
			_, err := primaryHandle.Client().Database(testDB).Collection(coll).
				InsertMany(ctx, docs)
			Expect(err).ToNot(HaveOccurred(), "seed primary with failover data")

			By("waiting for data to replicate before failover")
			Eventually(func(g Gomega) {
				cnt, err := sharedmongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(3)),
					"replica should have all 3 docs before failover")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			By("closing gateway handles before failover (connections may break)")
			_ = primaryHandle.Close(ctx)
			primaryHandle = nil
			_ = replicaHandle.Close(ctx)
			replicaHandle = nil
		})

		It("promotes the replica to primary via spec.clusterReplication.primary patch", func() {
			By(fmt.Sprintf("patching both DocumentDB CRs to set primary=%s", replicaName))

			primaryDD := getDD(ctx, ns, primaryName)
			err := shareddoc.PatchSpec(ctx, c, primaryDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch primary CR to demote")

			replicaDD := getDD(ctx, ns, replicaName)
			err = shareddoc.PatchSpec(ctx, c, replicaDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch replica CR to promote")

			By("waiting for the replica CNPG cluster to become the designated primary")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, replicaName)
				g.Expect(cnpg).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).To(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"replica should be self-designated as primary after promotion",
				)
			},
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "replica should become CNPG primary")

			By("waiting for the original primary CNPG cluster to become a replica")
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
			).Should(Succeed(), "original primary should become CNPG replica")

			By("waiting for the new primary (replica CR) to reach Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.ClusterReplicationFailover),
				timeouts.PollInterval(timeouts.ClusterReplicationFailover),
			).Should(Succeed(), "new primary should reach Ready after promotion")
		})

		It("post-failover: pre-existing data is accessible on the new primary", func() {
			const coll = "fo_data"

			By("connecting to the new primary (originally the replica)")
			var err error
			replicaHandle, err = emongo.NewFromDocumentDB(
				ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to new primary gateway")

			By("verifying all pre-failover documents are present")
			Eventually(func(g Gomega) {
				cnt, err := sharedmongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(3)),
					"new primary should have all 3 pre-failover documents")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			By("verifying document content fidelity")
			var doc bson.M
			err = replicaHandle.Client().Database(testDB).Collection(coll).
				FindOne(ctx, bson.M{"_id": "fo-1"}).Decode(&doc)
			Expect(err).ToNot(HaveOccurred())
			Expect(doc["origin"]).To(Equal("original-primary"))
			Expect(doc["seq"]).To(Equal(int32(1)))
		})

		It("post-failover: new primary accepts writes", func() {
			const coll = "fo_post_writes"

			By("ensuring connection to the new primary")
			if replicaHandle == nil {
				var err error
				replicaHandle, err = emongo.NewFromDocumentDB(
					ctx, e2e.SuiteEnv(), ns, replicaName)
				Expect(err).ToNot(HaveOccurred())
			}

			By("writing new data to the promoted primary")
			_, err := replicaHandle.Client().Database(testDB).Collection(coll).
				InsertOne(ctx, bson.M{
					"_id":    "post-fo-1",
					"origin": "new-primary",
					"msg":    "after failover",
				})
			Expect(err).ToNot(HaveOccurred(),
				"write to new primary should succeed")

			By("verifying the write is readable")
			var doc bson.M
			err = replicaHandle.Client().Database(testDB).Collection(coll).
				FindOne(ctx, bson.M{"_id": "post-fo-1"}).Decode(&doc)
			Expect(err).ToNot(HaveOccurred())
			Expect(doc["origin"]).To(Equal("new-primary"))
			Expect(doc["msg"]).To(Equal("after failover"))
		})

		It("post-failover: replication continues from new primary to demoted replica", func() {
			const coll = "fo_reverse_repl"

			By("ensuring connection to the new primary")
			if replicaHandle == nil {
				var err error
				replicaHandle, err = emongo.NewFromDocumentDB(
					ctx, e2e.SuiteEnv(), ns, replicaName)
				Expect(err).ToNot(HaveOccurred())
			}

			By("writing data on the new primary")
			docs := []interface{}{
				bson.M{"_id": "rev-1", "origin": "new-primary", "direction": "reverse"},
				bson.M{"_id": "rev-2", "origin": "new-primary", "direction": "reverse"},
			}
			_, err := replicaHandle.Client().Database(testDB).Collection(coll).
				InsertMany(ctx, docs)
			Expect(err).ToNot(HaveOccurred(),
				"write to new primary for reverse-replication test")

			By("connecting to the demoted replica (originally the primary)")
			oldPrimaryHandle, err := emongo.NewFromDocumentDB(
				ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(),
				"connect to old primary (now replica) gateway")
			defer func() { _ = oldPrimaryHandle.Close(ctx) }()

			By("waiting for replication from new primary to demoted replica")
			Eventually(func(g Gomega) {
				cnt, err := sharedmongo.Count(ctx, oldPrimaryHandle.Client(),
					testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(2)),
					"demoted replica should receive docs from new primary")
			},
				timeouts.For(timeouts.ClusterReplicationDataSync),
				timeouts.PollInterval(timeouts.ClusterReplicationDataSync),
			).Should(Succeed())

			By("verifying document content on the demoted replica")
			var doc bson.M
			err = oldPrimaryHandle.Client().Database(testDB).Collection(coll).
				FindOne(ctx, bson.M{"_id": "rev-1"}).Decode(&doc)
			Expect(err).ToNot(HaveOccurred())
			Expect(doc["origin"]).To(Equal("new-primary"))
			Expect(doc["direction"]).To(Equal("reverse"))
		})
	})
