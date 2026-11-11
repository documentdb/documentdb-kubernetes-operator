// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"fmt"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// formatMB formats a megabyte value as a PostgreSQL size string.
// Values >= 1024 MB that are evenly divisible by 1024 are expressed in GB.
func formatMB(mb int64) string {
	if mb >= 1024 && mb%1024 == 0 {
		return fmt.Sprintf("%dGB", mb/1024)
	}
	return fmt.Sprintf("%dMB", mb)
}

// ComputeMemoryAwareDefaults computes memory-sensitive PostgreSQL parameters
// from the pod memory limit. Based on PostgreSQL and CNPG official guidance
// (25% for shared_buffers, Guaranteed QoS model).
func ComputeMemoryAwareDefaults(memoryLimitBytes int64) map[string]string {
	if memoryLimitBytes <= 0 {
		return map[string]string{
			"shared_buffers":       "256MB",
			"effective_cache_size": "512MB",
			"work_mem":             "16MB",
			"maintenance_work_mem": "128MB",
		}
	}

	memMB := memoryLimitBytes / (1024 * 1024)

	// shared_buffers: 25% of memory limit
	sharedBuffers := memMB / 4

	// effective_cache_size: 75% of memory limit
	effectiveCacheSize := memMB * 3 / 4

	// work_mem: memory_limit / (max_connections × 4)
	// max_connections = 300, concurrent ops per connection = 4
	workMem := memMB / (300 * 4)
	if workMem < 4 {
		workMem = 4
	}

	// maintenance_work_mem: min(2GB, 10% of memory limit)
	maintenanceWorkMem := memMB / 10
	if maintenanceWorkMem > 2048 {
		maintenanceWorkMem = 2048
	}

	return map[string]string{
		"shared_buffers":       formatMB(sharedBuffers),
		"effective_cache_size": formatMB(effectiveCacheSize),
		"work_mem":             formatMB(workMem),
		"maintenance_work_mem": formatMB(maintenanceWorkMem),
	}
}

// StaticDefaults returns non-memory-sensitive parameters with fixed
// best-practice values.
func StaticDefaults() map[string]string {
	return map[string]string{
		"max_connections":                 "300",
		"random_page_cost":                "1.1",
		"effective_io_concurrency":        "200",
		"autovacuum_vacuum_scale_factor":  "0.1",
		"autovacuum_analyze_scale_factor": "0.05",
		"autovacuum_vacuum_cost_delay":    "2ms",
		"autovacuum_max_workers":          "4",
		"checkpoint_completion_target":    "0.9",
		"wal_buffers":                     "16MB",
		"min_wal_size":                    "256MB",
		"max_wal_size":                    "2GB",
	}
}

// ProtectedParameters returns parameters that are always force-set by the
// operator for a DocumentDB instance and cannot be overridden by users.
func ProtectedParameters(documentdb *dbv1.DocumentDB) map[string]string {
	return ProtectedParametersFor(product.DocumentDBAdapter{}.ToClusterIntent(documentdb))
}

// ProtectedParametersFor returns the core protected parameters, overlaid with
// the product's protected parameters and the feature-gate driven GUCs.
func ProtectedParametersFor(intent product.ClusterIntent) map[string]string {
	params := map[string]string{
		"max_replication_slots":     "10",
		"max_wal_senders":           "10",
		"max_prepared_transactions": "100",
	}
	for k, v := range intent.Product.Postgres.ProtectedParameters {
		params[k] = v
	}
	if intent.FeatureGates.ChangeStreams {
		params["wal_level"] = "logical"
	}
	if intent.FeatureGates.IOUring {
		params["io_method"] = "io_uring"
	}
	return params
}

// MergeParameters merges all parameter sources for a DocumentDB instance; see
// MergeParametersFor.
func MergeParameters(documentdb *dbv1.DocumentDB, memoryLimitBytes int64) map[string]string {
	return MergeParametersFor(product.DocumentDBAdapter{}.ToClusterIntent(documentdb), memoryLimitBytes)
}

// MergeParametersFor merges all parameter sources in priority order (last write wins):
// 1. StaticDefaults
// 2. ComputeMemoryAwareDefaults
// 3. User overrides (intent.Postgres.Parameters)
// 4. ProtectedParametersFor (always wins)
func MergeParametersFor(intent product.ClusterIntent, memoryLimitBytes int64) map[string]string {
	result := make(map[string]string)

	for k, v := range StaticDefaults() {
		result[k] = v
	}
	for k, v := range ComputeMemoryAwareDefaults(memoryLimitBytes) {
		result[k] = v
	}
	for k, v := range intent.Postgres.Parameters {
		result[k] = v
	}
	for k, v := range ProtectedParametersFor(intent) {
		result[k] = v
	}

	return result
}
