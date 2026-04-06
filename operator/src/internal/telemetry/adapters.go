// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"time"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DocumentDBTelemetry defines the telemetry interface for DocumentDB operations.
// Reconcilers call a single method; the adapter owns all data-gathering and nil-safety.
type DocumentDBTelemetry interface {
	ClusterCreated(ctx context.Context, cluster *dbpreview.DocumentDB, durationSeconds float64)
	ClusterUpdated(ctx context.Context, cluster *dbpreview.DocumentDB, updateType string, durationSeconds float64)
	ClusterDeleted(ctx context.Context, cluster *dbpreview.DocumentDB, k8sClient client.Client)
}

// BackupTelemetry defines the telemetry interface for Backup operations.
type BackupTelemetry interface {
	BackupCreated(ctx context.Context, backup *dbpreview.Backup, cluster *dbpreview.DocumentDB, backupType string)
	BackupExpired(ctx context.Context, backup *dbpreview.Backup, k8sClient client.Client)
	BackupDeleted(ctx context.Context, backup *dbpreview.Backup, reason string)
}

// ScheduledBackupTelemetry defines the telemetry interface for ScheduledBackup operations.
type ScheduledBackupTelemetry interface {
	ScheduledBackupCreated(ctx context.Context, scheduledBackup *dbpreview.ScheduledBackup, cluster *dbpreview.DocumentDB)
}

// --- Noop implementations (for tests and when telemetry is disabled) ---

// NoopDocumentDBTelemetry is a no-op implementation of DocumentDBTelemetry.
type NoopDocumentDBTelemetry struct{}

func (NoopDocumentDBTelemetry) ClusterCreated(_ context.Context, _ *dbpreview.DocumentDB, _ float64) {
}
func (NoopDocumentDBTelemetry) ClusterUpdated(_ context.Context, _ *dbpreview.DocumentDB, _ string, _ float64) {
}
func (NoopDocumentDBTelemetry) ClusterDeleted(_ context.Context, _ *dbpreview.DocumentDB, _ client.Client) {
}

// NoopBackupTelemetry is a no-op implementation of BackupTelemetry.
type NoopBackupTelemetry struct{}

func (NoopBackupTelemetry) BackupCreated(_ context.Context, _ *dbpreview.Backup, _ *dbpreview.DocumentDB, _ string) {
}
func (NoopBackupTelemetry) BackupExpired(_ context.Context, _ *dbpreview.Backup, _ client.Client) {}
func (NoopBackupTelemetry) BackupDeleted(_ context.Context, _ *dbpreview.Backup, _ string)        {}

// NoopScheduledBackupTelemetry is a no-op implementation of ScheduledBackupTelemetry.
type NoopScheduledBackupTelemetry struct{}

func (NoopScheduledBackupTelemetry) ScheduledBackupCreated(_ context.Context, _ *dbpreview.ScheduledBackup, _ *dbpreview.DocumentDB) {
}

// --- Real implementations ---

// documentDBTelemetryImpl is the real implementation of DocumentDBTelemetry.
type documentDBTelemetryImpl struct {
	mgr *Manager
}

