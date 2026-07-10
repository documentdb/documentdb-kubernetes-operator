// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package journal

import "time"

// OutagePolicy defines acceptable disruption bounds for an operation.
type OutagePolicy struct {
	// MaxWriteOutage bounds how long the write path (client -> gateway ->
	// primary) may be unavailable during the window. It is evaluated from the
	// observed write-failure count normalized by the workload's aggregate write
	// rate (see DisruptionWindow.EstimatedWriteOutage), so the budget is
	// expressed in wall-clock outage time and is independent of how many writer
	// goroutines (LONGHAUL_NUM_WRITERS) are configured.
	MaxWriteOutage time.Duration

	// MustRecoverWithin is the maximum time from operation start to full recovery.
	MustRecoverWithin time.Duration
}

// DefaultOutagePolicy returns a conservative policy suitable for most operations.
func DefaultOutagePolicy() OutagePolicy {
	return OutagePolicy{
		MaxWriteOutage:    5 * time.Second,
		MustRecoverWithin: 5 * time.Minute,
	}
}

// NoOutageWriteOutageCushion is the tiny write-outage budget granted to
// operations that are expected NOT to disrupt the data plane. It is not a
// tolerance for real outages: one fully-failed write tick (every configured
// writer failing once) maps to exactly one writeInterval of estimated outage
// (~100ms) regardless of writer count, so this ~3-tick cushion absorbs unrelated
// background noise (a client reconnect, service-endpoint churn) without
// tolerating a genuine primary outage. Centralized so it can be recalibrated
// against real long-haul runs in one place.
const NoOutageWriteOutageCushion = 300 * time.Millisecond

// NoOutagePolicy is the outage budget for operations that keep the write path
// up throughout and therefore must not cause a write outage. It is shared by
// every "no data-plane impact" operation:
//   - control-plane faults, e.g. an operator pod restart, and
//   - scaling that only adds or removes a standby replica (the primary, and
//     thus the write path, is never touched).
//
// recovery bounds how long the cluster may take to return to steady state.
func NoOutagePolicy(recovery time.Duration) OutagePolicy {
	return OutagePolicy{
		MaxWriteOutage:    NoOutageWriteOutageCushion,
		MustRecoverWithin: recovery,
	}
}

// DisruptionWindow represents an active or closed disruption period.
type DisruptionWindow struct {
	// OperationName identifies which operation opened this window.
	OperationName string

	// StartTime is when the disruption began.
	StartTime time.Time

	// EndTime is when the disruption ended. Zero means still active.
	EndTime time.Time

	// Policy is the outage budget for this window.
	Policy OutagePolicy

	// WriteFailures counts failures observed during this window.
	WriteFailures int64

	// WritesPerSecond is the workload's aggregate write rate at the time the
	// window opened. It is used to convert the raw WriteFailures count into an
	// estimated write-outage duration (see EstimatedWriteOutage). A real outage
	// makes every writer fail on every tick, so failures accrue at the full
	// aggregate rate and count/rate recovers the wall-clock outage duration
	// regardless of writer count. Zero disables the write-outage check.
	WritesPerSecond float64
}

// EstimatedWriteOutage converts the observed write-failure count into an
// approximate duration for which the write path was unavailable, using the
// aggregate write rate captured when the window opened. Returns 0 when the rate
// is unknown (<= 0), which disables the write-outage portion of the policy.
func (w *DisruptionWindow) EstimatedWriteOutage() time.Duration {
	if w.WritesPerSecond <= 0 {
		return 0
	}
	return time.Duration(float64(w.WriteFailures) / w.WritesPerSecond * float64(time.Second))
}

// IsActive returns true if the disruption window has not been closed.
func (w *DisruptionWindow) IsActive() bool {
	return w.EndTime.IsZero()
}

// Duration returns the elapsed time of the disruption window.
// For active windows, this is time since start.
func (w *DisruptionWindow) Duration() time.Duration {
	if w.IsActive() {
		return time.Since(w.StartTime)
	}
	return w.EndTime.Sub(w.StartTime)
}

// ExceededPolicy returns true if the window has violated its outage policy.
func (w *DisruptionWindow) ExceededPolicy() bool {
	if w.Duration() > w.Policy.MustRecoverWithin {
		return true
	}
	if w.EstimatedWriteOutage() > w.Policy.MaxWriteOutage {
		return true
	}
	return false
}
