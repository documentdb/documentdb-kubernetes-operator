# Application Insights Telemetry Collection Specification

## Overview
This document specifies all telemetry data points to be collected by Application Insights for the DocumentDB Kubernetes Operator. These metrics provide operational insights, usage patterns, and error tracking for operator deployments.

---

## 1. Operator Lifecycle Metrics

### Operator Startup Events
- **Event**: `OperatorStartup`
- **Properties**:
  - `operator_version`: Semantic version of the operator
  - `kubernetes_version`: K8s cluster version
  - `cloud_provider`: Detected environment (`aks`, `eks`, `gke`, `unknown`)
  - `startup_timestamp`: ISO 8601 timestamp
  - `restart_count`: Number of restarts in the last hour
  - `helm_chart_version`: Version of the Helm chart used (if applicable)

### Operator Health Checks
- **Metric**: `operator.health.status`
- **Value**: `1` (healthy) or `0` (unhealthy)
- **Frequency**: Every 60 seconds
- **Dimensions**: `pod_name`, `namespace`

---

## 2. Cluster Management Metrics

### Cluster Count & Configuration
- **Metric**: `documentdb.clusters.active.count`
- **Description**: Total number of active DocumentDB clusters managed by the operator
- **Dimensions**:
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `cloud_provider`: `aks`, `eks`, `gke`
  - `environment`: `aks`, `eks`, `gke` (from spec.environment)

### Cluster Size Metrics
- **Metric**: `documentdb.cluster.configuration`
- **Properties per cluster**:
  - `cluster_id`: Auto-generated GUID for the DocumentDB cluster (for correlation without PII)
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `node_count`: Number of nodes (currently always 1)
  - `instances_per_node`: Number of instances per node (1-3)
  - `total_instances`: node_count × instances_per_node
  - `pvc_size_category`: PVC size category (`small` <50Gi, `medium` 50-200Gi, `large` >200Gi)
  - `documentdb_version`: Version of DocumentDB components

### Multi-Region Configuration
- **Metric**: `documentdb.cluster.replication.enabled`
- **Value**: `1` (enabled) or `0` (disabled)
- **Properties**:
  - `cluster_id`: Auto-generated GUID for the DocumentDB cluster
  - `cross_cloud_networking_strategy`: `AzureFleet`, `Istio`, `None`
  - `primary_cluster_id`: GUID of the primary cluster
  - `replica_count`: Number of clusters in replication list
  - `high_availability`: Boolean indicating HA replicas on primary
  - `participating_cluster_count`: Number of participating clusters
  - `environments`: Comma-separated list of environments in replication

---

## 3. Cluster Lifecycle Operations

### Create Operations
- **Event**: `ClusterCreated`
- **Properties**:
  - `cluster_id`: Auto-generated GUID for the cluster
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `creation_duration_seconds`: Time to create cluster
  - `node_count`: Number of nodes
  - `instances_per_node`: Instances per node
  - `storage_size`: PVC size
  - `cloud_provider`: Deployment environment
  - `tls_enabled`: Boolean for TLS configuration
  - `bootstrap_type`: `new` or `recovery` (if recovery, from backup)
  - `sidecar_injector_plugin`: Boolean indicating if plugin is configured
  - `service_type`: `LoadBalancer` or `ClusterIP`

### Update Operations
- **Event**: `ClusterUpdated`
- **Properties**:
  - `cluster_id`: Auto-generated GUID for the cluster
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `update_type`: `scale`, `version`, `configuration`, `storage`
  - `update_duration_seconds`: Time to apply update

### Delete Operations
- **Event**: `ClusterDeleted`
- **Properties**:
  - `cluster_id`: Auto-generated GUID for the cluster
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `deletion_duration_seconds`: Time to delete cluster
  - `cluster_age_days`: Age of cluster at deletion
  - `backup_count`: Number of backups associated with the cluster

---

## 4. Backup & Restore Operations

