// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Backup", func() {
	var intPtr = func(v int) *int { return &v }

	Describe("CreateCNPGBackup", func() {
		It("creates a CNPG Backup with expected fields and owner reference", func() {
			// prepare scheme with known types so SetControllerReference can find GVKs
			scheme := runtime.NewScheme()
			Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
			gv := schema.GroupVersion{Group: "preview.test", Version: "preview"}
			scheme.AddKnownTypes(gv, &Backup{}, &BackupList{})

			backup := &Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-backup",
					Namespace: "my-ns",
				},
				Spec: BackupSpec{
					Cluster: cnpgv1.LocalObjectReference{Name: "my-cluster"},
				},
			}

			cnpg, err := backup.CreateCNPGBackup(scheme, "my-cluster-x")
			Expect(err).To(BeNil())
			Expect(cnpg).ToNot(BeNil())

			// metadata and spec checks
			Expect(cnpg.Name).To(Equal("my-backup"))
			Expect(cnpg.Namespace).To(Equal("my-ns"))
			Expect(cnpg.Spec.Method).To(Equal(cnpgv1.BackupMethodVolumeSnapshot))
			Expect(cnpg.Spec.Cluster.Name).To(Equal("my-cluster-x"))

			// owner reference set by SetControllerReference
			Expect(len(cnpg.OwnerReferences)).To(BeNumerically(">", 0))
			owner := cnpg.OwnerReferences[0]
			Expect(owner.Name).To(Equal("my-backup"))
			Expect(owner.Kind).To(Equal("Backup"))
			Expect(owner.APIVersion).To(Equal(gv.String()))
		})
	})

	Describe("UpdateStatus", func() {
		It("updates fields from cnpg backup and computes ExpiredAt when done", func() {
			startedAt := metav1.NewTime(time.Date(2025, 4, 1, 1, 0, 0, 0, time.UTC))
			stoppedAt := metav1.NewTime(time.Date(2025, 4, 1, 2, 0, 0, 0, time.UTC))

			cnpg := &cnpgv1.Backup{
				Status: cnpgv1.BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StartedAt: &startedAt,
					StoppedAt: &stoppedAt,
					Error:     "none",
				},
			}

			backup := &Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "my-backup",
					Namespace:         "my-ns",
					CreationTimestamp: metav1.NewTime(time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)),
				},
				Spec: BackupSpec{}, // no retention specified -> default 30 days
				Status: BackupStatus{
					Phase:     cnpgv1.BackupPhase(""),
					StartedAt: nil,
					StoppedAt: nil,
					Message:   "",
				},
			}

			needsUpdate := backup.UpdateStatus(cnpg, nil, "")
			Expect(needsUpdate).To(BeTrue())
			Expect(string(backup.Status.Phase)).To(Equal(cnpgv1.BackupPhaseCompleted))
			Expect(backup.Status.StartedAt).To(Equal(&startedAt))
			Expect(backup.Status.StoppedAt).To(Equal(&stoppedAt))
			Expect(backup.Status.Message).To(Equal("none"))
			// ExpiredAt should be StoppedAt + 30 days (default)
			Expect(backup.Status.ExpiredAt).ToNot(BeNil())
			Expect(backup.Status.ExpiredAt.Time.Equal(stoppedAt.Time.Add(30 * 24 * time.Hour))).To(BeTrue())
		})

		It("does not update when there are no changes", func() {
			startedAt := metav1.NewTime(time.Date(2025, 5, 1, 1, 0, 0, 0, time.UTC))
			stoppedAt := metav1.NewTime(time.Date(2025, 5, 1, 2, 0, 0, 0, time.UTC))
			expiredAt := metav1.NewTime(time.Date(2025, 5, 31, 2, 0, 0, 0, time.UTC))

			cnpg := &cnpgv1.Backup{
				Status: cnpgv1.BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StartedAt: &startedAt,
					StoppedAt: &stoppedAt,
					Error:     "none",
				},
			}

			backup := &Backup{
				Spec: BackupSpec{},
				Status: BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StartedAt: &startedAt,
					StoppedAt: &stoppedAt,
					Message:   "none",
					ExpiredAt: &expiredAt,
				},
			}

			needsUpdate := backup.UpdateStatus(cnpg, nil, "")
			Expect(needsUpdate).To(BeFalse())
		})

		It("captures the source schema version once and leaves it immutable", func() {
			cnpg := &cnpgv1.Backup{
				Status: cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseCompleted},
			}
			backup := &Backup{Spec: BackupSpec{}}

			// First observation records the source schema version.
			Expect(backup.UpdateStatus(cnpg, nil, "0.110.0")).To(BeTrue())
			Expect(backup.Status.SchemaVersion).To(Equal("0.110.0"))

			// A later, differing source schema version must NOT overwrite it
			// (it reflects the schema at backup time), and must not by itself
			// mark the status dirty.
			Expect(backup.UpdateStatus(cnpg, nil, "0.112.0")).To(BeFalse())
			Expect(backup.Status.SchemaVersion).To(Equal("0.110.0"))

			// An empty source schema version is a no-op and never clears it.
			Expect(backup.UpdateStatus(cnpg, nil, "")).To(BeFalse())
			Expect(backup.Status.SchemaVersion).To(Equal("0.110.0"))
		})
	})

	Describe("CalculateExpirationTime", func() {
		It("returns nil if backup is not done", func() {
			backup := &Backup{
				Status: BackupStatus{
					Phase: cnpgv1.BackupPhaseRunning,
				},
			}
			Expect(backup.CalculateExpirationTime(nil)).To(BeNil())
		})

		It("uses Spec.RetentionDays when specified", func() {
			base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			stopped := metav1.NewTime(base)
			backup := &Backup{
				Spec: BackupSpec{
					RetentionDays: intPtr(2),
				},
				Status: BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StoppedAt: &stopped,
				},
			}

			exp := backup.CalculateExpirationTime(nil)
			Expect(exp).ToNot(BeNil())
			Expect(exp.Time.Equal(base.Add(48 * time.Hour))).To(BeTrue())
		})

		It("uses BackupConfiguration.RetentionDays when Spec.RetentionDays is nil", func() {
			base := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
			stopped := metav1.NewTime(base)
			backup := &Backup{
				Spec: BackupSpec{}, // RetentionDays nil
				Status: BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StoppedAt: &stopped,
				},
			}
			cfg := &BackupConfiguration{RetentionDays: 3}

			exp := backup.CalculateExpirationTime(cfg)
			Expect(exp).ToNot(BeNil())
			Expect(exp.Time.Equal(base.Add(72 * time.Hour))).To(BeTrue())
		})

		It("defaults to 30 days and uses CreationTimestamp when StoppedAt is nil", func() {
			base := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
			backup := &Backup{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.NewTime(base),
				},
				Spec: BackupSpec{}, // RetentionDays nil
				Status: BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StoppedAt: nil,
				},
			}

			exp := backup.CalculateExpirationTime(nil)
			Expect(exp).ToNot(BeNil())
			Expect(exp.Time.Equal(base.Add(30 * 24 * time.Hour))).To(BeTrue())
		})
	})

	Describe("areTimesEqual", func() {
		It("returns true for nil nil", func() {
			Expect(areTimesEqual(nil, nil)).To(BeTrue())
		})

		It("returns false for nil and non-nil", func() {
			t := metav1.NewTime(time.Now())
			Expect(areTimesEqual(nil, &t)).To(BeFalse())
			Expect(areTimesEqual(&t, nil)).To(BeFalse())
		})

		It("returns true for identical times and false for different", func() {
			base := time.Now().Truncate(time.Second)
			t1 := metav1.NewTime(base)
			t2 := metav1.NewTime(base)
			t3 := metav1.NewTime(base.Add(time.Minute))

			Expect(areTimesEqual(&t1, &t2)).To(BeTrue())
			Expect(areTimesEqual(&t1, &t3)).To(BeFalse())
		})
	})

	Describe("IsDone", func() {
		It("returns true when phase is Completed", func() {
			status := &BackupStatus{
				Phase: cnpgv1.BackupPhaseCompleted,
			}
			Expect(status.IsDone()).To(BeTrue())
		})

		It("returns true when phase is Failed", func() {
			status := &BackupStatus{
				Phase: cnpgv1.BackupPhaseFailed,
			}
			Expect(status.IsDone()).To(BeTrue())
		})

		It("returns true when phase is Skipped", func() {
			status := &BackupStatus{
				Phase: BackupPhaseSkipped,
			}
			Expect(status.IsDone()).To(BeTrue())
		})

		It("returns false when phase is Running", func() {
			status := &BackupStatus{
				Phase: cnpgv1.BackupPhaseRunning,
			}
			Expect(status.IsDone()).To(BeFalse())
		})

		It("returns false when phase is empty", func() {
			status := &BackupStatus{
				Phase: cnpgv1.BackupPhase(""),
			}
			Expect(status.IsDone()).To(BeFalse())
		})
	})

	Describe("IsExpired", func() {
		It("returns false when ExpiredAt is nil", func() {
			status := &BackupStatus{ExpiredAt: nil}
			Expect(status.IsExpired()).To(BeFalse())
		})

		It("returns true when ExpiredAt is in the past", func() {
			past := metav1.NewTime(time.Now().Add(-1 * time.Hour))
			status := &BackupStatus{ExpiredAt: &past}
			Expect(status.IsExpired()).To(BeTrue())
		})

		It("returns false when ExpiredAt is in the future", func() {
			future := metav1.NewTime(time.Now().Add(1 * time.Hour))
			status := &BackupStatus{ExpiredAt: &future}
			Expect(status.IsExpired()).To(BeFalse())
		})
	})
})

