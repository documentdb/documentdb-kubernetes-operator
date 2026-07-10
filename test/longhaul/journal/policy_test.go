// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package journal

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DisruptionWindow", func() {
	Describe("IsActive", func() {
		It("returns true for an open window (no end time)", func() {
			w := DisruptionWindow{StartTime: time.Now()}
			Expect(w.IsActive()).To(BeTrue())
		})
		It("returns false once EndTime is set", func() {
			now := time.Now()
			w := DisruptionWindow{StartTime: now, EndTime: now.Add(time.Second)}
			Expect(w.IsActive()).To(BeFalse())
		})
	})

	Describe("Duration", func() {
		It("returns end-start for a closed window", func() {
			start := time.Now()
			end := start.Add(7 * time.Second)
			w := DisruptionWindow{StartTime: start, EndTime: end}
			Expect(w.Duration()).To(Equal(7 * time.Second))
		})
		It("returns at-least-since-start for an active window", func() {
			start := time.Now().Add(-3 * time.Second)
			w := DisruptionWindow{StartTime: start}
			Expect(w.Duration()).To(BeNumerically(">=", 3*time.Second))
		})
	})

	DescribeTable("ExceededPolicy",
		func(w DisruptionWindow, want bool) {
			Expect(w.ExceededPolicy()).To(Equal(want))
		},
		Entry("within all budgets",
			DisruptionWindow{
				StartTime:     time.Now().Add(-10 * time.Second),
				EndTime:       time.Now(),
				WriteFailures: 5,
				Policy:        OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 50},
			}, false),
		Entry("exceeds MustRecoverWithin",
			DisruptionWindow{
				StartTime:     time.Now().Add(-2 * time.Minute),
				EndTime:       time.Now(),
				WriteFailures: 1,
				Policy:        OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 50},
			}, true),
		Entry("exceeds AllowedWriteFailures",
			DisruptionWindow{
				StartTime:     time.Now().Add(-10 * time.Second),
				EndTime:       time.Now(),
				WriteFailures: 100,
				Policy:        OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 50},
			}, true),
		Entry("boundary: equal to write-failure budget is allowed",
			DisruptionWindow{
				StartTime:     time.Now().Add(-10 * time.Second),
				EndTime:       time.Now(),
				WriteFailures: 50,
				Policy:        OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 50},
			}, false),
		Entry("active window also evaluated against MustRecoverWithin",
			DisruptionWindow{
				StartTime: time.Now().Add(-2 * time.Minute),
				Policy:    OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 50},
			}, true),
	)

	It("DefaultOutagePolicy returns no zero-valued field", func() {
		p := DefaultOutagePolicy()
		Expect(p.MustRecoverWithin).NotTo(BeZero())
		Expect(p.AllowedWriteFailures).NotTo(BeZero())
	})

	It("NoOutagePolicy grants the near-zero cushion and echoes recovery", func() {
		p := NoOutagePolicy(3 * time.Minute)
		Expect(p.AllowedWriteFailures).To(Equal(NoOutageWriteFailureCushion))
		Expect(p.MustRecoverWithin).To(Equal(3 * time.Minute))
	})

	It("NoOutagePolicy is far tighter than DefaultOutagePolicy", func() {
		Expect(NoOutageWriteFailureCushion).To(BeNumerically("<", DefaultOutagePolicy().AllowedWriteFailures))
	})
})