### Backup Operations
- **Event**: `BackupCreated`
- **Properties**:
  - `backup_id`: Auto-generated GUID for the backup
  - `cluster_id`: GUID of the source cluster
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `backup_type`: `on-demand` or `scheduled`
  - `backup_method`: `VolumeSnapshot` (CNPG method)
  - `backup_size_bytes`: Size of the backup
  - `backup_duration_seconds`: Time to complete backup
  - `retention_days`: Configured retention period
  - `backup_phase`: `starting`, `running`, `completed`, `failed`, `skipped`
  - `cloud_provider`: Environment where backup was taken
  - `is_primary_cluster`: Boolean indicating if backup from primary

- **Event**: `BackupDeleted`
- **Properties**:
  - `backup_id`: GUID of the backup
  - `deletion_reason`: `expired`, `manual`, `cluster-deleted`
  - `backup_age_days`: Age of backup at deletion

- **Metric**: `documentdb.backups.active.count`
- **Description**: Total number of active backups
- **Dimensions**: `namespace_hash`, `cluster_id`, `backup_type`

### Scheduled Backup Operations
- **Event**: `ScheduledBackupCreated`
- **Properties**:
  - `scheduled_backup_id`: Auto-generated GUID for the scheduled backup
  - `cluster_id`: GUID of the target cluster
  - `schedule_frequency`: Frequency category (`hourly`, `daily`, `weekly`, `custom`)
  - `retention_days`: Retention policy

- **Metric**: `documentdb.scheduled_backups.active.count`
- **Description**: Number of active scheduled backup jobs

### Restore Operations
- **Event**: `ClusterRestored`
- **Properties**:
  - `new_cluster_id`: Auto-generated GUID for the restored cluster
  - `source_backup_id`: GUID of the backup used for recovery
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `restore_duration_seconds`: Time to restore from backup
  - `backup_age_hours`: Age of backup at restore time
  - `restore_phase`: `starting`, `running`, `completed`, `failed`

---

## 5. Failover & High Availability Metrics

### Failover Events
- **Event**: `FailoverOccurred`
- **Properties**:
  - `cluster_id`: Auto-generated GUID for the cluster
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `failover_type`: `automatic`, `manual`, `switchover`
  - `old_primary_index`: Index of the previous primary instance (e.g., 0, 1, 2)
  - `new_primary_index`: Index of the new primary instance
  - `failover_duration_seconds`: Time to complete failover
  - `downtime_seconds`: Observed downtime during failover
  - `replication_lag_bytes`: Replication lag before failover
  - `trigger_reason`: `node-failure`, `pod-crash`, `manual`, `health-check-failure`

### Replication Health
- **Metric**: `documentdb.replication.lag.bytes`
- **Description**: Replication lag in bytes (aggregated over 2-hour windows)
- **Dimensions**: `cluster_id`, `replica_cluster_id`, `namespace_hash`
- **Statistics**: min, max, avg (reported as tuple)
- **Frequency**: Every 2 hours (aggregated)

- **Metric**: `documentdb.replication.status`
- **Value**: `1` (healthy) or `0` (unhealthy)
- **Dimensions**: `cluster_id`, `replica_cluster_id`, `namespace_hash`

---

## 6. Error Tracking

### Reconciliation Errors
- **Event**: `ReconciliationError`
- **Properties**:
  - `resource_type`: `DocumentDB`, `Backup`, `ScheduledBackup`
  - `resource_id`: Auto-generated GUID of the resource
  - `namespace_hash`: SHA-256 hash of the Kubernetes namespace
  - `error_type`: `cluster-creation`, `backup-failure`, `restore-failure`, `volume-snapshot`, `replication-config`, `tls-cert`
  - `error_message`: Sanitized error message (no PII)
  - `error_code`: Standard error code
  - `retry_count`: Number of retry attempts
  - `resolution_status`: `pending`, `resolved`, `failed`

### Volume Snapshot Errors
- **Event**: `VolumeSnapshotError`
- **Properties**:
  - `backup_id`: GUID of the backup
  - `cluster_id`: GUID of the source cluster
  - `error_type`: `snapshot-class-missing`, `driver-unavailable`, `quota-exceeded`, `snapshot-failed`
  - `csi_driver_type`: CSI driver type (`azure-disk`, `aws-ebs`, `gce-pd`, `other`)
  - `cloud_provider`: Environment

