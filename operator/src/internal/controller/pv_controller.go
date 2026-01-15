// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// PersistentVolumeReconciler reconciles PersistentVolume objects
// to set their ReclaimPolicy based on the associated DocumentDB configuration
type PersistentVolumeReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch

func (r *PersistentVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the PersistentVolume
	pv := &corev1.PersistentVolume{}
	if err := r.Get(ctx, req.NamespacedName, pv); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get PersistentVolume")
		return ctrl.Result{}, err
	}

	// Skip if PV is not bound to a PVC
	if pv.Spec.ClaimRef == nil {
		logger.V(1).Info("PV has no claimRef, skipping", "pv", pv.Name)
		return ctrl.Result{}, nil
	}

	// Find the associated DocumentDB through the ownership chain:
	// PV -> PVC -> CNPG Cluster -> DocumentDB
	documentdb, err := r.findDocumentDBForPV(ctx, pv)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("No DocumentDB found for PV, skipping", "pv", pv.Name)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to find DocumentDB for PV")
		return ctrl.Result{}, err
	}

	if documentdb == nil {
		logger.V(1).Info("PV is not associated with a DocumentDB cluster, skipping", "pv", pv.Name)
		return ctrl.Result{}, nil
	}

	// Determine the desired reclaim policy from DocumentDB spec
	desiredPolicy := r.getDesiredReclaimPolicy(documentdb)
	currentPolicy := pv.Spec.PersistentVolumeReclaimPolicy

	// Update the PV's reclaim policy if it differs from the desired policy
	if currentPolicy != desiredPolicy {
		logger.Info("Updating PV reclaim policy",
			"pv", pv.Name,
			"currentPolicy", currentPolicy,
			"desiredPolicy", desiredPolicy,
			"documentdb", documentdb.Name)

		pv.Spec.PersistentVolumeReclaimPolicy = desiredPolicy
		if err := r.Update(ctx, pv); err != nil {
			logger.Error(err, "Failed to update PV reclaim policy")
			return ctrl.Result{}, err
		}

		logger.Info("Successfully updated PV reclaim policy",
			"pv", pv.Name,
			"newPolicy", desiredPolicy)
	}

	return ctrl.Result{}, nil
}

// findDocumentDBForPV traverses the ownership chain to find the DocumentDB
// associated with a PersistentVolume:
// PV.claimRef -> PVC -> (ownerRef) CNPG Cluster -> (ownerRef) DocumentDB
func (r *PersistentVolumeReconciler) findDocumentDBForPV(ctx context.Context, pv *corev1.PersistentVolume) (*dbpreview.DocumentDB, error) {
	logger := log.FromContext(ctx)

	// Step 1: Get the PVC from PV's claimRef
	if pv.Spec.ClaimRef == nil {
		return nil, nil
	}

	pvc := &corev1.PersistentVolumeClaim{}
	pvcKey := types.NamespacedName{
		Name:      pv.Spec.ClaimRef.Name,
		Namespace: pv.Spec.ClaimRef.Namespace,
	}
	if err := r.Get(ctx, pvcKey, pvc); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("PVC not found for PV", "pvc", pvcKey, "pv", pv.Name)
			return nil, nil
		}
		return nil, err
	}

	// Step 2: Find CNPG Cluster that owns the PVC
	cnpgCluster := r.findCNPGClusterOwner(ctx, pvc)
	if cnpgCluster == nil {
		logger.V(1).Info("No CNPG Cluster owner found for PVC", "pvc", pvc.Name)
		return nil, nil
	}

	// Step 3: Find DocumentDB that owns the CNPG Cluster
	documentdb := r.findDocumentDBOwner(ctx, cnpgCluster)
	if documentdb == nil {
		logger.V(1).Info("No DocumentDB owner found for CNPG Cluster", "cluster", cnpgCluster.Name)
		return nil, nil
	}

	logger.V(1).Info("Found DocumentDB for PV",
		"pv", pv.Name,
		"pvc", pvc.Name,
		"cluster", cnpgCluster.Name,
		"documentdb", documentdb.Name)

	return documentdb, nil
}

// findCNPGClusterOwner finds the CNPG Cluster that owns the given PVC
func (r *PersistentVolumeReconciler) findCNPGClusterOwner(ctx context.Context, pvc *corev1.PersistentVolumeClaim) *cnpgv1.Cluster {
	logger := log.FromContext(ctx)

	// Check owner references for CNPG Cluster
	for _, ownerRef := range pvc.OwnerReferences {
		if ownerRef.Kind == "Cluster" && strings.Contains(ownerRef.APIVersion, "cnpg") {
			cluster := &cnpgv1.Cluster{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: pvc.Namespace,
			}, cluster); err != nil {
				if !errors.IsNotFound(err) {
					logger.Error(err, "Failed to get CNPG Cluster", "name", ownerRef.Name)
				}
				continue
			}
			return cluster
		}
	}

	return nil
}

