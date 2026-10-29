// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	"fmt"
	"time"

	"github.com/robfig/cron"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateBackup generates a new Backup resource for this ScheduledBackup.
// The backup name is generated with a timestamp suffix to ensure uniqueness.
func (scheduledBackup *ScheduledBackup) CreateBackup(now time.Time) *Backup {
	return &Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ScheduledBackupName(scheduledBackup.Name, now),
			Namespace: scheduledBackup.Namespace,
			Labels: map[string]string{
				ScheduledBackupLabel: scheduledBackup.Name,
			},
		},
		Spec: BackupSpec{
			Cluster:       scheduledBackup.Spec.Cluster,
			RetentionDays: scheduledBackup.Spec.RetentionDays,
		},
	}
}

// ScheduledBackupLabel labels a Backup with the ScheduledBackup that created it.
const ScheduledBackupLabel = "scheduledbackup"

// ScheduledBackupName generates the name of the Backup a ScheduledBackup
// creates at now, with a timestamp suffix to ensure uniqueness.
func ScheduledBackupName(scheduledBackupName string, now time.Time) string {
	return fmt.Sprintf("%s-%s", scheduledBackupName, now.Format("20060102-150405"))
}

// GetNextScheduleTime calculates the next scheduled time
func (scheduledBackup *ScheduledBackup) GetNextScheduleTime(schedule cron.Schedule, lastBackup *Backup) time.Time {
	var lastBackupCreated *metav1.Time
	if lastBackup != nil {
		lastBackupCreated = &lastBackup.CreationTimestamp
	}
	return NextScheduleTime(schedule, &scheduledBackup.Status, lastBackupCreated)
}

// NextScheduleTime calculates the next scheduled time from the creation time
// of the last backup, the recorded next time, or now.
func NextScheduleTime(schedule cron.Schedule, status *ScheduledBackupStatus, lastBackupCreated *metav1.Time) time.Time {
	// If there is a last backup, calculate the next schedule time based on its creation time
	if lastBackupCreated != nil && lastBackupCreated.Time.After(time.Time{}) {
		return schedule.Next(lastBackupCreated.Time)
	}

	if status.NextScheduledTime != nil {
		return status.NextScheduledTime.Time
	}

	return schedule.Next(time.Now())
}
