// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// PVCFinalizerName is the finalizer added to PVCs to manage retention
	PVCFinalizerName = "documentdb.io/pvc-retention"

	// Annotation and label keys
	AnnotationPVCRetentionDays = "documentdb.io/pvc-retention-days"
	LabelDocumentDBCluster     = "documentdb.io/cluster"

	// DefaultPVCRetentionDays is the default retention period for PVCs after cluster deletion
	DefaultPVCRetentionDays = 7
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
	log := log.FromContext(ctx)

	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, req.NamespacedName, &pvc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Early exit: Only process PVCs belonging to a DocumentDB cluster
	clusterName, err := r.getDocumentDBClusterName(ctx, &pvc)
	if err != nil {
		log.Error(err, "Failed to determine ownership")
		return ctrl.Result{}, err
	}
	if clusterName == "" {
		// Not a DocumentDB PVC, ignore
		return ctrl.Result{}, nil
	}

	log.V(1).Info("Processing DocumentDB PVC", "cluster", clusterName)

	// Ensure PVC has cluster label for efficient lookups
	if err := r.ensureClusterLabel(ctx, &pvc, clusterName); err != nil {
		log.Error(err, "Failed to ensure cluster label")
		return ctrl.Result{}, err
	}

	// Update retention annotation if needed
	if err := r.updateRetentionAnnotation(ctx, &pvc, clusterName); err != nil {
		log.Error(err, "Failed to update retention annotation")
		return ctrl.Result{}, err
	}

	// Manage finalizer based on retention policy
	if err := r.manageFinalizer(ctx, &pvc); err != nil {
		log.Error(err, "Failed to manage finalizer")
		return ctrl.Result{}, err
	}

	// Requeue if PVC is being deleted to clean up finalizer after retention expires
	if pvc.DeletionTimestamp != nil && containsString(pvc.Finalizers, PVCFinalizerName) {
		retentionDays := r.getRetentionDays(&pvc)
		retentionExpiration := pvc.DeletionTimestamp.AddDate(0, 0, retentionDays)
		requeueAfter := time.Until(retentionExpiration)
		if requeueAfter > 0 {
			log.Info("PVC retention period active, will requeue", "requeueAfter", requeueAfter)
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
	}

	return ctrl.Result{}, nil
}

// ensureClusterLabel ensures the PVC has the documentdb.io/cluster label for efficient lookups.
func (r *PVCReconciler) ensureClusterLabel(ctx context.Context, pvc *corev1.PersistentVolumeClaim, clusterName string) error {
	if pvc.Labels[LabelDocumentDBCluster] == clusterName {
		return nil
	}

	if pvc.Labels == nil {
		pvc.Labels = make(map[string]string)
	}
	pvc.Labels[LabelDocumentDBCluster] = clusterName
	return r.Update(ctx, pvc)
}

// getDocumentDBClusterName determines if a PVC belongs to a DocumentDB cluster and returns the cluster name.
// Returns empty string if the PVC does not belong to a DocumentDB cluster.
func (r *PVCReconciler) getDocumentDBClusterName(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (string, error) {
	// Check if PVC already has the documentdb.io/cluster label
	if clusterName, hasLabel := pvc.Labels[LabelDocumentDBCluster]; hasLabel {
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

// updateRetentionAnnotation updates the PVC retention annotation based on cluster configuration.
// If the cluster is deleted, preserves existing annotation or sets default.
func (r *PVCReconciler) updateRetentionAnnotation(ctx context.Context, pvc *corev1.PersistentVolumeClaim, clusterName string) error {
	log := log.FromContext(ctx)

	var cluster dbpreview.DocumentDB
	err := r.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: pvc.Namespace}, &cluster)

	// If cluster doesn't exist (deleted), preserve or set default retention
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get DocumentDB cluster %s: %w", clusterName, err)
		}

		// Cluster deleted - ensure annotation exists for retention logic
		if pvc.Annotations == nil || pvc.Annotations[AnnotationPVCRetentionDays] == "" {
			if pvc.Annotations == nil {
				pvc.Annotations = make(map[string]string)
			}
			pvc.Annotations[AnnotationPVCRetentionDays] = strconv.Itoa(DefaultPVCRetentionDays)
			log.Info("Setting default retention for PVC from deleted cluster", "retentionDays", DefaultPVCRetentionDays)
			return r.Update(ctx, pvc)
		}
		return nil
	}

	// Cluster exists - sync annotation from cluster spec
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}

	retentionDays := cluster.Spec.Resource.Storage.PvcRetentionDays
	expectedRetention := strconv.Itoa(retentionDays)
	currentRetention := pvc.Annotations[AnnotationPVCRetentionDays]

	if currentRetention != expectedRetention {
		log.Info("Updating PVC retention annotation", "oldValue", currentRetention, "newValue", expectedRetention)
		pvc.Annotations[AnnotationPVCRetentionDays] = expectedRetention
		return r.Update(ctx, pvc)
	}

	return nil
}

// getRetentionDays extracts and validates the retention days from PVC annotations.
func (r *PVCReconciler) getRetentionDays(pvc *corev1.PersistentVolumeClaim) int {
	retentionStr := pvc.Annotations[AnnotationPVCRetentionDays]
	if retentionStr == "" {
		return DefaultPVCRetentionDays
	}

	days, err := strconv.Atoi(retentionStr)
	if err != nil {
		return DefaultPVCRetentionDays
	}
	return days
}

// manageFinalizer adds or removes the PVC finalizer based on cluster deletion status and retention period.
func (r *PVCReconciler) manageFinalizer(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	log := log.FromContext(ctx)
	shouldHaveFinalizer := r.shouldRetainPVC(pvc)
	hasFinalizer := containsString(pvc.Finalizers, PVCFinalizerName)

	if shouldHaveFinalizer == hasFinalizer {
		// Already in desired state
		return nil
	}

	if shouldHaveFinalizer {
		log.Info("Adding retention finalizer to PVC")
		if pvc.Finalizers == nil {
			pvc.Finalizers = []string{}
		}
		pvc.Finalizers = append(pvc.Finalizers, PVCFinalizerName)
	} else {
		log.Info("Removing retention finalizer from PVC (retention period expired)")
		if pvc.Finalizers == nil {
			return nil
		}
		pvc.Finalizers = removeString(pvc.Finalizers, PVCFinalizerName)
	}

	return r.Update(ctx, pvc)
}

// shouldRetainPVC determines if a PVC should have a retention finalizer.
// Returns true if:
// 1. PVC is not being deleted (actively in use)
// 2. PVC is being deleted but retention period has not expired
func (r *PVCReconciler) shouldRetainPVC(pvc *corev1.PersistentVolumeClaim) bool {
	if pvc.DeletionTimestamp == nil {
		// PVC is active, should have finalizer for future retention
		return true
	}

	// PVC is being deleted - check if retention period has expired
	retentionDays := r.getRetentionDays(pvc)
	retentionExpiration := pvc.DeletionTimestamp.AddDate(0, 0, retentionDays)
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
	if err := r.List(ctx, pvcList, client.InNamespace(cluster.GetNamespace()), client.MatchingLabels{LabelDocumentDBCluster: cluster.GetName()}); err != nil {
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

			// Only trigger if PvcRetentionDays has changed
			return oldCluster.Spec.Resource.Storage.PvcRetentionDays != newCluster.Spec.Resource.Storage.PvcRetentionDays
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
