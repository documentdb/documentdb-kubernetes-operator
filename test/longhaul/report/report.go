// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"
)

// Result is the terminal verdict of the test run.
type Result string

const (
	ResultPass Result = "PASS"
	ResultFail Result = "FAIL"
)

// Summary is the full state needed to render a checkpoint or final report.
// It is a pure value snapshot — no live counters, no channels — so it can be
// passed across goroutines and re-rendered offline.
type Summary struct {
	// Result is the current verdict. PASS while data-loss counters stay zero,
	// flipped to FAIL when the durability oracle detects gaps/checksum errors
	// or a disruption window blows its policy budget.
	Result Result

	// Duration is wall-clock time since the run started (process StartTime),
	// not since the cluster was created. Resets on pod restart.
	Duration time.Duration

	// Metrics is a snapshot of the workload counters (writes attempted/acked/
	// failed, verify passes, gaps, checksum errors).
	Metrics workload.MetricsSnapshot

	// LeakAnalysis is the operator-pod resource trend (memory/CPU slope over
	// the run); LeakAnalysis.HasLeak being true does NOT flip Result — it
	// only emits a warning annotation.
	LeakAnalysis monitor.LeakAnalysis

	// OpsExecuted is the count of operations (scale up/down, restart, etc.)
	// the operations scheduler has run since startup.
	OpsExecuted int

	// Windows is every disruption window opened during the run, in start
	// order. Each window records its op, duration, write-failure count, and
	// whether it exceeded its policy budget.
	Windows []journal.DisruptionWindow

	// Events is the journal's full event ring (info/warn/error log lines).
	// The renderer only includes the last 20 in the markdown body to keep
	// the ConfigMap value under the 1 MiB limit.
	Events []journal.Event

	// FailReason is a short human-readable cause when Result == FAIL
	// (e.g. "data loss: 17 gaps detected"). Empty when Result == PASS.
	FailReason string
}

// GenerateMarkdown produces a human-readable markdown report.
func GenerateMarkdown(s Summary) string {
	var b strings.Builder

	b.WriteString("# Long Haul Test Report\n\n")

	// Header
	fmt.Fprintf(&b, "**Result:** %s\n", s.Result)
	fmt.Fprintf(&b, "**Duration:** %s\n", s.Duration.Round(time.Second))
	fmt.Fprintf(&b, "**Operations Executed:** %d\n", s.OpsExecuted)
	if s.FailReason != "" {
		fmt.Fprintf(&b, "**Failure Reason:** %s\n", s.FailReason)
	}
	b.WriteString("\n")

	// Data Plane Metrics
	b.WriteString("## Data Plane Metrics\n\n")
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	fmt.Fprintf(&b, "| Writes Attempted | %d |\n", s.Metrics.WriteAttempted)
	fmt.Fprintf(&b, "| Writes Acknowledged | %d |\n", s.Metrics.WriteAcknowledged)
	fmt.Fprintf(&b, "| Writes Failed | %d |\n", s.Metrics.WriteFailed)
	fmt.Fprintf(&b, "| Write Success Rate | %.2f%% |\n", s.Metrics.WriteSuccessRate()*100)
	fmt.Fprintf(&b, "| Verify Passes | %d |\n", s.Metrics.VerifyPasses)
	fmt.Fprintf(&b, "| Gaps Detected | %d |\n", s.Metrics.GapsDetected)
	fmt.Fprintf(&b, "| Checksum Errors | %d |\n", s.Metrics.ChecksumErrors)
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
			fmt.Fprintf(&b, "| %s | %s | %d | %s |\n",
				w.OperationName, w.Duration().Round(time.Second), w.WriteFailures, exceeded)
		}
		b.WriteString("\n")
	}

	// Leak Analysis
	if s.LeakAnalysis.SampleCount > 0 {
		b.WriteString("## Resource Leak Analysis\n\n")
		fmt.Fprintf(&b, "- Samples: %d over %s\n",
			s.LeakAnalysis.SampleCount, s.LeakAnalysis.Duration.Round(time.Minute))
		fmt.Fprintf(&b, "- Memory trend: %.2f MB/hour\n", s.LeakAnalysis.MemorySlopeMB)
		fmt.Fprintf(&b, "- CPU trend: %.4f cores/hour\n", s.LeakAnalysis.CPUSlopeCores)
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
