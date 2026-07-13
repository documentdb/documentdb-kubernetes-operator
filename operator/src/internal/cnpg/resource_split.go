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
	// OTelCPULimit bounds the OTel collector CPU (a ceiling on burst). Empty
	// leaves the collector's CPU unbounded.
	OTelCPULimit string
}

// ComponentResource is a resolved per-container request/limit pair. Empty
// strings mean "unset" (the operator omits that request/limit).
type ComponentResource struct {
	MemoryRequest string
	MemoryLimit   string
	CPURequest    string
	CPULimit      string
}

// setMemory pins the container's memory request and limit to q (Guaranteed).
func (c *ComponentResource) setMemory(q string) {
	c.MemoryRequest = q
	c.MemoryLimit = q
}

// setCPU pins the container's CPU request and limit to q (Guaranteed).
func (c *ComponentResource) setCPU(q string) {
	c.CPURequest = q
	c.CPULimit = q
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
	capBytes := parseQuantityOr(os.Getenv(util.GATEWAY_MEMORY_CAP_ENV), util.DEFAULT_GATEWAY_MEMORY_CAP)
	return SplitConfig{
		GatewayMemoryFraction: frac,
		GatewayMemoryCapBytes: capBytes,
		GatewayCPULimit:       os.Getenv(util.GATEWAY_CPU_LIMIT_ENV),
		OTelMemoryRequest:     envOr(util.OTEL_MEMORY_REQUEST_ENV, util.DEFAULT_OTEL_MEMORY_REQUEST),
		OTelMemoryLimit:       envOr(util.OTEL_MEMORY_LIMIT_ENV, util.DEFAULT_OTEL_MEMORY_LIMIT),
		OTelCPURequest:        envOr(util.OTEL_CPU_REQUEST_ENV, util.DEFAULT_OTEL_CPU_REQUEST),
		OTelCPULimit:          envOr(util.OTEL_CPU_LIMIT_ENV, util.DEFAULT_OTEL_CPU_LIMIT),
	}
}

// ComputeResourceSplit resolves how the pod memory and CPU envelopes
// (spec.resource.memory / spec.resource.cpu) are divided across the PostgreSQL,
// gateway, and (when monitoring is enabled) OTel collector containers.
//
// The envelope is OPTIONAL. For each dimension:
//   - If the envelope is set, the operator carves it: the gateway and OTel
//     collector reservations are subtracted and PostgreSQL (the sink) gets the
//     remainder. Unset reservations fall back to defaults (gateway memory =
//     min(fraction × envelope, cap); OTel memory = limit default; OTel cpu =
//     request default).
//   - If the envelope is omitted, every container that has an explicit value
//     keeps it; the effective envelope is the sum of the resolved containers.
//     Containers whose default depends on the envelope (gateway memory fraction,
//     PostgreSQL remainder) can only be derived when the envelope is set, so the
//     omitted-envelope path requires those to be explicit — see ValidateResources.
//
// Legacy behavior is preserved: when neither the envelope nor any per-container
// value is set for a dimension, that dimension is left unmanaged (no limits).
func ComputeResourceSplit(documentdb *dbpreview.DocumentDB, cfg SplitConfig) ResourceSplit {
	res := documentdb.Spec.Resource
	monitoring := documentdb.Spec.Monitoring != nil && documentdb.Spec.Monitoring.Enabled

	envelopeBytes := parseMemoryToBytes(res.Memory)
	split := ResourceSplit{MonitoringEnabled: monitoring}

	// --- OTel collector (memory) ---
	var otelBytes int64
	if monitoring {
		if componentMemSet(res.OTel) {
			// Explicit override: requests == limits (Guaranteed).
			split.OTel.setMemory(res.OTel.Memory)
			otelBytes = parseMemoryToBytes(res.OTel.Memory)
		} else {
			split.OTel.MemoryRequest = cfg.OTelMemoryRequest
			split.OTel.MemoryLimit = cfg.OTelMemoryLimit
			// Carve the LIMIT (worst case) from the envelope so it is not
			// oversubscribed.
			otelBytes = parseMemoryToBytes(cfg.OTelMemoryLimit)
		}
		// OTel CPU: an explicit override pins request == limit (Guaranteed);
		// otherwise the collector keeps its Burstable default (request floor +
		// a bounded limit ceiling). CPU is compressible, so the carve-out below
		// only reserves the request from the envelope — the limit just caps burst.
		if cpu := componentCPU(res.OTel); cpu != "" {
			split.OTel.setCPU(cpu)
		} else {
			split.OTel.CPURequest = cfg.OTelCPURequest
			split.OTel.CPULimit = cfg.OTelCPULimit
		}
	}

	// --- Gateway (memory) ---
	var gatewayBytes int64
	if componentMemSet(res.Gateway) {
		split.Gateway.setMemory(res.Gateway.Memory)
		gatewayBytes = parseMemoryToBytes(res.Gateway.Memory)
	} else if envelopeBytes > 0 {
		gatewayBytes = gatewayMemoryReservationBytes(envelopeBytes, cfg)
		split.Gateway.setMemory(bytesToQuantity(gatewayBytes))
	}

	// Gateway CPU: explicit override wins, else operator-level limit (request
	// mirrors the limit so the container is Guaranteed on CPU when bounded).
	if cpu := componentCPU(res.Gateway); cpu != "" {
		split.Gateway.setCPU(cpu)
	} else if cfg.GatewayCPULimit != "" {
		split.Gateway.setCPU(cfg.GatewayCPULimit)
	}

	// --- PostgreSQL (remainder) ---
	if componentMemSet(res.Database) {
		split.Postgres.setMemory(res.Database.Memory)
		split.PostgresMemoryBytes = parseMemoryToBytes(res.Database.Memory)
	} else if envelopeBytes > 0 {
		dbBytes := envelopeBytes - gatewayBytes - otelBytes
		if dbBytes < 0 {
			dbBytes = 0
		}
		split.PostgresMemoryBytes = dbBytes
		if dbBytes > 0 {
			split.Postgres.setMemory(bytesToQuantity(dbBytes))
		}
	}

	// PostgreSQL CPU (sink): database override wins; otherwise the pod CPU
	// envelope minus the gateway and OTel CPU reservations, symmetric with the
	// memory carve-out so the resolved container CPUs sum to the envelope.
	if cpu := componentCPU(res.Database); cpu != "" {
		split.Postgres.setCPU(cpu)
	} else if env := normalizeCPU(res.CPU); env != "" {
		pgCPU := subtractCPU(env, split.Gateway.CPURequest, split.OTel.CPURequest)
		if pgCPU != "" {
			split.Postgres.setCPU(pgCPU)
		}
	}

	return split
}

