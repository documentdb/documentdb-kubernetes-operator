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

	// IOURING_SECCOMP_PROFILE_ENV overrides the Localhost seccomp profile path
	// applied to the postgres pods when the IOUring feature gate is enabled. The
	// path is relative to the node's kubelet seccomp root (/var/lib/kubelet/seccomp).
	IOURING_SECCOMP_PROFILE_ENV = "DOCUMENTDB_IOURING_SECCOMP_PROFILE"

	// DEFAULT_IOURING_SECCOMP_PROFILE is the default Localhost profile path for
	// the IOUring feature gate. It must be installed on every node that runs
	// postgres pods (see the io-uring feature playground) and is the upstream
	// RuntimeDefault profile plus the io_uring_{setup,enter,register} syscalls.
	DEFAULT_IOURING_SECCOMP_PROFILE = "profiles/documentdb-iouring.json"

	// Image repositories for deb-based images (must match build_images.yml naming)
	DOCUMENTDB_EXTENSION_IMAGE_REPO = "ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb"
	GATEWAY_IMAGE_REPO              = "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway"

	// MinK8sMinorVersion is the minimum required Kubernetes minor version.
	// The operator requires K8s 1.35+ for ImageVolume GA support.
	MinK8sMinorVersion = 35

	// DEFAULT_DOCUMENTDB_IMAGE is the extension image used in ImageVolume mode.
	DEFAULT_DOCUMENTDB_IMAGE = DOCUMENTDB_EXTENSION_IMAGE_REPO + ":0.110.0"
	// NOTE: Keep in sync with operator/cnpg-plugins/sidecar-injector/internal/config/config.go:applyDefaults()
	DEFAULT_GATEWAY_IMAGE                 = GATEWAY_IMAGE_REPO + ":0.110.0"
	DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET = "documentdb-credentials"
	DEFAULT_OTEL_COLLECTOR_IMAGE          = "otel/opentelemetry-collector-contrib:0.149.0"

	// --- Sidecar resource isolation (memory carve-out) ---
	// spec.resource.memory is the TOTAL pod envelope. The operator carves the
	// gateway (and, when monitoring is enabled, the OTel collector) memory out of
	// it and gives PostgreSQL the remainder. These operator-level defaults are
	// overridable via Helm values wired to the env vars below.

	// GATEWAY_MEMORY_FRACTION_ENV overrides the fraction of the pod memory
	// envelope reserved for the gateway sidecar (default DEFAULT_GATEWAY_MEMORY_FRACTION).
	GATEWAY_MEMORY_FRACTION_ENV = "DOCUMENTDB_GATEWAY_MEMORY_FRACTION"
	// GATEWAY_MEMORY_CAP_ENV overrides the absolute cap on gateway memory
	// (default DEFAULT_GATEWAY_MEMORY_CAP).
	GATEWAY_MEMORY_CAP_ENV = "DOCUMENTDB_GATEWAY_MEMORY_CAP"
	// GATEWAY_CPU_LIMIT_ENV optionally pins a CPU limit on the gateway sidecar to
	// bound its async-runtime worker threads. Empty (default) leaves CPU unbounded.
	GATEWAY_CPU_LIMIT_ENV = "DOCUMENTDB_GATEWAY_CPU_LIMIT"
	// OTEL_MEMORY_REQUEST_ENV / OTEL_MEMORY_LIMIT_ENV override the OTel collector
	// sidecar memory request/limit (defaults DEFAULT_OTEL_MEMORY_REQUEST / _LIMIT).
	OTEL_MEMORY_REQUEST_ENV = "DOCUMENTDB_OTEL_MEMORY_REQUEST"
	OTEL_MEMORY_LIMIT_ENV   = "DOCUMENTDB_OTEL_MEMORY_LIMIT"
	// OTEL_CPU_REQUEST_ENV optionally sets the OTel collector CPU request.
	OTEL_CPU_REQUEST_ENV = "DOCUMENTDB_OTEL_CPU_REQUEST"
	// OTEL_CPU_LIMIT_ENV optionally bounds the OTel collector CPU (a ceiling on
	// burst; CPU is compressible so this throttles rather than OOM-kills).
	OTEL_CPU_LIMIT_ENV = "DOCUMENTDB_OTEL_CPU_LIMIT"

	// DEFAULT_GATEWAY_MEMORY_FRACTION reserves 18.75% (3/16) of the pod memory
	// envelope for the gateway, matching the production sizing model.
	DEFAULT_GATEWAY_MEMORY_FRACTION = "0.1875"
	// DEFAULT_GATEWAY_MEMORY_CAP caps gateway memory at 32Gi (production model).
	DEFAULT_GATEWAY_MEMORY_CAP = "32Gi"
	// DEFAULT_OTEL_MEMORY_REQUEST / _LIMIT size the (tiny) metrics-only collector.
	DEFAULT_OTEL_MEMORY_REQUEST = "48Mi"
	DEFAULT_OTEL_MEMORY_LIMIT   = "128Mi"
	// DEFAULT_OTEL_CPU_REQUEST is the collector CPU request.
	DEFAULT_OTEL_CPU_REQUEST = "50m"
	// DEFAULT_OTEL_CPU_LIMIT bounds the collector's CPU burst (Burstable: the
	// 50m request above is the reserved floor, this is the hard ceiling).
	DEFAULT_OTEL_CPU_LIMIT = "200m"

	// --- Sidecar-injector plugin parameter names for component resources ---
	// The operator passes the resolved per-container requests/limits to the
	// sidecar-injector plugin via these CNPG plugin parameters; the plugin sets
	// them on the gateway and otel-collector container Resources.
	PLUGIN_PARAM_GATEWAY_MEMORY_REQUEST = "gatewayMemoryRequest"
	PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT   = "gatewayMemoryLimit"
	PLUGIN_PARAM_GATEWAY_CPU_REQUEST    = "gatewayCpuRequest"
	PLUGIN_PARAM_GATEWAY_CPU_LIMIT      = "gatewayCpuLimit"
	PLUGIN_PARAM_OTEL_MEMORY_REQUEST    = "otelMemoryRequest"
	PLUGIN_PARAM_OTEL_MEMORY_LIMIT      = "otelMemoryLimit"
	PLUGIN_PARAM_OTEL_CPU_REQUEST       = "otelCpuRequest"
	PLUGIN_PARAM_OTEL_CPU_LIMIT         = "otelCpuLimit"

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
