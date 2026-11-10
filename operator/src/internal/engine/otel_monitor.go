// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/lib/pq"
	"sigs.k8s.io/controller-runtime/pkg/log"

	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
)

// isPrimaryHealthy reports whether CNPG lists the cluster's current primary
// among its healthy instances.
func isPrimaryHealthy(cluster *cnpgv1.Cluster) bool {
	return slices.Contains(cluster.Status.InstancesStatus[cnpgv1.PodHealthy], cluster.Status.CurrentPrimary)
}

// grantOtelMonitorSchemas grants the OTel monitoring role USAGE on schemas
// when it does not hold it yet. It returns true when the role does not exist
// yet: CNPG creates managed roles asynchronously, so the caller retries later.
func (r *ClusterReconciler[T]) grantOtelMonitorSchemas(ctx context.Context, cluster *cnpgv1.Cluster, schemas []string) (bool, error) {
	output, err := r.SQLExecutor(ctx, cluster, otelMonitorGrantStateSQL(schemas))
	if err != nil {
		return false, fmt.Errorf("failed to check the OTel monitoring role's schema grants: %w", err)
	}
	switch state := parseOtelMonitorGrantState(output); state {
	case otelMonitorGranted:
		return false, nil
	case otelMonitorRoleMissing:
		return true, nil
	case otelMonitorNotGranted:
		if _, err := r.SQLExecutor(ctx, cluster, otelMonitorGrantSQL(schemas)); err != nil {
			return false, err
		}
		log.FromContext(ctx).Info("Granted schema usage to the OTel monitoring role",
			"role", otelcfg.MonitorRoleName, "schemas", schemas)
		return false, nil
	default:
		return false, fmt.Errorf("unexpected OTel monitoring role grant state: %q", output)
	}
}

// revokeOtelMonitorGrants revokes the OTel monitoring role's USAGE on schemas
// on the healthy primary so that CNPG can drop the role. It returns false when
// the grants could not be revoked.
func (r *ClusterReconciler[T]) revokeOtelMonitorGrants(ctx context.Context, cluster *cnpgv1.Cluster, schemas []string) bool {
	logger := log.FromContext(ctx)
	if !isPrimaryHealthy(cluster) {
		logger.Info("Keeping the OTel monitoring role until a healthy primary can revoke its schema grants")
		return false
	}
	if _, err := r.SQLExecutor(ctx, cluster, otelMonitorRevokeSQL(schemas)); err != nil {
		logger.Error(err, "Failed to revoke schema usage from the OTel monitoring role")
		return false
	}
	return true
}

// States reported by otelMonitorGrantStateSQL.
const (
	otelMonitorGranted     = "granted"
	otelMonitorNotGranted  = "pending"
	otelMonitorRoleMissing = "missing"
)

// otelMonitorGrantStateSQL reports whether the OTel monitoring role is
// missing, lacks USAGE on any of schemas, or holds all of it.
func otelMonitorGrantStateSQL(schemas []string) string {
	checks := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		checks = append(checks, fmt.Sprintf("has_schema_privilege(r.oid, %s, 'USAGE')", pq.QuoteLiteral(schema)))
	}
	return fmt.Sprintf(
		"SELECT CASE WHEN r.oid IS NULL THEN '%s' WHEN %s THEN '%s' ELSE '%s' END AS otel_monitor_grants "+
			"FROM (SELECT 1) AS one LEFT JOIN pg_roles r ON r.rolname = %s;",
		otelMonitorRoleMissing, strings.Join(checks, " AND "), otelMonitorGranted, otelMonitorNotGranted,
		pq.QuoteLiteral(otelcfg.MonitorRoleName),
	)
}

// parseOtelMonitorGrantState extracts the state from the psql output of
// otelMonitorGrantStateSQL, or returns "" when no state is found.
func parseOtelMonitorGrantState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		switch state := strings.TrimSpace(line); state {
		case otelMonitorGranted, otelMonitorNotGranted, otelMonitorRoleMissing:
			return state
		}
	}
	return ""
}

func otelMonitorGrantSQL(schemas []string) string {
	return fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", quoteIdentifiers(schemas), otelcfg.MonitorRoleName)
}

// otelMonitorRevokeSQL revokes the schema grants and does nothing when the
// role does not exist.
func otelMonitorRevokeSQL(schemas []string) string {
	return fmt.Sprintf(
		"DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN "+
			"REVOKE USAGE ON SCHEMA %s FROM %s; END IF; END $$;",
		pq.QuoteLiteral(otelcfg.MonitorRoleName), quoteIdentifiers(schemas), otelcfg.MonitorRoleName,
	)
}

// quoteIdentifiers quotes each name as an SQL identifier and joins them with
// commas.
func quoteIdentifiers(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = pq.QuoteIdentifier(name)
	}
	return strings.Join(quoted, ", ")
}
