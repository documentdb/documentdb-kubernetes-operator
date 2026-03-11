// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package telemetry provides Application Insights integration for the DocumentDB Kubernetes Operator.
// It implements telemetry collection as specified in docs/designs/appinsights-metrics.md.
package telemetry

import (
	"time"
)

// TelemetryAnnotations defines the annotation keys used for telemetry correlation.
const (
	// ClusterIDAnnotation is the annotation key for storing auto-generated cluster GUID.
	ClusterIDAnnotation = "telemetry.documentdb.io/cluster-id"
	// BackupIDAnnotation is the annotation key for storing auto-generated backup GUID.
	BackupIDAnnotation = "telemetry.documentdb.io/backup-id"
	// ScheduledBackupIDAnnotation is the annotation key for storing auto-generated scheduled backup GUID.
	ScheduledBackupIDAnnotation = "telemetry.documentdb.io/scheduled-backup-id"
)

// CloudProvider represents the detected cloud environment.
type CloudProvider string

const (
	CloudProviderAKS     CloudProvider = "aks"
	CloudProviderEKS     CloudProvider = "eks"
	CloudProviderGKE     CloudProvider = "gke"
	CloudProviderUnknown CloudProvider = "unknown"
)

// KubernetesDistribution represents the detected Kubernetes distribution.
type KubernetesDistribution string

const (
	DistributionAKS         KubernetesDistribution = "aks"
	DistributionEKS         KubernetesDistribution = "eks"
	DistributionGKE         KubernetesDistribution = "gke"
	DistributionOpenShift   KubernetesDistribution = "openshift"
	DistributionRancher     KubernetesDistribution = "rancher"
	DistributionVMwareTanzu KubernetesDistribution = "vmware-tanzu"
	DistributionOther       KubernetesDistribution = "other"
)

// PVCSizeCategory categorizes PVC sizes without exposing exact values.
type PVCSizeCategory string

const (
	PVCSizeSmall  PVCSizeCategory = "small"  // <50Gi
	PVCSizeMedium PVCSizeCategory = "medium" // 50-200Gi
	PVCSizeLarge  PVCSizeCategory = "large"  // >200Gi
)

// ScheduleFrequency categorizes backup schedule frequency.
type ScheduleFrequency string

const (
	ScheduleFrequencyHourly ScheduleFrequency = "hourly"
	ScheduleFrequencyDaily  ScheduleFrequency = "daily"
	ScheduleFrequencyWeekly ScheduleFrequency = "weekly"
	ScheduleFrequencyCustom ScheduleFrequency = "custom"
)

// OperatorContext contains deployment context collected at startup.
type OperatorContext struct {
	OperatorVersion        string
	KubernetesVersion      string
	KubernetesDistribution KubernetesDistribution
	CloudProvider          CloudProvider
	Region                 string
	OperatorNamespaceHash  string
	InstallationMethod     string
	HelmChartVersion       string
	StartupTimestamp       time.Time
}

// OperatorStartupEvent represents the OperatorStartup telemetry event.
type OperatorStartupEvent struct {
	OperatorVersion    string    `json:"operator_version"`
	KubernetesVersion  string    `json:"kubernetes_version"`
	CloudProvider      string    `json:"cloud_provider"`
	StartupTimestamp   time.Time `json:"startup_timestamp"`
	RestartCount       int       `json:"restart_count"`
	HelmChartVersion   string    `json:"helm_chart_version,omitempty"`
}

// ClusterCreatedEvent represents the ClusterCreated telemetry event.
type ClusterCreatedEvent struct {
	ClusterID               string  `json:"cluster_id"`
	NamespaceHash           string  `json:"namespace_hash"`
	CreationDurationSeconds float64 `json:"creation_duration_seconds"`
	NodeCount               int     `json:"node_count"`
	InstancesPerNode        int     `json:"instances_per_node"`
	StorageSize             string  `json:"storage_size"`
	CloudProvider           string  `json:"cloud_provider"`
	TLSEnabled              bool    `json:"tls_enabled"`
	BootstrapType           string  `json:"bootstrap_type"`
	SidecarInjectorPlugin   bool    `json:"sidecar_injector_plugin"`
	ServiceType             string  `json:"service_type"`
}

// ClusterUpdatedEvent represents the ClusterUpdated telemetry event.
type ClusterUpdatedEvent struct {
	ClusterID             string  `json:"cluster_id"`
	NamespaceHash         string  `json:"namespace_hash"`
	UpdateType            string  `json:"update_type"`
	UpdateDurationSeconds float64 `json:"update_duration_seconds"`
}

// ClusterDeletedEvent represents the ClusterDeleted telemetry event.
type ClusterDeletedEvent struct {
	ClusterID               string  `json:"cluster_id"`
	NamespaceHash           string  `json:"namespace_hash"`
	DeletionDurationSeconds float64 `json:"deletion_duration_seconds"`
	ClusterAgeDays          int     `json:"cluster_age_days"`
	BackupCount             int     `json:"backup_count"`
}

// BackupCreatedEvent represents the BackupCreated telemetry event.
type BackupCreatedEvent struct {
	BackupID              string  `json:"backup_id"`
	ClusterID             string  `json:"cluster_id"`
	NamespaceHash         string  `json:"namespace_hash"`
	BackupType            string  `json:"backup_type"` // on-demand or scheduled
	BackupMethod          string  `json:"backup_method"`
	BackupSizeBytes       int64   `json:"backup_size_bytes"`
	BackupDurationSeconds float64 `json:"backup_duration_seconds"`
	RetentionDays         int     `json:"retention_days"`
	BackupPhase           string  `json:"backup_phase"`
	CloudProvider         string  `json:"cloud_provider"`
	IsPrimaryCluster      bool    `json:"is_primary_cluster"`
}

