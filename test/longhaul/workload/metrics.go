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
//
// Writer-side counters (WriteAttempted/Acknowledged/Failed) feed the
// disruption-window budget but do not fail the test on their own.
// Verifier-side counters (VerifyGapsDetected, ChecksumErrors) are the
// durability oracle: any non-zero value flips Result to FAIL.
type Metrics struct {
	// WriteAttempted is the total number of InsertOne calls issued by writers.
	WriteAttempted atomic.Int64

	// WriteAcknowledged is the number of writes the server confirmed durable.
	// Includes DupKey replies (treated as idempotent acks for retryable writes).
	WriteAcknowledged atomic.Int64

	// WriteFailed counts non-DupKey insert errors. Does not advance seq, so
	// the next tick retries the same seq; charged against the disruption-window
	// budget via journal.RecordWriteFailure.
	WriteFailed atomic.Int64

	// VerifyPasses is the number of completed verifier scan cycles.
	VerifyPasses atomic.Int64

	// VerifyGapsDetected counts missing seq numbers observed by the verifier.
	// Non-zero => FAIL with reason "data loss".
	VerifyGapsDetected atomic.Int64

	// ChecksumErrors counts documents whose stored checksum doesn't match the
	// recomputed value. Non-zero => FAIL with reason "data loss".
	ChecksumErrors atomic.Int64

	// StartTime is when this Metrics was constructed; resets on pod restart.
	StartTime time.Time
}

// NewMetrics creates a new Metrics instance with the start time set to now.
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

// MetricsSnapshot is a point-in-time copy of Metrics. GapsDetected is the
// snapshot name for VerifyGapsDetected.
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
