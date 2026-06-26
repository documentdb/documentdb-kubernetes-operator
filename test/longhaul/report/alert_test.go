// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"bytes"
	"io"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. EmitAnnotation uses fmt.Printf which writes directly to os.Stdout,
// so we cannot use a *bytes.Buffer here.
func captureStdout(fn func()) string {
	r, w, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())

	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	return <-done
}

var _ = Describe("EmitAnnotation", func() {
	Context("outside GitHub Actions (GITHUB_ACTIONS unset)", func() {
		BeforeEach(func() {
			GinkgoT().Setenv("GITHUB_ACTIONS", "")
		})

		It("is a silent no-op", func() {
			out := captureStdout(func() {
				EmitAnnotation(Summary{Result: ResultFail, FailReason: "boom"})
			})
			Expect(out).To(BeEmpty())
		})
	})

	Context("inside GitHub Actions (GITHUB_ACTIONS=true)", func() {
		BeforeEach(func() {
			GinkgoT().Setenv("GITHUB_ACTIONS", "true")
		})

		It("emits ::error for FAIL and includes the reason", func() {
			out := captureStdout(func() {
				EmitAnnotation(Summary{Result: ResultFail, FailReason: "data loss detected"})
			})
			Expect(out).To(ContainSubstring("::error"))
			Expect(out).To(ContainSubstring("data loss detected"))
		})

		It("emits a default ::error when FAIL has no reason", func() {
			out := captureStdout(func() {
				EmitAnnotation(Summary{Result: ResultFail})
			})
			Expect(out).To(ContainSubstring("::error"))
			Expect(out).To(ContainSubstring("Long haul test FAILED"))
		})

		It("emits ::notice on PASS with metric values", func() {
			out := captureStdout(func() {
				EmitAnnotation(Summary{
					Result:      ResultPass,
					Duration:    2 * time.Hour,
					OpsExecuted: 17,
					Metrics:     workload.MetricsSnapshot{WriteAttempted: 1234, GapsDetected: 0},
				})
			})
			Expect(out).To(ContainSubstring("::notice"))
			Expect(out).To(ContainSubstring("1234"))
			Expect(out).To(ContainSubstring("17"))
		})

		DescribeTable("emits leak ::warning regardless of result when HasLeak=true",
			func(res Result) {
				leak := monitor.LeakAnalysis{
					HasLeak:       true,
					MemorySlopeMB: 12.5,
					Duration:      90 * time.Minute,
					SampleCount:   60,
				}
				out := captureStdout(func() {
					EmitAnnotation(Summary{Result: res, LeakAnalysis: leak})
				})
				Expect(out).To(ContainSubstring("::warning"))
				Expect(out).To(ContainSubstring("12.50"))
			},
			Entry("on PASS", ResultPass),
			Entry("on FAIL", ResultFail),
		)

		It("does not emit a leak ::warning when HasLeak=false", func() {
			out := captureStdout(func() {
				EmitAnnotation(Summary{Result: ResultPass, LeakAnalysis: monitor.LeakAnalysis{HasLeak: false}})
			})
			Expect(out).NotTo(ContainSubstring("::warning"))
		})
	})
})
