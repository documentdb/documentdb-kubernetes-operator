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
	var errs field.ErrorList
	res := documentdb.Spec.Resource
	monitoring := documentdb.Spec.Monitoring != nil && documentdb.Spec.Monitoring.Enabled
	base := field.NewPath("spec", "resource")

	// ----- memory -----
	envMemSet := isSet(res.Memory)
	gwMemSet := componentMemSet(res.Gateway)
	dbMemSet := componentMemSet(res.Database)
	otelMemSet := monitoring && componentMemSet(res.OTel)

	if !envMemSet {
		if (gwMemSet || dbMemSet || otelMemSet) && !(gwMemSet && dbMemSet) {
			errs = append(errs, field.Required(base.Child("memory"),
				"pod memory envelope is required unless memory is set on both spec.resource.gateway and spec.resource.database"))
		}
	} else {
		envBytes := parseMemoryToBytes(res.Memory)
		gwBytes := componentMemBytes(res.Gateway)
		if gwBytes == 0 {
			gwBytes = gatewayMemoryReservationBytes(envBytes, cfg)
		}
		otelBytes := int64(0)
		if monitoring {
			otelBytes = componentMemBytes(res.OTel)
			if otelBytes == 0 {
				otelBytes = parseMemoryToBytes(cfg.OTelMemoryLimit)
			}
		}
		reserved := gwBytes + otelBytes
		switch {
		case reserved >= envBytes:
			errs = append(errs, field.Invalid(base.Child("memory"), res.Memory,
				fmt.Sprintf("gateway and OTel memory reservations (%s) leave no memory for PostgreSQL within the pod memory envelope (%s)",
					bytesToQuantity(reserved), res.Memory)))
		case dbMemSet:
			total := reserved + componentMemBytes(res.Database)
			if total > envBytes {
				errs = append(errs, field.Invalid(base.Child("database", "memory"), res.Database.Memory,
					fmt.Sprintf("sum of gateway + OTel + database memory (%s) exceeds the pod memory envelope (%s)",
						bytesToQuantity(total), res.Memory)))
			}
		}
	}

	// ----- cpu -----
	envCPUSet := isSet(res.CPU)
	gwCPUSet := componentCPUSet(res.Gateway)
	dbCPUSet := componentCPUSet(res.Database)
	otelCPUSet := monitoring && componentCPUSet(res.OTel)

	if !envCPUSet {
		if (gwCPUSet || dbCPUSet || otelCPUSet) && !(gwCPUSet && dbCPUSet) {
			errs = append(errs, field.Required(base.Child("cpu"),
				"pod cpu envelope is required unless cpu is set on both spec.resource.gateway and spec.resource.database"))
		}
	} else {
		envMilli := cpuMilli(res.CPU)
		gwMilli := componentCPUMilli(res.Gateway)
		if gwMilli == 0 && cfg.GatewayCPULimit != "" {
			gwMilli = cpuMilli(cfg.GatewayCPULimit)
		}
		otelMilli := int64(0)
		if monitoring {
			otelMilli = componentCPUMilli(res.OTel)
			if otelMilli == 0 {
				otelMilli = cpuMilli(cfg.OTelCPURequest)
			}
		}
		reserved := gwMilli + otelMilli
		switch {
		case reserved >= envMilli:
			errs = append(errs, field.Invalid(base.Child("cpu"), res.CPU,
				fmt.Sprintf("gateway and OTel cpu reservations (%dm) leave no cpu for PostgreSQL within the pod cpu envelope (%s)",
					reserved, res.CPU)))
		case dbCPUSet:
			total := reserved + componentCPUMilli(res.Database)
			if total > envMilli {
				errs = append(errs, field.Invalid(base.Child("database", "cpu"), res.Database.CPU,
					fmt.Sprintf("sum of gateway + OTel + database cpu (%dm) exceeds the pod cpu envelope (%s)",
						total, res.CPU)))
			}
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
