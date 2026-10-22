// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// NamingProfile carries the product's Kubernetes naming conventions: the domain
// used for labels, annotations, and finalizers, the gateway Service name
// prefix, and the OTel resource attribute that identifies the cluster.
type NamingProfile struct {
	// Domain prefixes product labels, annotations, and finalizers
	// (for example "documentdb.io").
	Domain string
	// ServicePrefix is prepended to the custom resource name to form the
	// gateway Service name.
	ServicePrefix string
	// MetricClusterAttribute is the OTel resource attribute key carrying the
	// cluster name.
	MetricClusterAttribute string
	// HealthMetricName is the OTel gauge the monitoring sidecar reports
	// PostgreSQL liveness as; empty means "documentdb.postgres.up".
	HealthMetricName string
	// OperatorName identifies the operator in generated artifacts.
	OperatorName string
	// Kind is the custom resource kind; it identifies the product owner
	// reference on CNPG clusters.
	Kind string
}

// ClusterLabel is the PV/PVC label holding the owning custom resource name.
func (n NamingProfile) ClusterLabel() string { return n.Domain + "/cluster" }

// NamespaceLabel is the PV label holding the owning custom resource namespace.
func (n NamingProfile) NamespaceLabel() string { return n.Domain + "/namespace" }

// RecoveryTempLabel marks the temporary PVC created for PV recovery.
func (n NamingProfile) RecoveryTempLabel() string { return n.Domain + "/recovery-temp" }

// SchemaVersionAnnotation records the installed extension schema version on a PV.
func (n NamingProfile) SchemaVersionAnnotation() string { return n.Domain + "/schema-version" }

// Finalizer is the custom resource finalizer used to warn about retained PVs.
func (n NamingProfile) Finalizer() string { return n.Domain + "/pv-retention-finalizer" }

// ServiceName is the gateway Service name for a custom resource, truncated to
// the 63-character Kubernetes name limit without leaving a trailing hyphen.
func (n NamingProfile) ServiceName(clusterName string) string {
	name := n.ServicePrefix + clusterName
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// ClusterSpec is the product-neutral, read-only view of the operational inputs
// the shared reconcilers act on. Products typically reuse the api/preview
// sub-types in their own custom resources and copy them here.
type ClusterSpec struct {
	// ServiceType is "", "ClusterIP", or "LoadBalancer"; empty means no
	// gateway Service is managed.
	ServiceType string
	// Environment is the cloud environment ("aks", "eks", "gke", or empty)
	// used for LoadBalancer annotations and VolumeSnapshotClass defaults.
	Environment string
	// StorageClass is the PVC storage class; empty uses the cluster default.
	StorageClass string
	// PersistentVolumeReclaimPolicy is "Retain" (default when empty) or "Delete".
	PersistentVolumeReclaimPolicy string
	// RecoveryPersistentVolume, when set, bootstraps the cluster from a
	// retained PersistentVolume.
	RecoveryPersistentVolume string
	// GatewayTLS is the gateway TLS configuration; nil means self-signed.
	GatewayTLS *dbpreview.GatewayTLS
	// SchemaVersion controls the extension upgrade: "" (two-phase, manual),
	// "auto", or an explicit semantic version.
	SchemaVersion string
	// Monitoring is the optional OTel collector configuration.
	Monitoring *dbpreview.MonitoringSpec
	// Backup is the cluster-level backup configuration.
	Backup *dbpreview.BackupConfiguration
}

// MonitoringEnabled reports whether the OTel collector sidecar is enabled.
func (s ClusterSpec) MonitoringEnabled() bool {
	return s.Monitoring != nil && s.Monitoring.Enabled
}

// RetainsPVs reports whether PVs outlive the cluster (Retain is the default).
func (s ClusterSpec) RetainsPVs() bool {
	return s.PersistentVolumeReclaimPolicy == "" || s.PersistentVolumeReclaimPolicy == "Retain"
}

// ClusterStatus is the product-neutral view of the status fields the shared
// reconcilers own. Product status types keep their own field names; adapters
// map between them. SetStatus must only write these fields.
type ClusterStatus struct {
	// Phase mirrors the CNPG cluster phase.
	Phase string
	// ConnectionString is the client connection string for the gateway.
	ConnectionString string
	// SchemaVersion is the installed extension schema version (semver).
	SchemaVersion string
	// ExtensionImage and GatewayImage are the images currently applied to the
	// CNPG cluster.
	ExtensionImage string
	GatewayImage   string
	// TLS is the gateway TLS status.
	TLS *dbpreview.TLSStatus
}

// ClusterAdapter is the full contract a product implements to run on the shared
// reconcilers: it creates its custom resource type, translates it into the
// rendering intent and operational view, and maps the neutral status.
type ClusterAdapter[T client.Object] interface {
	Adapter
	// New returns an empty custom resource, used for Get and watches.
	New() T
	// Intent translates the custom resource into the CNPG rendering intent.
	Intent(obj T) ClusterIntent
	// Spec returns the operational view of the custom resource.
	Spec(obj T) ClusterSpec
	// Status reads the neutral status from the custom resource.
	Status(obj T) ClusterStatus
	// SetStatus writes the neutral status into the custom resource, leaving
	// product-only status fields untouched.
	SetStatus(obj T, status ClusterStatus)
	// ConnectionString builds the client connection string for a gateway
	// reachable at host:port. trustTLS is true when the gateway certificate
	// is ready and trusted.
	ConnectionString(obj T, host string, port int32, trustTLS bool) string
}
