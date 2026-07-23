// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
		Expect(snap.Failed).To(Equal(int64(1))) // skipped is not a failure
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

	It("prunes dedup state for garbage-collected backups without double-counting", func() {
		stopped := now.Add(-30 * time.Minute)
		fc := &fakeBackupClient{
			sb:       &previewv1.ScheduledBackup{},
			children: []previewv1.Backup{mkBackup("b1", cnpgv1.BackupPhaseCompleted, stopped)},
		}
		v, m := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		Expect(v.seenCompleted).To(HaveLen(1))
		Expect(m.Snapshot().Completed).To(Equal(int64(1)))

		// b1 is garbage-collected and replaced by a new backup b2.
		fc.children = []previewv1.Backup{mkBackup("b2", cnpgv1.BackupPhaseCompleted, stopped)}
		v.checkOnce(context.Background(), now)

		// Map stays bounded to the live set; b1 pruned, b2 counted once.
		Expect(v.seenCompleted).To(HaveLen(1))
		Expect(m.Snapshot().Completed).To(Equal(int64(2)))
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

var _ = Describe("Verifier.checkScheduling liveness", func() {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	It("warns once when scheduling stalls past nextScheduledTime + grace", func() {
		// nextScheduledTime is overdue by more than livenessGrace and no
		// backup has been scheduled since → the scheduler appears stalled.
		overdue := now.Add(-livenessGrace).Add(-time.Minute)
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{
				Status: previewv1.ScheduledBackupStatus{
					NextScheduledTime: &metav1.Time{Time: overdue},
				},
			},
		}
		v, _ := newTestVerifier(fc)

		v.checkOnce(context.Background(), now)
		Expect(v.stallWarnedFor).To(Equal(overdue))
		Expect(stallWarnings(v)).To(Equal(1))

		// Same overdue deadline → warn-once, no duplicate.
		v.checkOnce(context.Background(), now)
		Expect(stallWarnings(v)).To(Equal(1))

		// A new (still overdue) nextScheduledTime re-arms the warning.
		reArmed := now.Add(-livenessGrace).Add(-30 * time.Second)
		fc.sb.Status.NextScheduledTime = &metav1.Time{Time: reArmed}
		v.checkOnce(context.Background(), now)
		Expect(v.stallWarnedFor).To(Equal(reArmed))
		Expect(stallWarnings(v)).To(Equal(2))
	})

	It("does not warn when the next scheduled time is still within grace", func() {
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{
				Status: previewv1.ScheduledBackupStatus{
					// 1m overdue < 5m grace.
					NextScheduledTime: &metav1.Time{Time: now.Add(-time.Minute)},
				},
			},
		}
		v, _ := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(stallWarnings(v)).To(BeZero())
		Expect(v.stallWarnedFor.IsZero()).To(BeTrue())
	})

	It("does not warn when a backup was scheduled after the deadline", func() {
		overdue := now.Add(-livenessGrace).Add(-time.Minute)
		fc := &fakeBackupClient{
			sb: &previewv1.ScheduledBackup{
				Status: previewv1.ScheduledBackupStatus{
					// A backup fired just after nextScheduledTime → not stalled.
					LastScheduledTime: &metav1.Time{Time: overdue.Add(time.Second)},
					NextScheduledTime: &metav1.Time{Time: overdue},
				},
			},
		}
		v, _ := newTestVerifier(fc)
		v.checkOnce(context.Background(), now)
		Expect(stallWarnings(v)).To(BeZero())
	})
})

var _ = Describe("Verifier completion-stall oracle", func() {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	It("flags a stall when backups keep scheduling but never complete", func() {
		fc := &fakeBackupClient{sb: &previewv1.ScheduledBackup{}}
		v, m := newTestVerifier(fc)

		// Successive schedules, each with only a failing child backup.
		for i := 1; i <= completionStallThreshold; i++ {
			t := now.Add(time.Duration(i) * time.Hour)
			fc.sb.Status.LastScheduledTime = &metav1.Time{Time: t}
			fc.children = []previewv1.Backup{mkBackup(fmt.Sprintf("f%d", i), cnpgv1.BackupPhaseFailed, time.Time{})}
			v.checkOnce(context.Background(), t)
		}

		snap := m.Snapshot()
		Expect(snap.Completed).To(BeZero())
		Expect(snap.MaxScheduledWithoutCompletion).To(Equal(int64(completionStallThreshold)))
		Expect(snap.HasCompletionStall()).To(BeTrue())
	})

	It("does not flag a stall when completions keep pace with scheduling", func() {
		fc := &fakeBackupClient{sb: &previewv1.ScheduledBackup{}}
		v, m := newTestVerifier(fc)

		for i := 1; i <= completionStallThreshold+3; i++ {
			t := now.Add(time.Duration(i) * time.Hour)
			fc.sb.Status.LastScheduledTime = &metav1.Time{Time: t}
			// The freshly scheduled backup completes in the same cycle.
			fc.children = []previewv1.Backup{mkBackup(fmt.Sprintf("c%d", i), cnpgv1.BackupPhaseCompleted, t)}
			v.checkOnce(context.Background(), t)
		}

		snap := m.Snapshot()
		Expect(snap.Completed).To(Equal(int64(completionStallThreshold + 3)))
		Expect(snap.HasCompletionStall()).To(BeFalse())
	})

	It("resets the running gap once a completion recovers the path", func() {
		fc := &fakeBackupClient{sb: &previewv1.ScheduledBackup{}}
		v, m := newTestVerifier(fc)

		// Two schedules fail (gap climbs to 2, still under threshold)...
		for i := 1; i <= 2; i++ {
			t := now.Add(time.Duration(i) * time.Hour)
			fc.sb.Status.LastScheduledTime = &metav1.Time{Time: t}
			fc.children = []previewv1.Backup{mkBackup(fmt.Sprintf("f%d", i), cnpgv1.BackupPhaseFailed, time.Time{})}
			v.checkOnce(context.Background(), t)
		}
		Expect(m.Snapshot().MaxScheduledWithoutCompletion).To(Equal(int64(2)))

		// ...then one completes, resetting the running gap.
		t := now.Add(3 * time.Hour)
		fc.sb.Status.LastScheduledTime = &metav1.Time{Time: t}
		fc.children = []previewv1.Backup{mkBackup("c1", cnpgv1.BackupPhaseCompleted, t)}
		v.checkOnce(context.Background(), t)

		Expect(v.scheduledSinceCompleted).To(BeZero())
		Expect(m.Snapshot().HasCompletionStall()).To(BeFalse())
	})
})

// stallWarnings counts scheduling-stall warnings recorded on the verifier's
// journal.
func stallWarnings(v *Verifier) int {
	n := 0
	for _, e := range v.journal.Events() {
		if e.Level == journal.LevelWarn && strings.Contains(e.Message, "stalled") {
			n++
		}
	}
	return n
}
