// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/engine"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// BackupReconciler reconciles a Backup object. The logic lives in
// engine.BackupReconciler.
type BackupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *BackupReconciler) engine() *engine.BackupReconciler[*dbv1.DocumentDB, *dbv1.Backup] {
	return &engine.BackupReconciler[*dbv1.DocumentDB, *dbv1.Backup]{
		Client:      r.Client,
		Scheme:      r.Scheme,
		Recorder:    r.Recorder,
		Cluster:     product.DocumentDBAdapter{},
		Backups:     product.DocumentDBBackupAdapter{},
		Replication: documentDBReplication{&DocumentDBReconciler{Client: r.Client}},
	}
}

// Reconcile handles the reconciliation loop for Backup resources.
func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.engine().Reconcile(ctx, req)
}

// createCNPGBackup creates a new CNPG Backup resource
func (r *BackupReconciler) createCNPGBackup(ctx context.Context, backup *dbv1.Backup, cluster *dbv1.DocumentDB, replicationContext *util.ReplicationContext) (ctrl.Result, error) {
	return r.engine().CreateCNPGBackup(ctx, backup, cluster.Spec.Backup, replicationContext)
}

// updateBackupStatus reconciles and persists the Backup's status, returning the reconcile result.
func (r *BackupReconciler) updateBackupStatus(ctx context.Context, backup *dbv1.Backup, cnpgBackup *cnpgv1.Backup, backupConfiguration *dbv1.BackupConfiguration, sourceSchemaVersion string) (ctrl.Result, error) {
	return r.engine().UpdateBackupStatus(ctx, backup, cnpgBackup, backupConfiguration, sourceSchemaVersion)
}

func (r *BackupReconciler) SetBackupPhaseFailed(ctx context.Context, backup *dbv1.Backup, errMessage string, backupConfiguration *dbv1.BackupConfiguration) (ctrl.Result, error) {
	return r.engine().SetBackupPhaseFailed(ctx, backup, errMessage, backupConfiguration)
}

func (r *BackupReconciler) SetBackupPhaseSkipped(ctx context.Context, backup *dbv1.Backup, message string, backupConfiguration *dbv1.BackupConfiguration) (ctrl.Result, error) {
	return r.engine().SetBackupPhaseSkipped(ctx, backup, message, backupConfiguration)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.engine().SetupWithManager(mgr)
}
