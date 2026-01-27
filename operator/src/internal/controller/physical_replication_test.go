package controller

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/require"
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
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func buildDocumentDBReconciler(t *testing.T, objs ...runtime.Object) *DocumentDBReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, dbpreview.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))

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

func TestDocumentDBReconcileSkipsWhenNotPresent(t *testing.T) {
	ctx := context.Background()
	namespace := "default"

	documentdb := baseDocumentDB("docdb-not-present", namespace)
	documentdb.UID = types.UID("docdb-not-present-uid")
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

	reconciler := buildDocumentDBReconciler(t, documentdb, ownedService, ownedCluster, clusterNameConfigMap)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: documentdb.Name, Namespace: namespace}})
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, result)

	service := &corev1.Service{}
	err = reconciler.Client.Get(ctx, types.NamespacedName{Name: ownedService.Name, Namespace: namespace}, service)
	require.True(t, errors.IsNotFound(err))

	cluster := &cnpgv1.Cluster{}
	err = reconciler.Client.Get(ctx, types.NamespacedName{Name: ownedCluster.Name, Namespace: namespace}, cluster)
	require.True(t, errors.IsNotFound(err))
}

func TestTryUpdateClusterUpdatesExternalClusters(t *testing.T) {
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

	reconciler := buildDocumentDBReconciler(t, current)
	replicationContext, err := util.GetReplicationContext(ctx, reconciler.Client, *documentdb)
	require.NoError(t, err)

	err, requeue := reconciler.TryUpdateCluster(ctx, current, desired, documentdb, replicationContext)
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), requeue)

	updated := &cnpgv1.Cluster{}
	require.NoError(t, reconciler.Client.Get(ctx, types.NamespacedName{Name: current.Name, Namespace: namespace}, updated))
	require.Len(t, updated.Spec.ExternalClusters, 3)
	require.Equal(t, 2, updated.Spec.PostgresConfiguration.Synchronous.Number)
}
