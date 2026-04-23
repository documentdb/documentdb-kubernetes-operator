// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

const (
	POSTGRES_PORT = "POSTGRES_PORT"
	SIDECAR_PORT  = "SIDECAR_PORT"
	GATEWAY_PORT  = "GATEWAY_PORT"

	// DocumentDB versioning environment variable
	DOCUMENTDB_VERSION_ENV = "DOCUMENTDB_VERSION"

	// Gateway image pull policy environment variable
	GATEWAY_IMAGE_PULL_POLICY_ENV = "GATEWAY_IMAGE_PULL_POLICY"

	// DocumentDB extension image pull policy environment variable
	DOCUMENTDB_IMAGE_PULL_POLICY_ENV = "DOCUMENTDB_IMAGE_PULL_POLICY"

	// Image repositories for deb-based images (must match build_images.yml naming)
	DOCUMENTDB_EXTENSION_IMAGE_REPO = "ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb"
	GATEWAY_IMAGE_REPO              = "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway"

	// MinK8sMinorVersion is the minimum required Kubernetes minor version.
	// The operator requires K8s 1.35+ for ImageVolume GA support.
	MinK8sMinorVersion = 35

	// DEFAULT_DOCUMENTDB_IMAGE is the extension image used in ImageVolume mode.
	DEFAULT_DOCUMENTDB_IMAGE              = DOCUMENTDB_EXTENSION_IMAGE_REPO + ":0.109.0"
	// NOTE: Keep in sync with operator/cnpg-plugins/sidecar-injector/internal/config/config.go:applyDefaults()
	DEFAULT_GATEWAY_IMAGE                 = GATEWAY_IMAGE_REPO + ":0.109.0"
	DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET = "documentdb-credentials"
	DEFAULT_OTEL_COLLECTOR_IMAGE           = "otel/opentelemetry-collector-contrib:0.149.0"

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

	// SQL job resource requirements and container security context
	SQL_JOB_REQUESTS_MEMORY  = "32Mi"
	SQL_JOB_REQUESTS_CPU     = "10m"
	SQL_JOB_LIMITS_MEMORY    = "64Mi"
	SQL_JOB_LIMITS_CPU       = "50m"
	SQL_JOB_LINUX_UID        = 1000
	SQL_JOB_RUN_AS_NON_ROOT  = true
	SQL_JOB_ALLOW_PRIVILEGED = false
)
