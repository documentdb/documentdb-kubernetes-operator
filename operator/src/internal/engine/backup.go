// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

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

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// BackupAdapter maps a product Backup custom resource onto the shared backup
// spec and status types. Spec and Status return pointers into the object.
type BackupAdapter[B client.Object] interface {
	New() B
	NewList() client.ObjectList
	// Items returns pointers to the items of a list built by NewList.
	Items(list client.ObjectList) []B
	Spec(B) *dbv1.BackupSpec
	Status(B) *dbv1.BackupStatus
}

// BackupReconciler takes on-demand volume-snapshot backups of a product
// cluster through CNPG and expires them by retention policy.
type BackupReconciler[T client.Object, B client.Object] struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Cluster  product.ClusterAdapter[T]
	Backups  BackupAdapter[B]
	// Replication resolves the replication role of the cluster; nil means
	// single-region primary. Only Context is used.
	Replication ReplicationHooks[T]
}

func (r *BackupReconciler[T, B]) replicationContext(ctx context.Context, cluster T) (*util.ReplicationContext, error) {
	if r.Replication != nil {
		return r.Replication.Context(ctx, cluster)
	}
	spec := r.Cluster.Spec(cluster)
	return util.NewSingleRegionContext(cluster.GetName(), spec.Environment, spec.StorageClass), nil
}

// Reconcile handles the reconciliation loop for Backup resources.
func (r *BackupReconciler[T, B]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Backup resource
	backup := r.Backups.New()
	if err := r.Get(ctx, req.NamespacedName, backup); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Backup resource not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Backup")
		return ctrl.Result{}, err
	}
	status := r.Backups.Status(backup)

	// Delete the Backup resource if it has expired
	if status.IsExpired() {
		r.Recorder.Event(backup, "Normal", "BackupExpired", "Backup has expired and will be deleted")
		if err := r.Delete(ctx, backup); err != nil {
			r.Recorder.Event(backup, "Warning", "BackupDeleteFailed", "Failed to delete expired Backup: "+err.Error())
			return ctrl.Result{}, err
		}
		r.Recorder.Event(backup, "Normal", "BackupDeleted", "Expired Backup has been deleted")
		return ctrl.Result{}, nil
	}

	// If the backup is already done and not expired, requeue to check expiration
	if status.IsDone() && status.ExpiredAt != nil {
		requeueAfter := time.Until(status.ExpiredAt.Time)
		if requeueAfter < 0 {
			requeueAfter = time.Minute
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Fetch the associated cluster
	cluster := r.Cluster.New()
	clusterKey := client.ObjectKey{
		Name:      r.Backups.Spec(backup).Cluster.Name,
		Namespace: backup.GetNamespace(),
	}
	if err := r.Get(ctx, clusterKey, cluster); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to get associated "+r.Cluster.Profile().Naming.Kind+" cluster: "+err.Error(), nil)
	}
	clusterSpec := r.Cluster.Spec(cluster)

	// Ensure VolumeSnapshotClass exists
	if err := r.EnsureVolumeSnapshotClass(ctx, clusterSpec.Environment); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to ensure VolumeSnapshotClass: "+err.Error(), clusterSpec.Backup)
	}

	// Get or create the CNPG Backup
	cnpgBackup := &cnpgv1.Backup{}
	cnpgBackupKey := client.ObjectKey{
		Name:      backup.GetName(),
		Namespace: backup.GetNamespace(),
	}
	if err := r.Get(ctx, cnpgBackupKey, cnpgBackup); err != nil {
		if apierrors.IsNotFound(err) {

			// Skip backup if the cluster is not primary
			replicationContext, err := r.replicationContext(ctx, cluster)
			if err != nil {
				logger.Error(err, "Failed to determine replication context")
				return ctrl.Result{}, err
			}
			if !replicationContext.IsPrimary() {
				return r.SetBackupPhaseSkipped(ctx, backup, "Backups can only be created from the primary cluster", clusterSpec.Backup)
			}
			if !replicationContext.EndpointEnabled() {
				logger.Info("Backup deferred: primary cluster endpoint not ready, waiting for promotion to complete")
				return ctrl.Result{RequeueAfter: time.Minute * 1}, nil
			}

			return r.CreateCNPGBackup(ctx, backup, clusterSpec.Backup, replicationContext)
		}
		logger.Error(err, "Failed to get CNPG Backup")
		return ctrl.Result{}, err
	}

	return r.UpdateBackupStatus(ctx, backup, cnpgBackup, clusterSpec.Backup, r.Cluster.Status(cluster).SchemaVersion)
}

