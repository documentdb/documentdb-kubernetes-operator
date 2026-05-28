package controller

import (
	"context"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fleetv1alpha1 "go.goms.io/fleet-networking/api/v1alpha1"
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
	Expect(fleetv1alpha1.AddToScheme(scheme)).To(Succeed())

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

	Describe("ensureTokenServiceResources", func() {
		const namespace = "documentdb-preview-ns"
		const clusterName = "documentdb-preview"
		const tokenServiceName = "promotion-token"

		mkCluster := func(token string) *cnpgv1.Cluster {
			return &cnpgv1.Cluster{
				TypeMeta: metav1.TypeMeta{APIVersion: "postgresql.cnpg.io/v1", Kind: "Cluster"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
					UID:       types.UID("cluster-uid"),
				},
				Status: cnpgv1.ClusterStatus{DemotionToken: token},
			}
		}

		azureFleetCtx := &util.ReplicationContext{
			CrossCloudNetworkingStrategy: util.AzureFleet,
		}

		It("updates the ConfigMap with the latest token across consecutive failovers", func() {
			ctx := context.Background()
			reconciler := buildDocumentDBReconciler(mkCluster("token-v1"))
			clusterNN := types.NamespacedName{Name: clusterName, Namespace: namespace}

			done, err := reconciler.ensureTokenServiceResources(ctx, clusterNN, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())
			Expect(done).To(BeTrue())

			cm := &corev1.ConfigMap{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, cm)).To(Succeed())
			Expect(cm.Data["index.html"]).To(Equal("token-v1"))
			firstRevision := cm.Annotations[promotionTokenRevisionAnnotation]
			Expect(firstRevision).ToNot(BeEmpty())

			// Simulate back-to-back failover by changing the demotion token
			// and re-running the loop. This is the regression scenario: the
			// previous Update path silently failed with a conflict because
			// resourceVersion was missing, leaving the OLD token in place.
			cnpgCluster := &cnpgv1.Cluster{}
			Expect(reconciler.Client.Get(ctx, clusterNN, cnpgCluster)).To(Succeed())
			cnpgCluster.Status.DemotionToken = "token-v2"
			Expect(reconciler.Client.Status().Update(ctx, cnpgCluster)).To(Succeed())

			_, err = reconciler.ensureTokenServiceResources(ctx, clusterNN, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())

			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, cm)).To(Succeed())
			Expect(cm.Data["index.html"]).To(Equal("token-v2"),
				"ConfigMap must reflect the new demotion token after a back-to-back failover")
			Expect(cm.Annotations[promotionTokenRevisionAnnotation]).ToNot(Equal(firstRevision))
		})

		It("deletes the stale nginx Pod when the token revision changes", func() {
			ctx := context.Background()
			reconciler := buildDocumentDBReconciler(mkCluster("token-v1"))
			clusterNN := types.NamespacedName{Name: clusterName, Namespace: namespace}

			// First call creates Pod with revision-v1
			_, err := reconciler.ensureTokenServiceResources(ctx, clusterNN, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())
			podV1 := &corev1.Pod{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, podV1)).To(Succeed())
			revV1 := podV1.Annotations[promotionTokenRevisionAnnotation]
			Expect(revV1).ToNot(BeEmpty())

			// Rotate the token
			cnpgCluster := &cnpgv1.Cluster{}
			Expect(reconciler.Client.Get(ctx, clusterNN, cnpgCluster)).To(Succeed())
			cnpgCluster.Status.DemotionToken = "token-v2"
			Expect(reconciler.Client.Status().Update(ctx, cnpgCluster)).To(Succeed())

			// Second call should detect the stale Pod and delete it, requeueing
			done, err := reconciler.ensureTokenServiceResources(ctx, clusterNN, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())
			Expect(done).To(BeFalse(), "stale Pod deletion must requeue, not signal ready")

			err = reconciler.Client.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, podV1)
			Expect(errors.IsNotFound(err)).To(BeTrue(),
				"stale promotion-token Pod must be deleted so the recreated Pod picks up the fresh ConfigMap")

			// Third call recreates the Pod with the NEW revision
			_, err = reconciler.ensureTokenServiceResources(ctx, clusterNN, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())
			podV2 := &corev1.Pod{}
			Expect(reconciler.Client.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, podV2)).To(Succeed())
			Expect(podV2.Annotations[promotionTokenRevisionAnnotation]).ToNot(Equal(revV1))
		})

		It("returns false without error when the demotion token is not yet available", func() {
			ctx := context.Background()
			reconciler := buildDocumentDBReconciler(mkCluster(""))
			done, err := reconciler.ensureTokenServiceResources(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, azureFleetCtx)
			Expect(err).ToNot(HaveOccurred())
			Expect(done).To(BeFalse())
		})
	})
})
