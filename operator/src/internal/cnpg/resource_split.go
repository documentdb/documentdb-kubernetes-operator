// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// SplitConfig holds the operator-level (Helm/env configurable) knobs that drive
// the pod memory carve-out between PostgreSQL and the sidecars.
type SplitConfig struct {
	// GatewayMemoryFraction is the fraction (0..1) of the pod memory envelope
	// reserved for the gateway sidecar when no explicit override is given.
	GatewayMemoryFraction float64
	// GatewayMemoryCapBytes caps the derived gateway memory (bytes). 0 = no cap.
	GatewayMemoryCapBytes int64
	// GatewayCPULimit optionally pins a CPU limit on the gateway (quantity
	// string, e.g. "2"). Empty leaves the gateway CPU unbounded.
	GatewayCPULimit string
	// OTelMemoryRequest / OTelMemoryLimit size the OTel collector sidecar
	// (quantity strings). The limit is what gets carved from the envelope.
	OTelMemoryRequest string
	OTelMemoryLimit   string
	// OTelCPURequest is the OTel collector CPU request (quantity string).
	OTelCPURequest string
}

// ComponentResource is a resolved per-container request/limit pair. Empty
// strings mean "unset" (the operator omits that request/limit).
type ComponentResource struct {
	MemoryRequest string
	MemoryLimit   string
	CPURequest    string
	CPULimit      string
}

// ResourceSplit is the resolved allocation across the pod's containers.
type ResourceSplit struct {
	Postgres ComponentResource
	Gateway  ComponentResource
	// OTel is only populated when monitoring is enabled.
	OTel              ComponentResource
	MonitoringEnabled bool
	// PostgresMemoryBytes is the memory limit (bytes) PostgreSQL receives after
	// the carve-out. Used to compute memory-aware GUCs. 0 means unset/unlimited.
	PostgresMemoryBytes int64
}

// DefaultSplitConfig loads the carve-out configuration from the operator
// environment, falling back to the documented production defaults.
func DefaultSplitConfig() SplitConfig {
	frac := parseFloatOr(os.Getenv(util.GATEWAY_MEMORY_FRACTION_ENV), util.DEFAULT_GATEWAY_MEMORY_FRACTION)
	capBytes := parseQuantityOrZero(envOr(util.GATEWAY_MEMORY_CAP_ENV, util.DEFAULT_GATEWAY_MEMORY_CAP))
	return SplitConfig{
		GatewayMemoryFraction: frac,
		GatewayMemoryCapBytes: capBytes,
		GatewayCPULimit:       os.Getenv(util.GATEWAY_CPU_LIMIT_ENV),
		OTelMemoryRequest:     envOr(util.OTEL_MEMORY_REQUEST_ENV, util.DEFAULT_OTEL_MEMORY_REQUEST),
		OTelMemoryLimit:       envOr(util.OTEL_MEMORY_LIMIT_ENV, util.DEFAULT_OTEL_MEMORY_LIMIT),
		OTelCPURequest:        envOr(util.OTEL_CPU_REQUEST_ENV, util.DEFAULT_OTEL_CPU_REQUEST),
	}
}

