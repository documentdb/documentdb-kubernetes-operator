// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"time"

	"github.com/robfig/cron"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// BackupClusterField is the Backup field index holding the cluster name.
const BackupClusterField = "spec.cluster"

// ScheduledBackupAdapter maps a product ScheduledBackup custom resource onto
// the shared scheduled-backup spec and status types.
type ScheduledBackupAdapter[S client.Object] interface {
	New() S
	Spec(S) *dbpreview.ScheduledBackupSpec
	Status(S) *dbpreview.ScheduledBackupStatus
}

// ScheduledBackupReconciler creates Backups of a product cluster on a cron schedule.
type ScheduledBackupReconciler[T client.Object, S client.Object, B client.Object] struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Cluster   product.ClusterAdapter[T]
	Scheduled ScheduledBackupAdapter[S]
	Backups   BackupAdapter[B]
}

// Reconcile handles the reconciliation loop for ScheduledBackup resources.
func (r *ScheduledBackupReconciler[T, S, B]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ScheduledBackup resource
	scheduledBackup := r.Scheduled.New()
	if err := r.Get(ctx, req.NamespacedName, scheduledBackup); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ScheduledBackup resource not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ScheduledBackup")
		return ctrl.Result{}, err
	}
	spec := r.Scheduled.Spec(scheduledBackup)

	// Ensure ScheduledBackup is owned by the referenced Cluster so it's garbage collected when the Cluster is deleted.
	err := r.EnsureOwnerReference(ctx, scheduledBackup)
	if err != nil {
		logger.Error(err, "Failed to ensure owner reference on ScheduledBackup")
		return ctrl.Result{}, err
	}

	// Parse cron schedule
	schedule, err := cron.ParseStandard(spec.Schedule)
	if err != nil {
		logger.Error(err, "Failed to parse schedule", "schedule", spec.Schedule)
		r.Recorder.Event(scheduledBackup, "Warning", "InvalidSchedule", "Failed to parse schedule: "+err.Error())
		return ctrl.Result{}, err
	}

	// If there is an ongoing backup, wait for it to finish before starting a new one
	backupList := r.Backups.NewList()
	if err := r.List(ctx, backupList, client.InNamespace(scheduledBackup.GetNamespace()), client.MatchingFields{BackupClusterField: spec.Cluster.Name}); err != nil {
		logger.Error(err, "Failed to list backups")
		return ctrl.Result{}, err
	}
	backups := r.Backups.Items(backupList)

	if r.isBackupRunning(backups) {
		// If a backup is currently running, requeue after a short delay
		logger.Info("A backup is currently running, requeuing")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// If it's time to create a backup
	var lastBackupCreated *metav1.Time
	if last, ok := lastBackup(backups); ok {
		created := last.GetCreationTimestamp()
		lastBackupCreated = &created
	}
	status := r.Scheduled.Status(scheduledBackup)
	nextScheduleTime := dbpreview.NextScheduleTime(schedule, status, lastBackupCreated)
	now := time.Now()
	if !now.Before(nextScheduleTime) {
		backup := r.newBackup(scheduledBackup, now)
		logger.Info("Creating new backup", "backupName", backup.GetName())
		if err := r.Create(ctx, backup); err != nil {
			logger.Error(err, "Failed to create backup", "backupName", backup.GetName())
			r.Recorder.Event(scheduledBackup, "Warning", "BackupCreation", "Failed to create backup: "+err.Error())
			return ctrl.Result{}, err
		}

		status.LastScheduledTime = &metav1.Time{Time: now}

		// Calculate next run time
		nextScheduleTime = schedule.Next(now)
	}

	status.NextScheduledTime = &metav1.Time{Time: nextScheduleTime}
	if err := r.Status().Update(ctx, scheduledBackup); err != nil {
		logger.Error(err, "Failed to update ScheduledBackup status with next scheduled time")
		return ctrl.Result{}, err
	}

	logger.Info("Next backup scheduled", "time", nextScheduleTime)
	r.Recorder.Event(scheduledBackup, "Normal", "BackupSchedule", "Next backup scheduled at: "+nextScheduleTime.String())

	// Requeue at next schedule time
	requeueAfter := time.Until(nextScheduleTime)
	if requeueAfter < 0 {
		requeueAfter = time.Minute
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// newBackup builds the Backup a ScheduledBackup creates at now.
func (r *ScheduledBackupReconciler[T, S, B]) newBackup(scheduledBackup S, now time.Time) B {
	spec := r.Scheduled.Spec(scheduledBackup)
	backup := r.Backups.New()
	backup.SetName(dbpreview.ScheduledBackupName(scheduledBackup.GetName(), now))
	backup.SetNamespace(scheduledBackup.GetNamespace())
	backup.SetLabels(map[string]string{
		dbpreview.ScheduledBackupLabel: scheduledBackup.GetName(),
	})
	*r.Backups.Spec(backup) = dbpreview.BackupSpec{
		Cluster:       spec.Cluster,
		RetentionDays: spec.RetentionDays,
	}
	return backup
}

func (r *ScheduledBackupReconciler[T, S, B]) isBackupRunning(backups []B) bool {
	for _, backup := range backups {
		if !r.Backups.Status(backup).IsDone() {
			return true
		}
	}
	return false
}

// lastBackup returns the most recently created backup.
func lastBackup[B client.Object](backups []B) (B, bool) {
	var last B
	found := false
	for _, backup := range backups {
		if !found || backup.GetCreationTimestamp().After(last.GetCreationTimestamp().Time) {
			last = backup
			found = true
		}
	}
	return last, found
}

// EnsureOwnerReference makes the referenced cluster the controller of the ScheduledBackup.
func (r *ScheduledBackupReconciler[T, S, B]) EnsureOwnerReference(ctx context.Context, scheduledBackup S) error {
	if len(scheduledBackup.GetOwnerReferences()) > 0 {
		// Owner reference already set
		return nil
	}

	// Fetch the associated cluster
	cluster := r.Cluster.New()
	clusterKey := client.ObjectKey{
		Name:      r.Scheduled.Spec(scheduledBackup).Cluster.Name,
		Namespace: scheduledBackup.GetNamespace(),
	}
	if err := r.Get(ctx, clusterKey, cluster); err != nil {
		r.Recorder.Event(scheduledBackup, "Warning", "ClusterNotFound", "Failed to find associated "+r.Cluster.Profile().Naming.Kind+" cluster: "+err.Error())
		return err
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(cluster, scheduledBackup, r.Scheme); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "Failed to set owner reference on ScheduledBackup")
		return err
	}

	// Update the ScheduledBackup with the new owner reference
	if err := r.Update(ctx, scheduledBackup); err != nil {
		logger := log.FromContext(ctx)
		logger.Error(err, "Failed to update ScheduledBackup with owner reference")
		return err
	}

	return nil
}

// SetupWithManager registers the reconciler with the manager.
func (r *ScheduledBackupReconciler[T, S, B]) SetupWithManager(mgr ctrl.Manager) error {
	// Register field index for spec.cluster so we can query Backups by cluster name
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), r.Backups.New(), BackupClusterField, func(rawObj client.Object) []string {
		backup, ok := rawObj.(B)
		if !ok {
			return nil
		}
		return []string{r.Backups.Spec(backup).Cluster.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(r.Scheduled.New()).
		Complete(r)
}
