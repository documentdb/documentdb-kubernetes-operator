// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

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
	"github.com/documentdb/documentdb-operator/internal/telemetry"
)

// ScheduledBackupReconciler reconciles a ScheduledBackup object
type ScheduledBackupReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Recorder     record.EventRecorder
	TelemetryMgr *telemetry.Manager
}

// Reconcile handles the reconciliation loop for ScheduledBackup resources.
func (r *ScheduledBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ScheduledBackup resource
	scheduledBackup := &dbpreview.ScheduledBackup{}
	if err := r.Get(ctx, req.NamespacedName, scheduledBackup); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ScheduledBackup resource not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ScheduledBackup")
		return ctrl.Result{}, err
	}

	// Ensure ScheduledBackup is owned by the referenced Cluster so it's garbage collected when the Cluster is deleted.
	err := r.ensureOwnerReference(ctx, scheduledBackup)
	if err != nil {
		logger.Error(err, "Failed to ensure owner reference on ScheduledBackup")
		return ctrl.Result{}, err
	}

	// Parse cron schedule
	schedule, err := cron.ParseStandard(scheduledBackup.Spec.Schedule)
	if err != nil {
		logger.Error(err, "Failed to parse schedule", "schedule", scheduledBackup.Spec.Schedule)
		r.Recorder.Event(scheduledBackup, "Warning", "InvalidSchedule", "Failed to parse schedule: "+err.Error())
		return ctrl.Result{}, err
	}

	// If there is an ongoing backup, wait for it to finish before starting a new one
	backupList := &dbpreview.BackupList{}
	if err := r.List(ctx, backupList, client.InNamespace(scheduledBackup.Namespace), client.MatchingFields{"spec.cluster": scheduledBackup.Spec.Cluster.Name}); err != nil {
		logger.Error(err, "Failed to list backups")
		return ctrl.Result{}, err
	}

	if backupList.IsBackupRunning() {
		// If a backup is currently running, requeue after a short delay
		logger.Info("A backup is currently running, requeuing")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// If it's time to create a backup
	nextScheduleTime := scheduledBackup.GetNextScheduleTime(schedule, backupList.GetLastBackup())
	now := time.Now()
	if !now.Before(nextScheduleTime) {
		backup := scheduledBackup.CreateBackup(now)
		logger.Info("Creating new backup", "backupName", backup.Name)
		if err := r.Create(ctx, backup); err != nil {
			logger.Error(err, "Failed to create backup", "backupName", backup.Name)
			r.Recorder.Event(scheduledBackup, "Warning", "BackupCreation", "Failed to create backup: "+err.Error())
			return ctrl.Result{}, err
		}

		scheduledBackup.Status.LastScheduledTime = &metav1.Time{Time: now}

		// Calculate next run time
		nextScheduleTime = schedule.Next(now)
	}

	scheduledBackup.Status.NextScheduledTime = &metav1.Time{Time: nextScheduleTime}
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

func (r *ScheduledBackupReconciler) ensureOwnerReference(ctx context.Context, scheduledBackup *dbpreview.ScheduledBackup) error {
	if len(scheduledBackup.OwnerReferences) > 0 {
		// Owner reference already set
		return nil
	}

	// Fetch the associated DocumentDB cluster
	cluster := &dbpreview.DocumentDB{}
	clusterKey := client.ObjectKey{
		Name:      scheduledBackup.Spec.Cluster.Name,
		Namespace: scheduledBackup.Namespace,
	}
	if err := r.Get(ctx, clusterKey, cluster); err != nil {
		r.Recorder.Event(scheduledBackup, "Warning", "ClusterNotFound", "Failed to find associated DocumentDB cluster: "+err.Error())
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

// SetupWithManager sets up the controller with the Manager.
func (r *ScheduledBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Register field index for spec.cluster so we can query Backups by cluster name
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &dbpreview.Backup{}, "spec.cluster", func(rawObj client.Object) []string {
		backup := rawObj.(*dbpreview.Backup)
		return []string{backup.Spec.Cluster.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&dbpreview.ScheduledBackup{}).
		Complete(r)
}
