// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package backup implements the data-protection component of the long haul
// driver. It provisions a ScheduledBackup against the canary cluster and
// continuously verifies the properties that only a multi-day run can
// establish: that backups keep being produced on schedule and completing,
// and that expired backups are actually garbage-collected so the backup
// population stays bounded over time (no PVC / VolumeSnapshot accumulation).
//
// It deliberately does NOT re-verify the operator's retention *arithmetic*
// (expiredAt == stoppedAt + retentionDays*24h) — that is a pure function
// already covered by the operator's unit tests and needs no accumulation.
// The oracle here is black-box: expired backups disappear.
//
// The component runs concurrently with the operation scheduler — per the
// long-haul design, backup is deliberately NOT isolated so that
// backup-vs-topology serialization bugs surface here rather than in
// production.
package backup

import (
	"sync/atomic"
	"time"
)

// completionStallThreshold is the number of consecutive backups that may be
// scheduled with no intervening completion before the run is failed. A single
// recovered (completed) backup resets the running gap, so transient chaos-
// induced failures stay well under this ceiling; only a wholly-broken
// completion path (every backup failing or hanging) drives the gap this high.
// At the default 6h schedule this is ~18h of zero successful backups.
const completionStallThreshold = 3

// Metrics tracks aggregate backup-verification counters using atomic
// operations so the reporter goroutine can snapshot them without locking.
//
// Two independent oracles flip the run verdict to FAIL:
//   - RetentionLeaks > 0: a completed backup outlived its retention window
//     (the operator failed to garbage-collect it).
//   - MaxScheduledWithoutCompletion >= completionStallThreshold: backups keep
//     being scheduled but stop completing (a dead completion path the leak
//     oracle alone would miss).
//
// The remaining counters are observational and feed the report.
type Metrics struct {
	// Scheduled counts backups observed to have been scheduled by the
	// ScheduledBackup (advances of status.lastScheduledTime).
	Scheduled atomic.Int64

	// Completed is the number of child backups observed in the "completed"
	// phase (deduplicated by name across verification cycles).
	Completed atomic.Int64

	// Failed is the number of child backups observed in a terminal failure
	// phase (deduplicated by name).
	Failed atomic.Int64

	// RetentionLeaks counts completed backups still present past their
	// retention window (stoppedAt + retentionDays*24h). Non-zero => FAIL.
	RetentionLeaks atomic.Int64

	// MaxScheduledWithoutCompletion is the high-water mark of consecutive
	// backups scheduled with no intervening completion. It is the completion-
	// liveness oracle: a wholly-broken backup path drives it monotonically
	// upward while Completed stays flat. Reaching completionStallThreshold
	// flips the verdict to FAIL. See observeCompletionGap for how it advances.
	MaxScheduledWithoutCompletion atomic.Int64

	// LastChildCount is the number of child backups observed on the most
	// recent verification cycle (the live backup population — expected to
	// stabilize near retentionWindow/scheduleInterval at steady state).
	LastChildCount atomic.Int64

	// LastScheduledUnix is the Unix timestamp of the most recently observed
	// status.lastScheduledTime; 0 until the first backup is scheduled.
	LastScheduledUnix atomic.Int64
}

// NewMetrics creates an empty Metrics.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// observeCompletionGap records gap as a new high-water mark for consecutive
// scheduled-without-completion backups if it exceeds the current maximum.
func (m *Metrics) observeCompletionGap(gap int64) {
	for {
		cur := m.MaxScheduledWithoutCompletion.Load()
		if gap <= cur {
			return
		}
		if m.MaxScheduledWithoutCompletion.CompareAndSwap(cur, gap) {
			return
		}
	}
}

// MetricsSnapshot is a point-in-time copy of Metrics.
type MetricsSnapshot struct {
	Scheduled                     int64
	Completed                     int64
	Failed                        int64
	RetentionLeaks                int64
	MaxScheduledWithoutCompletion int64
	LastChildCount                int64
	LastScheduled                 time.Time
}

// Snapshot captures the current metric values atomically.
func (m *Metrics) Snapshot() MetricsSnapshot {
	var lastScheduled time.Time
	if unix := m.LastScheduledUnix.Load(); unix > 0 {
		lastScheduled = time.Unix(unix, 0)
	}
	return MetricsSnapshot{
		Scheduled:                     m.Scheduled.Load(),
		Completed:                     m.Completed.Load(),
		Failed:                        m.Failed.Load(),
		RetentionLeaks:                m.RetentionLeaks.Load(),
		MaxScheduledWithoutCompletion: m.MaxScheduledWithoutCompletion.Load(),
		LastChildCount:                m.LastChildCount.Load(),
		LastScheduled:                 lastScheduled,
	}
}

// HasRetentionLeak returns true if any completed backup has outlived its
// retention window. A true result flips the overall run verdict to FAIL.
func (s MetricsSnapshot) HasRetentionLeak() bool {
	return s.RetentionLeaks > 0
}

// HasCompletionStall returns true if backups kept being scheduled but stopped
// completing for completionStallThreshold consecutive schedules. A true result
// flips the overall run verdict to FAIL, catching a wholly-broken backup path
// that the retention-leak oracle alone would miss.
func (s MetricsSnapshot) HasCompletionStall() bool {
	return s.MaxScheduledWithoutCompletion >= completionStallThreshold
}