func (t *documentDBTelemetryImpl) ClusterCreated(ctx context.Context, cluster *dbpreview.DocumentDB, durationSeconds float64) {
	clusterID := GetResourceTelemetryID(cluster)
	bootstrapType := "new"
	if cluster.Spec.Bootstrap != nil && cluster.Spec.Bootstrap.Recovery != nil {
		bootstrapType = "recovery"
	}

	t.mgr.Events.TrackClusterCreated(ClusterCreatedEvent{
		ClusterID:               clusterID,
		NamespaceHash:           HashNamespace(cluster.Namespace),
		CreationDurationSeconds: durationSeconds,
		NodeCount:               cluster.Spec.NodeCount,
		InstancesPerNode:        cluster.Spec.InstancesPerNode,
		StorageSize:             cluster.Spec.Resource.Storage.PvcSize,
		CloudProvider:           string(t.mgr.operatorCtx.CloudProvider),
		TLSEnabled:              cluster.Spec.TLS != nil,
		BootstrapType:           bootstrapType,
		SidecarInjectorPlugin:   cluster.Spec.SidecarInjectorPluginName != "",
		ServiceType:             cluster.Spec.ExposeViaService.ServiceType,
	})

	// Also track cluster configuration
	t.mgr.Metrics.TrackClusterConfiguration(ClusterConfigurationMetric{
		ClusterID:         clusterID,
		NamespaceHash:     HashNamespace(cluster.Namespace),
		NodeCount:         cluster.Spec.NodeCount,
		InstancesPerNode:  cluster.Spec.InstancesPerNode,
		TotalInstances:    cluster.Spec.NodeCount * cluster.Spec.InstancesPerNode,
		PVCSizeCategory:   categorizePVCSize(cluster.Spec.Resource.Storage.PvcSize),
		DocumentDBVersion: cluster.Spec.DocumentDBVersion,
	})
}

func (t *documentDBTelemetryImpl) ClusterUpdated(_ context.Context, cluster *dbpreview.DocumentDB, updateType string, durationSeconds float64) {
	clusterID := GetResourceTelemetryID(cluster)
	t.mgr.Events.TrackClusterUpdated(ClusterUpdatedEvent{
		ClusterID:             clusterID,
		NamespaceHash:         HashNamespace(cluster.Namespace),
		UpdateType:            updateType,
		UpdateDurationSeconds: durationSeconds,
	})
}

func (t *documentDBTelemetryImpl) ClusterDeleted(ctx context.Context, cluster *dbpreview.DocumentDB, k8sClient client.Client) {
	clusterID := GetResourceTelemetryID(cluster)

	clusterAgeDays := 0
	if !cluster.CreationTimestamp.IsZero() {
		clusterAgeDays = int(time.Since(cluster.CreationTimestamp.Time).Hours() / 24)
	}

	// Count associated backups
	backupList := &dbpreview.BackupList{}
	backupCount := 0
	if k8sClient != nil {
		if err := k8sClient.List(ctx, backupList, client.InNamespace(cluster.Namespace)); err == nil {
			for _, b := range backupList.Items {
				if b.Spec.Cluster.Name == cluster.Name {
					backupCount++
				}
			}
		}
	}

	t.mgr.Events.TrackClusterDeleted(ClusterDeletedEvent{
		ClusterID:               clusterID,
		NamespaceHash:           HashNamespace(cluster.Namespace),
		DeletionDurationSeconds: 0,
		ClusterAgeDays:          clusterAgeDays,
		BackupCount:             backupCount,
	})
}

// backupTelemetryImpl is the real implementation of BackupTelemetry.
type backupTelemetryImpl struct {
	mgr *Manager
}

func (t *backupTelemetryImpl) BackupCreated(_ context.Context, backup *dbpreview.Backup, cluster *dbpreview.DocumentDB, backupType string) {
	backupID := GetResourceTelemetryID(backup)
	clusterID := ""
	if cluster != nil {
		clusterID = GetResourceTelemetryID(cluster)
	}

	retentionDays := 30 // default
	if backup.Spec.RetentionDays != nil && *backup.Spec.RetentionDays > 0 {
		retentionDays = *backup.Spec.RetentionDays
	} else if cluster != nil && cluster.Spec.Backup != nil && cluster.Spec.Backup.RetentionDays > 0 {
		retentionDays = cluster.Spec.Backup.RetentionDays
	}

	isPrimary := true
	if cluster != nil && cluster.Spec.ClusterReplication != nil {
		isPrimary = cluster.Spec.ClusterReplication.Primary == cluster.Name
	}

	t.mgr.Events.TrackBackupCreated(BackupCreatedEvent{
		BackupID:         backupID,
		ClusterID:        clusterID,
		NamespaceHash:    HashNamespace(backup.Namespace),
		BackupType:       backupType,
		BackupMethod:     "VolumeSnapshot",
		BackupPhase:      "starting",
		RetentionDays:    retentionDays,
		CloudProvider:    string(t.mgr.operatorCtx.CloudProvider),
		IsPrimaryCluster: isPrimary,
	})
}

