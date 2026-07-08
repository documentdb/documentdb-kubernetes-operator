// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package backup implements the data-protection component of the long haul
// driver. It provisions a ScheduledBackup against the canary cluster and
// continuously verifies that backups are produced on schedule, complete
// successfully, and that the operator honors the configured retention
// period (expiration is calculated correctly and expired backups are
// garbage-collected).
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
// RetentionViolations and GCViolations are the retention oracle: any
// non-zero value indicates a real operator bug and flips the run verdict
// to FAIL. The remaining counters are observational and feed the report.
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

	// RetentionViolations counts completed backups whose status.expiredAt
	// does not match stoppedAt + retentionDays*24h within tolerance.
	// Non-zero => FAIL (operator miscalculated retention).
	RetentionViolations atomic.Int64

	// GCViolations counts backups that remain present well past their
	// status.expiredAt — i.e. the operator failed to garbage-collect an
	// expired backup. Non-zero => FAIL (retention GC leak).
	GCViolations atomic.Int64

	// VerifyCycles is the number of completed verification passes.
	VerifyCycles atomic.Int64

	// LastChildCount is the number of child backups observed on the most
	// recent verification cycle (a proxy for steady-state backup population).
	LastChildCount atomic.Int64

	// LastScheduledUnix is the Unix timestamp of the most recently observed
	// status.lastScheduledTime; 0 until the first backup is scheduled.
	LastScheduledUnix atomic.Int64

	// StartTime is when this Metrics was constructed; resets on pod restart.
	StartTime time.Time
}

// NewMetrics creates a Metrics with the start time set to now.
func NewMetrics() *Metrics {
	return &Metrics{StartTime: time.Now()}
}

// MetricsSnapshot is a point-in-time copy of Metrics.
type MetricsSnapshot struct {
	Scheduled           int64
	Completed           int64
	Failed              int64
	RetentionViolations int64
	GCViolations        int64
	VerifyCycles        int64
	LastChildCount      int64
	LastScheduled       time.Time
	Elapsed             time.Duration
}

// Snapshot captures the current metric values atomically.
func (m *Metrics) Snapshot() MetricsSnapshot {
	var lastScheduled time.Time
	if unix := m.LastScheduledUnix.Load(); unix > 0 {
		lastScheduled = time.Unix(unix, 0)
	}
	return MetricsSnapshot{
		Scheduled:           m.Scheduled.Load(),
		Completed:           m.Completed.Load(),
		Failed:              m.Failed.Load(),
		RetentionViolations: m.RetentionViolations.Load(),
		GCViolations:        m.GCViolations.Load(),
		VerifyCycles:        m.VerifyCycles.Load(),
		LastChildCount:      m.LastChildCount.Load(),
		LastScheduled:       lastScheduled,
		Elapsed:             time.Since(m.StartTime),
	}
}

// HasRetentionFailure returns true if any retention or GC violation has
// been detected. A true result flips the overall run verdict to FAIL.
func (s MetricsSnapshot) HasRetentionFailure() bool {
	return s.RetentionViolations > 0 || s.GCViolations > 0
}
