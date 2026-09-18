// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
)

// Images holds the fully-resolved container images for a cluster.
type Images struct {
	// PostgresExtension is the extension image mounted into PostgreSQL via ImageVolume.
	PostgresExtension string
	// Gateway is the MongoDB wire-protocol gateway sidecar image.
	Gateway string
	// Postgres is the base PostgreSQL image.
	Postgres string
	// PullSecrets are the image pull secrets shared by all cluster containers.
	PullSecrets []corev1.LocalObjectReference
}

// Topology describes the cluster shape and scheduling.
type Topology struct {
	// NodeCount is the number of nodes (shards) in the cluster.
	NodeCount int
	// InstancesPerNode is the number of PostgreSQL instances per node.
	InstancesPerNode int
	// Affinity is the CNPG affinity/anti-affinity passthrough.
	Affinity cnpgv1.AffinityConfiguration
}

// Storage describes the persistent volume request. StorageClass is resolved by
// the controller from replication context and passed to the builder separately
// (as a render parameter), so it is not part of this adapter-derived model.
type Storage struct {
	// PvcSize is the persistent volume claim size (for example "10Gi").
	PvcSize string
}

// Identity carries the owning custom resource's identity for owner references
// and resource labels.
type Identity struct {
	Name       string
	Namespace  string
	UID        types.UID
	APIVersion string
	Kind       string
}

// Postgres carries the operator-managed PostgreSQL process and init tuning taken
// from the custom resource.
type Postgres struct {
	// UID and GID are the process identity overrides; nil leaves the CNPG default.
	UID *int64
	GID *int64
	// PostInitSQL is appended to the mandatory bootstrap SQL.
	PostInitSQL []string
	// Parameters are the resolved PostgreSQL GUC overrides. They start from the
	// user-supplied values and may include product-mandated defaults contributed
	// by the adapter (for example DocumentDB change streams adds
	// wal_level=logical). They are merged on top of the operator's static and
	// memory-aware defaults and below the neutral protected parameters when the
	// builder assembles the final GUC set.
	Parameters map[string]string
}

// FeatureGates carries the resolved, product-neutral feature-gate flags the
// builder acts on. Only genuinely cross-product (infrastructure) gates belong
// here; product-specific gates are expressed through their concrete effect (for
// example DocumentDB change streams contributing Postgres.Parameters
// wal_level=logical) instead of leaking into this struct.
type FeatureGates struct {
	// IOUring relaxes the postgres seccomp profile and enables io_method=io_uring.
	// It is an infrastructure concern shared across products.
	IOUring bool
}

// ComponentResource is a per-container resource override (request==limit when
// set). Empty strings mean "unset".
type ComponentResource struct {
	Memory string
	CPU    string
}

// Resource is the product-neutral pod resource envelope plus optional
// per-container overrides. It mirrors the shape the builder carves across the
// PostgreSQL, gateway, and OTel collector containers.
type Resource struct {
	// Memory and CPU are the total pod envelope (may be empty/unset).
	Memory string
	CPU    string
	// Database, Gateway, and OTel optionally override individual containers.
	Database *ComponentResource
	Gateway  *ComponentResource
	OTel     *ComponentResource
}

// TLS carries the resolved TLS inputs the builder renders onto the Cluster.
type TLS struct {
	// GatewaySecretName is the ready gateway TLS secret surfaced to the plugin.
	// Empty when TLS is not yet provisioned.
	GatewaySecretName string
	// PostgresCertificates is the CNPG certificates passthrough for the Postgres
	// server (nil when TLS is not configured).
	PostgresCertificates *cnpgv1.CertificatesConfiguration
}

// Recovery describes a bootstrap-from-source request. A nil Recovery on Bootstrap
// means default initialization.
type Recovery struct {
	BackupName           string
	PersistentVolumeName string
}

// Bootstrap describes how the cluster is initialized.
type Bootstrap struct {
	Recovery *Recovery
}

// ClusterIntent is the product-neutral desired state the reconciler renders into
// a CNPG Cluster. Product adapters populate it from their custom resource; the
// reconciler consumes it without product-branding logic. Fields are added to
// this struct as the builder is progressively rewired onto the seam.
type ClusterIntent struct {
	// Images are the resolved extension, gateway, and postgres images.
	Images Images

	// Topology is the cluster shape and scheduling.
	Topology Topology

	// Storage is the persistent volume request.
	Storage Storage

	// Identity is the owning custom resource's identity.
	Identity Identity

	// Postgres is the operator-managed PostgreSQL process and init tuning.
	Postgres Postgres

	// Resource is the product-neutral pod resource envelope and per-container
	// overrides the builder carves across containers.
	Resource Resource

	// TLS carries the resolved TLS inputs (gateway secret + Postgres certificates).
	TLS TLS

	// Monitoring carries the resolved, product-neutral OTel collector config the
	// builder renders (config map name/hash + Prometheus port) on the fly.
	Monitoring otelcfg.MonitoringConfig

	// LogLevel is the desired CNPG log level (empty means the builder default).
	LogLevel string

	// Timeouts carries the resolved CNPG process timeouts (defaults applied).
	Timeouts Timeouts

	// FeatureGates are the resolved feature-gate flags.
	FeatureGates FeatureGates

	// Bootstrap describes how the cluster is initialized.
	Bootstrap Bootstrap

	// CredentialSecret is the resolved credential secret name.
	CredentialSecret string

	// Plugins carries the resolved CNPG plugin names to wire onto the Cluster.
	Plugins Plugins

	// Product is the profile this intent was produced from.
	Product ProductProfile
}

// Timeouts carries the resolved CNPG process timeouts. It mirrors the CRD's
// Timeouts grouping; values here are resolved (CNPG defaults applied).
type Timeouts struct {
	// StopDelay is the resolved CNPG max stop delay in seconds.
	StopDelay int32
}

// Plugins carries the resolved CNPG plugin names. It mirrors the CRD's Plugins
// grouping; values here are resolved (profile defaults applied, spec honored).
type Plugins struct {
	// SidecarInjectorName is the CNPG sidecar injector plugin name.
	SidecarInjectorName string
	// WalReplicaName is the CNPG WAL replica plugin name used for cross-cluster
	// replication.
	WalReplicaName string
}
