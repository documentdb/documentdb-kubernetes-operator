// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// ValidateResources checks that spec.resource is internally consistent under the
// envelope-optional model. It returns a field.ErrorList suitable for the
// validating webhook.
//
// For each dimension (memory, cpu) the rule is:
//   - If the pod envelope (spec.resource.<dim>) is set, the gateway + OTel
//     reservations must leave room for PostgreSQL, and any explicit per-container
//     values must not sum beyond the envelope.
//   - If the envelope is omitted but at least one container sets the dimension,
//     both the gateway and the database must set it explicitly (the gateway
//     memory default is a fraction of the envelope and PostgreSQL is the
//     remainder, so neither can be derived without the envelope). OTel may be
//     omitted; it falls back to an envelope-independent default.
//   - If neither the envelope nor any container sets the dimension, it is left
//     unmanaged (no error).
func ValidateResources(documentdb *dbpreview.DocumentDB, cfg SplitConfig) field.ErrorList {
	res := documentdb.Spec.Resource
	monitoring := documentdb.Spec.Monitoring != nil && documentdb.Spec.Monitoring.Enabled
	base := field.NewPath("spec", "resource")

	// Memory reservations, with defaults applied when a container omits the
	// value (gateway always defaults to its fraction of the envelope).
	memEnv := parseMemoryToBytes(res.Memory)
	memGw := componentMemBytes(res.Gateway)
	if memGw == 0 {
		memGw = gatewayMemoryReservationBytes(memEnv, cfg)
	}
	var memOTel int64
	if monitoring {
		if memOTel = componentMemBytes(res.OTel); memOTel == 0 {
			memOTel = parseMemoryToBytes(cfg.OTelMemoryLimit)
		}
	}

	// CPU reservations. Unlike memory, the gateway only reserves CPU when an
	// operator-level limit is configured.
	cpuEnv := cpuMilli(res.CPU)
	cpuGw := componentCPUMilli(res.Gateway)
	if cpuGw == 0 && cfg.GatewayCPULimit != "" {
		cpuGw = cpuMilli(cfg.GatewayCPULimit)
	}
	var cpuOTel int64
	if monitoring {
		if cpuOTel = componentCPUMilli(res.OTel); cpuOTel == 0 {
			cpuOTel = cpuMilli(cfg.OTelCPURequest)
		}
	}

	errs := validateDimension(base, dimension{
		noun:         "memory",
		envSet:       isSet(res.Memory),
		gwSet:        componentMemSet(res.Gateway),
		dbSet:        componentMemSet(res.Database),
		otelSet:      monitoring && componentMemSet(res.OTel),
		envValue:     res.Memory,
		dbValue:      componentMemoryValue(res.Database),
		envQty:       memEnv,
		gwReserved:   memGw,
		otelReserved: memOTel,
		dbQty:        componentMemBytes(res.Database),
		format:       bytesToQuantity,
	})
	errs = append(errs, validateDimension(base, dimension{
		noun:         "cpu",
		envSet:       isSet(res.CPU),
		gwSet:        componentCPUSet(res.Gateway),
		dbSet:        componentCPUSet(res.Database),
		otelSet:      monitoring && componentCPUSet(res.OTel),
		envValue:     res.CPU,
		dbValue:      componentCPUValue(res.Database),
		envQty:       cpuEnv,
		gwReserved:   cpuGw,
		otelReserved: cpuOTel,
		dbQty:        componentCPUMilli(res.Database),
		format:       milliCPUString,
	})...)
	return errs
}

// dimension is a fully resolved view of one resource dimension (memory or cpu)
// used by validateDimension. Quantities are in the dimension's native unit
// (bytes for memory, milli-CPU for cpu); format renders that unit for messages.
type dimension struct {
	noun                             string // "memory" or "cpu", used in paths and messages
	envSet, gwSet, dbSet, otelSet    bool
	envValue, dbValue                string // raw quantity strings for error display
	envQty, gwReserved, otelReserved int64
	dbQty                            int64
	format                           func(int64) string
}

// validateDimension enforces the envelope-optional rules for a single dimension.
// Memory and CPU share this logic; only the unit and noun differ.
func validateDimension(base *field.Path, d dimension) field.ErrorList {
	var errs field.ErrorList
	if !d.envSet {
		// Envelope omitted: it can only be derived when both the gateway and the
		// database pin the dimension; any other partial configuration is invalid.
		if (d.gwSet || d.dbSet || d.otelSet) && !(d.gwSet && d.dbSet) {
			errs = append(errs, field.Required(base.Child(d.noun),
				fmt.Sprintf("pod %s envelope is required unless %s is set on both spec.resource.gateway and spec.resource.database", d.noun, d.noun)))
		}
		return errs
	}
	reserved := d.gwReserved + d.otelReserved
	switch {
	case reserved >= d.envQty:
		errs = append(errs, field.Invalid(base.Child(d.noun), d.envValue,
			fmt.Sprintf("gateway and OTel %s reservations (%s) leave no %s for PostgreSQL within the pod %s envelope (%s)",
				d.noun, d.format(reserved), d.noun, d.noun, d.envValue)))
	case d.dbSet:
		if total := reserved + d.dbQty; total > d.envQty {
			errs = append(errs, field.Invalid(base.Child("database", d.noun), d.dbValue,
				fmt.Sprintf("sum of gateway + OTel + database %s (%s) exceeds the pod %s envelope (%s)",
					d.noun, d.format(total), d.noun, d.envValue)))
		}
	}
	return errs
}

// --- small accessors ---

func isSet(s string) bool { return s != "" && s != "0" }

func componentMemSet(c *dbpreview.ComponentResources) bool {
	return c != nil && isSet(c.Memory)
}

func componentCPUSet(c *dbpreview.ComponentResources) bool {
	return c != nil && isSet(c.CPU)
}

func componentMemBytes(c *dbpreview.ComponentResources) int64 {
	if c == nil {
		return 0
	}
	return parseMemoryToBytes(c.Memory)
}

func componentMemoryValue(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return c.Memory
}

func componentCPUValue(c *dbpreview.ComponentResources) string {
	if c == nil {
		return ""
	}
	return c.CPU
}

// milliCPUString renders a milli-CPU count as a Kubernetes quantity (e.g. "500m").
func milliCPUString(m int64) string { return fmt.Sprintf("%dm", m) }

func componentCPUMilli(c *dbpreview.ComponentResources) int64 {
	if c == nil {
		return 0
	}
	return cpuMilli(c.CPU)
}

// cpuMilli parses a CPU quantity string to milli-CPU; returns 0 on empty/invalid.
func cpuMilli(s string) int64 {
	if !isSet(s) {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.MilliValue()
}
