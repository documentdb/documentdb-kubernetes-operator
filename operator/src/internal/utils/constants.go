// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

const (
	POSTGRES_PORT = "POSTGRES_PORT"
	SIDECAR_PORT  = "SIDECAR_PORT"
	GATEWAY_PORT  = "GATEWAY_PORT"

	// DocumentDB versioning environment variable
	DOCUMENTDB_VERSION_ENV = "DOCUMENTDB_VERSION"

	// DocumentDB image repository
	DOCUMENTDB_IMAGE_REPOSITORY = "ghcr.io/microsoft/documentdb/documentdb-local"

	// DEFAULT_DOCUMENTDB_IMAGE is the extension image used in ImageVolume mode (K8s >= 1.35).
	DEFAULT_DOCUMENTDB_IMAGE = "ghcr.io/guanzhousongmicrosoft/documentdb-pg18:0.110.0"
	// DEFAULT_COMBINED_DOCUMENTDB_IMAGE is the all-in-one image used in combined mode (K8s < 1.35).
	DEFAULT_COMBINED_DOCUMENTDB_IMAGE     = DOCUMENTDB_IMAGE_REPOSITORY + ":16"
	DEFAULT_GATEWAY_IMAGE                 = DOCUMENTDB_IMAGE_REPOSITORY + ":16"
	DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET = "documentdb-credentials"

	// TODO: remove these constants once change stream support is included in the official images.
	CHANGESTREAM_DOCUMENTDB_IMAGE_REPOSITORY = "ghcr.io/wentingwu666666/documentdb-kubernetes-operator"
	CHANGESTREAM_DOCUMENTDB_IMAGE            = CHANGESTREAM_DOCUMENTDB_IMAGE_REPOSITORY + "/documentdb-oss:16-changestream"
	CHANGESTREAM_GATEWAY_IMAGE               = CHANGESTREAM_DOCUMENTDB_IMAGE_REPOSITORY + "/documentdb-gateway:16-changestream"

	LABEL_APP                      = "app"
	LABEL_REPLICA_TYPE             = "replica_type"
	LABEL_ROLE                     = "role"
	LABEL_NODE_INDEX               = "node_index"
	LABEL_SERVICE_TYPE             = "service_type"
	LABEL_REPLICATION_CLUSTER_TYPE = "replication_cluster_type"
	LABEL_DOCUMENTDB_NAME          = "documentdb.io/name"
	LABEL_DOCUMENTDB_COMPONENT     = "documentdb.io/component"
	FLEET_IN_USE_BY_ANNOTATION     = "networking.fleet.azure.com/service-in-use-by"

	DOCUMENTDB_SERVICE_PREFIX = "documentdb-service-"

	DEFAULT_SIDECAR_INJECTOR_PLUGIN = "cnpg-i-sidecar-injector.documentdb.io"

	DEFAULT_WAL_REPLICA_PLUGIN = "cnpg-i-wal-replica.documentdb.io"

	CNPG_DEFAULT_STOP_DELAY = 30

	CNPG_MAX_CLUSTER_NAME_LENGTH = 50

	// Combined image mode constants (K8s < 1.35, no ImageVolume support)
	// The documentdb-local combined image uses UID 105 / GID 108 for the postgres user.
	COMBINED_IMAGE_POSTGRES_UID int64 = 105
	COMBINED_IMAGE_POSTGRES_GID int64 = 108

	// MinK8sMinorVersionForImageVolume is the minimum Kubernetes minor version where
	// ImageVolume is GA (always enabled). K8s 1.35+ has ImageVolume GA.
	MinK8sMinorVersionForImageVolume = 35

	// JSON Patch paths
	JSON_PATCH_PATH_REPLICA_CLUSTER      = "/spec/replica"
	JSON_PATCH_PATH_POSTGRES_CONFIG      = "/spec/postgresql"
	JSON_PATCH_PATH_POSTGRES_CONFIG_SYNC = "/spec/postgresql/synchronous"
	JSON_PATCH_PATH_INSTANCES            = "/spec/instances"
	JSON_PATCH_PATH_PLUGINS              = "/spec/plugins"
	JSON_PATCH_PATH_REPLICATION_SLOTS    = "/spec/replicationSlots"
	JSON_PATCH_PATH_EXTERNAL_CLUSTERS    = "/spec/externalClusters"
	JSON_PATCH_PATH_MANAGED_SERVICES     = "/spec/managed/services/additional"
	JSON_PATCH_PATH_SYNCHRONOUS          = "/spec/postgresql/synchronous"

	// JSON Patch operations
	JSON_PATCH_OP_REPLACE = "replace"
	JSON_PATCH_OP_ADD     = "add"
	JSON_PATCH_OP_REMOVE  = "remove"

	// SQL job resource requirements and container security context
	SQL_JOB_REQUESTS_MEMORY  = "32Mi"
	SQL_JOB_REQUESTS_CPU     = "10m"
	SQL_JOB_LIMITS_MEMORY    = "64Mi"
	SQL_JOB_LIMITS_CPU       = "50m"
	SQL_JOB_LINUX_UID        = 1000
	SQL_JOB_RUN_AS_NON_ROOT  = true
	SQL_JOB_ALLOW_PRIVILEGED = false
)
