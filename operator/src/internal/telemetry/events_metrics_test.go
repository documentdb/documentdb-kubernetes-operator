// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"testing"
	"time"
)

func newDisabledClient() *TelemetryClient {
	ctx := &OperatorContext{OperatorVersion: "test"}
	return NewTelemetryClient(ctx)
}

func TestNewEventTracker(t *testing.T) {
	client := newDisabledClient()
	tracker := NewEventTracker(client)
	if tracker == nil {
		t.Fatal("expected non-nil EventTracker")
	}

	// All Track methods should not panic on disabled client
	tracker.TrackOperatorStartup(OperatorStartupEvent{
		OperatorVersion:   "1.0.0",
		KubernetesVersion: "v1.30.0",
		CloudProvider:     "aks",
		StartupTimestamp:  time.Now(),
	})
	tracker.TrackClusterCreated(ClusterCreatedEvent{ClusterID: "test"})
	tracker.TrackClusterUpdated(ClusterUpdatedEvent{ClusterID: "test"})
	tracker.TrackClusterDeleted(ClusterDeletedEvent{ClusterID: "test"})
	tracker.TrackBackupCreated(BackupCreatedEvent{BackupID: "test"})
	tracker.TrackBackupDeleted(BackupDeletedEvent{BackupID: "test"})
	tracker.TrackScheduledBackupCreated(ScheduledBackupCreatedEvent{ScheduledBackupID: "test"})
	tracker.TrackClusterRestored(ClusterRestoredEvent{NewClusterID: "test"})
	tracker.TrackFailoverOccurred(FailoverOccurredEvent{ClusterID: "test"})
	tracker.TrackReconciliationError(ReconciliationErrorEvent{ResourceType: "test"})
	tracker.TrackVolumeSnapshotError(VolumeSnapshotErrorEvent{BackupID: "test"})
	tracker.TrackCNPGIntegrationError(CNPGIntegrationErrorEvent{ClusterID: "test"})
	tracker.TrackBackupExpired(BackupExpiredEvent{BackupID: "test"})
}

func TestNewMetricsTracker(t *testing.T) {
	client := newDisabledClient()
	tracker := NewMetricsTracker(client)
	if tracker == nil {
		t.Fatal("expected non-nil MetricsTracker")
	}

	// All Track methods should not panic on disabled client
	tracker.TrackOperatorHealthStatus(true, "pod-1", "hash-1")
	tracker.TrackActiveClustersCount(5, "hash", "aks", "prod")
	tracker.TrackClusterConfiguration(ClusterConfigurationMetric{ClusterID: "test"})
	tracker.TrackReplicationEnabled(true, ReplicationEnabledMetric{ClusterID: "test"})
	tracker.TrackActiveBackupsCount(3, "hash", "cluster-1", "on-demand")
	tracker.TrackScheduledBackupsCount(2)
	tracker.TrackReplicationLag(ReplicationLagMetric{ClusterID: "test", AvgLagBytes: 100})
	tracker.TrackReplicationStatus(true, "cluster-1", "cluster-2", "hash")
	tracker.TrackTLSEnabledCount(1, "cert-manager", true, false)
	tracker.TrackServiceExposureCount(2, "LoadBalancer", "aks")
	tracker.TrackPluginUsageCount(true, false)
	tracker.TrackReconciliationDuration(ReconciliationDurationMetric{
		ResourceType: "DocumentDB", Operation: "reconcile", Status: "success", DurationSeconds: 1.5,
	})
	tracker.TrackAPICallDuration(0.5, "get", "Cluster", "success")
	tracker.TrackBackupRetentionDays(30, "cluster-1", "cluster")
}