// findDocumentDBOwner finds the DocumentDB that owns the given CNPG Cluster
func (r *PersistentVolumeReconciler) findDocumentDBOwner(ctx context.Context, cluster *cnpgv1.Cluster) *dbpreview.DocumentDB {
	logger := log.FromContext(ctx)

	// Check owner references for DocumentDB
	for _, ownerRef := range cluster.OwnerReferences {
		if ownerRef.Kind == "DocumentDB" {
			documentdb := &dbpreview.DocumentDB{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      ownerRef.Name,
				Namespace: cluster.Namespace,
			}, documentdb); err != nil {
				if !errors.IsNotFound(err) {
					logger.Error(err, "Failed to get DocumentDB", "name", ownerRef.Name)
				}
				continue
			}
			return documentdb
		}
	}

	return nil
}

// getDesiredReclaimPolicy returns the reclaim policy based on DocumentDB configuration
func (r *PersistentVolumeReconciler) getDesiredReclaimPolicy(documentdb *dbpreview.DocumentDB) corev1.PersistentVolumeReclaimPolicy {
	policy := documentdb.Spec.Resource.Storage.PersistentVolumeReclaimPolicy

	switch policy {
	case "Retain":
		return corev1.PersistentVolumeReclaimRetain
	case "Delete":
		return corev1.PersistentVolumeReclaimDelete
	default:
		// Default to Delete if not specified
		return corev1.PersistentVolumeReclaimDelete
	}
}

// pvPredicate filters PV events to only process bound PVs
func pvPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pv, ok := e.Object.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			// Only process PVs that are bound and have a claimRef
			return pv.Status.Phase == corev1.VolumeBound && pv.Spec.ClaimRef != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newPV, ok := e.ObjectNew.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			// Process when PV becomes bound or when claimRef changes
			return newPV.Status.Phase == corev1.VolumeBound && newPV.Spec.ClaimRef != nil
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// No need to reconcile deleted PVs
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			pv, ok := e.Object.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			return pv.Status.Phase == corev1.VolumeBound && pv.Spec.ClaimRef != nil
		},
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *PersistentVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Apply pvPredicate only to PersistentVolume events, not globally
		For(&corev1.PersistentVolume{}, builder.WithPredicates(pvPredicate())).
		// Watch DocumentDB changes and trigger reconciliation of associated PVs
		Watches(
			&dbpreview.DocumentDB{},
			handler.EnqueueRequestsFromMapFunc(r.findPVsForDocumentDB),
			builder.WithPredicates(documentDBReclaimPolicyPredicate()),
		).
		Named("pv-controller").
		Complete(r)
}

// documentDBReclaimPolicyPredicate only triggers when the reclaim policy field changes
func documentDBReclaimPolicyPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldDB, ok := e.ObjectOld.(*dbpreview.DocumentDB)
			if !ok {
				return false
			}
			newDB, ok := e.ObjectNew.(*dbpreview.DocumentDB)
			if !ok {
				return false
			}
			return oldDB.Spec.Resource.Storage.PersistentVolumeReclaimPolicy != newDB.Spec.Resource.Storage.PersistentVolumeReclaimPolicy
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// findPVsForDocumentDB finds all PVs associated with a DocumentDB and returns reconcile requests for them
func (r *PersistentVolumeReconciler) findPVsForDocumentDB(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil
	}

	// Find CNPG Cluster owned by this DocumentDB
	clusterList := &cnpgv1.ClusterList{}
	if err := r.List(ctx, clusterList, client.InNamespace(documentdb.Namespace)); err != nil {
		logger.Error(err, "Failed to list CNPG Clusters")
		return nil
	}

	var requests []reconcile.Request

	for _, cluster := range clusterList.Items {
		// Check if this cluster is owned by the DocumentDB
		isOwned := false
		for _, ownerRef := range cluster.OwnerReferences {
			if ownerRef.Kind == "DocumentDB" && ownerRef.Name == documentdb.Name {
				isOwned = true
				break
			}
		}
		if !isOwned {
			continue
		}

		// Find PVCs owned by this cluster
		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList, client.InNamespace(cluster.Namespace)); err != nil {
			logger.Error(err, "Failed to list PVCs")
			continue
		}

		for _, pvc := range pvcList.Items {
			// Check if this PVC is owned by the cluster
			for _, ownerRef := range pvc.OwnerReferences {
				if ownerRef.Kind == "Cluster" && ownerRef.Name == cluster.Name && strings.Contains(ownerRef.APIVersion, "cnpg") {
					// Find the PV bound to this PVC
					if pvc.Spec.VolumeName != "" {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name: pvc.Spec.VolumeName,
							},
						})
					}
					break
				}
			}
		}
	}

	logger.Info("Found PVs to reconcile for DocumentDB update",
		"documentdb", documentdb.Name,
		"pvCount", len(requests))

	return requests
}
