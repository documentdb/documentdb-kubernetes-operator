// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/lib/pq"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// The helpers below build the SQL and read the cluster state the extension
// lifecycle needs for any product. Product-defined names are quoted as SQL
// identifiers in statements and as string literals in catalog lookups.

// ExtensionVersionSQL returns the query reporting the default (binary) and
// installed versions of the named extension.
func ExtensionVersionSQL(extension string) string {
	return fmt.Sprintf("SELECT default_version, installed_version FROM pg_available_extensions WHERE name = %s", pq.QuoteLiteral(extension))
}

// ExtensionUpdateSQL returns the ALTER EXTENSION statement for the named
// extension. An empty targetVersion updates to the binary's default version.
// targetVersion must already be validated (digits, dots, and hyphens only).
func ExtensionUpdateSQL(extension, targetVersion string) string {
	if targetVersion == "" {
		return fmt.Sprintf("ALTER EXTENSION %s UPDATE", pq.QuoteIdentifier(extension))
	}
	return fmt.Sprintf("ALTER EXTENSION %s UPDATE TO %s", pq.QuoteIdentifier(extension), pq.QuoteLiteral(targetVersion))
}

// ReplicationRoleCheckSQL returns the query that yields one row when CNPG's
// streaming_replica user already holds the given role.
func ReplicationRoleCheckSQL(role string) string {
	return fmt.Sprintf("SELECT 1 FROM pg_roles WHERE rolname = 'streaming_replica' AND pg_has_role('streaming_replica', %s, 'USAGE');", pq.QuoteLiteral(role))
}

// ReplicationRoleGrantSQL returns the statement granting the given role to
// CNPG's streaming_replica user.
func ReplicationRoleGrantSQL(role string) string {
	return fmt.Sprintf("GRANT %s TO streaming_replica;", pq.QuoteIdentifier(role))
}

// ExtensionImage returns the image currently declared for the named extension
// on the cluster, or "" when absent.
func ExtensionImage(cluster *cnpgv1.Cluster, extension string) string {
	_, image := findExtensionImage(cluster, extension)
	return image
}

// GatewayImage returns the gateway image currently passed to the sidecar
// injector plugin (the first plugin), or "" when absent.
func GatewayImage(cluster *cnpgv1.Cluster) string {
	if len(cluster.Spec.Plugins) == 0 {
		return ""
	}
	return getParam(cluster.Spec.Plugins[0].Parameters, util.PLUGIN_PARAM_GATEWAY_IMAGE)
}
