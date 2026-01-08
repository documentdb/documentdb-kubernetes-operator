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
  - `namespace`: Kubernetes namespace
  - `cloud_provider`: `aks`, `eks`, `gke`
  - `environment`: `aks`, `eks`, `gke` (from spec.environment)

### Cluster Size Metrics
- **Metric**: `documentdb.cluster.configuration`
- **Properties per cluster**:
  - `cluster_name`: Name of the DocumentDB cluster
  - `namespace`: Kubernetes namespace
  - `node_count`: Number of nodes (currently always 1)
  - `instances_per_node`: Number of instances per node (1-3)
  - `total_instances`: node_count × instances_per_node
  - `storage_class`: Storage class name
  - `pvc_size`: Persistent volume claim size (e.g., "10Gi")
  - `documentdb_version`: Version of DocumentDB components
  - `postgresql_image`: Container image for PostgreSQL
  - `gateway_image`: Container image for Gateway sidecar

### Multi-Region Configuration
- **Metric**: `documentdb.cluster.replication.enabled`
- **Value**: `1` (enabled) or `0` (disabled)
- **Properties**:
  - `cluster_name`: Name of the DocumentDB cluster
  - `cross_cloud_networking_strategy`: `AzureFleet`, `Istio`, `None`
  - `primary_cluster`: Name of the primary cluster
  - `replica_count`: Number of clusters in replication list
  - `high_availability`: Boolean indicating HA replicas on primary
  - `participating_clusters`: Comma-separated list of cluster names
  - `environments`: Comma-separated list of environments in replication

---

## 3. Cluster Lifecycle Operations

### Create Operations
- **Event**: `ClusterCreated`
- **Properties**:
  - `cluster_name`: Name of the cluster
  - `namespace`: Kubernetes namespace
  - `creation_duration_seconds`: Time to create cluster
  - `node_count`: Number of nodes
  - `instances_per_node`: Instances per node
  - `storage_size`: PVC size
  - `cloud_provider`: Deployment environment
  - `tls_enabled`: Boolean for TLS configuration
  - `bootstrap_type`: `new` or `recovery` (if recovery, from backup)
  - `sidecar_injector_plugin`: Plugin name if configured
  - `service_type`: `LoadBalancer` or `ClusterIP`

### Update Operations
- **Event**: `ClusterUpdated`
- **Properties**:
  - `cluster_name`: Name of the cluster
  - `namespace`: Kubernetes namespace
  - `update_type`: `scale`, `version`, `configuration`, `storage`
  - `previous_value`: Previous configuration value
  - `new_value`: New configuration value
  - `update_duration_seconds`: Time to apply update

### Delete Operations
- **Event**: `ClusterDeleted`
- **Properties**:
  - `cluster_name`: Name of the cluster
  - `namespace`: Kubernetes namespace
  - `deletion_duration_seconds`: Time to delete cluster
  - `cluster_age_days`: Age of cluster at deletion
  - `backup_count`: Number of backups associated with the cluster

---

## 4. Backup & Restore Operations

### Backup Operations
- **Event**: `BackupCreated`
- **Properties**:
  - `backup_name`: Name of the backup
  - `cluster_name`: Source cluster name
  - `namespace`: Kubernetes namespace
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
  - `backup_name`: Name of the backup
  - `deletion_reason`: `expired`, `manual`, `cluster-deleted`
  - `backup_age_days`: Age of backup at deletion

- **Metric**: `documentdb.backups.active.count`
- **Description**: Total number of active backups
- **Dimensions**: `namespace`, `cluster_name`, `backup_type`

### Scheduled Backup Operations
- **Event**: `ScheduledBackupCreated`
- **Properties**:
  - `scheduled_backup_name`: Name of the scheduled backup
  - `cluster_name`: Target cluster name
  - `schedule`: Cron expression
  - `retention_days`: Retention policy

- **Metric**: `documentdb.scheduled_backups.active.count`
- **Description**: Number of active scheduled backup jobs

### Restore Operations
- **Event**: `ClusterRestored`
- **Properties**:
  - `new_cluster_name`: Name of the restored cluster
  - `source_backup_name`: Backup used for recovery
  - `namespace`: Kubernetes namespace
  - `restore_duration_seconds`: Time to restore from backup
  - `backup_age_hours`: Age of backup at restore time
  - `restore_phase`: `starting`, `running`, `completed`, `failed`

