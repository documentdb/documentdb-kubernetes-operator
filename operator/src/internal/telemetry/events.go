// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"time"
)

// EventTracker provides high-level methods for tracking telemetry events.
type EventTracker struct {
	client      *TelemetryClient
	guidManager *GUIDManager
}

// NewEventTracker creates a new EventTracker.
func NewEventTracker(client *TelemetryClient, guidManager *GUIDManager) *EventTracker {
	return &EventTracker{
		client:      client,
		guidManager: guidManager,
	}
}

// TrackOperatorStartup tracks the OperatorStartup event.
func (t *EventTracker) TrackOperatorStartup(event OperatorStartupEvent) {
	t.client.TrackEvent("OperatorStartup", map[string]interface{}{
		"operator_version":   event.OperatorVersion,
		"kubernetes_version": event.KubernetesVersion,
		"cloud_provider":     event.CloudProvider,
		"startup_timestamp":  event.StartupTimestamp.Format(time.RFC3339),
		"restart_count":      event.RestartCount,
		"helm_chart_version": event.HelmChartVersion,
	})
}

// TrackClusterCreated tracks the ClusterCreated event.
func (t *EventTracker) TrackClusterCreated(event ClusterCreatedEvent) {
	t.client.TrackEvent("ClusterCreated", map[string]interface{}{
		"cluster_id":                event.ClusterID,
		"namespace_hash":            event.NamespaceHash,
		"creation_duration_seconds": event.CreationDurationSeconds,
		"node_count":                event.NodeCount,
		"instances_per_node":        event.InstancesPerNode,
		"storage_size":              event.StorageSize,
		"cloud_provider":            event.CloudProvider,
		"tls_enabled":               event.TLSEnabled,
		"bootstrap_type":            event.BootstrapType,
		"sidecar_injector_plugin":   event.SidecarInjectorPlugin,
		"service_type":              event.ServiceType,
	})
}

// TrackClusterUpdated tracks the ClusterUpdated event.
func (t *EventTracker) TrackClusterUpdated(event ClusterUpdatedEvent) {
	t.client.TrackEvent("ClusterUpdated", map[string]interface{}{
		"cluster_id":              event.ClusterID,
		"namespace_hash":          event.NamespaceHash,
		"update_type":             event.UpdateType,
		"update_duration_seconds": event.UpdateDurationSeconds,
	})
}

// TrackClusterDeleted tracks the ClusterDeleted event.
func (t *EventTracker) TrackClusterDeleted(event ClusterDeletedEvent) {
	t.client.TrackEvent("ClusterDeleted", map[string]interface{}{
		"cluster_id":                event.ClusterID,
		"namespace_hash":            event.NamespaceHash,
		"deletion_duration_seconds": event.DeletionDurationSeconds,
		"cluster_age_days":          event.ClusterAgeDays,
		"backup_count":              event.BackupCount,
	})
}

// TrackBackupCreated tracks the BackupCreated event.
func (t *EventTracker) TrackBackupCreated(event BackupCreatedEvent) {
	t.client.TrackEvent("BackupCreated", map[string]interface{}{
		"backup_id":               event.BackupID,
		"cluster_id":              event.ClusterID,
		"namespace_hash":          event.NamespaceHash,
		"backup_type":             event.BackupType,
		"backup_method":           event.BackupMethod,
		"backup_size_bytes":       event.BackupSizeBytes,
		"backup_duration_seconds": event.BackupDurationSeconds,
		"retention_days":          event.RetentionDays,
		"backup_phase":            event.BackupPhase,
		"cloud_provider":          event.CloudProvider,
		"is_primary_cluster":      event.IsPrimaryCluster,
	})
}

// TrackBackupDeleted tracks the BackupDeleted event.
func (t *EventTracker) TrackBackupDeleted(event BackupDeletedEvent) {
	t.client.TrackEvent("BackupDeleted", map[string]interface{}{
		"backup_id":       event.BackupID,
		"deletion_reason": event.DeletionReason,
		"backup_age_days": event.BackupAgeDays,
	})
}

