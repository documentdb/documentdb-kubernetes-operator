package controller

import (
	"context"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/cnpg"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func buildDocumentDBReconciler(objs ...runtime.Object) *DocumentDBReconciler {
	scheme := runtime.NewScheme()
	Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
	Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(rbacv1.AddToScheme(scheme)).To(Succeed())

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithRuntimeObjects(objs...)
		clientObjs := make([]client.Object, 0, len(objs))
		for _, obj := range objs {
			if co, ok := obj.(client.Object); ok {
				clientObjs = append(clientObjs, co)
			}
		}
		if len(clientObjs) > 0 {
			builder = builder.WithStatusSubresource(clientObjs...)
		}
	}

	return &DocumentDBReconciler{Client: builder.Build(), Scheme: scheme}
}

var _ = Describe("Physical Replication", func() {
	It("deletes owned resources when DocumentDB is not present", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-not-present", namespace)
		documentdb.UID = types.UID("docdb-not-present-uid")
		documentdb.Finalizers = []string{documentDBFinalizer}
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.AzureFleet),
			Primary:                      "member-2",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "member-2"},
				{Name: "member-3"},
			},
		}

		ownerRef := metav1.OwnerReference{
			APIVersion: "documentdb.io/preview",
			Kind:       "DocumentDB",
			Name:       documentdb.Name,
			UID:        documentdb.UID,
		}

		ownedService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "owned-service",
				Namespace:       namespace,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
		}

		ownedCluster := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "owned-cnpg",
				Namespace:       namespace,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
		}

		clusterNameConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-name",
				Namespace: "kube-system",
			},
			Data: map[string]string{
				"name": "member-1",
			},
		}

		reconciler := buildDocumentDBReconciler(documentdb, ownedService, ownedCluster, clusterNameConfigMap)

		// Handle finalizer
		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: documentdb.Name, Namespace: namespace}})
		Expect(err).ToNot(HaveOccurred())

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: documentdb.Name, Namespace: namespace}})
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))

		service := &corev1.Service{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: ownedService.Name, Namespace: namespace}, service)
		Expect(errors.IsNotFound(err)).To(BeTrue())

		cluster := &cnpgv1.Cluster{}
		err = reconciler.Client.Get(ctx, types.NamespacedName{Name: ownedCluster.Name, Namespace: namespace}, cluster)
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("returns nil when ReplicaCluster is nil (non-replicated)", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-norepl", namespace)

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-norepl",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{},
		}
		desired := current.DeepCopy()

		reconciler := buildDocumentDBReconciler(current)
		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(patchOps).To(BeNil())
		Expect(requeue).To(Equal(time.Duration(-1)))
	})

	It("returns error when Self is changed", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-selferr", namespace)

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-selferr",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-a",
					Source:  "cluster-a",
				},
			},
		}
		desired := current.DeepCopy()
		desired.Spec.ReplicaCluster.Self = "cluster-b"

		reconciler := buildDocumentDBReconciler(current)
		_, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("self cannot be changed"))
		Expect(requeue).To(Equal(time.Second * 60))
	})

	It("builds patch ops for replica => replica primary change", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-r2r", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-c",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
				{Name: "cluster-c"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-r2r",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-b",
					Source:  "cluster-b",
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: "cluster-a"},
					{Name: "cluster-b"},
					{Name: "cluster-c"},
				},
			},
		}

		desired := current.DeepCopy()
		desired.Spec.ReplicaCluster.Primary = "cluster-c"
		desired.Spec.ReplicaCluster.Source = "cluster-c"

		reconciler := buildDocumentDBReconciler(current)
		replicationContext, err := util.GetReplicationContext(ctx, reconciler.Client, *documentdb)
		Expect(err).ToNot(HaveOccurred())

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))
		// Should have a ReplicaCluster replace patch
		Expect(patchOps).ToNot(BeEmpty())
		found := false
		for _, op := range patchOps {
			if op.Path == cnpg.PatchPathReplicaCluster {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("builds patch ops for replica => primary promotion without old primary", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-r2p", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-a",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-r2p",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-b",
					Source:  "cluster-b",
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: "cluster-a"},
					{Name: "cluster-b"},
				},
			},
		}

		desired := current.DeepCopy()
		// Promote cluster-a to primary
		desired.Spec.ReplicaCluster.Primary = "cluster-a"

		reconciler := buildDocumentDBReconciler(current)
		// Empty OtherCNPGClusterNames means old primary is not available
		replicationContext := &util.ReplicationContext{
			OtherCNPGClusterNames: []string{}, // old primary not available
		}

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))
		Expect(patchOps).ToNot(BeEmpty())
		found := false
		for _, op := range patchOps {
			if op.Path == cnpg.PatchPathReplicaCluster {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("builds patch ops for primary => replica demotion", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-p2r", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-b",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-p2r",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-a",
					Source:  "cluster-a",
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: "cluster-a"},
					{Name: "cluster-b"},
				},
				Bootstrap: &cnpgv1.BootstrapConfiguration{
					PgBaseBackup: &cnpgv1.BootstrapPgBaseBackup{
						Source: "cluster-b",
					},
				},
			},
		}

		desired := current.DeepCopy()
		desired.Spec.ReplicaCluster.Primary = "cluster-b"

		reconciler := buildDocumentDBReconciler(current)
		replicationContext := &util.ReplicationContext{
			OtherCNPGClusterNames: []string{"cluster-b"},
		}

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))
		Expect(patchOps).ToNot(BeEmpty())
		// Should have bootstrap remove and replica cluster replace
		hasBootstrapRemove := false
		hasReplicaReplace := false
		for _, op := range patchOps {
			if op.Path == cnpg.PatchPathBootstrap && op.Op == cnpg.PatchOpRemove {
				hasBootstrapRemove = true
			}
			if op.Path == cnpg.PatchPathReplicaCluster && op.Op == cnpg.PatchOpReplace {
				hasReplicaReplace = true
			}
		}
		Expect(hasBootstrapRemove).To(BeTrue())
		Expect(hasReplicaReplace).To(BeTrue())
	})

	It("builds patch ops for primary => replica demotion with HA", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-p2r-ha", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-b",
			HighAvailability:             true,
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-p2r-ha",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				Instances: 2,
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-a",
					Source:  "cluster-a",
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: "cluster-a"},
					{Name: "cluster-b"},
				},
				PostgresConfiguration: cnpgv1.PostgresConfiguration{
					Synchronous: &cnpgv1.SynchronousReplicaConfiguration{
						Method: cnpgv1.SynchronousReplicaConfigurationMethodAny,
						Number: 1,
					},
				},
				Plugins: []cnpgv1.PluginConfiguration{
					{Name: "my-plugin"},
				},
			},
		}

		desired := current.DeepCopy()
		desired.Spec.ReplicaCluster.Primary = "cluster-b"
		desired.Spec.Instances = 1
		desired.Spec.Plugins = []cnpgv1.PluginConfiguration{{Name: "my-plugin-updated"}}

		reconciler := buildDocumentDBReconciler(current)
		replicationContext := &util.ReplicationContext{
			OtherCNPGClusterNames: []string{"cluster-b"},
		}

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))
		// HA demotion should include: bootstrap remove, replica replace, sync remove, instances replace, plugins replace
		Expect(len(patchOps)).To(BeNumerically(">=", 4))

		paths := make(map[string]bool)
		for _, op := range patchOps {
			paths[op.Path] = true
		}
		Expect(paths).To(HaveKey(cnpg.PatchPathReplicaCluster))
		Expect(paths).To(HaveKey(cnpg.PatchPathInstances))
		Expect(paths).To(HaveKey(cnpg.PatchPathPlugins))
		Expect(paths).To(HaveKey(cnpg.PatchPathPostgresConfigSyn))
	})

	It("builds patch ops for replica => primary promotion with HA", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-r2p-ha", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-a",
			HighAvailability:             true,
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-r2p-ha",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				Instances: 1,
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    "cluster-a",
					Primary: "cluster-b",
					Source:  "cluster-b",
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: "cluster-a"},
					{Name: "cluster-b"},
				},
			},
		}

		desired := current.DeepCopy()
		desired.Spec.ReplicaCluster.Primary = "cluster-a"
		desired.Spec.Instances = 2
		desired.Spec.PostgresConfiguration = cnpgv1.PostgresConfiguration{
			Synchronous: &cnpgv1.SynchronousReplicaConfiguration{
				Method: cnpgv1.SynchronousReplicaConfigurationMethodAny,
				Number: 1,
			},
		}
		desired.Spec.Plugins = []cnpgv1.PluginConfiguration{{Name: "my-plugin"}}
		desired.Spec.ReplicationSlots = &cnpgv1.ReplicationSlotsConfiguration{}

		reconciler := buildDocumentDBReconciler(current)
		// Old primary not available — skip token read
		replicationContext := &util.ReplicationContext{
			OtherCNPGClusterNames: []string{},
		}

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))
		// HA promotion should include: replica replace, postgres config, instances, plugins, replication slots
		Expect(len(patchOps)).To(BeNumerically(">=", 4))

		paths := make(map[string]bool)
		for _, op := range patchOps {
			paths[op.Path] = true
		}
		Expect(paths).To(HaveKey(cnpg.PatchPathReplicaCluster))
		Expect(paths).To(HaveKey(cnpg.PatchPathInstances))
		Expect(paths).To(HaveKey(cnpg.PatchPathPlugins))
		Expect(paths).To(HaveKey(cnpg.PatchPathPostgresConfig))
		Expect(paths).To(HaveKey(cnpg.PatchPathReplicationSlots))
	})

	It("updates external clusters and synchronous config", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-repl", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      documentdb.Name,
			ClusterList: []dbpreview.MemberCluster{
				{Name: documentdb.Name},
				{Name: "member-2"},
			},
		}

		current := &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "docdb-repl",
				Namespace: namespace,
			},
			Spec: cnpgv1.ClusterSpec{
				ReplicaCluster: &cnpgv1.ReplicaClusterConfiguration{
					Self:    documentdb.Name,
					Primary: documentdb.Name,
					Source:  documentdb.Name,
				},
				ExternalClusters: []cnpgv1.ExternalCluster{
					{Name: documentdb.Name},
					{Name: "member-2"},
				},
				PostgresConfiguration: cnpgv1.PostgresConfiguration{
					Synchronous: &cnpgv1.SynchronousReplicaConfiguration{
						Method: cnpgv1.SynchronousReplicaConfigurationMethodAny,
						Number: 1,
					},
				},
			},
		}

		desired := current.DeepCopy()
		desired.Spec.ExternalClusters = []cnpgv1.ExternalCluster{
			{Name: documentdb.Name},
			{Name: "member-2"},
			{Name: "member-3"},
		}
		desired.Spec.PostgresConfiguration.Synchronous = &cnpgv1.SynchronousReplicaConfiguration{
			Method: cnpgv1.SynchronousReplicaConfigurationMethodAny,
			Number: 2,
		}

		reconciler := buildDocumentDBReconciler(current)
		replicationContext, err := util.GetReplicationContext(ctx, reconciler.Client, *documentdb)
		Expect(err).ToNot(HaveOccurred())

		patchOps, err, requeue := reconciler.syncReplicationChanges(ctx, current, desired, documentdb, replicationContext)
		Expect(err).ToNot(HaveOccurred())
		Expect(requeue).To(Equal(time.Duration(-1)))

		// Apply the ops via SyncCnpgCluster (consolidates all patches)
		syncErr := cnpg.SyncCnpgCluster(ctx, reconciler.Client, current, desired, patchOps)
		Expect(syncErr).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: current.Name, Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.ExternalClusters).To(HaveLen(3))
		Expect(updated.Spec.PostgresConfiguration.Synchronous.Number).To(Equal(2))
	})
})

