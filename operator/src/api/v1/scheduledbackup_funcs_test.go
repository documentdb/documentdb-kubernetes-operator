// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package v1

import (
	"reflect"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/robfig/cron"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ScheduledBackup", func() {

	Describe("CreateBackup", func() {
		It("creates a Backup with expected fields", func() {
			retentionDays := 7
			sb := &ScheduledBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-scheduled-backup",
					Namespace: "default",
				},
				Spec: ScheduledBackupSpec{
					Cluster: cnpgv1.LocalObjectReference{
						Name: "test-cluster",
					},
					RetentionDays: &retentionDays,
				},
			}

			fixedTime := time.Date(2025, 10, 20, 15, 30, 45, 0, time.UTC)
			backup := sb.CreateBackup(fixedTime)

			Expect(backup.Name).To(Equal("my-scheduled-backup-20251020-153045"))
			Expect(backup.Namespace).To(Equal("default"))
			Expect(backup.Labels).To(HaveKeyWithValue("scheduledbackup", "my-scheduled-backup"))
			Expect(backup.Spec.Cluster.Name).To(Equal("test-cluster"))
			Expect(backup.Spec.RetentionDays).ToNot(BeNil())
			Expect(*backup.Spec.RetentionDays).To(Equal(7))
		})

		It("creates a Backup without RetentionDays when not specified", func() {
			sb := &ScheduledBackup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-scheduled-backup",
					Namespace: "default",
				},
				Spec: ScheduledBackupSpec{
					Cluster: cnpgv1.LocalObjectReference{
						Name: "test-cluster",
					},
				},
			}

			fixedTime := time.Date(2025, 10, 20, 15, 30, 45, 0, time.UTC)
			backup := sb.CreateBackup(fixedTime)

			Expect(backup.Name).To(Equal("my-scheduled-backup-20251020-153045"))
			Expect(backup.Namespace).To(Equal("default"))
			Expect(backup.Labels).To(HaveKeyWithValue("scheduledbackup", "my-scheduled-backup"))
			Expect(backup.Spec.Cluster.Name).To(Equal("test-cluster"))
			Expect(reflect.ValueOf(backup.Spec.RetentionDays).IsNil()).To(BeTrue())
		})
	})

	Describe("getNextScheduleTime", func() {
		sb := ScheduledBackup{
			Spec: ScheduledBackupSpec{
				Schedule: "0 0 * * *",
			},
			Status: ScheduledBackupStatus{
				NextScheduledTime: &metav1.Time{Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		}
		schedule, _ := cron.ParseStandard("0 0 * * *")

		It("returns next time based on the last backup", func() {
			backup := &Backup{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
				},
			}

			nextScheduleTime := sb.GetNextScheduleTime(schedule, backup)
			Expect(nextScheduleTime).To(Equal(schedule.Next(backup.CreationTimestamp.Time)))
		})

		It("returns Status.NextScheduledTime when no backups exist", func() {
			nextScheduleTime := sb.GetNextScheduleTime(schedule, nil)
			Expect(nextScheduleTime).To(Equal(sb.Status.NextScheduledTime.Time))
		})

		It("returns next time based on now when no backups exist and Status.NextScheduledTime is not set", func() {
			sb := ScheduledBackup{
				Spec: ScheduledBackupSpec{
					Schedule: "0 0 * * *",
				},
			}
			nextScheduleTime := sb.GetNextScheduleTime(schedule, nil)
			Expect(nextScheduleTime.After(time.Now()))
		})
	})
})
