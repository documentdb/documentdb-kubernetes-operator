// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GUIDManager handles generation and retrieval of telemetry GUIDs.
type GUIDManager struct {
	client client.Client
}

// NewGUIDManager creates a new GUIDManager.
func NewGUIDManager(c client.Client) *GUIDManager {
	return &GUIDManager{client: c}
}

// GetOrCreateClusterID retrieves or creates a telemetry GUID for a cluster.
func (m *GUIDManager) GetOrCreateClusterID(ctx context.Context, obj client.Object) (string, error) {
	return m.getOrCreateID(ctx, obj, ClusterIDAnnotation)
}

// GetOrCreateBackupID retrieves or creates a telemetry GUID for a backup.
func (m *GUIDManager) GetOrCreateBackupID(ctx context.Context, obj client.Object) (string, error) {
	return m.getOrCreateID(ctx, obj, BackupIDAnnotation)
}

// GetOrCreateScheduledBackupID retrieves or creates a telemetry GUID for a scheduled backup.
func (m *GUIDManager) GetOrCreateScheduledBackupID(ctx context.Context, obj client.Object) (string, error) {
	return m.getOrCreateID(ctx, obj, ScheduledBackupIDAnnotation)
}

// GetClusterID retrieves the telemetry GUID for a cluster without creating one.
func (m *GUIDManager) GetClusterID(obj client.Object) string {
	return getAnnotation(obj, ClusterIDAnnotation)
}

// GetBackupID retrieves the telemetry GUID for a backup without creating one.
func (m *GUIDManager) GetBackupID(obj client.Object) string {
	return getAnnotation(obj, BackupIDAnnotation)
}

// getOrCreateID retrieves or creates a GUID in the specified annotation.
func (m *GUIDManager) getOrCreateID(ctx context.Context, obj client.Object, annotationKey string) (string, error) {
	// Check if ID already exists
	existingID := getAnnotation(obj, annotationKey)
	if existingID != "" {
		return existingID, nil
	}

	// Generate new UUID
	newID := uuid.New().String()

	// Update the object with the new annotation
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[annotationKey] = newID
	obj.SetAnnotations(annotations)

	// Persist the annotation
	if m.client != nil {
		if err := m.client.Update(ctx, obj); err != nil {
			return newID, err
		}
	}

	return newID, nil
}

// SetClusterID sets a telemetry GUID for a cluster (without persisting).
// Useful when creating new resources.
func SetClusterID(obj metav1.Object) string {
	return setAnnotation(obj, ClusterIDAnnotation)
}

// SetBackupID sets a telemetry GUID for a backup (without persisting).
func SetBackupID(obj metav1.Object) string {
	return setAnnotation(obj, BackupIDAnnotation)
}

// SetScheduledBackupID sets a telemetry GUID for a scheduled backup (without persisting).
func SetScheduledBackupID(obj metav1.Object) string {
	return setAnnotation(obj, ScheduledBackupIDAnnotation)
}

// getAnnotation safely retrieves an annotation value.
func getAnnotation(obj client.Object, key string) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}
	return annotations[key]
}

// setAnnotation sets a new UUID in an annotation and returns it.
func setAnnotation(obj metav1.Object, key string) string {
	newID := uuid.New().String()
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[key] = newID
	obj.SetAnnotations(annotations)
	return newID
}
