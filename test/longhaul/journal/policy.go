// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package journal

import "time"

// OutagePolicy defines acceptable disruption bounds for an operation.
type OutagePolicy struct {
	// AllowedDowntime is the maximum duration of write unavailability.
	AllowedDowntime time.Duration

	// AllowedWriteFailures is the maximum number of write failures during the window.
	AllowedWriteFailures int64

	// MustRecoverWithin is the maximum time from operation start to full recovery.
	MustRecoverWithin time.Duration
}

// DefaultOutagePolicy returns a conservative policy suitable for most operations.
func DefaultOutagePolicy() OutagePolicy {
	return OutagePolicy{
		AllowedDowntime:      60 * time.Second,
		AllowedWriteFailures: 50,
		MustRecoverWithin:    5 * time.Minute,
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
	if w.WriteFailures > w.Policy.AllowedWriteFailures {
		return true
	}
	return false
}
