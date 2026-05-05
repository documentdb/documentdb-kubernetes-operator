// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

// ResourceSample represents a single observation of resource usage.
type ResourceSample struct {
	Timestamp time.Time
	MemoryMB  float64
	CPUCores  float64
}

// LeakDetector analyzes resource usage trends over time using linear regression.
// A consistently positive slope above the threshold indicates a resource leak.
type LeakDetector struct {
	journal        *journal.Journal
	slopeThreshold float64 // MB/hour threshold for memory leak detection
	minSamples     int     // minimum samples before analysis

	mu      sync.RWMutex
	samples []ResourceSample
}

// NewLeakDetector creates a leak detector with the given sensitivity.
// slopeThreshold is in MB/hour — a memory growth rate above this is flagged.
func NewLeakDetector(j *journal.Journal, slopeThresholdMBPerHour float64, minSamples int) *LeakDetector {
	if minSamples < 3 {
		minSamples = 3
	}
	return &LeakDetector{
		journal:        j,
		slopeThreshold: slopeThresholdMBPerHour,
		minSamples:     minSamples,
		samples:        make([]ResourceSample, 0, 256),
	}
}

// AddSample records a resource usage observation.
func (l *LeakDetector) AddSample(s ResourceSample) {
	l.mu.Lock()
	l.samples = append(l.samples, s)
	l.mu.Unlock()
}

// LeakAnalysis contains the results of trend analysis.
type LeakAnalysis struct {
	HasLeak       bool
	MemorySlopeMB float64 // MB per hour
	CPUSlopeCores float64 // cores per hour
	SampleCount   int
	Duration      time.Duration
}

// Analyze performs linear regression on collected samples and returns the trend.
func (l *LeakDetector) Analyze() LeakAnalysis {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := LeakAnalysis{SampleCount: len(l.samples)}

	if len(l.samples) < l.minSamples {
		return result
	}

	first := l.samples[0].Timestamp
	last := l.samples[len(l.samples)-1].Timestamp
	result.Duration = last.Sub(first)

	// Compute linear regression for memory.
	result.MemorySlopeMB = linearRegressionSlope(l.samples, func(s ResourceSample) float64 {
		return s.MemoryMB
	}) * 3600 // convert per-second to per-hour

	// Compute linear regression for CPU.
	result.CPUSlopeCores = linearRegressionSlope(l.samples, func(s ResourceSample) float64 {
		return s.CPUCores
	}) * 3600

	if result.MemorySlopeMB > l.slopeThreshold {
		result.HasLeak = true
		l.journal.Warn("leakdetect", fmt.Sprintf(
			"memory leak suspected: %.2f MB/hour over %s (%d samples)",
			result.MemorySlopeMB, result.Duration.Round(time.Minute), len(l.samples)))
	}

	return result
}

// linearRegressionSlope computes the slope of a least-squares linear fit.
// x-axis is elapsed seconds from first sample, y-axis is extracted value.
func linearRegressionSlope(samples []ResourceSample, getValue func(ResourceSample) float64) float64 {
	n := float64(len(samples))
	if n < 2 {
		return 0
	}

	t0 := samples[0].Timestamp
	var sumX, sumY, sumXY, sumX2 float64

	for _, s := range samples {
		x := s.Timestamp.Sub(t0).Seconds()
		y := getValue(s)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denom := n*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-10 {
		return 0
	}

	return (n*sumXY - sumX*sumY) / denom
}
