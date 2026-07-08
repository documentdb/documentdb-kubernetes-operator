// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

const (
	// retentionTolerance is the allowed skew between the operator-computed
	// status.expiredAt and the value we recompute from stoppedAt +
	// retentionDays*24h. The operator derives both from the same stoppedAt,
	// so any real drift is tiny; the tolerance only absorbs clock rounding.
	retentionTolerance = 2 * time.Minute

	// gcGrace is how long past status.expiredAt a backup may still exist
	// before we treat it as a garbage-collection failure. It absorbs the
	// backup controller's requeue latency and apiserver propagation.
	gcGrace = 10 * time.Minute

	// livenessGrace is how far past status.nextScheduledTime the scheduler
	// may run before we warn that scheduling appears stalled.
	livenessGrace = 5 * time.Minute
)

// Client is the subset of the cluster API the backup verifier needs.
// monitor.K8sClusterClient satisfies it structurally.
type Client interface {
	// EnsureScheduledBackup idempotently creates the ScheduledBackup CR.
	EnsureScheduledBackup(ctx context.Context, name, schedule string, retentionDays int) error
	// GetScheduledBackup fetches the ScheduledBackup CR by name.
	GetScheduledBackup(ctx context.Context, name string) (*previewv1.ScheduledBackup, error)
	// ListScheduledChildBackups lists the Backup CRs the ScheduledBackup produced.
	ListScheduledChildBackups(ctx context.Context, scheduledBackupName string) ([]previewv1.Backup, error)
}

// Config parameterizes the backup verifier.
type Config struct {
	// ScheduledBackupName is the name of the ScheduledBackup CR to manage.
	ScheduledBackupName string
	// Schedule is the cron expression for the ScheduledBackup.
	Schedule string
	// RetentionDays is the retention window applied to every child backup.
	RetentionDays int
	// VerifyInterval is how often the verification loop runs.
	VerifyInterval time.Duration
}

// Verifier bootstraps a ScheduledBackup and continuously checks that the
// operator schedules, completes, and retires backups per policy.
type Verifier struct {
	client  Client
	journal *journal.Journal
	metrics *Metrics
	cfg     Config

	// dedup state — the verification loop re-observes the same backups every
	// cycle, so we count each transition/violation exactly once by name.
	seenCompleted    map[string]struct{}
	seenFailed       map[string]struct{}
	retentionFlagged map[string]struct{}
	gcFlagged        map[string]struct{}
	lastScheduled    time.Time
	stallWarnedFor   time.Time
}

// NewVerifier creates a backup verifier. metrics must be non-nil.
func NewVerifier(client Client, j *journal.Journal, metrics *Metrics, cfg Config) *Verifier {
	return &Verifier{
		client:           client,
		journal:          j,
		metrics:          metrics,
		cfg:              cfg,
		seenCompleted:    make(map[string]struct{}),
		seenFailed:       make(map[string]struct{}),
		retentionFlagged: make(map[string]struct{}),
		gcFlagged:        make(map[string]struct{}),
	}
}

// Bootstrap ensures the ScheduledBackup CR exists. It is safe to call on
// every startup; an existing ScheduledBackup is left untouched so the
// accumulated backup history survives driver restarts.
func (v *Verifier) Bootstrap(ctx context.Context) error {
	if err := v.client.EnsureScheduledBackup(ctx, v.cfg.ScheduledBackupName, v.cfg.Schedule, v.cfg.RetentionDays); err != nil {
		return fmt.Errorf("ensure ScheduledBackup: %w", err)
	}
	v.journal.Info("backup", fmt.Sprintf("ScheduledBackup %q ensured (schedule=%q retentionDays=%d)",
		v.cfg.ScheduledBackupName, v.cfg.Schedule, v.cfg.RetentionDays))
	return nil
}

// Run starts the verification loop. It blocks until ctx is cancelled.
func (v *Verifier) Run(ctx context.Context) {
	v.journal.Info("backup", "backup verifier started")
	defer v.journal.Info("backup", "backup verifier stopped")

	ticker := time.NewTicker(v.cfg.VerifyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.checkOnce(ctx, time.Now())
		}
	}
}

// checkOnce runs a single verification pass. now is injected so unit tests
// can drive time deterministically.
func (v *Verifier) checkOnce(ctx context.Context, now time.Time) {
	v.checkScheduling(ctx, now)
	v.checkChildBackups(ctx, now)
	v.metrics.VerifyCycles.Add(1)
}

