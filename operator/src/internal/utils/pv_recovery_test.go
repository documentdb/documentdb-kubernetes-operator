// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTempPVCNameForPVRecovery(t *testing.T) {
	tests := []struct {
		name           string
		documentdbName string
		expected       string
	}{
		{
			name:           "simple name",
			documentdbName: "my-cluster",
			expected:       "my-cluster-pv-recovery-temp",
		},
		{
			name:           "short name",
			documentdbName: "db",
			expected:       "db-pv-recovery-temp",
		},
		{
			name:           "longer name",
			documentdbName: "production-database-cluster",
			expected:       "production-database-cluster-pv-recovery-temp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TempPVCNameForPVRecovery(tt.documentdbName)
			if result != tt.expected {
				t.Errorf("TempPVCNameForPVRecovery(%q) = %q, want %q", tt.documentdbName, result, tt.expected)
			}
		})
	}
}

func TestBuildTempPVCForPVRecovery(t *testing.T) {
	storageClass := "premium-storage"
	storageQuantity := resource.MustParse("100Gi")

	tests := []struct {
		name           string
		documentdbName string
		namespace      string
		pv             *corev1.PersistentVolume
		expectedName   string
		expectedLabels map[string]string
	}{
		{
			name:           "basic PVC creation",
			documentdbName: "my-cluster",
			namespace:      "default",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-abc123-retained",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: storageClass,
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: storageQuantity,
					},
				},
			},
			expectedName: "my-cluster-pv-recovery-temp",
			expectedLabels: map[string]string{
				LabelRecoveryTemp: "true",
				LabelCluster:      "my-cluster",
			},
		},
		{
			name:           "PVC with no access modes defaults to ReadWriteOnce",
			documentdbName: "test-db",
			namespace:      "test-ns",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-no-access-modes",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: storageClass,
					AccessModes:      []corev1.PersistentVolumeAccessMode{}, // empty
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: storageQuantity,
					},
				},
			},
			expectedName: "test-db-pv-recovery-temp",
			expectedLabels: map[string]string{
				LabelRecoveryTemp: "true",
				LabelCluster:      "test-db",
			},
		},
		{
			name:           "PVC with no storage class",
			documentdbName: "cluster-no-sc",
			namespace:      "production",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-no-storage-class",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "", // empty
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: storageQuantity,
					},
				},
			},
			expectedName: "cluster-no-sc-pv-recovery-temp",
			expectedLabels: map[string]string{
				LabelRecoveryTemp: "true",
				LabelCluster:      "cluster-no-sc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildTempPVCForPVRecovery(tt.documentdbName, tt.namespace, tt.pv)

			// Check name
			if result.Name != tt.expectedName {
				t.Errorf("Name = %q, want %q", result.Name, tt.expectedName)
			}

			// Check namespace
			if result.Namespace != tt.namespace {
				t.Errorf("Namespace = %q, want %q", result.Namespace, tt.namespace)
			}

			// Check labels
			for key, expectedValue := range tt.expectedLabels {
				if result.Labels[key] != expectedValue {
					t.Errorf("Label[%q] = %q, want %q", key, result.Labels[key], expectedValue)
				}
			}

			// Check volume name binding
			if result.Spec.VolumeName != tt.pv.Name {
				t.Errorf("VolumeName = %q, want %q", result.Spec.VolumeName, tt.pv.Name)
			}

			// Check access modes
			if len(result.Spec.AccessModes) == 0 {
				t.Error("AccessModes should not be empty")
			}
			if len(tt.pv.Spec.AccessModes) == 0 {
				// Should default to ReadWriteOnce
				if result.Spec.AccessModes[0] != corev1.ReadWriteOnce {
					t.Errorf("AccessModes[0] = %q, want %q", result.Spec.AccessModes[0], corev1.ReadWriteOnce)
				}
			}

			// Check storage class
			if tt.pv.Spec.StorageClassName == "" {
				if result.Spec.StorageClassName != nil {
					t.Errorf("StorageClassName = %v, want nil", result.Spec.StorageClassName)
				}
			} else {
				if result.Spec.StorageClassName == nil || *result.Spec.StorageClassName != tt.pv.Spec.StorageClassName {
					t.Errorf("StorageClassName = %v, want %q", result.Spec.StorageClassName, tt.pv.Spec.StorageClassName)
				}
			}
		})
	}
}

func TestIsPVAvailableForRecovery(t *testing.T) {
	tests := []struct {
		name     string
		pv       *corev1.PersistentVolume
		expected bool
	}{
		{
			name: "Available PV is available for recovery",
			pv: &corev1.PersistentVolume{
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			},
			expected: true,
		},
		{
			name: "Released PV is available for recovery",
			pv: &corev1.PersistentVolume{
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeReleased,
				},
			},
			expected: true,
		},
		{
			name: "Bound PV is not available for recovery",
			pv: &corev1.PersistentVolume{
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			expected: false,
		},
		{
			name: "Pending PV is not available for recovery",
			pv: &corev1.PersistentVolume{
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumePending,
				},
			},
			expected: false,
		},
		{
			name: "Failed PV is not available for recovery",
			pv: &corev1.PersistentVolume{
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeFailed,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPVAvailableForRecovery(tt.pv)
			if result != tt.expected {
				t.Errorf("IsPVAvailableForRecovery() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNeedsToClearClaimRef(t *testing.T) {
	tests := []struct {
		name     string
		pv       *corev1.PersistentVolume
		expected bool
	}{
		{
			name: "Released PV with claimRef needs clearing",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Name:      "old-pvc",
						Namespace: "default",
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeReleased,
				},
			},
			expected: true,
		},
		{
			name: "Released PV without claimRef does not need clearing",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: nil,
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeReleased,
				},
			},
			expected: false,
		},
		{
			name: "Available PV with claimRef does not need clearing",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Name:      "some-pvc",
						Namespace: "default",
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			},
			expected: false,
		},
		{
			name: "Bound PV with claimRef does not need clearing",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Name:      "active-pvc",
						Namespace: "default",
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NeedsToClearClaimRef(tt.pv)
			if result != tt.expected {
				t.Errorf("NeedsToClearClaimRef() = %v, want %v", result, tt.expected)
			}
		})
	}
}
