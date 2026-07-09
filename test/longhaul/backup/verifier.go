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
	// gcGrace is how long past a backup's retention deadline
	// (stoppedAt + retentionDays*24h) it may still exist before we treat it
	// as a garbage-collection leak. It absorbs the backup controller's
	// requeue latency and apiserver propagation.
	gcGrace = 10 * time.Minute

	// livenessGrace is how far past status.nextScheduledTime the scheduler
	// may run before we warn that scheduling appears stalled.
	livenessGrace = 5 * time.Minute
)

// Client is the subset of the cluster API the backup verifier needs.
// monitor.K8sClusterClient satisfies it structurally.
type Client interface {
	EnsureScheduledBackup(ctx context.Context, name, schedule string, retentionDays int) error
	GetScheduledBackup(ctx context.Context, name string) (*previewv1.ScheduledBackup, error)
	ListScheduledChildBackups(ctx context.Context, scheduledBackupName string) ([]previewv1.Backup, error)
}

// Config parameterizes the backup verifier.
type Config struct {
	ScheduledBackupName string
	Schedule            string
	RetentionDays       int
	VerifyInterval      time.Duration
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
	seenCompleted  map[string]struct{}
	seenFailed     map[string]struct{}
	leakFlagged    map[string]struct{}
	lastScheduled  time.Time
	stallWarnedFor time.Time
}

// NewVerifier creates a backup verifier. metrics must be non-nil.
func NewVerifier(client Client, j *journal.Journal, metrics *Metrics, cfg Config) *Verifier {
	return &Verifier{
		client:        client,
		journal:       j,
		metrics:       metrics,
		cfg:           cfg,
		seenCompleted: make(map[string]struct{}),
		seenFailed:    make(map[string]struct{}),
		leakFlagged:   make(map[string]struct{}),
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
// failure, and retention leaks.
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
		v.checkRetentionLeak(b, now)
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

// checkRetentionLeak flags a completed backup still present past its
// retention window. The deadline is derived from our own configured policy
// (stoppedAt + retentionDays*24h + gcGrace), not the operator-populated
// status.expiredAt, so the oracle stays black-box (see package doc).
// Flagged once per backup.
func (v *Verifier) checkRetentionLeak(b *previewv1.Backup, now time.Time) {
	if !isCompleted(b) || b.Status.StoppedAt == nil {
		return
	}
	if _, done := v.leakFlagged[b.Name]; done {
		return
	}
	deadline := b.Status.StoppedAt.Time.
		Add(time.Duration(v.cfg.RetentionDays) * 24 * time.Hour).
		Add(gcGrace)
	if now.After(deadline) {
		v.leakFlagged[b.Name] = struct{}{}
		v.metrics.RetentionLeaks.Add(1)
		v.journal.Error("backup", fmt.Sprintf(
			"retention leak: backup %q still present %s past its %d-day retention window (stoppedAt %s)",
			b.Name, now.Sub(b.Status.StoppedAt.Time).Round(time.Second),
			v.cfg.RetentionDays, b.Status.StoppedAt.Time.Format(time.RFC3339)))
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