// checkScheduling inspects the ScheduledBackup status: it counts new
// scheduling events and warns if the scheduler appears stalled.
func (v *Verifier) checkScheduling(ctx context.Context, now time.Time) {
	sb, err := v.client.GetScheduledBackup(ctx, v.cfg.ScheduledBackupName)
	if err != nil {
		v.journal.Warn("backup", fmt.Sprintf("cannot read ScheduledBackup (will retry): %v", err))
		return
	}

	if sb.Status.LastScheduledTime != nil {
		last := sb.Status.LastScheduledTime.Time
		if last.After(v.lastScheduled) {
			v.lastScheduled = last
			v.metrics.Scheduled.Add(1)
			v.metrics.LastScheduledUnix.Store(last.Unix())
			v.journal.Info("backup", fmt.Sprintf("backup scheduled at %s", last.Format(time.RFC3339)))
		}
	}

	// Liveness: if the next scheduled time is well in the past and no new
	// backup has been scheduled since, surface a warning (once per overdue
	// deadline). This is a Degraded signal, not a Fatal one — scheduling may
	// be transiently delayed by apiserver load.
	if next := sb.Status.NextScheduledTime; next != nil {
		deadline := next.Time.Add(livenessGrace)
		if now.After(deadline) && !v.lastScheduled.After(next.Time) && !v.stallWarnedFor.Equal(next.Time) {
			v.stallWarnedFor = next.Time
			v.journal.Warn("backup", fmt.Sprintf(
				"scheduling appears stalled: nextScheduledTime %s passed %s ago with no new backup",
				next.Time.Format(time.RFC3339), now.Sub(next.Time).Round(time.Second)))
		}
	}
}

// checkChildBackups inspects every child Backup for completion, terminal
// failure, retention-calculation correctness, and garbage collection.
func (v *Verifier) checkChildBackups(ctx context.Context, now time.Time) {
	children, err := v.client.ListScheduledChildBackups(ctx, v.cfg.ScheduledBackupName)
	if err != nil {
		v.journal.Warn("backup", fmt.Sprintf("cannot list child backups (will retry): %v", err))
		return
	}

	v.metrics.LastChildCount.Store(int64(len(children)))

	for i := range children {
		b := &children[i]
		v.recordPhase(b)
		v.checkRetention(b)
		v.checkGarbageCollection(b, now)
	}
}

// recordPhase counts new completed / terminally-failed child backups,
// deduplicated by name.
func (v *Verifier) recordPhase(b *previewv1.Backup) {
	switch {
	case isCompleted(b):
		if _, seen := v.seenCompleted[b.Name]; !seen {
			v.seenCompleted[b.Name] = struct{}{}
			v.metrics.Completed.Add(1)
			v.journal.Info("backup", fmt.Sprintf("backup %q completed", b.Name))
		}
	case isTerminalFailure(b):
		if _, seen := v.seenFailed[b.Name]; !seen {
			v.seenFailed[b.Name] = struct{}{}
			v.metrics.Failed.Add(1)
			v.journal.Warn("backup", fmt.Sprintf("backup %q reached terminal failure phase %q: %s",
				b.Name, b.Status.Phase, b.Status.Message))
		}
	}
}

// checkRetention verifies that a completed backup's status.expiredAt equals
// stoppedAt + retentionDays*24h within tolerance. A mismatch is a Fatal
// operator bug and is flagged exactly once per backup.
func (v *Verifier) checkRetention(b *previewv1.Backup) {
	if !isCompleted(b) || b.Status.StoppedAt == nil || b.Status.ExpiredAt == nil || b.Spec.RetentionDays == nil {
		return
	}
	if _, done := v.retentionFlagged[b.Name]; done {
		return
	}

	expected := b.Status.StoppedAt.Time.Add(time.Duration(*b.Spec.RetentionDays) * 24 * time.Hour)
	actual := b.Status.ExpiredAt.Time
	skew := actual.Sub(expected)
	if skew < 0 {
		skew = -skew
	}
	if skew > retentionTolerance {
		v.retentionFlagged[b.Name] = struct{}{}
		v.metrics.RetentionViolations.Add(1)
		v.journal.Error("backup", fmt.Sprintf(
			"retention miscalculated for %q: retentionDays=%d stoppedAt=%s expiredAt=%s (expected %s, skew %s)",
			b.Name, *b.Spec.RetentionDays,
			b.Status.StoppedAt.Time.Format(time.RFC3339),
			actual.Format(time.RFC3339), expected.Format(time.RFC3339), skew.Round(time.Second)))
	}
}

// checkGarbageCollection flags a backup that still exists well past its
// status.expiredAt — the operator failed to retire it. Flagged once per
// backup.
func (v *Verifier) checkGarbageCollection(b *previewv1.Backup, now time.Time) {
	if b.Status.ExpiredAt == nil {
		return
	}
	if _, done := v.gcFlagged[b.Name]; done {
		return
	}
	if now.After(b.Status.ExpiredAt.Time.Add(gcGrace)) {
		v.gcFlagged[b.Name] = struct{}{}
		v.metrics.GCViolations.Add(1)
		v.journal.Error("backup", fmt.Sprintf(
			"retention GC failure: backup %q still present %s past expiredAt %s",
			b.Name, now.Sub(b.Status.ExpiredAt.Time).Round(time.Second),
			b.Status.ExpiredAt.Time.Format(time.RFC3339)))
	}
}

// isCompleted reports whether a Backup reached the "completed" phase.
func isCompleted(b *previewv1.Backup) bool {
	return b != nil && b.Status.Phase == cnpgv1.BackupPhaseCompleted
}

// isTerminalFailure reports whether a Backup reached a phase from which no
// reconcile will recover it.
func isTerminalFailure(b *previewv1.Backup) bool {
	if b == nil {
		return false
	}
	switch b.Status.Phase {
	case cnpgv1.BackupPhaseFailed, previewv1.BackupPhaseSkipped:
		return true
	default:
		return false
	}
}
