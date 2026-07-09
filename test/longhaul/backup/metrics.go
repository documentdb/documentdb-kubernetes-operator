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

// Metrics tracks aggregate backup-verification counters using atomic
// operations so the reporter goroutine can snapshot them without locking.
//
// RetentionLeaks is the retention oracle: any non-zero value means a
// completed backup outlived its retention window (the operator failed to
// garbage-collect it) and flips the run verdict to FAIL. The remaining
// counters are observational and feed the report.
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

// MetricsSnapshot is a point-in-time copy of Metrics.
type MetricsSnapshot struct {
	Scheduled      int64
	Completed      int64
	Failed         int64
	RetentionLeaks int64
	LastChildCount int64
	LastScheduled  time.Time
}

// Snapshot captures the current metric values atomically.
func (m *Metrics) Snapshot() MetricsSnapshot {
	var lastScheduled time.Time
	if unix := m.LastScheduledUnix.Load(); unix > 0 {
		lastScheduled = time.Unix(unix, 0)
	}
	return MetricsSnapshot{
		Scheduled:      m.Scheduled.Load(),
		Completed:      m.Completed.Load(),
		Failed:         m.Failed.Load(),
		RetentionLeaks: m.RetentionLeaks.Load(),
		LastChildCount: m.LastChildCount.Load(),
		LastScheduled:  lastScheduled,
	}
}

// HasRetentionLeak returns true if any completed backup has outlived its
// retention window. A true result flips the overall run verdict to FAIL.
func (s MetricsSnapshot) HasRetentionLeak() bool {
	return s.RetentionLeaks > 0
}
