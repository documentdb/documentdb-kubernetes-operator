// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"
)

var _ = Describe("GenerateMarkdown", func() {
	It("PASS report has header, rounded duration, and no failure reason", func() {
		md := GenerateMarkdown(Summary{Result: ResultPass, Duration: 3 * time.Hour})
		Expect(md).To(ContainSubstring("**Result:** PASS"))
		Expect(md).To(ContainSubstring("**Duration:** 3h0m0s"))
		Expect(md).NotTo(ContainSubstring("Failure Reason"))
	})

	It("FAIL report includes header and reason", func() {
		md := GenerateMarkdown(Summary{
			Result:     ResultFail,
			Duration:   90 * time.Second,
			FailReason: "policy exceeded on scale-up",
		})
		Expect(md).To(ContainSubstring("**Result:** FAIL"))
		Expect(md).To(ContainSubstring("policy exceeded on scale-up"))
	})

	It("rounds duration to seconds", func() {
		md := GenerateMarkdown(Summary{
			Result:   ResultPass,
			Duration: 2*time.Second + 678*time.Millisecond,
		})
		Expect(md).To(ContainSubstring("**Duration:** 3s"))
	})

	It("always includes the Data Plane Metrics table", func() {
		md := GenerateMarkdown(Summary{
			Result: ResultPass,
			Metrics: workload.MetricsSnapshot{
				WriteAttempted:    1000,
				WriteAcknowledged: 998,
				WriteFailed:       2,
				VerifyPasses:      10,
				GapsDetected:      0,
				ChecksumErrors:    0,
			},
		})
		for _, want := range []string{
			"## Data Plane Metrics",
			"| Writes Attempted | 1000 |",
			"| Writes Acknowledged | 998 |",
			"| Writes Failed | 2 |",
		} {
			Expect(md).To(ContainSubstring(want))
		}
	})

	Describe("Disruption Windows section", func() {
		It("is hidden when there are no windows", func() {
			md := GenerateMarkdown(Summary{Result: ResultPass})
			Expect(md).NotTo(ContainSubstring("Disruption Windows"))
		})

		It("appears with the operation name when at least one window exists", func() {
			now := time.Now()
			md := GenerateMarkdown(Summary{
				Result: ResultPass,
				Windows: []journal.DisruptionWindow{{
					OperationName:   "scale-up",
					StartTime:       now.Add(-30 * time.Second),
					EndTime:         now,
					WriteFailures:   3,
					WritesPerSecond: 50,
					Policy:          journal.OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
				}},
			})
			Expect(md).To(ContainSubstring("Disruption Windows"))
			Expect(md).To(ContainSubstring("scale-up"))
		})
	})

	Describe("Resource Leak Analysis section", func() {
		It("is hidden when SampleCount=0", func() {
			md := GenerateMarkdown(Summary{Result: ResultPass})
			Expect(md).NotTo(ContainSubstring("Resource Leak Analysis"))
		})

		It("appears with a warning when HasLeak=true and SampleCount>0", func() {
			md := GenerateMarkdown(Summary{
				Result:       ResultPass,
				LeakAnalysis: monitor.LeakAnalysis{SampleCount: 100, HasLeak: true, MemorySlopeMB: 250.5},
			})
			Expect(md).To(ContainSubstring("Resource Leak Analysis"))
			Expect(md).To(ContainSubstring("Memory leak suspected"))
		})
	})

	It("truncates Recent Events to the last 20", func() {
		events := make([]journal.Event, 30)
		for i := range events {
			events[i] = journal.Event{
				Timestamp: time.Now(),
				Level:     journal.LevelInfo,
				Component: "x",
				Message:   "evt",
			}
		}
		md := GenerateMarkdown(Summary{Result: ResultPass, Events: events})
		Expect(strings.Count(md, "INFO x: evt")).To(Equal(20))
	})
})
