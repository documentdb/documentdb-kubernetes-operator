// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// ReconcilePVRecovery handles recovery from a retained PersistentVolume.
//
// CNPG only supports recovery from PVC (via VolumeSnapshots.Storage with Kind: PersistentVolumeClaim),
// not directly from PV. To bridge this gap, we create a temporary PVC that binds to the retained PV
// via spec.volumeName. CNPG then clones the data from this temp PVC to new cluster PVCs.
// After recovery completes (cluster healthy), we delete the temp PVC to release the source PV
// back to the user for manual cleanup or reuse.
//
// Flow:
//   - If no PV recovery configured, return immediately
//   - If CNPG exists and healthy, delete temp PVC (recovery complete)
//   - If CNPG doesn't exist, validate PV and create temp PVC bound to it
func (r *ClusterReconciler[T]) ReconcilePVRecovery(ctx context.Context, obj T, namespace, cnpgClusterName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Skip if PV recovery is not configured
	pvName := r.Adapter.Spec(obj).RecoveryPersistentVolume
	if pvName == "" {
		return ctrl.Result{}, nil
	}

	tempPVCName := util.TempPVCNameForPVRecovery(obj.GetName())

	// Check if CNPG cluster exists
	cnpgCluster := &cnpgv1.Cluster{}
	cnpgErr := r.Get(ctx, types.NamespacedName{Name: cnpgClusterName, Namespace: namespace}, cnpgCluster)

	if cnpgErr == nil {
		// CNPG exists - check if healthy and cleanup temp PVC
		if cnpgCluster.Status.Phase == CNPGClusterHealthyPhase {
			tempPVC := &corev1.PersistentVolumeClaim{}
			if err := r.Get(ctx, types.NamespacedName{Name: tempPVCName, Namespace: namespace}, tempPVC); err == nil {
				logger.Info("Deleting temp PVC after successful recovery", "pvc", tempPVCName)
				if err := r.Delete(ctx, tempPVC); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to delete temp PVC %s: %w", tempPVCName, err)
				}
			}
		}
		return ctrl.Result{}, nil
	}

	if !errors.IsNotFound(cnpgErr) {
		return ctrl.Result{}, fmt.Errorf("failed to get CNPG cluster: %w", cnpgErr)
	}

	// CNPG doesn't exist - prepare temp PVC for recovery

	// Check if temp PVC already exists
	tempPVC := &corev1.PersistentVolumeClaim{}
	tempPVCErr := r.Get(ctx, types.NamespacedName{Name: tempPVCName, Namespace: namespace}, tempPVC)
	if tempPVCErr == nil {
		// Temp PVC exists, check if bound
		if tempPVC.Status.Phase != corev1.ClaimBound {
			logger.Info("Waiting for temp PVC to bind to PV", "pvc", tempPVCName, "phase", tempPVC.Status.Phase)
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}
		// PVC is bound, ready to proceed with CNPG creation
		return ctrl.Result{}, nil
	}

	if !errors.IsNotFound(tempPVCErr) {
		return ctrl.Result{}, fmt.Errorf("failed to get temp PVC %s: %w", tempPVCName, tempPVCErr)
	}

	// Verify PV exists and is available
	pv := &corev1.PersistentVolume{}
	if err := r.Get(ctx, types.NamespacedName{Name: pvName}, pv); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("PV %s not found for recovery", pvName)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PV %s: %w", pvName, err)
	}

	if !util.IsPVAvailableForRecovery(pv) {
		return ctrl.Result{}, fmt.Errorf("PV %s must be Available or Released for recovery, current phase: %s.", pvName, pv.Status.Phase)
	}

	// Clear claimRef if PV is Released
	if util.NeedsToClearClaimRef(pv) {
		logger.Info("Clearing claimRef on Released PV", "pv", pvName)
		pv.Spec.ClaimRef = nil
		if err := r.Update(ctx, pv); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clear claimRef on PV %s: %w", pvName, err)
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	// Create temp PVC
	naming := r.profile().Naming
	newPVC := util.BuildTempPVCForPVRecoveryWithLabels(obj.GetName(), namespace, pv, naming.RecoveryTempLabel(), naming.ClusterLabel())
	if err := controllerutil.SetControllerReference(obj, newPVC, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set owner reference on temp PVC: %w", err)
	}

	if err := r.Create(ctx, newPVC); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temp PVC %s: %w", tempPVCName, err)
	}

	logger.Info("Created temp PVC for PV recovery", "pvc", tempPVCName, "pv", pvName)
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}
