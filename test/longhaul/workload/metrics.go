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
//
// Note on roles:
//   - The writer-side counters (WriteAttempted/Acknowledged/Failed) are local
//     observations made by writers from their InsertOne return values; they
//     feed the disruption-window budget but do NOT by themselves fail the test.
//   - The verifier-side counters (VerifyGapsDetected, ChecksumErrors) are the
//     durability oracle: any non-zero value flips Result to FAIL in main.
type Metrics struct {
	// WriteAttempted is the total number of InsertOne calls issued by all
	// writers (one per writer tick). Includes calls that later fail or are
	// retried as DupKey acks. Each writer ticks every writeInterval (100 ms).
	WriteAttempted atomic.Int64

	// WriteAcknowledged is the number of writes the server confirmed as
	// durable. Includes DupKey replies, which are treated as idempotent acks
	// because the v2 driver's retryable-writes path can resend a committed
	// insert after a dropped ACK (see writer.go:99-110). Equals
	// WriteAttempted - WriteFailed in steady state.
	WriteAcknowledged atomic.Int64

	// WriteFailed counts non-DupKey insert errors observed by writers.
	// These do NOT advance the writer's seq counter (so the next tick retries
	// the same seq) and therefore do not cause data-loss gaps on their own.
	// They DO get charged against the active disruption window's
	// AllowedWriteFailures budget via journal.RecordWriteFailure.
	WriteFailed atomic.Int64

	// VerifyPasses is the number of completed verifier scan cycles (not the
	// number of documents verified). Each verifier ticks every verifyInterval
	// (10 s) and increments this on a clean scan with no gaps/checksum
	// mismatches in the rows it observed this cycle.
	VerifyPasses atomic.Int64

	// VerifyGapsDetected is the durability-oracle signal: count of missing seq
	// numbers observed in the workload collection. Incremented by
	// (doc.Seq - expectedSeq) when the verifier reads a document whose seq is
	// higher than the next expected one for that writer (verifier.go:127-135).
	// A non-zero value flips Result to FAIL with reason "data loss".
	VerifyGapsDetected atomic.Int64

	// ChecksumErrors counts documents whose stored SHA-256 checksum doesn't
	// match the recomputed checksum over (writer_id, seq, payload). Indicates
	// silent corruption (writer never sees these — only the verifier does).
	// A non-zero value flips Result to FAIL with reason "data loss".
	ChecksumErrors atomic.Int64

	// StartTime is the process-local clock time when this Metrics was
	// constructed. Used to derive Elapsed in snapshots. Resets when the pod
	// restarts (the data history does not — see Writer.Resume).
	StartTime time.Time
}

// NewMetrics creates a new Metrics instance with the start time set to now.
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

// MetricsSnapshot is a point-in-time copy of all metric values.
// Field semantics mirror the Metrics counters above; GapsDetected is the
// snapshot name for VerifyGapsDetected, and Elapsed is time.Since(StartTime)
// captured at snapshot time.
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
