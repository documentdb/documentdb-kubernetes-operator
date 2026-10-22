// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cnpg "github.com/documentdb/documentdb-operator/internal/cnpg"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// ParseExtensionVersions parses the output of the pg_available_extensions query.
// Returns defaultVersion, installedVersion, and a boolean indicating if parsing was successful.
// Expected output format:
//
//	 default_version | installed_version
//	-----------------+-------------------
//	 0.110-0         | 0.110-0
//
// TODO: This parsing is fragile as it relies on psql tabular output format which can vary
// with locale or PostgreSQL version. Consider one of these improvements:
// - Option 1: Add -t -A flags to SQLExecutor for unaligned, tuples-only output ("0.110-0|0.110-0")
// - Option 2: Use json_build_object() in SQL and json.Unmarshal() in Go for robust parsing
func ParseExtensionVersions(output string) (defaultVersion, installedVersion string, ok bool) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 3 {
		return "", "", false
	}

	// Parse the data row (3rd line, index 2)
	dataLine := strings.TrimSpace(lines[2])
	parts := strings.Split(dataLine, "|")
	if len(parts) != 2 {
		return "", "", false
	}

	defaultVersion = strings.TrimSpace(parts[0])
	installedVersion = strings.TrimSpace(parts[1])
	return defaultVersion, installedVersion, true
}

func (r *ClusterReconciler[T]) extensionName() string {
	return r.profile().Postgres.Extension.Name
}

func (r *ClusterReconciler[T]) refetch(ctx context.Context, obj T) error {
	return r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
}

