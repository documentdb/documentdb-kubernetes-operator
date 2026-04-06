// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetResourceTelemetryID returns the telemetry ID for a Kubernetes resource.
// Uses metadata.uid directly — it's globally unique, immutable, always present,
// zero-cost, and contains no PII.
func GetResourceTelemetryID(obj client.Object) string {
	return string(obj.GetUID())
}
