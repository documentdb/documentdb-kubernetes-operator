// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package report generates a markdown summary of the long haul test run.
package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"
)

// Result represents the overall test outcome.
type Result string

const (
	ResultPass Result = "PASS"
	ResultFail Result = "FAIL"
)

// Summary contains all data needed to generate the final report.
type Summary struct {
	Result       Result
	Duration     time.Duration
	Metrics      workload.MetricsSnapshot
	LeakAnalysis monitor.LeakAnalysis
	OpsExecuted  int
	Windows      []journal.DisruptionWindow
	Events       []journal.Event
	FailReason   string
}

// GenerateMarkdown produces a human-readable markdown report.
func GenerateMarkdown(s Summary) string {
	var b strings.Builder

	b.WriteString("# Long Haul Test Report\n\n")

	// Header
	b.WriteString(fmt.Sprintf("**Result:** %s\n", s.Result))
	b.WriteString(fmt.Sprintf("**Duration:** %s\n", s.Duration.Round(time.Second)))
	b.WriteString(fmt.Sprintf("**Operations Executed:** %d\n", s.OpsExecuted))
	if s.FailReason != "" {
		b.WriteString(fmt.Sprintf("**Failure Reason:** %s\n", s.FailReason))
	}
	b.WriteString("\n")

	// Data Plane Metrics
	b.WriteString("## Data Plane Metrics\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	b.WriteString(fmt.Sprintf("| Writes Attempted | %d |\n", s.Metrics.WriteAttempted))
	b.WriteString(fmt.Sprintf("| Writes Acknowledged | %d |\n", s.Metrics.WriteAcknowledged))
	b.WriteString(fmt.Sprintf("| Writes Failed | %d |\n", s.Metrics.WriteFailed))
	b.WriteString(fmt.Sprintf("| Write Success Rate | %.2f%% |\n", s.Metrics.WriteSuccessRate()*100))
	b.WriteString(fmt.Sprintf("| Verify Passes | %d |\n", s.Metrics.VerifyPasses))
	b.WriteString(fmt.Sprintf("| Gaps Detected | %d |\n", s.Metrics.GapsDetected))
	b.WriteString(fmt.Sprintf("| Checksum Errors | %d |\n", s.Metrics.ChecksumErrors))
	b.WriteString("\n")

	// Disruption Windows
	if len(s.Windows) > 0 {
		b.WriteString("## Disruption Windows\n\n")
		b.WriteString("| Operation | Duration | Write Failures | Policy Exceeded |\n")
		b.WriteString("|-----------|----------|----------------|------------------|\n")
		for _, w := range s.Windows {
			exceeded := "No"
			if w.ExceededPolicy() {
				exceeded = "**YES**"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
				w.OperationName, w.Duration().Round(time.Second), w.WriteFailures, exceeded))
		}
		b.WriteString("\n")
	}

	// Leak Analysis
	if s.LeakAnalysis.SampleCount > 0 {
		b.WriteString("## Resource Leak Analysis\n\n")
		b.WriteString(fmt.Sprintf("- Samples: %d over %s\n",
			s.LeakAnalysis.SampleCount, s.LeakAnalysis.Duration.Round(time.Minute)))
		b.WriteString(fmt.Sprintf("- Memory trend: %.2f MB/hour\n", s.LeakAnalysis.MemorySlopeMB))
		b.WriteString(fmt.Sprintf("- CPU trend: %.4f cores/hour\n", s.LeakAnalysis.CPUSlopeCores))
		if s.LeakAnalysis.HasLeak {
			b.WriteString("- **⚠️ Memory leak suspected**\n")
		}
		b.WriteString("\n")
	}

	// Recent Events (last 20)
	b.WriteString("## Recent Events\n\n")
	b.WriteString("```\n")
	events := s.Events
	start := 0
	if len(events) > 20 {
		start = len(events) - 20
	}
	for _, e := range events[start:] {
		b.WriteString(e.String() + "\n")
	}
	b.WriteString("```\n")

	return b.String()
}
