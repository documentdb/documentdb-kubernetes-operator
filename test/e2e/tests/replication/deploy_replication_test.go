package replication

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB replication — deploy primary and replica",
	Label(e2e.ReplicationLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const (
			primaryName = "primary-db"
			replicaName = "replica-db"
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
			ns = namespaces.NamespaceForSpec(e2e.ReplicationLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)
		})

		It("deploys a primary + replica pair and both reach Ready with correct CNPG replication config", func() {
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

			By("waiting for the primary to become Ready before creating replica")
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

			By("verifying primary CNPG Cluster has ReplicaCluster config")
			primaryCNPG := findCNPGCluster(ctx, c, ns, primaryName)
			Expect(primaryCNPG).ToNot(BeNil(), "CNPG Cluster for primary should exist")
			Expect(primaryCNPG.Spec.ReplicaCluster).ToNot(BeNil(),
				"primary CNPG Cluster should have ReplicaCluster config")
			Expect(primaryCNPG.Spec.ReplicaCluster.Primary).To(
				Equal(primaryCNPG.Spec.ReplicaCluster.Self),
				"primary CNPG Cluster should be self-designated as primary")

			By("verifying replica CNPG Cluster has ReplicaCluster config")
			replicaCNPG := findCNPGCluster(ctx, c, ns, replicaName)
			Expect(replicaCNPG).ToNot(BeNil(), "CNPG Cluster for replica should exist")
			Expect(replicaCNPG.Spec.ReplicaCluster).ToNot(BeNil(),
				"replica CNPG Cluster should have ReplicaCluster config")
			Expect(replicaCNPG.Spec.ReplicaCluster.Primary).ToNot(
				Equal(replicaCNPG.Spec.ReplicaCluster.Self),
				"replica CNPG Cluster primary should differ from self")

			By("verifying replica bootstraps from primary via pg_basebackup")
			Expect(replicaCNPG.Spec.Bootstrap).ToNot(BeNil(),
				"replica CNPG Cluster should have Bootstrap config")
			Expect(replicaCNPG.Spec.Bootstrap.PgBaseBackup).ToNot(BeNil(),
				"replica should bootstrap via pg_basebackup")
			// With crossCloudNetworkingStrategy=None, the pg_basebackup source
			// references the external cluster entry named using the replica's
			// naming scheme. The bridge ExternalName service resolves the DNS
			// to the actual primary service.
			expectedSource := cnpgClusterName(replicaName, primaryName)
			Expect(replicaCNPG.Spec.Bootstrap.PgBaseBackup.Source).To(
				Equal(expectedSource),
				"replica pg_basebackup source should reference the external cluster entry for the primary")

			By("verifying both CNPG Clusters have ExternalClusters configured")
			Expect(primaryCNPG.Spec.ExternalClusters).ToNot(BeEmpty(),
				"primary CNPG Cluster should have ExternalClusters")
			Expect(replicaCNPG.Spec.ExternalClusters).ToNot(BeEmpty(),
				"replica CNPG Cluster should have ExternalClusters")

			By("verifying external cluster references use in-cluster service DNS")
			for _, ext := range primaryCNPG.Spec.ExternalClusters {
				host, ok := ext.ConnectionParameters["host"]
				Expect(ok).To(BeTrue(), "ExternalCluster %s should have a host", ext.Name)
				Expect(host).To(ContainSubstring(ns+".svc"),
					"ExternalCluster %s host should reference in-cluster service DNS", ext.Name)
			}
		})
	})

// findCNPGCluster discovers the CNPG Cluster backing a DocumentDB CR.
// In replication mode, the CNPG cluster name is a hash-based derivative
// of the DocumentDB name (via generateCNPGClusterName), so we list all
// CNPG Clusters in the namespace and match by the documentdb ownership
// label.
func findCNPGCluster(ctx context.Context, c client.Client, ns, ddName string) *cnpgv1.Cluster {
	var list cnpgv1.ClusterList
	err := c.List(ctx, &list, client.InNamespace(ns))
	if err != nil {
		Fail(fmt.Sprintf("listing CNPG clusters in %s: %v", ns, err))
	}
	for i := range list.Items {
		cluster := &list.Items[i]
		for _, ref := range cluster.OwnerReferences {
			if ref.Kind == "DocumentDB" && ref.Name == ddName {
				return cluster
			}
		}
	}
	return nil
}
