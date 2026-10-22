// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// Images holds the fully-resolved container images for a cluster.
type Images struct {
	// PostgresExtension is the extension image mounted into PostgreSQL via ImageVolume.
	PostgresExtension string
	// PostgresExtensionPullPolicy is the ImageVolume pull policy for the
	// extension image; empty leaves the Kubernetes default.
	PostgresExtensionPullPolicy corev1.PullPolicy
	// Gateway is the product wire-protocol gateway sidecar image.
	Gateway string
	// GatewayPullPolicy is passed through to the sidecar injector verbatim;
	// empty lets the injector apply its default.
	GatewayPullPolicy string
	// Postgres is the base PostgreSQL image.
	Postgres string
	// PullSecrets are the image pull secrets shared by all cluster containers.
	PullSecrets []corev1.LocalObjectReference
}

// Topology describes the cluster shape and scheduling.
type Topology struct {
	// Instances is the number of PostgreSQL instances in the cluster.
	Instances int
	// Affinity is the CNPG affinity/anti-affinity passthrough.
	Affinity cnpgv1.AffinityConfiguration
}

// Storage describes the persistent volume request. StorageClass is resolved by
// the controller from replication context and passed to the builder separately,
// so it is not part of this adapter-derived model yet.
type Storage struct {
	// PvcSize is the persistent volume claim size (for example "10Gi").
	PvcSize string
}

// ComponentResources is an optional per-container memory/CPU override.
// Empty or "0" values mean "unset".
type ComponentResources struct {
	Memory string
	CPU    string
}

// Resources is the pod resource envelope and optional per-container overrides
// the operator splits across the PostgreSQL, gateway, and OTel containers.
type Resources struct {
	// Memory and CPU are the optional pod-level envelopes.
	Memory string
	CPU    string
	// Database, Gateway, and OTel are optional per-container overrides.
	Database *ComponentResources
	Gateway  *ComponentResources
	OTel     *ComponentResources
}

// Identity carries the owning custom resource's identity for owner references
// and resource labels.
type Identity struct {
	Name       string
	UID        types.UID
	APIVersion string
	Kind       string
}

// Postgres carries the per-instance PostgreSQL process, init, and parameter
// tuning taken from the custom resource. Product-wide defaults (extension,
// libraries, bootstrap SQL, protected GUCs) live on ProductProfile.Postgres.
type Postgres struct {
	// UID and GID are the process identity overrides; nil leaves the CNPG default.
	UID *int64
	GID *int64
	// PostInitSQL is appended to the product's mandatory bootstrap SQL.
	PostInitSQL []string
	// Parameters are user GUC overrides. They win over operator defaults but
	// never over protected parameters.
	Parameters map[string]string
	// LogLevel is the CNPG instance manager log level; empty means "info".
	LogLevel string
	// StopDelay is the CNPG maxStopDelay in seconds; 0 uses the operator default.
	StopDelay int32
}

// FeatureGates carries the resolved feature-gate flags the builder acts on.
type FeatureGates struct {
	// ChangeStreams forces wal_level=logical.
	ChangeStreams bool
	// IOUring forces io_method=io_uring and relaxes the seccomp profile.
	IOUring bool
}

// TLS carries the resolved TLS material for the cluster.
type TLS struct {
	// GatewaySecret is the ready gateway certificate secret; empty when gateway
	// TLS is not (yet) ready.
	GatewaySecret string
	// Postgres is the optional CNPG server/client certificate configuration.
	Postgres *cnpgv1.CertificatesConfiguration
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
// renderer consumes only the intent (and the ProductProfile embedded in it), so
// it carries no product-branding logic.
type ClusterIntent struct {
	// Images are the resolved extension, gateway, and postgres images.
	Images Images

	// Topology is the cluster shape and scheduling.
	Topology Topology

	// Storage is the persistent volume request.
	Storage Storage

	// Resources is the pod resource envelope and per-container overrides.
	Resources Resources

	// Monitoring is the optional OTel collector sidecar configuration. The
	// MonitoringSpec API type is shared across products.
	Monitoring *dbpreview.MonitoringSpec

	// TLS is the resolved gateway and PostgreSQL TLS material.
	TLS TLS

	// Identity is the owning custom resource's identity.
	Identity Identity

	// Postgres is the operator-managed PostgreSQL process and init tuning.
	Postgres Postgres

	// FeatureGates are the resolved feature-gate flags.
	FeatureGates FeatureGates

	// Bootstrap describes how the cluster is initialized.
	Bootstrap Bootstrap

	// CredentialSecret is the resolved credential secret name.
	CredentialSecret string

	// SidecarInjectorPlugin and WALReplicaPlugin are the resolved CNPG plugin
	// names to wire onto the Cluster.
	SidecarInjectorPlugin string
	WALReplicaPlugin      string

	// Product is the profile this intent was produced from.
	Product ProductProfile
}

// ParsePullPolicy converts a string to a corev1.PullPolicy, returning "" for
// unrecognized values so callers fall back to the Kubernetes default.
func ParsePullPolicy(value string) corev1.PullPolicy {
	switch corev1.PullPolicy(value) {
	case corev1.PullAlways, corev1.PullNever, corev1.PullIfNotPresent:
		return corev1.PullPolicy(value)
	default:
		return ""
	}
}