// HandleExtensionUpgrade handles the ALTER EXTENSION lifecycle after images have been synced
// by SyncCnpgCluster. It:
// 1. Updates status with the current images from the CNPG cluster
// 2. Checks if ALTER EXTENSION UPDATE is needed and runs it (with two-phase upgrade support)
// 3. Updates the schema version status
func (r *ClusterReconciler[T]) HandleExtensionUpgrade(ctx context.Context, currentCluster *cnpgv1.Cluster, obj T) error {
	logger := log.FromContext(ctx)
	name := r.profile().Name

	// Refetch to avoid potential race conditions with status updates
	if err := r.refetch(ctx, obj); err != nil {
		return fmt.Errorf("failed to refetch %s resource: %w", name, err)
	}

	// Update image status fields to reflect what's currently applied
	if err := r.UpdateImageStatus(ctx, obj, currentCluster); err != nil {
		logger.Error(err, "Failed to update image status")
	}

	// CNPG will trigger a rolling restart when images change. Wait for pods to become healthy
	// before running ALTER EXTENSION.
	if !slices.Contains(currentCluster.Status.InstancesStatus[cnpgv1.PodHealthy], currentCluster.Status.CurrentPrimary) {
		logger.Info("Current primary pod is not healthy; skipping " + name + " extension upgrade")
		return nil
	}

	// Check if ALTER EXTENSION UPDATE is needed
	extension := r.extensionName()
	output, err := r.SQLExecutor(ctx, currentCluster, cnpg.ExtensionVersionSQL(extension))
	if err != nil {
		return fmt.Errorf("failed to check %s extension versions: %w", extension, err)
	}

	defaultVersion, installedVersion, ok := ParseExtensionVersions(output)
	if !ok {
		logger.Info(name+" extension not found or not installed yet", "output", output)
		return nil
	}

	if installedVersion == "" {
		logger.Info(name + " extension is not installed yet")
		return nil
	}

	// Update schema version in status (even if no upgrade needed)
	// Convert from pg_available_extensions format ("0.110-0") to semver ("0.110.0")
	installedSemver := util.ExtensionVersionToSemver(installedVersion)
	if r.Adapter.Status(obj).SchemaVersion != installedSemver {
		// Re-fetch to get latest resourceVersion before status update
		if err := r.refetch(ctx, obj); err != nil {
			return fmt.Errorf("failed to refetch %s before schema version update: %w", name, err)
		}
		r.setSchemaVersion(obj, installedSemver)
		if err := r.Status().Update(ctx, obj); err != nil {
			logger.Error(err, "Failed to update "+name+" status with schema version")
			return fmt.Errorf("failed to update %s status with schema version: %w", name, err)
		}
	}

	// If versions match, no upgrade needed
	if defaultVersion == installedVersion {
		logger.V(1).Info(name+" extension is up to date", "version", installedVersion)
		return nil
	}

	// Rollback detection — check if the new binary is older than the installed schema.
	// Note: Image rollback below installed schema version is also blocked by the validating
	// webhook at admission time. This is a defense-in-depth guard.
	cmp, err := util.CompareExtensionVersions(defaultVersion, installedVersion)
	if err != nil {
		logger.Error(err, "Failed to compare extension versions, skipping ALTER EXTENSION as a safety measure",
			"defaultVersion", defaultVersion,
			"installedVersion", installedVersion)
		return nil
	}

	if cmp < 0 {
		// ALTER EXTENSION UPDATE would attempt an unsupported downgrade. Skip it and warn the user.
		msg := fmt.Sprintf(
			"Extension rollback detected: binary offers version %s but schema is at %s. "+
				"ALTER EXTENSION UPDATE skipped — %s does not provide downgrade scripts. "+
				"The cluster will run with the older binary against the newer schema, which may cause issues. "+
				"To resolve, update the extension image to a version that matches or exceeds %s.",
			defaultVersion, installedVersion, name, installedVersion)
		logger.Info(msg)
		if r.Recorder != nil {
			r.Recorder.Event(obj, corev1.EventTypeWarning, "ExtensionRollback", msg)
		}
		return nil
	}

	// Determine schema target based on spec.schemaVersion (two-phase upgrade logic)
	schemaTarget, updateSQL := r.DetermineSchemaTarget(ctx, obj, defaultVersion, installedVersion)
	if schemaTarget == "" {
		// Two-phase mode or validation failure — do not run ALTER EXTENSION
		return nil
	}

	// Run ALTER EXTENSION to upgrade
	logger.Info("Upgrading "+name+" extension",
		"fromVersion", installedVersion,
		"toVersion", schemaTarget)

	if _, err := r.SQLExecutor(ctx, currentCluster, updateSQL); err != nil {
		return fmt.Errorf("failed to run ALTER EXTENSION %s UPDATE: %w", extension, err)
	}

	logger.Info("Successfully upgraded "+name+" extension",
		"fromVersion", installedVersion,
		"toVersion", schemaTarget)

	// Update schema version in status after upgrade
	if err := r.refetch(ctx, obj); err != nil {
		return fmt.Errorf("failed to refetch %s after schema upgrade: %w", name, err)
	}
	r.setSchemaVersion(obj, util.ExtensionVersionToSemver(schemaTarget))
	if err := r.Status().Update(ctx, obj); err != nil {
		logger.Error(err, "Failed to update "+name+" status after schema upgrade")
		return fmt.Errorf("failed to update %s status after schema upgrade: %w", name, err)
	}

	return nil
}

func (r *ClusterReconciler[T]) setSchemaVersion(obj T, version string) {
	status := r.Adapter.Status(obj)
	status.SchemaVersion = version
	r.Adapter.SetStatus(obj, status)
}