// gatewayMemoryReservationBytes returns the gateway's memory reservation derived
// from the pod memory envelope: min(fraction × envelope, cap).
func gatewayMemoryReservationBytes(envelopeBytes int64, cfg SplitConfig) int64 {
	b := int64(float64(envelopeBytes) * cfg.GatewayMemoryFraction)
	if cfg.GatewayMemoryCapBytes > 0 && b > cfg.GatewayMemoryCapBytes {
		b = cfg.GatewayMemoryCapBytes
	}
	return b
}

// subtractCPU returns (envelope − Σ reserved) as a milli-CPU quantity string.
// Empty reservations are ignored; a non-positive remainder yields "".
func subtractCPU(envelope string, reserved ...string) string {
	env, err := resource.ParseQuantity(envelope)
	if err != nil {
		return ""
	}
	milli := env.MilliValue()
	for _, r := range reserved {
		if r == "" || r == "0" {
			continue
		}
		q, err := resource.ParseQuantity(r)
		if err != nil {
			continue
		}
		milli -= q.MilliValue()
	}
	if milli <= 0 {
		return ""
	}
	return resource.NewMilliQuantity(milli, resource.DecimalSI).String()
}

// --- helpers ---

// componentCPU returns the component's CPU override, or "" when unset/zero.
func componentCPU(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return normalizeCPU(c.CPU)
}

// normalizeCPU returns cpu unless it is unset/zero, in which case "".
func normalizeCPU(cpu string) string {
	if !isSet(cpu) {
		return ""
	}
	return cpu
}

func envOr(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

// parseFloatOr parses value as a float, falling back to fallback when value is
// empty or unparseable. The result is clamped to [0, 1]: 0 disables the gateway
// carve-out, and values above 1 would otherwise reserve more than the envelope.
func parseFloatOr(value, fallback string) float64 {
	s := value
	if s == "" {
		s = fallback
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Fall back to the documented default (a constant, so ignore its error).
		f, _ = strconv.ParseFloat(fallback, 64)
	}
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// parseQuantityOr parses value as a Kubernetes resource quantity and returns its
// byte value. It falls back to fallback when value is empty or unparseable (so a
// typo'd fleet-wide knob does not silently disable the cap) and clamps negatives
// to 0.
func parseQuantityOr(value, fallback string) int64 {
	s := value
	if s == "" {
		s = fallback
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		if q, err = resource.ParseQuantity(fallback); err != nil {
			return 0
		}
	}
	if v := q.Value(); v > 0 {
		return v
	}
	return 0
}

// bytesToQuantity renders a byte count as a binary-SI Kubernetes quantity string
// (e.g. 6442450944 -> "6Gi"). Values that are not a clean Ki/Mi/Gi multiple fall
// back to the smallest exact binary unit the quantity package can express.
func bytesToQuantity(bytes int64) string {
	return resource.NewQuantity(bytes, resource.BinarySI).String()
}
