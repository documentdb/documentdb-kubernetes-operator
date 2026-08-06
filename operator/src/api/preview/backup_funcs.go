// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// CreateCNPGBackup creates a CNPG Backup resource based on the DocumentDB Backup spec.
func (backup *Backup) CreateCNPGBackup(scheme *runtime.Scheme, clusterName string) (*cnpgv1.Backup, error) {
	cnpgBackup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
		},
		Spec: cnpgv1.BackupSpec{
			Method: cnpgv1.BackupMethodVolumeSnapshot,
			Cluster: cnpgv1.LocalObjectReference{
				Name: clusterName,
			},
		},
	}
	// Set owner reference for garbage collection
	// This ensures that the CNPG Backup is deleted when the DocumentDB Backup is deleted.
	if err := controllerutil.SetControllerReference(backup, cnpgBackup, scheme); err != nil {
		return nil, err
	}
	return cnpgBackup, nil
}

// UpdateStatus reconciles the Backup status from the observed inputs and returns
// whether any status field changed.
func (backup *Backup) UpdateStatus(cnpgBackup *cnpgv1.Backup, backupConfiguration *BackupConfiguration, sourceSchemaVersion string) bool {
	needsUpdate := false
	if backup.Status.Phase != cnpgBackup.Status.Phase {
		backup.Status.Phase = cnpgBackup.Status.Phase
		needsUpdate = true
	}

	if !areTimesEqual(backup.Status.StartedAt, cnpgBackup.Status.StartedAt) {
		backup.Status.StartedAt = cnpgBackup.Status.StartedAt
		needsUpdate = true
	}

	if !areTimesEqual(backup.Status.StoppedAt, cnpgBackup.Status.StoppedAt) {
		backup.Status.StoppedAt = cnpgBackup.Status.StoppedAt
		needsUpdate = true
	}

	if backup.Status.Message != cnpgBackup.Status.Error {
		backup.Status.Message = cnpgBackup.Status.Error
		needsUpdate = true
	}

	expirationTime := backup.CalculateExpirationTime(backupConfiguration)
	if !areTimesEqual(backup.Status.ExpiredAt, expirationTime) {
		backup.Status.ExpiredAt = expirationTime
		needsUpdate = true
	}

	// Record the source cluster's schema version at backup time: captured once
	// when first available and left immutable afterward, so a later restore can
	// validate binary-vs-schema compatibility.
	if backup.Status.SchemaVersion == "" && sourceSchemaVersion != "" {
		backup.Status.SchemaVersion = sourceSchemaVersion
		needsUpdate = true
	}

	return needsUpdate
}

// CalculateExpirationTime calculates the expiration time of the backup based on retention policy.
func (backup *Backup) CalculateExpirationTime(backupConfiguration *BackupConfiguration) *metav1.Time {
	if !backup.Status.IsDone() {
		return nil
	}

	retentionHours := 0
	if backup.Spec.RetentionDays != nil {
		retentionHours = *backup.Spec.RetentionDays * 24
	} else if backupConfiguration != nil {
		retentionHours = backupConfiguration.RetentionDays * 24
	} else {
		retentionHours = 30 * 24 // Default to 30 days
	}

	// Determine the start time for retention calculation
	// If backup completed, use StoppedAt;
	// If backup failed, StoppedAt is not set, use CreationTimestamp
	retentionStart := backup.Status.StoppedAt
	if retentionStart == nil {
		retentionStart = &backup.CreationTimestamp
	}

	expirationTime := retentionStart.Time.Add(time.Duration(retentionHours) * time.Hour)
	return &metav1.Time{Time: expirationTime}
}

// areTimesEqual compares two metav1.Time pointers for equality
func areTimesEqual(t1, t2 *metav1.Time) bool {
	if t1 == nil && t2 == nil {
		return true
	}
	if t1 == nil || t2 == nil {
		return false
	}
	return t1.Equal(t2)
}

// IsDone returns true if the backup operation is completed or failed.
func (backupStatus *BackupStatus) IsDone() bool {
	return backupStatus.Phase == cnpgv1.BackupPhaseCompleted || backupStatus.Phase == cnpgv1.BackupPhaseFailed || backupStatus.Phase == BackupPhaseSkipped
}

// IsExpired returns true if the backup has expired based on the current time.
func (backupStatus *BackupStatus) IsExpired() bool {
	if backupStatus.ExpiredAt == nil {
		return false
	}
	return backupStatus.ExpiredAt.Time.Before(time.Now())
}

// IsRunning returns true if the backup is currently in progress (not in a terminal state).
func (backupList *BackupList) IsBackupRunning() bool {
	for _, backup := range backupList.Items {
		if !backup.Status.IsDone() {
			return true
		}
	}
	return false
}

// GetLastBackup returns the most recent Backup from the list, or nil if the list is empty.
func (backupList *BackupList) GetLastBackup() *Backup {
	if len(backupList.Items) == 0 {
		return nil
	}

	var lastBackup *Backup
	for i, backup := range backupList.Items {
		if lastBackup == nil || backup.CreationTimestamp.After(lastBackup.CreationTimestamp.Time) {
			lastBackup = &backupList.Items[i]
		}
	}
	return lastBackup
}
