// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"slices"
	"sort"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

const (
	// cnpgAPIVersionPrefix is the prefix for CNPG API versions
	cnpgAPIVersionPrefix = "postgresql.cnpg.io"

	// ownerRefKindCluster is the Kind for CNPG Cluster owner references
	ownerRefKindCluster = "Cluster"

	// ownerRefKindDocumentDB is the Kind for DocumentDB owner references
	ownerRefKindDocumentDB = "DocumentDB"

	// reclaimPolicyRetain is the string value for Retain policy in DocumentDB spec
	reclaimPolicyRetain = "Retain"

	// reclaimPolicyDelete is the string value for Delete policy in DocumentDB spec
	reclaimPolicyDelete = "Delete"

	// cnpgClusterLabel is the label used by CNPG to identify PVCs belonging to a cluster.
	// This is defined here to avoid coupling with documentdb_controller.go.
	cnpgClusterLabelPV = "cnpg.io/cluster"
)

// securityMountOptions defines the mount options applied to PVs for security hardening:
// - nodev: Prevents device files from being interpreted on the filesystem
// - nosuid: Prevents setuid/setgid bits from taking effect
// - noexec: Prevents execution of binaries on the filesystem
//
// NOTE: These mount options are compatible with most CSI drivers and storage providers.
// However, some storage classes may not support these options, which could cause PV
// binding issues or pod startup failures. If you encounter issues with PV binding after
// deploying the operator, verify your storage provider supports these mount options.
// Common compatible providers: Azure Disk, AWS EBS, GCE PD, most NFS implementations.
var securityMountOptions = []string{"nodev", "noexec", "nosuid"}

// unsupportedMountOptionsProvisioners lists storage provisioners that do not support
// mount options. These are typically local/dev environment provisioners used in
// kind, minikube, or similar local Kubernetes clusters.
// When a PV uses one of these provisioners, security mount options will be skipped
// to avoid PV binding failures.
var unsupportedMountOptionsProvisioners = []string{
	"rancher.io/local-path",      // kind default local-path-provisioner
	"k8s.io/minikube-hostpath",   // minikube default hostpath provisioner
}

// PersistentVolumeReconciler reconciles PersistentVolume objects
// to set their ReclaimPolicy and mount options based on the associated DocumentDB configuration
type PersistentVolumeReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

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
		logger.Error(err, "Failed to find DocumentDB for PV")
		return ctrl.Result{}, err
	}

	if documentdb == nil {
		logger.V(1).Info("PV is not associated with a DocumentDB cluster, skipping", "pv", pv.Name)
		return ctrl.Result{}, nil
	}

	// Apply desired configuration to PV
	needsUpdate := r.applyDesiredPVConfiguration(ctx, pv, documentdb)

	if needsUpdate {
		if err := r.Update(ctx, pv); err != nil {
			logger.Error(err, "Failed to update PV")
			return ctrl.Result{}, err
		}

		logger.Info("Successfully updated PV",
			"pv", pv.Name,
			"reclaimPolicy", pv.Spec.PersistentVolumeReclaimPolicy,
			"mountOptions", pv.Spec.MountOptions)
	}

	return ctrl.Result{}, nil
}

// applyDesiredPVConfiguration applies the desired reclaim policy and mount options to a PV.
// Returns true if any changes were made.
func (r *PersistentVolumeReconciler) applyDesiredPVConfiguration(ctx context.Context, pv *corev1.PersistentVolume, documentdb *dbpreview.DocumentDB) bool {
	logger := log.FromContext(ctx)
	needsUpdate := false

	// Check if reclaim policy needs update
	desiredPolicy := r.getDesiredReclaimPolicy(documentdb)
	if pv.Spec.PersistentVolumeReclaimPolicy != desiredPolicy {
		logger.Info("PV reclaim policy needs update",
			"pv", pv.Name,
			"currentPolicy", pv.Spec.PersistentVolumeReclaimPolicy,
			"desiredPolicy", desiredPolicy,
			"documentdb", documentdb.Name)
		pv.Spec.PersistentVolumeReclaimPolicy = desiredPolicy
		needsUpdate = true
	}

	// Check if the storage provisioner supports mount options
	// Skip mount options for local/dev provisioners (kind, minikube, etc.)
	if r.provisionerSupportsMountOptions(ctx, pv) {
		// Check if mount options need update
		if !containsAllMountOptions(pv.Spec.MountOptions, securityMountOptions) {
			logger.Info("PV mount options need update",
				"pv", pv.Name,
				"currentMountOptions", pv.Spec.MountOptions,
				"desiredMountOptions", securityMountOptions)
			pv.Spec.MountOptions = mergeMountOptions(pv.Spec.MountOptions, securityMountOptions)
			needsUpdate = true
		}
	} else {
		logger.V(1).Info("Skipping mount options for PV - provisioner does not support them",
			"pv", pv.Name,
			"storageClassName", pv.Spec.StorageClassName)
	}

	return needsUpdate
}