// ComputeResourceSplit resolves how the pod memory envelope
// (spec.resource.memory) is divided across the PostgreSQL, gateway, and (when
// monitoring is enabled) OTel collector containers.
//
// Algorithm (memory):
//   - otelMem    = override ?? (monitoring ? OTelMemoryLimit : 0)   [carved only if monitoring]
//   - gatewayMem = override ?? min(fraction × envelope, cap)        [carved only if envelope > 0]
//   - dbMem      = override ?? (envelope − gatewayMem − otelMem)
//
// When the envelope memory is unset, no automatic carve-out happens; only
// explicit per-component overrides are applied (preserving legacy behavior).
//
// CPU: PostgreSQL gets spec.resource.database.cpu ?? spec.resource.cpu. The
// gateway/otel get their explicit cpu overrides, or the operator-level
// GatewayCPULimit / OTelCPURequest defaults.
func ComputeResourceSplit(documentdb *dbpreview.DocumentDB, cfg SplitConfig) ResourceSplit {
	res := documentdb.Spec.Resource
	monitoring := documentdb.Spec.Monitoring != nil && documentdb.Spec.Monitoring.Enabled

	envelopeBytes := parseMemoryToBytes(res.Memory)
	split := ResourceSplit{MonitoringEnabled: monitoring}

	// --- OTel collector (memory) ---
	var otelBytes int64
	if monitoring {
		if o := res.OTel; o != nil && o.Memory != "" && o.Memory != "0" {
			// Explicit override: requests == limits (Guaranteed).
			split.OTel.MemoryRequest = o.Memory
			split.OTel.MemoryLimit = o.Memory
			otelBytes = parseMemoryToBytes(o.Memory)
		} else {
			split.OTel.MemoryRequest = cfg.OTelMemoryRequest
			split.OTel.MemoryLimit = cfg.OTelMemoryLimit
			// Carve the LIMIT (worst case) from the envelope so it is not
			// oversubscribed.
			otelBytes = parseMemoryToBytes(cfg.OTelMemoryLimit)
		}
		split.OTel.CPURequest = firstNonEmpty(otelCPUOverride(res.OTel), cfg.OTelCPURequest)
		split.OTel.CPULimit = otelCPUOverride(res.OTel) // limit only when explicitly set
	}

	// --- Gateway (memory) ---
	var gatewayBytes int64
	if g := res.Gateway; g != nil && g.Memory != "" && g.Memory != "0" {
		split.Gateway.MemoryRequest = g.Memory
		split.Gateway.MemoryLimit = g.Memory
		gatewayBytes = parseMemoryToBytes(g.Memory)
	} else if envelopeBytes > 0 {
		gatewayBytes = int64(float64(envelopeBytes) * cfg.GatewayMemoryFraction)
		if cfg.GatewayMemoryCapBytes > 0 && gatewayBytes > cfg.GatewayMemoryCapBytes {
			gatewayBytes = cfg.GatewayMemoryCapBytes
		}
		q := bytesToQuantity(gatewayBytes)
		split.Gateway.MemoryRequest = q
		split.Gateway.MemoryLimit = q
	}

	// Gateway CPU: explicit override wins, else operator-level limit (request
	// mirrors the limit so the container is Guaranteed on CPU when bounded).
	if cpu := gatewayCPUOverride(res.Gateway); cpu != "" {
		split.Gateway.CPURequest = cpu
		split.Gateway.CPULimit = cpu
	} else if cfg.GatewayCPULimit != "" {
		split.Gateway.CPURequest = cfg.GatewayCPULimit
		split.Gateway.CPULimit = cfg.GatewayCPULimit
	}

	// --- PostgreSQL (remainder) ---
	if d := res.Database; d != nil && d.Memory != "" && d.Memory != "0" {
		split.Postgres.MemoryRequest = d.Memory
		split.Postgres.MemoryLimit = d.Memory
		split.PostgresMemoryBytes = parseMemoryToBytes(d.Memory)
	} else if envelopeBytes > 0 {
		dbBytes := envelopeBytes - gatewayBytes - otelBytes
		if dbBytes < 0 {
			dbBytes = 0
		}
		split.PostgresMemoryBytes = dbBytes
		if dbBytes > 0 {
			q := bytesToQuantity(dbBytes)
			split.Postgres.MemoryRequest = q
			split.Postgres.MemoryLimit = q
		}
	}

	// PostgreSQL CPU: database override wins, else the pod envelope CPU.
	pgCPU := firstNonEmpty(databaseCPUOverride(res.Database), normalizeCPU(res.CPU))
	if pgCPU != "" {
		split.Postgres.CPURequest = pgCPU
		split.Postgres.CPULimit = pgCPU
	}

	return split
}

// --- helpers ---

func gatewayCPUOverride(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return normalizeCPU(c.CPU)
}

func otelCPUOverride(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return normalizeCPU(c.CPU)
}

func databaseCPUOverride(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return normalizeCPU(c.CPU)
}

func normalizeCPU(cpu string) string {
	if cpu == "" || cpu == "0" {
		return ""
	}
	return cpu
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func envOr(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

func parseFloatOr(value, fallback string) float64 {
	s := value
	if s == "" {
		s = fallback
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		// Re-parse the fallback as a last resort.
		if ff, ferr := strconv.ParseFloat(fallback, 64); ferr == nil {
			return ff
		}
		return 0
	}
	return f
}

func parseQuantityOrZero(value string) int64 {
	if value == "" {
		return 0
	}
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return 0
	}
	return q.Value()
}

// bytesToQuantity renders a byte count as a binary-SI Kubernetes quantity string
// (e.g. 6442450944 -> "6Gi"). Values that are not a clean Ki/Mi/Gi multiple fall
// back to the smallest exact binary unit the quantity package can express.
func bytesToQuantity(bytes int64) string {
	return resource.NewQuantity(bytes, resource.BinarySI).String()
}