// EnsureVolumeSnapshotClass creates a VolumeSnapshotClass based on the cloud environment
func (r *BackupReconciler[T, B]) EnsureVolumeSnapshotClass(ctx context.Context, environment string) error {
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
	vsc := BuildVolumeSnapshotClass(environment)
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

// BuildVolumeSnapshotClass builds a VolumeSnapshotClass based on cloud provider
func BuildVolumeSnapshotClass(environment string) *snapshotv1.VolumeSnapshotClass {
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

// CreateCNPGBackup creates a new CNPG Backup resource
func (r *BackupReconciler[T, B]) CreateCNPGBackup(ctx context.Context, backup B, backupConfiguration *dbv1.BackupConfiguration, replicationContext *util.ReplicationContext) (ctrl.Result, error) {
	cnpgClusterName := replicationContext.CNPGClusterName

	cnpgBackup, err := dbv1.NewCNPGBackup(r.Scheme, backup, cnpgClusterName)
	if err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to initialize backup: "+err.Error(), backupConfiguration)
	}

	if err := r.Create(ctx, cnpgBackup); err != nil {
		return r.SetBackupPhaseFailed(ctx, backup, "Failed to initialize backup: "+err.Error(), backupConfiguration)
	}

	r.Recorder.Event(backup, "Normal", "BackupInitialized", "Successfully initialized backup")

	// Requeue to check status
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *BackupReconciler[T, B]) expirationTime(backup B, backupConfiguration *dbv1.BackupConfiguration) *metav1.Time {
	return dbv1.BackupExpirationTime(r.Backups.Spec(backup), r.Backups.Status(backup), backup.GetCreationTimestamp(), backupConfiguration)
}

func (r *BackupReconciler[T, B]) patchStatus(ctx context.Context, backup B, original client.Object) error {
	if err := r.Status().Patch(ctx, backup, client.MergeFrom(original)); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "Failed to patch Backup status")
		return err
	}
	return nil
}

// UpdateBackupStatus reconciles and persists the Backup's status, returning the reconcile result.
func (r *BackupReconciler[T, B]) UpdateBackupStatus(ctx context.Context, backup B, cnpgBackup *cnpgv1.Backup, backupConfiguration *dbv1.BackupConfiguration, sourceSchemaVersion string) (ctrl.Result, error) {
	original := backup.DeepCopyObject().(client.Object)
	status := r.Backups.Status(backup)
	needsUpdate := dbv1.UpdateBackupStatus(r.Backups.Spec(backup), status, backup.GetCreationTimestamp(), cnpgBackup, backupConfiguration, sourceSchemaVersion)

	if needsUpdate {
		if err := r.patchStatus(ctx, backup, original); err != nil {
			return ctrl.Result{}, err
		}
	}

	if status.IsDone() && status.ExpiredAt != nil {
		requeueAfter := time.Until(status.ExpiredAt.Time)
		if requeueAfter < 0 {
			requeueAfter = time.Minute
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// Backup is still in progress, requeue to check status again
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *BackupReconciler[T, B]) setTerminalPhase(ctx context.Context, backup B, phase cnpgv1.BackupPhase, reason, message string, backupConfiguration *dbv1.BackupConfiguration) (ctrl.Result, error) {
	original := backup.DeepCopyObject().(client.Object)

	status := r.Backups.Status(backup)
	status.Phase = phase
	status.Message = message
	status.ExpiredAt = r.expirationTime(backup, backupConfiguration)

	if err := r.patchStatus(ctx, backup, original); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(backup, "Warning", reason, message)
	requeueAfter := time.Until(status.ExpiredAt.Time)
	if requeueAfter < 0 {
		requeueAfter = time.Minute
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetBackupPhaseFailed marks the backup failed and records errMessage.
func (r *BackupReconciler[T, B]) SetBackupPhaseFailed(ctx context.Context, backup B, errMessage string, backupConfiguration *dbv1.BackupConfiguration) (ctrl.Result, error) {
	return r.setTerminalPhase(ctx, backup, cnpgv1.BackupPhaseFailed, "BackupFailed", errMessage, backupConfiguration)
}

// SetBackupPhaseSkipped marks the backup skipped and records message.
func (r *BackupReconciler[T, B]) SetBackupPhaseSkipped(ctx context.Context, backup B, message string, backupConfiguration *dbv1.BackupConfiguration) (ctrl.Result, error) {
	return r.setTerminalPhase(ctx, backup, dbv1.BackupPhaseSkipped, "BackupSkipped", message, backupConfiguration)
}

// SetupWithManager registers the reconciler with the manager.
func (r *BackupReconciler[T, B]) SetupWithManager(mgr ctrl.Manager) error {
	// Register VolumeSnapshotClass with the scheme
	if err := snapshotv1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(r.Backups.New()).
		Owns(&cnpgv1.Backup{}).
		Complete(r)
}
