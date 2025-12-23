// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

const (
	// PVCFinalizerName is the finalizer added to PVCs to manage retention
	PVCFinalizerName = "documentdb.io/pvc-retention"
	// PVCDeletionTimeAnnotation stores when a PVC was marked for deletion
	PVCDeletionTimeAnnotation = "documentdb.io/deletion-time"
)

// PVCReconciler handles PVC lifecycle management including retention after cluster deletion
type PVCReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch

// Reconcile handles PVC events, managing finalizers and retention periods
func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the PVC
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, req.NamespacedName, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get PVC")
		return ctrl.Result{}, err
	}

	// Check if PVC has our finalizer
	hasFinalizer := false
	for _, finalizer := range pvc.Finalizers {
		if finalizer == PVCFinalizerName {
			hasFinalizer = true
			break
		}
	}

	if !hasFinalizer {
		// Finalizer not present, nothing to do
		return ctrl.Result{}, nil
	}

	// Check if PVC is being deleted
	if pvc.DeletionTimestamp == nil {
		// PVC is not being deleted, nothing to do
		return ctrl.Result{}, nil
	}

	// PVC is being deleted, check if retention period has been set
	deletionTimeStr, hasAnnotation := pvc.Annotations[PVCDeletionTimeAnnotation]
	if !hasAnnotation {
		// Find the DocumentDB instance to get retention period
		retentionDays := 7 // default
		documentDBName := ""

		// Find CNPG cluster owner
		for _, ownerRef := range pvc.OwnerReferences {
			if ownerRef.Kind == "Cluster" {
				// Get the CNPG cluster
				cnpgCluster := &cnpgv1.Cluster{}
				if err := r.Get(ctx, types.NamespacedName{Name: ownerRef.Name, Namespace: pvc.Namespace}, cnpgCluster); err == nil {
					// Find DocumentDB owner of CNPG cluster
					for _, cnpgOwner := range cnpgCluster.OwnerReferences {
						if cnpgOwner.Kind == "DocumentDB" {
							documentDBName = cnpgOwner.Name
							break
						}
					}
				}
				break
			}
		}

		// Get retention period from DocumentDB spec
		if documentDBName != "" {
			documentDB := &dbpreview.DocumentDB{}
			if err := r.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: pvc.Namespace}, documentDB); err == nil {
				retentionDays = documentDB.Spec.Resource.Storage.PvcRetentionPeriodDays
			}
		}

		// Set deletion timestamp annotation
		if pvc.Annotations == nil {
			pvc.Annotations = make(map[string]string)
		}
		pvc.Annotations[PVCDeletionTimeAnnotation] = time.Now().Format(time.RFC3339)
		if err := r.Client.Update(ctx, pvc); err != nil {
			logger.Error(err, "Failed to add deletion time annotation to PVC")
			return ctrl.Result{}, err
		}

		logger.Info("PVC marked for deletion with retention period",
			"PVC", pvc.Name,
			"RetentionDays", retentionDays,
			"DeletionTime", pvc.Annotations[PVCDeletionTimeAnnotation])

		// Requeue to check again later
		return ctrl.Result{RequeueAfter: 24 * time.Hour}, nil
	}

	// Parse the deletion time
	deletionTime, err := time.Parse(time.RFC3339, deletionTimeStr)
	if err != nil {
		logger.Error(err, "Failed to parse deletion time annotation, removing finalizer immediately")
		// Remove finalizer to allow deletion
		pvc.Finalizers = removeFinalizer(pvc.Finalizers, PVCFinalizerName)
		if err := r.Client.Update(ctx, pvc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Get retention period from DocumentDB
	retentionDays := 7 // default
	documentDBName := ""

	// Find CNPG cluster owner
	for _, ownerRef := range pvc.OwnerReferences {
		if ownerRef.Kind == "Cluster" {
			// Get the CNPG cluster
			cnpgCluster := &cnpgv1.Cluster{}
			if err := r.Get(ctx, types.NamespacedName{Name: ownerRef.Name, Namespace: pvc.Namespace}, cnpgCluster); err == nil {
				// Find DocumentDB owner of CNPG cluster
				for _, cnpgOwner := range cnpgCluster.OwnerReferences {
					if cnpgOwner.Kind == "DocumentDB" {
						documentDBName = cnpgOwner.Name
						break
					}
				}
			}
			break
		}
	}

	// Get retention period from DocumentDB spec
	if documentDBName != "" {
		documentDB := &dbpreview.DocumentDB{}
		if err := r.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: pvc.Namespace}, documentDB); err == nil {
			retentionDays = documentDB.Spec.Resource.Storage.PvcRetentionPeriodDays
		}
	}

	// Check if retention period has passed
	retentionDuration := time.Duration(retentionDays) * 24 * time.Hour
	if time.Since(deletionTime) >= retentionDuration {
		logger.Info("Retention period expired, removing finalizer from PVC",
			"PVC", pvc.Name,
			"RetentionDays", retentionDays,
			"DeletionTime", deletionTimeStr)

		// Remove finalizer to allow deletion
		pvc.Finalizers = removeFinalizer(pvc.Finalizers, PVCFinalizerName)
		if err := r.Client.Update(ctx, pvc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Retention period not yet expired, requeue
	timeRemaining := retentionDuration - time.Since(deletionTime)
	logger.Info("PVC retention period not yet expired",
		"PVC", pvc.Name,
		"TimeRemaining", timeRemaining.String())

	// Requeue after remaining time or max 24 hours
	requeueAfter := timeRemaining
	if requeueAfter > 24*time.Hour {
		requeueAfter = 24 * time.Hour
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// ensurePVCFinalizers ensures that all PVCs owned by the CNPG cluster have the retention finalizer
func (r *PVCReconciler) ensurePVCFinalizers(ctx context.Context, cnpgCluster *cnpgv1.Cluster, documentdb *dbpreview.DocumentDB) error {
	logger := log.FromContext(ctx)

	// List all PVCs in the namespace
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.Client.List(ctx, pvcList, client.InNamespace(cnpgCluster.Namespace)); err != nil {
		return fmt.Errorf("failed to list PVCs: %w", err)
	}

	// Filter PVCs owned by the CNPG cluster
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]

		// Check if this PVC is owned by the CNPG cluster
		isOwnedByCNPG := false
		for _, ownerRef := range pvc.OwnerReferences {
			if ownerRef.Kind == "Cluster" && ownerRef.Name == cnpgCluster.Name {
				isOwnedByCNPG = true
				break
			}
		}

		if !isOwnedByCNPG {
			continue
		}

		// Check if finalizer is already present
		hasFinalizer := false
		for _, finalizer := range pvc.Finalizers {
			if finalizer == PVCFinalizerName {
				hasFinalizer = true
				break
			}
		}

		// Add finalizer if not present
		if !hasFinalizer {
			logger.Info("Adding finalizer to PVC", "PVC", pvc.Name, "Cluster", cnpgCluster.Name)
			pvc.Finalizers = append(pvc.Finalizers, PVCFinalizerName)
			if err := r.Client.Update(ctx, pvc); err != nil {
				return fmt.Errorf("failed to add finalizer to PVC %s: %w", pvc.Name, err)
			}
		}
	}

	return nil
}

// removeFinalizer removes a specific finalizer from a slice of finalizers
func removeFinalizer(finalizers []string, finalizer string) []string {
	result := []string{}
	for _, f := range finalizers {
		if f != finalizer {
			result = append(result, f)
		}
	}
	return result
}