// provisionerSupportsMountOptions checks if the PV's storage class provisioner supports mount options.
// Returns false for known local/dev provisioners (kind, minikube, etc.) that don't support mount options.
// Returns true for production provisioners (Azure Disk, AWS EBS, etc.) or if the provisioner cannot be determined.
func (r *PersistentVolumeReconciler) provisionerSupportsMountOptions(ctx context.Context, pv *corev1.PersistentVolume) bool {
	logger := log.FromContext(ctx)

	// If no storage class is specified, assume mount options are supported (safer default for production)
	if pv.Spec.StorageClassName == "" {
		return true
	}

	// Fetch the StorageClass to get the provisioner
	storageClass := &storagev1.StorageClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: pv.Spec.StorageClassName}, storageClass); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("StorageClass not found, assuming mount options are supported",
				"storageClassName", pv.Spec.StorageClassName)
			return true
		}
		logger.Error(err, "Failed to get StorageClass, assuming mount options are supported",
			"storageClassName", pv.Spec.StorageClassName)
		return true
	}

	// Check if the provisioner is in the unsupported list
	for _, unsupportedProvisioner := range unsupportedMountOptionsProvisioners {
		if storageClass.Provisioner == unsupportedProvisioner {
			logger.V(1).Info("Provisioner does not support mount options",
				"provisioner", storageClass.Provisioner,
				"storageClassName", pv.Spec.StorageClassName)
			return false
		}
	}

	return true
}

// containsAllMountOptions checks if all desired mount options are present in current options
func containsAllMountOptions(current, desired []string) bool {
	for _, opt := range desired {
		if !slices.Contains(current, opt) {
			return false
		}
	}
	return true
}

// mergeMountOptions merges desired mount options into current, avoiding duplicates.
// Returns a sorted slice for deterministic output.
func mergeMountOptions(current, desired []string) []string {
	optSet := make(map[string]struct{}, len(current)+len(desired))
	for _, opt := range current {
		optSet[opt] = struct{}{}
	}
	for _, opt := range desired {
		optSet[opt] = struct{}{}
	}

	result := make([]string, 0, len(optSet))
	for opt := range optSet {
		result = append(result, opt)
	}
	sort.Strings(result)
	return result
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

	for _, ownerRef := range pvc.OwnerReferences {
		if !isCNPGClusterOwnerRef(ownerRef) {
			continue
		}

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

	return nil
}

// findDocumentDBOwner finds the DocumentDB that owns the given CNPG Cluster
func (r *PersistentVolumeReconciler) findDocumentDBOwner(ctx context.Context, cluster *cnpgv1.Cluster) *dbpreview.DocumentDB {
	logger := log.FromContext(ctx)

	for _, ownerRef := range cluster.OwnerReferences {
		if ownerRef.Kind != ownerRefKindDocumentDB {
			continue
		}

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

	return nil
}

// isCNPGClusterOwnerRef checks if an owner reference refers to a CNPG Cluster
func isCNPGClusterOwnerRef(ownerRef metav1.OwnerReference) bool {
	return ownerRef.Kind == ownerRefKindCluster && strings.Contains(ownerRef.APIVersion, cnpgAPIVersionPrefix)
}

// isOwnedByDocumentDB checks if a CNPG Cluster is owned by a specific DocumentDB
func isOwnedByDocumentDB(cluster *cnpgv1.Cluster, documentdbName string) bool {
	for _, ownerRef := range cluster.OwnerReferences {
		if ownerRef.Kind == ownerRefKindDocumentDB && ownerRef.Name == documentdbName {
			return true
		}
	}
	return false
}

// getDesiredReclaimPolicy returns the reclaim policy based on DocumentDB configuration
func (r *PersistentVolumeReconciler) getDesiredReclaimPolicy(documentdb *dbpreview.DocumentDB) corev1.PersistentVolumeReclaimPolicy {
	switch documentdb.Spec.Resource.Storage.PersistentVolumeReclaimPolicy {
	case reclaimPolicyRetain:
		return corev1.PersistentVolumeReclaimRetain
	case reclaimPolicyDelete:
		return corev1.PersistentVolumeReclaimDelete
	default:
		// Default to Retain if not specified - safer for database workloads
		return corev1.PersistentVolumeReclaimRetain
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

// findPVsForDocumentDB finds all PVs associated with a DocumentDB and returns reconcile requests for them.
// Uses CNPG's cluster label (cnpg.io/cluster) for efficient PVC filtering instead of listing all resources.
func (r *PersistentVolumeReconciler) findPVsForDocumentDB(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil
	}

	// Use CNPG's cluster label to efficiently find PVCs belonging to this DocumentDB.
	// CNPG automatically labels PVCs with "cnpg.io/cluster" set to the cluster name,
	// and CNPG cluster name matches DocumentDB name by convention.
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList,
		client.InNamespace(documentdb.Namespace),
		client.MatchingLabels{cnpgClusterLabelPV: documentdb.Name},
	); err != nil {
		logger.Error(err, "Failed to list PVCs for DocumentDB")
		return nil
	}

	var requests []reconcile.Request
	for _, pvc := range pvcList.Items {
		if pvc.Spec.VolumeName != "" {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: pvc.Spec.VolumeName,
				},
			})
		}
	}

	logger.Info("Found PVs to reconcile for DocumentDB update",
		"documentdb", documentdb.Name,
		"pvCount", len(requests))

	return requests
}