---

## 5. Failover & High Availability Metrics

### Failover Events
- **Event**: `FailoverOccurred`
- **Properties**:
  - `cluster_name`: Name of the cluster
  - `namespace`: Kubernetes namespace
  - `failover_type`: `automatic`, `manual`, `switchover`
  - `old_primary`: Previous primary instance
  - `new_primary`: New primary instance
  - `failover_duration_seconds`: Time to complete failover
  - `downtime_seconds`: Observed downtime during failover
  - `replication_lag_bytes`: Replication lag before failover
  - `trigger_reason`: `node-failure`, `pod-crash`, `manual`, `health-check-failure`

### Replication Health
- **Metric**: `documentdb.replication.lag.bytes`
- **Description**: Replication lag in bytes
- **Dimensions**: `cluster_name`, `replica_cluster`, `namespace`
- **Frequency**: Every 30 seconds

- **Metric**: `documentdb.replication.status`
- **Value**: `1` (healthy) or `0` (unhealthy)
- **Dimensions**: `cluster_name`, `replica_cluster`, `namespace`

---

## 6. Error Tracking

### Reconciliation Errors
- **Event**: `ReconciliationError`
- **Properties**:
  - `resource_type`: `DocumentDB`, `Backup`, `ScheduledBackup`
  - `resource_name`: Name of the resource
  - `namespace`: Kubernetes namespace
  - `error_type`: `cluster-creation`, `backup-failure`, `restore-failure`, `volume-snapshot`, `replication-config`, `tls-cert`
  - `error_message`: Sanitized error message (no PII)
  - `error_code`: Standard error code
  - `retry_count`: Number of retry attempts
  - `resolution_status`: `pending`, `resolved`, `failed`

### Volume Snapshot Errors
- **Event**: `VolumeSnapshotError`
- **Properties**:
  - `backup_name`: Name of the backup
  - `cluster_name`: Source cluster name
  - `error_type`: `snapshot-class-missing`, `driver-unavailable`, `quota-exceeded`, `snapshot-failed`
  - `csi_driver`: CSI driver name (`disk.csi.azure.com`, etc.)
  - `cloud_provider`: Environment

### CNPG Integration Errors
- **Event**: `CNPGIntegrationError`
- **Properties**:
  - `cluster_name`: DocumentDB cluster name
  - `cnpg_resource_type`: `Cluster`, `Backup`, `ScheduledBackup`
  - `error_message`: Error from CNPG operator
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
  - `sidecar_injector_plugin`: Plugin name (if used)
  - `wal_replica_plugin`: Plugin name (if used)

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
- **Dimensions**: `cluster_name`, `policy_level` (`cluster`, `backup`, `scheduled-backup`)

### Expired Backups
- **Event**: `BackupExpired`
- **Properties**:
  - `backup_name`: Name of the expired backup
  - `cluster_name`: Source cluster
  - `retention_days`: Configured retention
  - `actual_age_days`: Actual age at expiration

---

## 10. Deployment Context

### Cluster Environment
- **Properties** (collected once at startup, attached to all events):
  - `kubernetes_distribution`: `aks`, `eks`, `gke`, `openshift`, `other`
  - `kubernetes_version`: K8s version
  - `region`: Cloud region (if detectable)
  - `operator_namespace`: Namespace where operator runs
  - `installation_method`: `helm`, `kubectl`, `operator-sdk`

---

## Data Privacy & Security

- **No PII**: Do not collect usernames, passwords, connection strings, or IP addresses
- **Sanitize errors**: Remove sensitive data from error messages
- **Cluster names**: Use hashed cluster names if privacy required
- **Opt-out**: Provide mechanism to disable telemetry collection

---

## Implementation Notes

1. **Sampling**: Apply sampling for high-frequency metrics (e.g., reconciliation events)
2. **Batching**: Batch events in 30-second windows to reduce API calls
3. **Cardinality**: Monitor dimension cardinality to avoid explosion
4. **Retry logic**: Implement exponential backoff for telemetry submission failures
5. **Local buffering**: Buffer events locally if Application Insights is unreachable
6. **Health endpoint**: Expose `/metrics` endpoint for Prometheus scraping

---

## Revision History

| Date | Version | Changes |
|------|---------|---------|
| 2026-01-08 | 1.0 | Initial specification |
