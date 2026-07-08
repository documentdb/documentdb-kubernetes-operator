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

// mkBackup builds a child Backup with the given phase, retention and
// stopped/expired times. Zero times are left nil.
func mkBackup(name string, phase cnpgv1.BackupPhase, retentionDays int, stopped, expired time.Time) previewv1.Backup {
	rd := retentionDays
	b := previewv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       previewv1.BackupSpec{RetentionDays: &rd},
		Status:     previewv1.BackupStatus{Phase: phase},
	}
	if !stopped.IsZero() {
		b.Status.StoppedAt = &metav1.Time{Time: stopped}
	}
	if !expired.IsZero() {
		b.Status.ExpiredAt = &metav1.Time{Time: expired}
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
		expired := stopped.Add(24 * time.Hour)
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{
				mkBackup("b1", cnpgv1.BackupPhaseCompleted, 1, stopped, expired),
				mkBackup("b2", previewv1.BackupPhaseSkipped, 1, time.Time{}, time.Time{}),
				mkBackup("b3", cnpgv1.BackupPhaseFailed, 1, time.Time{}, time.Time{}),
			},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		v.checkOnce(context.Background(), now) // idempotent

		snap := m.Snapshot()
		Expect(snap.Completed).To(Equal(int64(1)))
		Expect(snap.Failed).To(Equal(int64(2))) // skipped + failed are both terminal
		Expect(snap.LastChildCount).To(Equal(int64(3)))
		Expect(snap.RetentionViolations).To(BeZero())
		Expect(snap.GCViolations).To(BeZero())
	})

	It("passes retention when expiredAt matches stoppedAt + retention", func() {
		stopped := now.Add(-time.Hour)
		expired := stopped.Add(2 * 24 * time.Hour)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, 2, stopped, expired)},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionViolations).To(BeZero())
	})

	It("flags a retention miscalculation once", func() {
		stopped := now.Add(-time.Hour)
		// Wrong: only 12h instead of 24h.
		expired := stopped.Add(12 * time.Hour)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, 1, stopped, expired)},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		v.checkOnce(context.Background(), now) // must not double-count
		Expect(m.Snapshot().RetentionViolations).To(Equal(int64(1)))
	})

	It("tolerates sub-tolerance skew", func() {
		stopped := now.Add(-time.Hour)
		expired := stopped.Add(24 * time.Hour).Add(30 * time.Second)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, 1, stopped, expired)},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().RetentionViolations).To(BeZero())
	})

	It("flags a garbage-collection failure once", func() {
		stopped := now.Add(-48 * time.Hour)
		// Expired 30m ago (well past gcGrace) but still present.
		expired := now.Add(-30 * time.Minute)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, 1, stopped, expired)},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().GCViolations).To(Equal(int64(1)))
	})

	It("does not flag GC within the grace window", func() {
		stopped := now.Add(-48 * time.Hour)
		expired := now.Add(-1 * time.Minute) // within gcGrace
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, 1, stopped, expired)},
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(m.Snapshot().GCViolations).To(BeZero())
	})

	It("survives transient read errors without panicking", func() {
		fc := &fakeBackupClient{
			sbErr:    errors.New("apiserver throttled"),
			childErr: errors.New("apiserver throttled"),
		}
		v, m := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		snap := m.Snapshot()
		Expect(snap.VerifyCycles).To(Equal(int64(1)))
		Expect(snap.HasRetentionFailure()).To(BeFalse())
	})
})
