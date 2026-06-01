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

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB replication — data replication validation",
	Ordered,
	Label(e2e.ReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName = "repl-primary"
			replicaName = "repl-replica"
			testDB      = "repl_test_db"
		)
		var (
			ctx            context.Context
			ns             string
			c              client.Client
			primaryHandle  *emongo.Handle
			replicaHandle  *emongo.Handle
		)

		BeforeAll(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			ns = namespaces.NamespaceForSpec(e2e.ReplicationLabel + "-data")
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
			"replica pg_basebackup source should reference the external cluster entry for the primary")
		Expect(replicaCNPG.Spec.ExternalClusters).ToNot(BeEmpty(),
			"replica CNPG Cluster should have ExternalClusters")

		By("connecting to the primary gateway")
			primaryHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, primaryName)
			Expect(err).ToNot(HaveOccurred(), "connect to primary gateway")

			By("connecting to the replica gateway")
			replicaHandle, err = emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, replicaName)
			Expect(err).ToNot(HaveOccurred(), "connect to replica gateway")
		})

		AfterAll(func() {
			if primaryHandle != nil {
				_ = primaryHandle.Client().Database(testDB).Drop(ctx)
				_ = primaryHandle.Close(ctx)
			}
			if replicaHandle != nil {
				_ = replicaHandle.Close(ctx)
			}
		})

		It("replicates inserted documents from primary to replica", func() {
			const coll = "repl_smoke"
			docs := seed.SmallDataset()

			By(fmt.Sprintf("seeding %d documents into primary %s.%s", len(docs), testDB, coll))
			n, err := emongo.Seed(ctx, primaryHandle.Client(), testDB, coll, docs)
			Expect(err).ToNot(HaveOccurred(), "seed primary")
			Expect(n).To(Equal(seed.SmallDatasetSize), "all documents should be accepted")

			By("verifying documents appear on the primary")
			cnt, err := emongo.Count(ctx, primaryHandle.Client(), testDB, coll, nil)
			Expect(err).ToNot(HaveOccurred(), "count on primary")
			Expect(cnt).To(Equal(int64(seed.SmallDatasetSize)),
				"primary should have all seeded documents")

			By("waiting for documents to replicate to the replica")
			Eventually(func(g Gomega) {
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll, nil)
				g.Expect(err).ToNot(HaveOccurred(), "count on replica")
				g.Expect(cnt).To(Equal(int64(seed.SmallDatasetSize)),
					"replica should have all replicated documents")
			},
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
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
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
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
				cnt, err := emongo.Count(ctx, replicaHandle.Client(), testDB, coll,
					bson.M{"_id": "upd-1"})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cnt).To(Equal(int64(1)))
			},
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
			).Should(Succeed())

			By("updating the document on the primary")
			_, err = primaryHandle.Client().Database(testDB).Collection(coll).
				UpdateOne(ctx, bson.M{"_id": "upd-1"}, bson.M{"$set": bson.M{"status": "updated"}})
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
				timeouts.For(timeouts.DataSync),
				timeouts.PollInterval(timeouts.DataSync),
			).Should(Succeed(), "updates should replicate from primary to replica")
		})
	})