// DetermineSchemaTarget decides the target schema version based on the spec schema version.
// Returns the target version (in pg_available_extensions format) and the SQL to execute.
// Returns ("", "") if no schema update should run (two-phase mode or validation failure).
func (r *ClusterReconciler[T]) DetermineSchemaTarget(
	ctx context.Context,
	obj T,
	binaryVersion string,
	installedVersion string,
) (string, string) {
	logger := log.FromContext(ctx)
	specSchemaVersion := r.Adapter.Spec(obj).SchemaVersion

	switch {
	case specSchemaVersion == "":
		// Two-phase mode: schema stays at current version until user explicitly sets schemaVersion.
		// Use V(1) to avoid log spam — this fires every reconcile while upgrade is pending.
		logger.V(1).Info("Schema update available but not requested (two-phase mode). "+
			"Set spec.schemaVersion to finalize the upgrade.",
			"binaryVersion", binaryVersion,
			"installedVersion", installedVersion)
		// Only emit the event when the binary is actually ahead of the installed schema
		// (avoids firing on new clusters where status.schemaVersion is empty).
		// Kubernetes deduplicates events with the same reason+message, but skipping
		// entirely is cleaner.
		if r.Recorder != nil && binaryVersion != installedVersion {
			msg := fmt.Sprintf(
				"Schema update available: binary version is %s, schema is at %s. "+
					"Set spec.schemaVersion to %q or \"auto\" to finalize the upgrade.",
				util.ExtensionVersionToSemver(binaryVersion),
				util.ExtensionVersionToSemver(installedVersion),
				util.ExtensionVersionToSemver(binaryVersion))
			r.Recorder.Event(obj, corev1.EventTypeNormal, "SchemaUpdateAvailable", msg)
		}
		return "", ""

	case specSchemaVersion == "auto":
		// Auto-finalize: update schema to match binary version
		return binaryVersion, cnpg.ExtensionUpdateSQL(r.extensionName(), "")

	default:
		// Explicit version: update to that specific version.
		targetPgVersion := util.SemverToExtensionVersion(specSchemaVersion)

		// Guard: target must not exceed binary version.
		// Note: schemaVersion <= binary version is enforced by the validating webhook at
		// admission time. This is a defense-in-depth guard.
		targetCmp, err := util.CompareExtensionVersions(targetPgVersion, binaryVersion)
		if err != nil {
			logger.Error(err, "Failed to compare target schema version with binary version",
				"targetVersion", specSchemaVersion,
				"binaryVersion", binaryVersion)
			return "", ""
		}
		if targetCmp > 0 {
			logger.Info("Skipping schema update: schemaVersion exceeds binary version (should have been rejected by webhook)",
				"schemaVersion", specSchemaVersion,
				"binaryVersion", util.ExtensionVersionToSemver(binaryVersion))
			return "", ""
		}

		// Check: target must be > installed version (otherwise no-op)
		installedCmp, err := util.CompareExtensionVersions(targetPgVersion, installedVersion)
		if err != nil {
			logger.Error(err, "Failed to compare target schema version with installed version",
				"targetVersion", specSchemaVersion,
				"installedVersion", installedVersion)
			return "", ""
		}
		if installedCmp <= 0 {
			logger.V(1).Info("Schema already at or beyond requested version",
				"requestedVersion", specSchemaVersion,
				"installedVersion", installedVersion)
			return "", ""
		}

		// Safety: targetPgVersion is derived from spec.schemaVersion via SemverToExtensionVersion,
		// which produces a "Major.Minor-Patch" string (e.g., "0.112-0"). The input is validated
		// by the CRD schema regex pattern and the validating webhook's version parsing, so it
		// contains only digits, dots, and hyphens — no SQL injection risk.
		return targetPgVersion, cnpg.ExtensionUpdateSQL(r.extensionName(), targetPgVersion)
	}
}

// UpdateImageStatus reads the current extension and gateway images from the CNPG cluster
// and persists them into the status. This is a no-op if both fields are already up to date.
func (r *ClusterReconciler[T]) UpdateImageStatus(ctx context.Context, obj T, cluster *cnpgv1.Cluster) error {
	name := r.profile().Name
	currentExtImage := cnpg.ExtensionImage(cluster, r.extensionName())
	currentGwImage := cnpg.GatewayImage(cluster)

	// Only update if something changed
	status := r.Adapter.Status(obj)
	if status.ExtensionImage == currentExtImage && status.GatewayImage == currentGwImage {
		return nil
	}

	// Re-fetch to get latest resourceVersion before status update
	if err := r.refetch(ctx, obj); err != nil {
		return fmt.Errorf("failed to refetch %s before image status update: %w", name, err)
	}
	status = r.Adapter.Status(obj)
	status.ExtensionImage = currentExtImage
	status.GatewayImage = currentGwImage
	r.Adapter.SetStatus(obj, status)
	if err := r.Status().Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to update %s image status: %w", name, err)
	}
	return nil
}
