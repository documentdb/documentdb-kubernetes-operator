package replication

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
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB replication — failover promotion",
	Ordered,
	Label(e2e.ReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName = "fo-primary"
			replicaName = "fo-replica"
			testDB      = "failover_test_db"
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
			ns = namespaces.NamespaceForSpec(e2e.ReplicationLabel + "-failover")
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			By("creating the primary DocumentDB CR")
			primaryDD, err := documentdb.Create(ctx, c, ns, primaryName, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"replication"},
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
				Mixins: []string{"replication"},
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
				timeouts.For(timeouts.ReplicationReady),
				timeouts.PollInterval(timeouts.ReplicationReady),
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

		It("pre-failover: seeds data into the original primary", func() {
			const coll = "fo_data"

			By("connecting to the primary gateway")
			var err error
			primaryHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(), "connect to primary gateway")

			By("inserting seed documents into the primary")
			docs := []interface{}{
				bson.M{"_id": "fo-1", "origin": "original-primary", "seq": 1},
				bson.M{"_id": "fo-2", "origin": "original-primary", "seq": 2},
				bson.M{"_id": "fo-3", "origin": "original-primary", "seq": 3},
			}
			_, err = primaryHandle.Client().Database(testDB).Collection(coll).InsertMany(ctx, docs)
			Expect(err).ToNot(HaveOccurred(), "seed primary")

			By("waiting for data to replicate to the replica before failover")
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to replica gateway")

			Eventually(func(g Gomega) {
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(3)), "replica should have all 3 docs before failover")
			},
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
			).Should(Succeed())

			// Close handles — the gateway connections may break during failover
			_ = primaryHandle.Close(ctx)
			primaryHandle = nil
			_ = replicaHandle.Close(ctx)
			replicaHandle = nil
		})

		It("promotes the replica to primary via spec.clusterReplication.primary patch", func() {
			By(fmt.Sprintf("patching both DocumentDB CRs to set primary=%s", replicaName))

			// Patch the primary CR: change its clusterReplication.primary to the replica
			primaryDD := getDD(ctx, ns, primaryName)
			err := documentdb.PatchSpec(ctx, c, primaryDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch primary CR to demote")

			// Patch the replica CR: change its clusterReplication.primary to itself
			replicaDD := getDD(ctx, ns, replicaName)
			err = documentdb.PatchSpec(ctx, c, replicaDD, func(spec *previewv1.DocumentDBSpec) {
				spec.ClusterReplication.Primary = replicaName
			})
			Expect(err).ToNot(HaveOccurred(), "patch replica CR to promote")

			By("waiting for the replica CNPG cluster to become the designated primary")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, replicaName)
				g.Expect(cnpg).ToNot(BeNil(), "replica CNPG cluster should exist")
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).To(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"replica CNPG cluster should be self-designated as primary after promotion",
				)
			},
				timeouts.For(timeouts.Failover),
				timeouts.PollInterval(timeouts.Failover),
			).Should(Succeed(), "replica should become CNPG primary")

			By("waiting for the original primary CNPG cluster to become a replica")
			Eventually(func(g Gomega) {
				cnpg := findCNPGCluster(ctx, c, ns, primaryName)
				g.Expect(cnpg).ToNot(BeNil(), "original primary CNPG cluster should exist")
				g.Expect(cnpg.Spec.ReplicaCluster).ToNot(BeNil())
				g.Expect(cnpg.Spec.ReplicaCluster.Primary).ToNot(
					Equal(cnpg.Spec.ReplicaCluster.Self),
					"original primary CNPG cluster should no longer be self-designated as primary",
				)
			},
				timeouts.For(timeouts.Failover),
				timeouts.PollInterval(timeouts.Failover),
			).Should(Succeed(), "original primary should become CNPG replica")

			By("waiting for the new primary (replica CR) to reach Ready")
			replicaKey := types.NamespacedName{Namespace: ns, Name: replicaName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, replicaKey),
				timeouts.For(timeouts.Failover),
				timeouts.PollInterval(timeouts.Failover),
			).Should(Succeed(), "new primary should reach Ready after promotion")
		})

		It("post-failover: pre-existing data is accessible on the new primary", func() {
			const coll = "fo_data"

			By("connecting to the new primary (originally the replica)")
			var err error
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to new primary gateway")

			By("verifying all pre-failover documents are present")
			Eventually(func(g Gomega) {
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(3)),
					"new primary should have all 3 pre-failover documents")
			},
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
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

			By("ensuring we have a connection to the new primary")
			if replicaHandle == nil {
				var err error
				replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
				Expect(err).ToNot(HaveOccurred(), "connect to new primary gateway")
			}

			By("writing new data to the promoted primary")
			_, err := replicaHandle.Client().Database(testDB).Collection(coll).
				InsertOne(ctx, bson.M{"_id": "post-fo-1", "origin": "new-primary", "msg": "after failover"})
			Expect(err).ToNot(HaveOccurred(), "write to new primary should succeed")

			By("verifying the write is readable")
			var doc bson.M
			err = replicaHandle.Client().Database(testDB).Collection(coll).
				FindOne(ctx, bson.M{"_id": "post-fo-1"}).Decode(&doc)
			Expect(err).ToNot(HaveOccurred())
			Expect(doc["origin"]).To(Equal("new-primary"))
			Expect(doc["msg"]).To(Equal("after failover"))
		})
	})
