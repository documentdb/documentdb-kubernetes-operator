// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package workload implements the data-plane workload for long haul tests,
// including writers that generate sequential inserts and verifiers that
// detect gaps and checksum mismatches.
package workload

import (
	"sync/atomic"
	"time"
)

// Metrics tracks aggregate workload counters using atomic operations.
// All fields are safe for concurrent access from multiple goroutines.
type Metrics struct {
	// Writer metrics
	WriteAttempted    atomic.Int64
	WriteAcknowledged atomic.Int64
	WriteFailed       atomic.Int64

	// Verifier metrics
	VerifyPasses       atomic.Int64
	VerifyGapsDetected atomic.Int64
	ChecksumErrors     atomic.Int64

	// Timing
	StartTime time.Time
}

// NewMetrics creates a new Metrics instance with the start time set to now.
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

// Snapshot returns a point-in-time copy of all metric values.
type MetricsSnapshot struct {
	WriteAttempted    int64
	WriteAcknowledged int64
	WriteFailed       int64
	VerifyPasses      int64
	GapsDetected      int64
	ChecksumErrors    int64
	Elapsed           time.Duration
}

// Snapshot captures the current metric values atomically.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		WriteAttempted:    m.WriteAttempted.Load(),
		WriteAcknowledged: m.WriteAcknowledged.Load(),
		WriteFailed:       m.WriteFailed.Load(),
		VerifyPasses:      m.VerifyPasses.Load(),
		GapsDetected:      m.VerifyGapsDetected.Load(),
		ChecksumErrors:    m.ChecksumErrors.Load(),
		Elapsed:           time.Since(m.StartTime),
	}
}

// WriteSuccessRate returns the ratio of acknowledged to attempted writes.
// Returns 1.0 if no writes have been attempted.
func (s MetricsSnapshot) WriteSuccessRate() float64 {
	if s.WriteAttempted == 0 {
		return 1.0
	}
	return float64(s.WriteAcknowledged) / float64(s.WriteAttempted)
}

// HasDataLoss returns true if any gaps or checksum errors have been detected.
func (s MetricsSnapshot) HasDataLoss() bool {
	return s.GapsDetected > 0 || s.ChecksumErrors > 0
}
