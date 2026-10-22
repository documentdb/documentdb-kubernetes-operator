// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package core is the public, product-neutral surface of the operator's
// CloudNative-PG orchestration. Another operator module (for example an
// operator for a different PostgreSQL-extension product) builds a
// ProductProfile and a ClusterIntent from its own custom resource and drives
// the same renderer, sync, parameter, resource and extension-lifecycle logic
// the DocumentDB operator uses.
//
// The package only re-exports types and functions from the operator's internal
// packages; it adds no behavior of its own, so both products share one
// implementation.
package core

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/internal/cnpg"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// Product-neutral seam.
type (
	ProductProfile  = product.ProductProfile
	PostgresProfile = product.PostgresProfile
	ExtensionSpec   = product.ExtensionSpec
	GatewayProfile  = product.GatewayProfile
	Adapter         = product.Adapter

	ClusterIntent      = product.ClusterIntent
	Images             = product.Images
	Topology           = product.Topology
	Storage            = product.Storage
	Resources          = product.Resources
	ComponentResources = product.ComponentResources
	Identity           = product.Identity
	Postgres           = product.Postgres
	FeatureGates       = product.FeatureGates
	TLS                = product.TLS
	Recovery           = product.Recovery
	Bootstrap          = product.Bootstrap
)

// Rendering and sync.
type (
	SplitConfig       = cnpg.SplitConfig
	ResourceSplit     = cnpg.ResourceSplit
	ComponentResource = cnpg.ComponentResource
	JSONPatch         = cnpg.JSONPatch
)

// RenderCluster renders the CNPG Cluster for a product-neutral intent.
func RenderCluster(req ctrl.Request, intent ClusterIntent, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	return cnpg.GetCnpgClusterSpecFromIntent(req, intent, serviceAccountName, storageClass, isPrimaryRegion, log)
}

// SyncCluster patches the mutable fields of current towards desired, including
// the product extension and gateway images, plus any extraOps.
func SyncCluster(ctx context.Context, c client.Client, current, desired *cnpgv1.Cluster, extraOps []JSONPatch) error {
	return cnpg.SyncCnpgCluster(ctx, c, current, desired, extraOps)
}

// MergeParameters returns the PostgreSQL parameters for an intent: memory-aware
// defaults, user overrides, then core and product protected parameters.
func MergeParameters(intent ClusterIntent, memoryLimitBytes int64) map[string]string {
	return cnpg.MergeParametersFor(intent, memoryLimitBytes)
}

// ProtectedParameters returns the parameters the operator always forces.
func ProtectedParameters(intent ClusterIntent) map[string]string {
	return cnpg.ProtectedParametersFor(intent)
}

// DefaultSplitConfig returns the resource split configuration, honoring the
// operator's environment overrides.
func DefaultSplitConfig() SplitConfig { return cnpg.DefaultSplitConfig() }

// ComputeResourceSplit divides a pod-level resource envelope across
// PostgreSQL, the gateway and (when monitoring) the OTel collector.
func ComputeResourceSplit(res Resources, monitoring bool, cfg SplitConfig) ResourceSplit {
	return cnpg.ComputeResourceSplitFor(res, monitoring, cfg)
}

// ValidateResources validates a resource envelope; errors are reported under
// base, the product's field path for its resources.
func ValidateResources(res Resources, monitoring bool, base *field.Path, cfg SplitConfig) field.ErrorList {
	return cnpg.ValidateResourcesFor(res, monitoring, base, cfg)
}

// ExtensionVersionSQL queries the default and installed versions of extension.
func ExtensionVersionSQL(extension string) string { return cnpg.ExtensionVersionSQL(extension) }

// ExtensionUpdateSQL upgrades extension, to targetVersion when set. Pass the
// unquoted extension name; it is quoted as a SQL identifier.
func ExtensionUpdateSQL(extension, targetVersion string) string {
	return cnpg.ExtensionUpdateSQL(extension, targetVersion)
}

// ReplicationRoleCheckSQL checks whether streaming_replica is a member of role.
func ReplicationRoleCheckSQL(role string) string { return cnpg.ReplicationRoleCheckSQL(role) }

// ReplicationRoleGrantSQL grants role to streaming_replica. Pass the unquoted
// role name; it is quoted as a SQL identifier.
func ReplicationRoleGrantSQL(role string) string { return cnpg.ReplicationRoleGrantSQL(role) }

// ExtensionImage returns the image currently mounted for extension in cluster.
func ExtensionImage(cluster *cnpgv1.Cluster, extension string) string {
	return cnpg.ExtensionImage(cluster, extension)
}

// GatewayImage returns the gateway image currently configured on cluster.
func GatewayImage(cluster *cnpgv1.Cluster) string { return cnpg.GatewayImage(cluster) }

// ParsePullPolicy returns value as a PullPolicy, or "" when it is not valid.
func ParsePullPolicy(value string) corev1.PullPolicy { return product.ParsePullPolicy(value) }

// DocumentDBProfile returns the DocumentDB product profile, a reference for
// what a complete profile contains.
func DocumentDBProfile() ProductProfile { return product.DocumentDBProfile() }