// TrackScheduledBackupCreated tracks the ScheduledBackupCreated event.
func (t *EventTracker) TrackScheduledBackupCreated(event ScheduledBackupCreatedEvent) {
	t.client.TrackEvent("ScheduledBackupCreated", map[string]interface{}{
		"scheduled_backup_id": event.ScheduledBackupID,
		"cluster_id":          event.ClusterID,
		"schedule_frequency":  event.ScheduleFrequency,
		"retention_days":      event.RetentionDays,
	})
}

// TrackClusterRestored tracks the ClusterRestored event.
func (t *EventTracker) TrackClusterRestored(event ClusterRestoredEvent) {
	t.client.TrackEvent("ClusterRestored", map[string]interface{}{
		"new_cluster_id":           event.NewClusterID,
		"source_backup_id":         event.SourceBackupID,
		"namespace_hash":           event.NamespaceHash,
		"restore_duration_seconds": event.RestoreDurationSeconds,
		"backup_age_hours":         event.BackupAgeHours,
		"restore_phase":            event.RestorePhase,
	})
}

// TrackFailoverOccurred tracks the FailoverOccurred event.
func (t *EventTracker) TrackFailoverOccurred(event FailoverOccurredEvent) {
	t.client.TrackEvent("FailoverOccurred", map[string]interface{}{
		"cluster_id":                event.ClusterID,
		"namespace_hash":            event.NamespaceHash,
		"failover_type":             event.FailoverType,
		"old_primary_index":         event.OldPrimaryIndex,
		"new_primary_index":         event.NewPrimaryIndex,
		"failover_duration_seconds": event.FailoverDurationSeconds,
		"downtime_seconds":          event.DowntimeSeconds,
		"replication_lag_bytes":     event.ReplicationLagBytes,
		"trigger_reason":            event.TriggerReason,
	})
}

// TrackReconciliationError tracks the ReconciliationError event.
func (t *EventTracker) TrackReconciliationError(event ReconciliationErrorEvent) {
	t.client.TrackEvent("ReconciliationError", map[string]interface{}{
		"resource_type":     event.ResourceType,
		"resource_id":       event.ResourceID,
		"namespace_hash":    event.NamespaceHash,
		"error_type":        event.ErrorType,
		"error_message":     event.ErrorMessage,
		"error_code":        event.ErrorCode,
		"retry_count":       event.RetryCount,
		"resolution_status": event.ResolutionStatus,
	})
}

// TrackVolumeSnapshotError tracks the VolumeSnapshotError event.
func (t *EventTracker) TrackVolumeSnapshotError(event VolumeSnapshotErrorEvent) {
	t.client.TrackEvent("VolumeSnapshotError", map[string]interface{}{
		"backup_id":       event.BackupID,
		"cluster_id":      event.ClusterID,
		"error_type":      event.ErrorType,
		"csi_driver_type": event.CSIDriverType,
		"cloud_provider":  event.CloudProvider,
	})
}

// TrackCNPGIntegrationError tracks the CNPGIntegrationError event.
func (t *EventTracker) TrackCNPGIntegrationError(event CNPGIntegrationErrorEvent) {
	t.client.TrackEvent("CNPGIntegrationError", map[string]interface{}{
		"cluster_id":         event.ClusterID,
		"cnpg_resource_type": event.CNPGResourceType,
		"error_category":     event.ErrorCategory,
		"operation":          event.Operation,
	})
}

// TrackBackupExpired tracks the BackupExpired event.
func (t *EventTracker) TrackBackupExpired(event BackupExpiredEvent) {
	t.client.TrackEvent("BackupExpired", map[string]interface{}{
		"backup_id":       event.BackupID,
		"cluster_id":      event.ClusterID,
		"retention_days":  event.RetentionDays,
		"actual_age_days": event.ActualAgeDays,
	})
}
