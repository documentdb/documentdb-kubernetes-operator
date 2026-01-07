// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"strconv"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// PVCFinalizerName is the finalizer added to PVCs to manage retention
	PVCFinalizerName = "documentdb.io/pvc-retention"
)

// PVCReconciler handles PVC lifecycle management including retention after cluster deletion
type PVCReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch

// Reconcile handles PVC events for DocumentDB clusters only, managing finalizers and retention periods.
// PVCs not belonging to a DocumentDB cluster are ignored.
func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Early exit: Only process PVCs belonging to a DocumentDB cluster
	clusterName, err := r.getDocumentDBClusterName(ctx, &pvc)
	if err != nil || clusterName == "" {
		return ctrl.Result{}, err
	}

	// Label the PVC for faster lookup next time if not already labeled
	if pvc.Labels["documentdb.io/cluster"] != clusterName {
		if pvc.Labels == nil {
			pvc.Labels = make(map[string]string)
		}
		pvc.Labels["documentdb.io/cluster"] = clusterName
		if err := r.Update(ctx, &pvc); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update retention annotation if needed
	if err := r.updateRetentionAnnotation(ctx, &pvc, clusterName); err != nil {
		return ctrl.Result{}, err
	}

	// Manage finalizer based on retention policy
	if err := r.manageFinalizer(ctx, &pvc); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getDocumentDBClusterName determines if a PVC belongs to a DocumentDB cluster and returns the cluster name.
// Returns empty string if the PVC does not belong to a DocumentDB cluster.
func (r *PVCReconciler) getDocumentDBClusterName(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (string, error) {
	// Check if PVC already has the documentdb.io/cluster label
	if clusterName, hasLabel := pvc.Labels["documentdb.io/cluster"]; hasLabel {
		return clusterName, nil
	}

	// Try to find DocumentDB ownership through CNPG cluster
	clusterName, err := r.findDocumentDBOwnerThroughCNPG(ctx, pvc)
	if err != nil || clusterName == "" {
		return "", err
	}

	return clusterName, nil
}

// findDocumentDBOwnerThroughCNPG checks if the PVC is owned by a CNPG cluster that is owned by a DocumentDB.
func (r *PVCReconciler) findDocumentDBOwnerThroughCNPG(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (string, error) {
	for _, ownerRef := range pvc.OwnerReferences {
		if ownerRef.Kind != "Cluster" {
			continue
		}

		var cnpgCluster cnpgv1.Cluster
		err := r.Get(ctx, types.NamespacedName{Name: ownerRef.Name, Namespace: pvc.Namespace}, &cnpgCluster)
		if err != nil {
			continue
		}

		// Check if CNPG cluster is owned by a DocumentDB
		for _, cnpgOwnerRef := range cnpgCluster.OwnerReferences {
			if cnpgOwnerRef.Kind == "DocumentDB" {
				return cnpgOwnerRef.Name, nil
			}
		}
	}

	return "", nil
}

// updateRetentionAnnotation updates the PVC retention annotation if it has changed.
func (r *PVCReconciler) updateRetentionAnnotation(ctx context.Context, pvc *corev1.PersistentVolumeClaim, clusterName string) error {
	var cluster dbpreview.DocumentDB
	err := r.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: pvc.Namespace}, &cluster)

	// If cluster doesn't exist
	if err != nil {
		// If no annotation exists, set default value 7
		if pvc.Annotations == nil || pvc.Annotations["documentdb.io/pvc-retention-days"] == "" {
			if pvc.Annotations == nil {
				pvc.Annotations = make(map[string]string)
			}
			pvc.Annotations["documentdb.io/pvc-retention-days"] = "7"
			return r.Update(ctx, pvc)
		}
		// If annotation exists, do nothing
		return nil
	}

	// Cluster exists - set or update annotation based on cluster value
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}

	retentionDays := cluster.Spec.Resource.Storage.PvcRetentionPeriodDays
	expectedRetention := string(rune(retentionDays))
	currentRetention := pvc.Annotations["documentdb.io/pvc-retention-days"]

	if currentRetention != expectedRetention {
		pvc.Annotations["documentdb.io/pvc-retention-days"] = expectedRetention
		return r.Update(ctx, pvc)
	}

	return nil
}

// manageFinalizer adds or removes the PVC finalizer based on cluster deletion status and retention period.
func (r *PVCReconciler) manageFinalizer(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	shouldHaveFinalizer := r.shouldRetainPVC(pvc)
	hasFinalizer := containsString(pvc.Finalizers, PVCFinalizerName)

	if shouldHaveFinalizer == hasFinalizer {
		// Already in desired state
		return nil
	}

	if shouldHaveFinalizer {
		if pvc.Finalizers == nil {
			pvc.Finalizers = []string{}
		}
		pvc.Finalizers = append(pvc.Finalizers, PVCFinalizerName)
	} else {
		if pvc.Finalizers == nil {
			return nil
		}
		pvc.Finalizers = removeString(pvc.Finalizers, PVCFinalizerName)
	}

	return r.Update(ctx, pvc)
}

// shouldRetainPVC determines if a PVC should be retained based on cluster deletion status and retention period.
func (r *PVCReconciler) shouldRetainPVC(pvc *corev1.PersistentVolumeClaim) bool {
	if pvc.DeletionTimestamp == nil {
		return true
	}

	retentionDays := pvc.Annotations["documentdb.io/pvc-retention-days"]
	if retentionDays == "" {
		// Default retention if annotation missing
		retentionDays = "7"
	}

	// Check if retention period has expired
	days, err := strconv.Atoi(retentionDays)
	if err != nil {
		// If conversion fails, use default of 7 days
		days = 7
	}
	retentionExpiration := pvc.DeletionTimestamp.AddDate(0, 0, days)
	return time.Now().Before(retentionExpiration)
}

func (r *PVCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// 1. Watch PVCs directly
		For(&corev1.PersistentVolumeClaim{}).
		// 2. Watch DocumentDB for updates to retention settings
		Watches(
			&dbpreview.DocumentDB{},
			handler.EnqueueRequestsFromMapFunc(r.findPVCsForCluster),
			builder.WithPredicates(ClusterRetentionChangedPredicate()),
		).
		Complete(r)
}

// Maps a Cluster event to a list of PVC Reconcile Requests
func (r *PVCReconciler) findPVCsForCluster(ctx context.Context, cluster client.Object) []reconcile.Request {
	pvcList := &corev1.PersistentVolumeClaimList{}

	// List PVCs that have a label matching this cluster
	if err := r.List(ctx, pvcList, client.MatchingLabels{"documentdb.io/cluster": cluster.GetName()}); err != nil {
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(pvcList.Items))
	for i, pvc := range pvcList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      pvc.Name,
				Namespace: pvc.Namespace,
			},
		}
	}
	return requests
}

func ClusterRetentionChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCluster := e.ObjectOld.(*dbpreview.DocumentDB)
			newCluster := e.ObjectNew.(*dbpreview.DocumentDB)

			// Only trigger if the specific field we care about has changed
			return oldCluster.Spec.Resource.Storage.PvcRetentionPeriodDays != newCluster.Spec.Resource.Storage.PvcRetentionPeriodDays
		},
	}
}

// containsString checks if a string slice contains a specific string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
