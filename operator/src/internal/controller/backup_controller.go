// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/telemetry"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// BackupReconciler reconciles a Backup object
type BackupReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	TelemetryMgr *telemetry.Manager
}

// Reconcile handles the reconciliation loop for Backup resources.
func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Backup resource
	backup := &dbpreview.Backup{}
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Backup resource not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Backup")
		return ctrl.Result{}, err
	}

	// Delete the Backup resource if it has expired
	if backup.Status.IsExpired() {
		r.Recorder.Event(backup, "Normal", "BackupExpired", "Backup has expired and will be deleted")
		if err := r.Delete(ctx, backup); err != nil {
			r.Recorder.Event(backup, "Warning", "BackupDeleteFailed", "Failed to delete expired Backup: "+err.Error())
			return ctrl.Result{}, err
		}
		r.Recorder.Event(backup, "Normal", "BackupDeleted", "Expired Backup has been deleted")
		return ctrl.Result{}, nil
	}

	// If the backup is already done and not expired, requeue to check expiration
	if backup.Status.IsDone() && backup.Status.ExpiredAt != nil {
		requeueAfter := time.Until(backup.Status.ExpiredAt.Time)
		if requeueAfter < 0 {
			requeueAfter = time.Minute
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Fetch the associated DocumentDB cluster
	cluster := &dbpreview.DocumentDB{}
	clusterKey := client.ObjectKey{
		Name:      backup.Spec.Cluster.Name,
		Namespace: backup.Namespace,
	}
	if err := r.Get(ctx, clusterKey, cluster); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to get associated DocumentDB cluster: "+err.Error(), nil)
	}

	// Ensure VolumeSnapshotClass exists
	if err := r.ensureVolumeSnapshotClass(ctx, cluster.Spec.Environment); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to ensure VolumeSnapshotClass: "+err.Error(), cluster.Spec.Backup)
	}

	// Get or create the CNPG Backup
	cnpgBackup := &cnpgv1.Backup{}
	cnpgBackupKey := client.ObjectKey{
		Name:      backup.Name,
		Namespace: backup.Namespace,
	}
	if err := r.Get(ctx, cnpgBackupKey, cnpgBackup); err != nil {
		if apierrors.IsNotFound(err) {

			// Skip backup if the cluster is not primary
			replicationContext, err := util.GetReplicationContext(ctx, r.Client, *cluster)
			if err != nil {
				logger.Error(err, "Failed to determine replication context")
				return ctrl.Result{}, err
			}
			if !replicationContext.IsPrimary() {
				return r.SetBackupPhaseSkipped(ctx, backup, "Backups can only be created from the primary cluster", cluster.Spec.Backup)
			}
			if !replicationContext.EndpointEnabled() {
				logger.Info("Backup deferred: primary cluster endpoint not ready, waiting for promotion to complete")
				return ctrl.Result{RequeueAfter: time.Minute * 1}, nil
			}

			return r.createCNPGBackup(ctx, backup, cluster)
		}
		logger.Error(err, "Failed to get CNPG Backup")
		return ctrl.Result{}, err
	}

	// Update status based on CNPG Backup status
	return r.updateBackupStatus(ctx, backup, cnpgBackup, cluster.Spec.Backup)
}

// ensureVolumeSnapshotClass creates a VolumeSnapshotClass based on the cloud environment
func (r *BackupReconciler) ensureVolumeSnapshotClass(ctx context.Context, environment string) error {
	logger := log.FromContext(ctx)

	// Check if any VolumeSnapshotClass exists
	vscList := &snapshotv1.VolumeSnapshotClassList{}
	if err := r.List(ctx, vscList); err != nil {
		logger.Error(err, "Failed to list VolumeSnapshotClasses")
		return err
	}

	for _, vsc := range vscList.Items {
		if val, ok := vsc.Annotations["snapshot.storage.kubernetes.io/is-default-class"]; ok && val == "true" {
			return nil
		}
	}

	r.Recorder.Event(nil, "Normal", "VolumeSnapshotClass", "No default VolumeSnapshotClass found, creating one")
	vsc := buildVolumeSnapshotClass(environment)
	if vsc == nil {
		err := fmt.Errorf("Please create a default VolumeSnapshotClass before creating backups")
		logger.Error(err, "Failed to build VolumeSnapshotClass", "environment", environment)
		return err
	}

	if err := r.Create(ctx, vsc); err != nil {
		logger.Error(err, "Failed to create VolumeSnapshotClass")
		return err
	}

	r.Recorder.Event(nil, "Normal", "VolumeSnapshotClass", "Successfully created VolumeSnapshotClass "+vsc.Name)
	return nil
}

