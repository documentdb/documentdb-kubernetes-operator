// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// mockReconciler is a test reconciler that returns configurable results.
type mockReconciler struct {
	result ctrl.Result
	err    error
}

func (m *mockReconciler) Reconcile(_ context.Context, _ ctrl.Request) (ctrl.Result, error) {
	return m.result, m.err
}

func TestInstrumentedReconciler_Success(t *testing.T) {
	inner := &mockReconciler{result: ctrl.Result{}, err: nil}
	instrumented := &InstrumentedReconciler{
		Inner:        inner,
		Telemetry:    nil, // No telemetry manager — should not panic
		ResourceType: "DocumentDB",
	}

	result, err := instrumented.Reconcile(context.Background(), reconcile.Request{})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}
}

func TestInstrumentedReconciler_Error(t *testing.T) {
	expectedErr := context.DeadlineExceeded
	inner := &mockReconciler{result: ctrl.Result{}, err: expectedErr}
	instrumented := &InstrumentedReconciler{
		Inner:        inner,
		Telemetry:    nil,
		ResourceType: "Backup",
	}

	_, err := instrumented.Reconcile(context.Background(), reconcile.Request{})
	if err != expectedErr {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
}

func TestInstrumentedReconciler_RequeueAfter(t *testing.T) {
	inner := &mockReconciler{result: ctrl.Result{RequeueAfter: 10 * time.Second}, err: nil}
	instrumented := &InstrumentedReconciler{
		Inner:        inner,
		Telemetry:    nil,
		ResourceType: "ScheduledBackup",
	}

	result, err := instrumented.Reconcile(context.Background(), reconcile.Request{})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result.RequeueAfter != 10*time.Second {
		t.Errorf("expected 10s requeue, got %v", result.RequeueAfter)
	}
}

func TestCategorizeError(t *testing.T) {
	if msg := categorizeError(nil); msg != "" {
		t.Errorf("expected empty string for nil error, got '%s'", msg)
	}
	if msg := categorizeError(context.DeadlineExceeded); msg != "reconciliation-error" {
		t.Errorf("expected 'reconciliation-error', got '%s'", msg)
	}
}