### CNPG Integration Errors
- **Event**: `CNPGIntegrationError`
- **Properties**:
  - `cluster_id`: GUID of the DocumentDB cluster
  - `cnpg_resource_type`: `Cluster`, `Backup`, `ScheduledBackup`
  - `error_category`: Categorized error type (no raw error messages)
  - `operation`: `create`, `update`, `delete`, `status-sync`

---

## 7. Feature Usage Metrics

### TLS Configuration Usage
- **Metric**: `documentdb.tls.enabled.count`
- **Description**: Number of clusters with TLS enabled
- **Properties per cluster**:
  - `tls_mode`: `manual-provided`, `cert-manager`
  - `server_tls_enabled`: Boolean
  - `client_tls_enabled`: Boolean

### Service Exposure Methods
- **Metric**: `documentdb.service_exposure.count`
- **Dimensions**:
  - `service_type`: `LoadBalancer`, `ClusterIP`
  - `cloud_provider`: `aks`, `eks`, `gke`

### Plugin Usage
- **Metric**: `documentdb.plugin.usage.count`
- **Properties**:
  - `sidecar_injector_plugin_enabled`: Boolean indicating if plugin is used
  - `wal_replica_plugin_enabled`: Boolean indicating if plugin is used

---

## 8. Performance & Resource Metrics

### Reconciliation Performance
- **Metric**: `documentdb.reconciliation.duration.seconds`
- **Description**: Time to reconcile resources
- **Dimensions**: `resource_type`, `operation`, `status`
- **Statistics**: p50, p95, p99

### API Call Latency
- **Metric**: `documentdb.api.duration.seconds`
- **Description**: Kubernetes API call duration
- **Dimensions**: `operation`, `resource_type`, `result`

---

## 9. Compliance & Retention Metrics

### Backup Retention Policy
- **Metric**: `documentdb.backup.retention.days`
- **Description**: Configured retention days per cluster
- **Dimensions**: `cluster_id`, `policy_level` (`cluster`, `backup`, `scheduled-backup`)

### Expired Backups
- **Event**: `BackupExpired`
- **Properties**:
  - `backup_id`: GUID of the expired backup
  - `cluster_id`: GUID of the source cluster
  - `retention_days`: Configured retention
  - `actual_age_days`: Actual age at expiration

---

## 10. Deployment Context

### Cluster Environment
- **Properties** (collected once at startup, attached to all events):
  - `kubernetes_distribution`: `aks`, `eks`, `gke`, `openshift`, `rancher`, `vmware-tanzu`, `other`
  - `kubernetes_version`: K8s version
  - `region`: Cloud region (from `topology.kubernetes.io/region` label if available)
  - `operator_namespace_hash`: SHA-256 hash of the namespace where operator runs
  - `installation_method`: `helm`, `kubectl`, `operator-sdk`

---

## Data Privacy & Security

- **No PII**: Do not collect usernames, passwords, connection strings, or IP addresses
- **Resource Identifiers**: Use auto-generated GUIDs for cluster, backup, and resource identification instead of user-provided names
- **Namespace Protection**: Use SHA-256 hashed namespace values to prevent leaking organizational structure
- **Storage Class**: Do not collect storage class names (may contain PII)
- **Sanitize errors**: Remove sensitive data from error messages; use error categories instead of raw messages
- **GUID Correlation**: GUIDs are generated and stored in resource annotations for event correlation
- **Opt-out**: Provide mechanism to disable telemetry collection

---

## Implementation Notes

1. **Sampling**: Apply sampling for high-frequency metrics (e.g., reconciliation events)
2. **Batching**: Batch events in 30-second windows to reduce API calls
3. **Cardinality**: Monitor dimension cardinality to avoid explosion
4. **Retry logic**: Implement exponential backoff for telemetry submission failures
5. **Local buffering**: Buffer events locally if Application Insights is unreachable
6. **GUID Generation**: Generate and persist GUIDs in resource annotations (`telemetry.documentdb.io/cluster-id`) at resource creation time

---

## Revision History

| Date | Version | Changes |
|------|---------|---------|
| 2026-01-08 | 1.0 | Initial specification |
| 2026-01-29 | 1.1 | Address PII concerns: replaced cluster/backup names with GUIDs, hashed namespaces, removed storage class and container image names, categorized errors instead of raw messages, added more kubernetes distributions |