// buildVolumeSnapshotClass builds a VolumeSnapshotClass based on cloud provider
func buildVolumeSnapshotClass(environment string) *snapshotv1.VolumeSnapshotClass {
	deletionPolicy := snapshotv1.VolumeSnapshotContentDelete

	var driver string
	var name string

	switch environment {
	case "aks":
		driver = "disk.csi.azure.com"
		name = "azure-disk-snapclass"
	default:
		// TODO: add support for other cloud providers
		return nil
	}

	return &snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"snapshot.storage.kubernetes.io/is-default-class": "true",
			},
		},
		Driver:         driver,
		DeletionPolicy: deletionPolicy,
	}
}

// createCNPGBackup creates a new CNPG Backup resource
func (r *BackupReconciler) createCNPGBackup(ctx context.Context, backup *dbpreview.Backup, cluster *dbpreview.DocumentDB) (ctrl.Result, error) {
	cnpgClusterName := cluster.Name
	if cluster.Spec.ClusterReplication != nil && cluster.Spec.ClusterReplication.Primary != "" {
		cnpgClusterName = cluster.Spec.ClusterReplication.Primary
	}

	cnpgBackup, err := backup.CreateCNPGBackup(r.Scheme, cnpgClusterName)
	if err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to initialize backup: "+err.Error(), cluster.Spec.Backup)
	}

	if err := r.Create(ctx, cnpgBackup); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to initialize backup: "+err.Error(), cluster.Spec.Backup)
	}

	r.Recorder.Event(backup, "Normal", "BackupInitialized", "Successfully initialized backup")

	// Requeue to check status
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// updateBackupStatus updates the Backup status based on CNPG Backup status
func (r *BackupReconciler) updateBackupStatus(ctx context.Context, backup *dbpreview.Backup, cnpgBackup *cnpgv1.Backup, backupConfiguration *dbpreview.BackupConfiguration) (ctrl.Result, error) {
	original := backup.DeepCopy()
	needsUpdate := backup.UpdateStatus(cnpgBackup, backupConfiguration)

	if needsUpdate {
		if err := r.Status().Patch(ctx, backup, client.MergeFrom(original)); err != nil {
			logger := log.FromContext(ctx)
			logger.Error(err, "Failed to patch Backup status")
			return ctrl.Result{}, err
		}
	}

	if backup.Status.IsDone() && backup.Status.ExpiredAt != nil {
		requeueAfter := time.Until(backup.Status.ExpiredAt.Time)
		if requeueAfter < 0 {
			requeueAfter = time.Minute
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Backup is still in progress, requeue to check status again
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *BackupReconciler) SetBackupPhaseFailed(ctx context.Context, backup *dbpreview.Backup, errMessage string, backupConfiguration *dbpreview.BackupConfiguration) (ctrl.Result, error) {
	original := backup.DeepCopy()

	backup.Status.Phase = cnpgv1.BackupPhaseFailed
	backup.Status.Message = errMessage
	backup.Status.ExpiredAt = backup.CalculateExpirationTime(backupConfiguration)

	if err := r.Status().Patch(ctx, backup, client.MergeFrom(original)); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "Failed to patch Backup status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(backup, "Warning", "BackupFailed", errMessage)
	requeueAfter := time.Until(backup.Status.ExpiredAt.Time)
	if requeueAfter < 0 {
		requeueAfter = time.Minute
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *BackupReconciler) SetBackupPhaseSkipped(ctx context.Context, backup *dbpreview.Backup, message string, backupConfiguration *dbpreview.BackupConfiguration) (ctrl.Result, error) {
	original := backup.DeepCopy()

	backup.Status.Phase = dbpreview.BackupPhaseSkipped
	backup.Status.Message = message
	backup.Status.ExpiredAt = backup.CalculateExpirationTime(backupConfiguration)

	if err := r.Status().Patch(ctx, backup, client.MergeFrom(original)); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "Failed to patch Backup status")
		return ctrl.Result{}, err
	}

	r.Recorder.Event(backup, "Warning", "BackupSkipped", message)
	requeueAfter := time.Until(backup.Status.ExpiredAt.Time)
	if requeueAfter < 0 {
		requeueAfter = time.Minute
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Register VolumeSnapshotClass with the scheme
	if err := snapshotv1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&dbpreview.Backup{}).
		Owns(&cnpgv1.Backup{}).
		Complete(r)
}