// BackupDeletedEvent represents the BackupDeleted telemetry event.
type BackupDeletedEvent struct {
	BackupID       string `json:"backup_id"`
	DeletionReason string `json:"deletion_reason"` // expired, manual, cluster-deleted
	BackupAgeDays  int    `json:"backup_age_days"`
}

// ScheduledBackupCreatedEvent represents the ScheduledBackupCreated telemetry event.
type ScheduledBackupCreatedEvent struct {
	ScheduledBackupID string `json:"scheduled_backup_id"`
	ClusterID         string `json:"cluster_id"`
	ScheduleFrequency string `json:"schedule_frequency"`
	RetentionDays     int    `json:"retention_days"`
}

// ClusterRestoredEvent represents the ClusterRestored telemetry event.
type ClusterRestoredEvent struct {
	NewClusterID           string  `json:"new_cluster_id"`
	SourceBackupID         string  `json:"source_backup_id"`
	NamespaceHash          string  `json:"namespace_hash"`
	RestoreDurationSeconds float64 `json:"restore_duration_seconds"`
	BackupAgeHours         float64 `json:"backup_age_hours"`
	RestorePhase           string  `json:"restore_phase"`
}

// FailoverOccurredEvent represents the FailoverOccurred telemetry event.
type FailoverOccurredEvent struct {
	ClusterID               string  `json:"cluster_id"`
	NamespaceHash           string  `json:"namespace_hash"`
	FailoverType            string  `json:"failover_type"` // automatic, manual, switchover
	OldPrimaryIndex         int     `json:"old_primary_index"`
	NewPrimaryIndex         int     `json:"new_primary_index"`
	FailoverDurationSeconds float64 `json:"failover_duration_seconds"`
	DowntimeSeconds         float64 `json:"downtime_seconds"`
	ReplicationLagBytes     int64   `json:"replication_lag_bytes"`
	TriggerReason           string  `json:"trigger_reason"`
}

// ReconciliationErrorEvent represents the ReconciliationError telemetry event.
type ReconciliationErrorEvent struct {
	ResourceType     string `json:"resource_type"` // DocumentDB, Backup, ScheduledBackup
	ResourceID       string `json:"resource_id"`
	NamespaceHash    string `json:"namespace_hash"`
	ErrorType        string `json:"error_type"`
	ErrorMessage     string `json:"error_message"` // Sanitized, no PII
	ErrorCode        string `json:"error_code"`
	RetryCount       int    `json:"retry_count"`
	ResolutionStatus string `json:"resolution_status"` // pending, resolved, failed
}

// VolumeSnapshotErrorEvent represents the VolumeSnapshotError telemetry event.
type VolumeSnapshotErrorEvent struct {
	BackupID      string `json:"backup_id"`
	ClusterID     string `json:"cluster_id"`
	ErrorType     string `json:"error_type"`
	CSIDriverType string `json:"csi_driver_type"`
	CloudProvider string `json:"cloud_provider"`
}

// CNPGIntegrationErrorEvent represents the CNPGIntegrationError telemetry event.
type CNPGIntegrationErrorEvent struct {
	ClusterID        string `json:"cluster_id"`
	CNPGResourceType string `json:"cnpg_resource_type"`
	ErrorCategory    string `json:"error_category"`
	Operation        string `json:"operation"`
}

// BackupExpiredEvent represents the BackupExpired telemetry event.
type BackupExpiredEvent struct {
	BackupID      string `json:"backup_id"`
	ClusterID     string `json:"cluster_id"`
	RetentionDays int    `json:"retention_days"`
	ActualAgeDays int    `json:"actual_age_days"`
}

// ClusterConfigurationMetric represents cluster configuration metrics.
type ClusterConfigurationMetric struct {
	ClusterID        string          `json:"cluster_id"`
	NamespaceHash    string          `json:"namespace_hash"`
	NodeCount        int             `json:"node_count"`
	InstancesPerNode int             `json:"instances_per_node"`
	TotalInstances   int             `json:"total_instances"`
	PVCSizeCategory  PVCSizeCategory `json:"pvc_size_category"`
	DocumentDBVersion string         `json:"documentdb_version"`
}

// ReplicationEnabledMetric represents replication configuration metrics.
type ReplicationEnabledMetric struct {
	ClusterID                    string `json:"cluster_id"`
	CrossCloudNetworkingStrategy string `json:"cross_cloud_networking_strategy"`
	PrimaryClusterID             string `json:"primary_cluster_id"`
	ReplicaCount                 int    `json:"replica_count"`
	HighAvailability             bool   `json:"high_availability"`
	ParticipatingClusterCount    int    `json:"participating_cluster_count"`
	Environments                 string `json:"environments"`
}

// ReplicationLagMetric represents replication lag metrics (aggregated).
type ReplicationLagMetric struct {
	ClusterID        string `json:"cluster_id"`
	ReplicaClusterID string `json:"replica_cluster_id"`
	NamespaceHash    string `json:"namespace_hash"`
	MinLagBytes      int64  `json:"min_lag_bytes"`
	MaxLagBytes      int64  `json:"max_lag_bytes"`
	AvgLagBytes      int64  `json:"avg_lag_bytes"`
}

// ReconciliationDurationMetric represents reconciliation performance metrics.
type ReconciliationDurationMetric struct {
	ResourceType    string  `json:"resource_type"`
	Operation       string  `json:"operation"`
	Status          string  `json:"status"`
	DurationSeconds float64 `json:"duration_seconds"`
}