func (t *backupTelemetryImpl) BackupExpired(ctx context.Context, backup *dbpreview.Backup, k8sClient client.Client) {
	backupID := GetResourceTelemetryID(backup)

	clusterID := ""
	if backup.Spec.Cluster.Name != "" {
		cluster := &dbpreview.DocumentDB{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: backup.Spec.Cluster.Name, Namespace: backup.Namespace}, cluster); err == nil {
			clusterID = GetResourceTelemetryID(cluster)
		}
	}

	actualAgeDays := 0
	if backup.CreationTimestamp.Time.Year() > 1 {
		actualAgeDays = int(time.Since(backup.CreationTimestamp.Time).Hours() / 24)
	}

	retentionDays := 0
	if backup.Spec.RetentionDays != nil {
		retentionDays = *backup.Spec.RetentionDays
	}

	t.mgr.Events.TrackBackupExpired(BackupExpiredEvent{
		BackupID:      backupID,
		ClusterID:     clusterID,
		RetentionDays: retentionDays,
		ActualAgeDays: actualAgeDays,
	})
}

func (t *backupTelemetryImpl) BackupDeleted(_ context.Context, backup *dbpreview.Backup, reason string) {
	backupID := GetResourceTelemetryID(backup)

	ageDays := 0
	if backup.CreationTimestamp.Time.Year() > 1 {
		ageDays = int(time.Since(backup.CreationTimestamp.Time).Hours() / 24)
	}

	t.mgr.Events.TrackBackupDeleted(BackupDeletedEvent{
		BackupID:       backupID,
		DeletionReason: reason,
		BackupAgeDays:  ageDays,
	})
}

// scheduledBackupTelemetryImpl is the real implementation of ScheduledBackupTelemetry.
type scheduledBackupTelemetryImpl struct {
	mgr *Manager
}

func (t *scheduledBackupTelemetryImpl) ScheduledBackupCreated(_ context.Context, sb *dbpreview.ScheduledBackup, cluster *dbpreview.DocumentDB) {
	sbID := GetResourceTelemetryID(sb)
	clusterID := ""
	if cluster != nil {
		clusterID = GetResourceTelemetryID(cluster)
	}

	retentionDays := 0
	if sb.Spec.RetentionDays != nil {
		retentionDays = *sb.Spec.RetentionDays
	}

	t.mgr.Events.TrackScheduledBackupCreated(ScheduledBackupCreatedEvent{
		ScheduledBackupID: sbID,
		ClusterID:         clusterID,
		ScheduleFrequency: string(CategorizeScheduleFrequency(sb.Spec.Schedule)),
		RetentionDays:     retentionDays,
	})
}

// NewDocumentDBTelemetry creates the real DocumentDB telemetry adapter.
func NewDocumentDBTelemetry(mgr *Manager) DocumentDBTelemetry {
	if mgr == nil || !mgr.IsEnabled() {
		return NoopDocumentDBTelemetry{}
	}
	return &documentDBTelemetryImpl{mgr: mgr}
}

// NewBackupTelemetry creates the real Backup telemetry adapter.
func NewBackupTelemetry(mgr *Manager) BackupTelemetry {
	if mgr == nil || !mgr.IsEnabled() {
		return NoopBackupTelemetry{}
	}
	return &backupTelemetryImpl{mgr: mgr}
}

// NewScheduledBackupTelemetry creates the real ScheduledBackup telemetry adapter.
func NewScheduledBackupTelemetry(mgr *Manager) ScheduledBackupTelemetry {
	if mgr == nil || !mgr.IsEnabled() {
		return NoopScheduledBackupTelemetry{}
	}
	return &scheduledBackupTelemetryImpl{mgr: mgr}
}
