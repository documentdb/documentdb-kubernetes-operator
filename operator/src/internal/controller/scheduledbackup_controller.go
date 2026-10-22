// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/engine"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// ScheduledBackupReconciler reconciles a ScheduledBackup object. The logic
// lives in engine.ScheduledBackupReconciler.
type ScheduledBackupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *ScheduledBackupReconciler) engine() *engine.ScheduledBackupReconciler[*dbpreview.DocumentDB, *dbpreview.ScheduledBackup, *dbpreview.Backup] {
	return &engine.ScheduledBackupReconciler[*dbpreview.DocumentDB, *dbpreview.ScheduledBackup, *dbpreview.Backup]{
		Client:    r.Client,
		Scheme:    r.Scheme,
		Recorder:  r.Recorder,
		Cluster:   product.DocumentDBAdapter{},
		Scheduled: product.DocumentDBScheduledBackupAdapter{},
		Backups:   product.DocumentDBBackupAdapter{},
	}
}

// Reconcile handles the reconciliation loop for ScheduledBackup resources.
func (r *ScheduledBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.engine().Reconcile(ctx, req)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScheduledBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.engine().SetupWithManager(mgr)
}