var _ = Describe("AddClusterReplicationToClusterSpec - cert management fields", func() {
	// Helper to build a minimal cnpgCluster suitable for AddClusterReplicationToClusterSpec.
	buildCnpgCluster := func(name, namespace string) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: cnpgv1.ClusterSpec{
				InheritedMetadata: &cnpgv1.EmbeddedObjectMetadata{
					Labels: map[string]string{},
				},
			},
		}
	}

	// Helper to build a ReplicationContext in primary state (zero value state == NoReplication which
	// satisfies IsPrimary()) with two remote cluster members, using the None networking strategy so
	// no service import/export objects are required.
	buildPrimaryReplicationContext := func(name string, tlsSecret, caSecret string) *util.ReplicationContext {
		return &util.ReplicationContext{
			CNPGClusterName:              name + "-local",
			OtherCNPGClusterNames:        []string{name + "-remote-a", name + "-remote-b"},
			PrimaryCNPGClusterName:       name + "-local",
			CrossCloudNetworkingStrategy: util.None,
			ReplicationTLSSecret:         tlsSecret,
			ClientCASecret:               caSecret,
		}
	}

	It("does not set Certificates and falls back to trust-based pg_hba when ReplicationTLSSecret is empty", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-cert-none", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-a",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		cnpgCluster := buildCnpgCluster("docdb-cert-none", namespace)
		replicationContext := buildPrimaryReplicationContext("docdb-cert-none", "", "")

		reconciler := buildDocumentDBReconciler()
		Expect(reconciler.AddClusterReplicationToClusterSpec(ctx, documentdb, replicationContext, cnpgCluster)).To(Succeed())

		Expect(cnpgCluster.Spec.Certificates).To(BeNil())
		// Self + two remote external clusters
		Expect(cnpgCluster.Spec.ExternalClusters).To(HaveLen(3))
		for _, ec := range cnpgCluster.Spec.ExternalClusters {
			if ec.Name == replicationContext.CNPGClusterName {
				// Self cluster still uses the superuser for self-loopback.
				Expect(ec.ConnectionParameters["user"]).To(Equal("postgres"))
				continue
			}
			// External (remote) clusters use the dedicated replication user but no TLS material.
			Expect(ec.ConnectionParameters["user"]).To(Equal("streaming_replica"))
			Expect(ec.ConnectionParameters).ToNot(HaveKey("sslmode"))
			Expect(ec.SSLCert).To(BeNil())
			Expect(ec.SSLKey).To(BeNil())
		}
		// Fallback pg_hba configuration is applied when no TLS secret is provided.
		Expect(cnpgCluster.Spec.PostgresConfiguration.PgHBA).To(Equal([]string{
			"host all all localhost trust",
			"host replication streaming_replica all trust",
		}))
	})

	It("propagates ClientCASecret onto the Certificates spec when set alongside ReplicationTLSSecret", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-cert-ca", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-a",
			ReplicationTLSSecret:         "replication-tls",
			ClientCASecret:               "client-ca",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		cnpgCluster := buildCnpgCluster("docdb-cert-ca", namespace)
		replicationContext := buildPrimaryReplicationContext("docdb-cert-ca", "replication-tls", "client-ca")

		reconciler := buildDocumentDBReconciler()
		Expect(reconciler.AddClusterReplicationToClusterSpec(ctx, documentdb, replicationContext, cnpgCluster)).To(Succeed())

		Expect(cnpgCluster.Spec.Certificates).ToNot(BeNil())
		Expect(cnpgCluster.Spec.Certificates.ReplicationTLSSecret).To(Equal("replication-tls"))
		Expect(cnpgCluster.Spec.Certificates.ClientCASecret).To(Equal("client-ca"))
	})

	It("ignores ClientCASecret when ReplicationTLSSecret is empty", func() {
		ctx := context.Background()
		namespace := "default"

		documentdb := baseDocumentDB("docdb-cert-ca-only", namespace)
		documentdb.Spec.ClusterReplication = &dbpreview.ClusterReplication{
			CrossCloudNetworkingStrategy: string(util.None),
			Primary:                      "cluster-a",
			ClientCASecret:               "client-ca",
			ClusterList: []dbpreview.MemberCluster{
				{Name: "cluster-a"},
				{Name: "cluster-b"},
			},
		}

		cnpgCluster := buildCnpgCluster("docdb-cert-ca-only", namespace)
		// A ClientCASecret without a ReplicationTLSSecret should not enable TLS.
		replicationContext := buildPrimaryReplicationContext("docdb-cert-ca-only", "", "client-ca")

		reconciler := buildDocumentDBReconciler()
		Expect(reconciler.AddClusterReplicationToClusterSpec(ctx, documentdb, replicationContext, cnpgCluster)).To(Succeed())

		Expect(cnpgCluster.Spec.Certificates).To(BeNil())
		for _, ec := range cnpgCluster.Spec.ExternalClusters {
			Expect(ec.ConnectionParameters).ToNot(HaveKey("sslmode"))
			Expect(ec.SSLCert).To(BeNil())
			Expect(ec.SSLKey).To(BeNil())
		}
	})
})

