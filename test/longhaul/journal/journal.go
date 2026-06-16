// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package journal provides an append-only event log for tracking test execution,
// disruption windows, and significant state changes during long haul tests.
package journal

import (
	"fmt"
	"sync"
	"time"
)

// Level represents the severity of a journal event.
type Level string

const (
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Event represents a single journal entry.
type Event struct {
	Timestamp time.Time
	Level     Level
	Component string
	Message   string
	Metadata  map[string]string
}

// String returns a human-readable representation of the event.
func (e Event) String() string {
	return fmt.Sprintf("[%s] %s %s: %s",
		e.Timestamp.Format("15:04:05"), e.Level, e.Component, e.Message)
}

// Journal is a thread-safe, append-only event log.
type Journal struct {
	mu     sync.RWMutex
	events []Event

	// Active disruption window (nil if none).
	activeWindow *DisruptionWindow

	// All closed disruption windows.
	closedWindows []DisruptionWindow
}

// New creates a new empty Journal.
func New() *Journal {
	return &Journal{
		events: make([]Event, 0, 256),
	}
}

// Record appends a new event to the journal.
func (j *Journal) Record(level Level, component, message string, metadata map[string]string) {
	e := Event{
		Timestamp: time.Now(),
		Level:     level,
		Component: component,
		Message:   message,
		Metadata:  metadata,
	}
	j.mu.Lock()
	j.events = append(j.events, e)
	j.mu.Unlock()
}

// Info records an info-level event.
func (j *Journal) Info(component, message string) {
	j.Record(LevelInfo, component, message, nil)
}

// Warn records a warn-level event.
func (j *Journal) Warn(component, message string) {
	j.Record(LevelWarn, component, message, nil)
}

// Error records an error-level event.
func (j *Journal) Error(component, message string) {
	j.Record(LevelError, component, message, nil)
}

// OpenDisruptionWindow starts tracking a new disruption period.
func (j *Journal) OpenDisruptionWindow(operationName string, policy OutagePolicy) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Close any existing window first.
	if j.activeWindow != nil {
		j.activeWindow.EndTime = time.Now()
		j.closedWindows = append(j.closedWindows, *j.activeWindow)
	}

	j.activeWindow = &DisruptionWindow{
		OperationName: operationName,
		StartTime:     time.Now(),
		Policy:        policy,
	}

	j.events = append(j.events, Event{
		Timestamp: time.Now(),
		Level:     LevelWarn,
		Component: "journal",
		Message:   fmt.Sprintf("disruption window opened: %s", operationName),
	})
}

// CloseDisruptionWindow ends the active disruption period.
func (j *Journal) CloseDisruptionWindow() {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.activeWindow == nil {
		return
	}

	j.activeWindow.EndTime = time.Now()
	j.closedWindows = append(j.closedWindows, *j.activeWindow)

	j.events = append(j.events, Event{
		Timestamp: time.Now(),
		Level:     LevelInfo,
		Component: "journal",
		Message: fmt.Sprintf("disruption window closed: %s (duration: %s)",
			j.activeWindow.OperationName, j.activeWindow.Duration()),
	})

	j.activeWindow = nil
}

// RecordWriteFailure increments the failure count for the active disruption window.
func (j *Journal) RecordWriteFailure() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.activeWindow != nil {
		j.activeWindow.WriteFailures++
	}
}

// ActiveWindow returns the current disruption window, or nil if none is active.
func (j *Journal) ActiveWindow() *DisruptionWindow {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.activeWindow == nil {
		return nil
	}
	// Return a copy to avoid data races.
	w := *j.activeWindow
	return &w
}

// HasPolicyViolation returns true if any disruption window exceeded its policy.
func (j *Journal) HasPolicyViolation() bool {
	j.mu.RLock()
	defer j.mu.RUnlock()

	if j.activeWindow != nil && j.activeWindow.ExceededPolicy() {
		return true
	}
	for i := range j.closedWindows {
		if j.closedWindows[i].ExceededPolicy() {
			return true
		}
	}
	return false
}

// Events returns a copy of all events recorded so far.
func (j *Journal) Events() []Event {
	j.mu.RLock()
	defer j.mu.RUnlock()
	result := make([]Event, len(j.events))
	copy(result, j.events)
	return result
}

// EventsSince returns events recorded after the given time.
func (j *Journal) EventsSince(t time.Time) []Event {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var result []Event
	for _, e := range j.events {
		if e.Timestamp.After(t) {
			result = append(result, e)
		}
	}
	return result
}

// Len returns the number of events in the journal.
func (j *Journal) Len() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.events)
}

// DisruptionWindows returns all closed disruption windows.
func (j *Journal) DisruptionWindows() []DisruptionWindow {
	j.mu.RLock()
	defer j.mu.RUnlock()
	result := make([]DisruptionWindow, len(j.closedWindows))
	copy(result, j.closedWindows)
	return result
}
