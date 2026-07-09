// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"errors"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

// fakeBackupClient is a minimal Client stub for unit tests.
type fakeBackupClient struct {
	ensureCalls []ensureArgs
	ensureErr   error
	sb          *previewv1.ScheduledBackup
	sbErr       error
	children    []previewv1.Backup
	childErr    error
}

type ensureArgs struct {
	name          string
	schedule      string
	retentionDays int
}

func (f *fakeBackupClient) EnsureScheduledBackup(_ context.Context, name, schedule string, retentionDays int) error {
	f.ensureCalls = append(f.ensureCalls, ensureArgs{name, schedule, retentionDays})
	return f.ensureErr
}

func (f *fakeBackupClient) GetScheduledBackup(_ context.Context, _ string) (*previewv1.ScheduledBackup, error) {
	return f.sb, f.sbErr
}

func (f *fakeBackupClient) ListScheduledChildBackups(_ context.Context, _ string) ([]previewv1.Backup, error) {
	return f.children, f.childErr
}

// mkBackup builds a child Backup with the given phase and stopped time.
// A zero stopped time is left nil. RetentionDays/ExpiredAt are omitted
// because the leak oracle derives its deadline from the verifier config,
// not from the backup's own fields.
func mkBackup(name string, phase cnpgv1.BackupPhase, stopped time.Time) previewv1.Backup {
	b := previewv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     previewv1.BackupStatus{Phase: phase},
	}
	if !stopped.IsZero() {
		b.Status.StoppedAt = &metav1.Time{Time: stopped}
	}
	return b
}

func newTestVerifier(client Client) (*Verifier, *Metrics) {
	m := NewMetrics()
	v := NewVerifier(client, journal.New(), m, Config{
		ScheduledBackupName: "cluster-longhaul",
		Schedule:            "0 */6 * * *",
		RetentionDays:       1,
		VerifyInterval:      time.Minute,
	})
	return v, m
}

var _ = Describe("Verifier.Bootstrap", func() {
	It("ensures the ScheduledBackup with configured parameters", func() {
		fc := &fakeBackupClient{}
		v, _ := newTestVerifier(fc)
		Expect(v.Bootstrap(context.Background())).To(Succeed())
		Expect(fc.ensureCalls).To(HaveLen(1))
		Expect(fc.ensureCalls[0]).To(Equal(ensureArgs{"cluster-longhaul", "0 */6 * * *", 1}))
	})

	It("wraps ensure errors", func() {
		fc := &fakeBackupClient{ensureErr: errors.New("boom")}
		v, _ := newTestVerifier(fc)
		Expect(v.Bootstrap(context.Background())).To(MatchError(ContainSubstring("boom")))
	})
})

var _ = Describe("Verifier.checkOnce", func() {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	It("counts scheduling advances without double counting", func() {
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{
				Status: previewv1.ScheduledBackupStatus{
					LastScheduledTime: &metav1.Time{Time: now.Add(-time.Hour)},
				},
			},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().Scheduled).To(Equal(int64(1)))

		// Same lastScheduledTime → no new count.
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().Scheduled).To(Equal(int64(1)))

		// Advance → counted.
		fc.sb.Status.LastScheduledTime = &metav1.Time{Time: now.Add(-30 * time.Minute)}
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().Scheduled).To(Equal(int64(2)))
	})

	It("counts completed and failed child backups once each", func() {
		stopped := now.Add(-30 * time.Minute)
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{
				mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped),
				mkBackup("b2", previewv1.BackupPhaseSkipped, time.Time{}),
				mkBackup("b3", cnpgv1.BackupPhaseFailed, time.Time{}),
			},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		v.checkOnce(context.Background(), now) // idempotent

		snap := m.Snapshot()
		Expect(snap.Completed).To(Equal(int64(1)))
		Expect(snap.Failed).To(Equal(int64(2))) // skipped + failed are both terminal
		Expect(snap.LastChildCount).To(Equal(int64(3)))
		Expect(snap.RetentionLeaks).To(BeZero())
	})

	It("does not flag a fresh completed backup within its retention window", func() {
		// retentionDays=1; stopped 1h ago → well within the 1-day window.
		stopped := now.Add(-time.Hour)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped)},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionLeaks).To(BeZero())
	})

	It("flags a retention leak when a backup outlives its window, once", func() {
		// retentionDays=1 (verifier config); stopped 48h ago and still present
		// → well past the 1-day window + grace.
		stopped := now.Add(-48 * time.Hour)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped)},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		v.checkOnce(context.Background(), now) // must not double-count
		Expect(m.Snapshot().RetentionLeaks).To(Equal(int64(1)))
	})

	It("does not flag a leak within the GC grace window", func() {
		// stopped just over 1 day ago but inside the 10m GC grace.
		stopped := now.Add(-24 * time.Hour).Add(-1 * time.Minute)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped)},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionLeaks).To(BeZero())
	})

	It("ignores incomplete backups for leak detection", func() {
		// A never-completed backup has no stoppedAt → not a leak candidate.
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseRunning, time.Time{})},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionLeaks).To(BeZero())
	})

	It("uses the backup's own retention, not the run config, for the deadline", func() {
		// Run config is retentionDays=1, but this backup was created under a
		// 7-day policy (e.g. a run started earlier with different params).
		// At 2 days old it is well within its own window → not a leak.
		stopped := now.Add(-48 * time.Hour)
		b := mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped)
		rd := 7
		b.Spec.RetentionDays = &rd
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{b},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionLeaks).To(BeZero())
	})

	It("survives transient read errors without panicking", func() {
		fc := &fakeBackupClient{
			sbErr:    errors.New("apiserver throttled"),
			childErr: errors.New("apiserver throttled"),
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().HasRetentionLeak()).To(BeFalse())
	})
})