var _ = Describe("getReplicasChangePatchOps - cert management fields", func() {
	It("emits a replace patch for spec.certificates alongside externalClusters", func() {
		desired := &cnpgv1.Cluster{
			Spec: cnpgv1.ClusterSpec{
				ExternalClusters: []cnpgv1.ExternalCluster{{Name: "cluster-a"}},
				Certificates: &cnpgv1.CertificatesConfiguration{
					ReplicationTLSSecret: "replication-tls",
					ClientCASecret:       "client-ca",
				},
			},
		}
		replicationContext := &util.ReplicationContext{
			CrossCloudNetworkingStrategy: util.None,
		}

		var patchOps []cnpg.JSONPatch
		getReplicasChangePatchOps(&patchOps, desired, replicationContext)

		hasCerts := false
		for _, op := range patchOps {
			if op.Path == cnpg.PatchPathCertificates {
				Expect(op.Op).To(Equal(cnpg.PatchOpReplace))
				Expect(op.Value).To(Equal(desired.Spec.Certificates))
				hasCerts = true
			}
		}
		Expect(hasCerts).To(BeTrue())
	})

	It("emits a replace patch for spec.certificates with a nil value when TLS is disabled", func() {
		desired := &cnpgv1.Cluster{
			Spec: cnpgv1.ClusterSpec{
				ExternalClusters: []cnpgv1.ExternalCluster{{Name: "cluster-a"}},
			},
		}
		replicationContext := &util.ReplicationContext{
			CrossCloudNetworkingStrategy: util.None,
		}

		var patchOps []cnpg.JSONPatch
		getReplicasChangePatchOps(&patchOps, desired, replicationContext)

		hasCerts := false
		for _, op := range patchOps {
			if op.Path == cnpg.PatchPathCertificates {
				Expect(op.Op).To(Equal(cnpg.PatchOpReplace))
				Expect(op.Value).To(BeNil())
				hasCerts = true
			}
		}
		Expect(hasCerts).To(BeTrue())
	})
})
