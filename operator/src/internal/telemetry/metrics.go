// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

// MetricsTracker provides high-level methods for tracking telemetry metrics.
type MetricsTracker struct {
	client *TelemetryClient
}

// NewMetricsTracker creates a new MetricsTracker.
func NewMetricsTracker(client *TelemetryClient) *MetricsTracker {
	return &MetricsTracker{
		client: client,
	}
}

// TrackOperatorHealthStatus tracks the operator health status metric.
func (m *MetricsTracker) TrackOperatorHealthStatus(healthy bool, podName, namespaceHash string) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	m.client.TrackMetric("operator.health.status", value, map[string]interface{}{
		"pod_name":       podName,
		"namespace_hash": namespaceHash,
	})
}

// TrackActiveClustersCount tracks the number of active DocumentDB clusters.
func (m *MetricsTracker) TrackActiveClustersCount(count int, namespaceHash, cloudProvider, environment string) {
	m.client.TrackMetric("documentdb.clusters.active.count", float64(count), map[string]interface{}{
		"namespace_hash": namespaceHash,
		"cloud_provider": cloudProvider,
		"environment":    environment,
	})
}

// TrackClusterConfiguration tracks cluster configuration metrics.
func (m *MetricsTracker) TrackClusterConfiguration(metric ClusterConfigurationMetric) {
	m.client.TrackMetric("documentdb.cluster.configuration", 1, map[string]interface{}{
		"cluster_id":         metric.ClusterID,
		"namespace_hash":     metric.NamespaceHash,
		"node_count":         metric.NodeCount,
		"instances_per_node": metric.InstancesPerNode,
		"total_instances":    metric.TotalInstances,
		"pvc_size_category":  string(metric.PVCSizeCategory),
		"documentdb_version": metric.DocumentDBVersion,
	})
}

// TrackReplicationEnabled tracks replication configuration metrics.
func (m *MetricsTracker) TrackReplicationEnabled(enabled bool, metric ReplicationEnabledMetric) {
	value := 0.0
	if enabled {
		value = 1.0
	}
	m.client.TrackMetric("documentdb.cluster.replication.enabled", value, map[string]interface{}{
		"cluster_id":                      metric.ClusterID,
		"cross_cloud_networking_strategy": metric.CrossCloudNetworkingStrategy,
		"primary_cluster_id":              metric.PrimaryClusterID,
		"replica_count":                   metric.ReplicaCount,
		"high_availability":               metric.HighAvailability,
		"participating_cluster_count":     metric.ParticipatingClusterCount,
		"environments":                    metric.Environments,
	})
}

// TrackActiveBackupsCount tracks the number of active backups.
func (m *MetricsTracker) TrackActiveBackupsCount(count int, namespaceHash, clusterID, backupType string) {
	m.client.TrackMetric("documentdb.backups.active.count", float64(count), map[string]interface{}{
		"namespace_hash": namespaceHash,
		"cluster_id":     clusterID,
		"backup_type":    backupType,
	})
}

// TrackScheduledBackupsCount tracks the number of active scheduled backup jobs.
func (m *MetricsTracker) TrackScheduledBackupsCount(count int) {
	m.client.TrackMetric("documentdb.scheduled_backups.active.count", float64(count), nil)
}

// TrackReplicationLag tracks replication lag metrics.
func (m *MetricsTracker) TrackReplicationLag(metric ReplicationLagMetric) {
	m.client.TrackMetric("documentdb.replication.lag.bytes", float64(metric.AvgLagBytes), map[string]interface{}{
		"cluster_id":         metric.ClusterID,
		"replica_cluster_id": metric.ReplicaClusterID,
		"namespace_hash":     metric.NamespaceHash,
		"min_lag_bytes":      metric.MinLagBytes,
		"max_lag_bytes":      metric.MaxLagBytes,
		"avg_lag_bytes":      metric.AvgLagBytes,
	})
}

// TrackReplicationStatus tracks replication health status.
func (m *MetricsTracker) TrackReplicationStatus(healthy bool, clusterID, replicaClusterID, namespaceHash string) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	m.client.TrackMetric("documentdb.replication.status", value, map[string]interface{}{
		"cluster_id":         clusterID,
		"replica_cluster_id": replicaClusterID,
		"namespace_hash":     namespaceHash,
	})
}

// TrackTLSEnabledCount tracks the number of clusters with TLS enabled.
func (m *MetricsTracker) TrackTLSEnabledCount(count int, tlsMode string, serverEnabled, clientEnabled bool) {
	m.client.TrackMetric("documentdb.tls.enabled.count", float64(count), map[string]interface{}{
		"tls_mode":           tlsMode,
		"server_tls_enabled": serverEnabled,
		"client_tls_enabled": clientEnabled,
	})
}

// TrackServiceExposureCount tracks service exposure methods.
func (m *MetricsTracker) TrackServiceExposureCount(count int, serviceType, cloudProvider string) {
	m.client.TrackMetric("documentdb.service_exposure.count", float64(count), map[string]interface{}{
		"service_type":   serviceType,
		"cloud_provider": cloudProvider,
	})
}

// TrackPluginUsageCount tracks plugin usage.
func (m *MetricsTracker) TrackPluginUsageCount(sidecarInjectorEnabled, walReplicaEnabled bool) {
	m.client.TrackMetric("documentdb.plugin.usage.count", 1, map[string]interface{}{
		"sidecar_injector_plugin_enabled": sidecarInjectorEnabled,
		"wal_replica_plugin_enabled":      walReplicaEnabled,
	})
}

// TrackReconciliationDuration tracks reconciliation performance.
func (m *MetricsTracker) TrackReconciliationDuration(metric ReconciliationDurationMetric) {
	m.client.TrackMetric("documentdb.reconciliation.duration.seconds", metric.DurationSeconds, map[string]interface{}{
		"resource_type": metric.ResourceType,
		"operation":     metric.Operation,
		"status":        metric.Status,
	})
}

// TrackAPICallDuration tracks Kubernetes API call latency.
func (m *MetricsTracker) TrackAPICallDuration(durationSeconds float64, operation, resourceType, result string) {
	m.client.TrackMetric("documentdb.api.duration.seconds", durationSeconds, map[string]interface{}{
		"operation":     operation,
		"resource_type": resourceType,
		"result":        result,
	})
}

// TrackBackupRetentionDays tracks backup retention policy.
func (m *MetricsTracker) TrackBackupRetentionDays(retentionDays int, clusterID, policyLevel string) {
	m.client.TrackMetric("documentdb.backup.retention.days", float64(retentionDays), map[string]interface{}{
		"cluster_id":   clusterID,
		"policy_level": policyLevel,
	})
}
