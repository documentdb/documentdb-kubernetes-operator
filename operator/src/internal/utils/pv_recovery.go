// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Label for identifying temporary PVCs created for PV recovery
	LabelRecoveryTemp = "documentdb.io/recovery-temp"
	LabelCluster      = "documentdb.io/cluster"
)

// TempPVCNameForPVRecovery generates the name for a temporary PVC used during PV recovery.
// The name is deterministic based on the DocumentDB cluster name.
func TempPVCNameForPVRecovery(documentdbName string) string {
	return fmt.Sprintf("%s-pv-recovery-temp", documentdbName)
}

// BuildTempPVCForPVRecovery creates a PersistentVolumeClaim spec that binds to a specific PV.
// The PVC uses the PV's storage class, access modes, and capacity to ensure successful binding.
// This temp PVC is used as a data source for CNPG to clone data during recovery.
func BuildTempPVCForPVRecovery(documentdbName, namespace string, pv *corev1.PersistentVolume) *corev1.PersistentVolumeClaim {
	// Get storage class name - handle both pointer and empty string cases
	var storageClassName *string
	if pv.Spec.StorageClassName != "" {
		storageClassName = &pv.Spec.StorageClassName
	}

	// Get capacity from PV
	storageCapacity := pv.Spec.Capacity[corev1.ResourceStorage]

	// Get access modes from PV, default to ReadWriteOnce if not specified
	accessModes := pv.Spec.AccessModes
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TempPVCNameForPVRecovery(documentdbName),
			Namespace: namespace,
			Labels: map[string]string{
				LabelRecoveryTemp: "true",
				LabelCluster:      documentdbName,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			VolumeName:       pv.Name, // Bind directly to the specified PV
			StorageClassName: storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageCapacity,
				},
			},
		},
	}
}

// IsPVAvailableForRecovery checks if a PV is in a state suitable for recovery.
// The PV must be either Available (unbound) or Released (previously bound but now free).
func IsPVAvailableForRecovery(pv *corev1.PersistentVolume) bool {
	return pv.Status.Phase == corev1.VolumeAvailable || pv.Status.Phase == corev1.VolumeReleased
}

// NeedsToClearClaimRef checks if the PV needs its claimRef cleared before recovery.
// A Released PV with a claimRef must have it cleared before a new PVC can bind to it.
func NeedsToClearClaimRef(pv *corev1.PersistentVolume) bool {
	return pv.Status.Phase == corev1.VolumeReleased && pv.Spec.ClaimRef != nil
}
