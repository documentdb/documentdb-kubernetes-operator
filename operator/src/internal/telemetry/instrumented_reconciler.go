// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// InstrumentedReconciler wraps a reconciler to automatically track
// reconciliation duration and errors as cross-cutting concerns.
type InstrumentedReconciler struct {
	Inner        reconcile.Reconciler
	Telemetry    *Manager
	ResourceType string
}

// Reconcile delegates to the inner reconciler and tracks duration/errors.
func (r *InstrumentedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	result, err := r.Inner.Reconcile(ctx, req)
	duration := time.Since(start).Seconds()

	if r.Telemetry != nil && r.Telemetry.IsEnabled() {
		status := "success"
		if err != nil {
			status = "error"
		}

		r.Telemetry.Metrics.TrackReconciliationDuration(ReconciliationDurationMetric{
			ResourceType:    r.ResourceType,
			Operation:       "reconcile",
			DurationSeconds: duration,
			Status:          status,
		})

		if err != nil {
			r.Telemetry.Events.TrackReconciliationError(ReconciliationErrorEvent{
				ResourceType:     r.ResourceType,
				ResourceID:       "", // Not available at wrapper level
				NamespaceHash:    HashNamespace(req.Namespace),
				ErrorType:        "reconciliation",
				ErrorMessage:     categorizeError(err),
				RetryCount:       0,
				ResolutionStatus: "pending",
			})
		}
	}

	return result, err
}

// categorizeError returns a safe, sanitized error category.
func categorizeError(err error) string {
	if err == nil {
		return ""
	}
	// Return a coarse-grained description, never raw error strings
	return "reconciliation-error"
}