var _ = Describe("BackupList", func() {
	Describe("IsBackupRunning", func() {
		It("returns false when all backups are in terminal phases", func() {
			backupList := &BackupList{
				Items: []Backup{
					{
						Status: BackupStatus{
							Phase: cnpgv1.BackupPhaseCompleted,
						},
					},
					{
						Status: BackupStatus{
							Phase: cnpgv1.BackupPhaseFailed,
						},
					},
				},
			}
			Expect(backupList.IsBackupRunning()).To(BeFalse())
		})

		It("returns false when backup list is empty", func() {
			backupList := &BackupList{
				Items: []Backup{},
			}
			Expect(backupList.IsBackupRunning()).To(BeFalse())
		})

		It("returns true when at least one backup is running among completed backups", func() {
			backupList := &BackupList{
				Items: []Backup{
					{
						Status: BackupStatus{
							Phase: cnpgv1.BackupPhaseCompleted,
						},
					},
					{
						Status: BackupStatus{
							Phase: cnpgv1.BackupPhaseRunning,
						},
					},
					{
						Status: BackupStatus{
							Phase: cnpgv1.BackupPhaseFailed,
						},
					},
				},
			}
			Expect(backupList.IsBackupRunning()).To(BeTrue())
		})
	})

	Describe("GetLastBackup", func() {
		It("returns nil for empty list", func() {
			backupList := &BackupList{
				Items: []Backup{},
			}
			Expect(backupList.GetLastBackup()).To(BeNil())
		})

		It("returns the most recent backup by CreationTimestamp", func() {
			t1 := metav1.NewTime(time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC))
			t2 := metav1.NewTime(time.Date(2025, 6, 1, 11, 0, 0, 0, time.UTC))
			t3 := metav1.NewTime(time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC))

			backupList := &BackupList{
				Items: []Backup{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "b1",
							CreationTimestamp: t1,
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "b2",
							CreationTimestamp: t2,
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "b3",
							CreationTimestamp: t3,
						},
					},
				},
			}

			last := backupList.GetLastBackup()
			Expect(last).ToNot(BeNil())
			Expect(last.Name).To(Equal("b2"))
			// ensure pointer points into the slice
			Expect(last).To(Equal(&backupList.Items[1]))
		})
	})
})
