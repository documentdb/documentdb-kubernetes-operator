package replication

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
)

// baseVars returns the envsubst variables for the base template.
func baseVars() map[string]string {
	ddImage := os.Getenv("DOCUMENTDB_IMAGE")
	gwImage := os.Getenv("GATEWAY_IMAGE")
	storageClass := "standard"
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		storageClass = v
	}
	return map[string]string{
		"INSTANCES":         "1",
		"STORAGE_SIZE":      "1Gi",
		"STORAGE_CLASS":     storageClass,
		"DOCUMENTDB_IMAGE":  ddImage,
		"GATEWAY_IMAGE":     gwImage,
		"CREDENTIAL_SECRET": fixtures.DefaultCredentialSecretName,
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
}

// replicationVars returns the envsubst variables for the replication
// mixin. primaryName is the CR name acting as the replication primary.
func replicationVars(primaryName, member1, member2 string) map[string]string {
	return map[string]string{
		"REPL_PRIMARY":  primaryName,
		"REPL_MEMBER_1": member1,
		"REPL_MEMBER_2": member2,
	}
}

// mergeVars merges multiple variable maps. Later maps override earlier.
func mergeVars(maps ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// createNamespace creates the namespace with ownership labels and
// registers cleanup via DeferCleanup.
func createNamespace(ctx context.Context, c client.Client, ns string) {
	if err := fixtures.CreateLabeledNamespace(ctx, c, ns, "replication"); err != nil {
		Fail("create namespace " + ns + ": " + err.Error())
	}
	DeferCleanup(func(ctx SpecContext) {
		_ = c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})
}

// createCredentialSecret seeds the default DocumentDB credential secret.
func createCredentialSecret(ctx context.Context, c client.Client, ns string) {
	if err := fixtures.CreateLabeledCredentialSecret(ctx, c, ns); err != nil {
		Fail("create credential secret " + ns + ": " + err.Error())
	}
}

// getDD fetches a DocumentDB CR by namespace and name.
func getDD(ctx context.Context, ns, name string) *previewv1.DocumentDB {
	c := e2e.SuiteEnv().Client
	dd, err := documentdb.Get(ctx, c, types.NamespacedName{Namespace: ns, Name: name})
	Expect(err).ToNot(HaveOccurred())
	return dd
}

// cnpgClusterName mirrors the operator's generateCNPGClusterName logic:
// "<docdbName>-<fnv64a(memberName) hex>", capped at 50 characters.
func cnpgClusterName(docdbName, memberName string) string {
	const maxLen = 50
	h := fnv.New64a()
	h.Write([]byte(memberName))
	maxPrefix := maxLen - 9 // dash + 16-char hex
	name := fmt.Sprintf("%.*s-%x", maxPrefix, docdbName, h.Sum64())
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

// createReplicationBridgeServices creates ExternalName Services that
// bridge the DNS naming gap when using crossCloudNetworkingStrategy=None
// with two DocumentDB CRs in the same cluster.
//
// With "None" strategy, each CR's CNPG cluster is named
// <docdbCRName>-<hash(memberName)>, and since memberName == docdbCRName
// for "None", external cluster host references embed the CR name as prefix.
// The replica's external cluster expects a service like
//
//	<replicaCRName>-<hash(primaryMemberName)>-rw
//
// but the actual primary service is named
//
//	<primaryCRName>-<hash(primaryMemberName)>-rw
//
// This function creates ExternalName services that CNAME the expected
// names to the actual service FQDNs.
func createReplicationBridgeServices(ctx context.Context, c client.Client, ns, primaryCR, replicaCR string) {
	// For the replica: it needs to reach the primary via pg_basebackup.
	// Expected:  <replicaCR>-<hash(primaryCR)>-rw → actual: <primaryCR>-<hash(primaryCR)>-rw
	replicaExpected := cnpgClusterName(replicaCR, primaryCR)
	primaryActual := cnpgClusterName(primaryCR, primaryCR)

	By(fmt.Sprintf("creating bridge service %s-rw → %s-rw (replica→primary)", replicaExpected, primaryActual))
	createExternalNameService(ctx, c, ns,
		replicaExpected+"-rw",
		fmt.Sprintf("%s-rw.%s.svc.cluster.local", primaryActual, ns),
	)

	// For the primary: it references the replica (not strictly needed for
	// pg_basebackup but keeps CNPG health checks happy).
	primaryExpected := cnpgClusterName(primaryCR, replicaCR)
	replicaActual := cnpgClusterName(replicaCR, replicaCR)

	By(fmt.Sprintf("creating bridge service %s-rw → %s-rw (primary→replica)", primaryExpected, replicaActual))
	createExternalNameService(ctx, c, ns,
		primaryExpected+"-rw",
		fmt.Sprintf("%s-rw.%s.svc.cluster.local", replicaActual, ns),
	)
}

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

func createExternalNameService(ctx context.Context, c client.Client, ns, name, externalName string) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"e2e-replication-bridge": "true"},
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: externalName,
		},
	}
	err := c.Create(ctx, svc)
	Expect(err).ToNot(HaveOccurred(), "create bridge service "+name)
}
