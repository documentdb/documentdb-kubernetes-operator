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
				StartTime:       time.Now().Add(-10 * time.Second),
				EndTime:         time.Now(),
				WriteFailures:   5, // 5/50 = 0.1s < 1s
				WritesPerSecond: 50,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, false),
		Entry("exceeds MustRecoverWithin",
			DisruptionWindow{
				StartTime:       time.Now().Add(-2 * time.Minute),
				EndTime:         time.Now(),
				WriteFailures:   1,
				WritesPerSecond: 50,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, true),
		Entry("exceeds MaxWriteOutage",
			DisruptionWindow{
				StartTime:       time.Now().Add(-10 * time.Second),
				EndTime:         time.Now(),
				WriteFailures:   100, // 100/50 = 2s > 1s
				WritesPerSecond: 50,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, true),
		Entry("boundary: estimated outage equal to budget is allowed",
			DisruptionWindow{
				StartTime:       time.Now().Add(-10 * time.Second),
				EndTime:         time.Now(),
				WriteFailures:   50, // 50/50 = exactly 1s
				WritesPerSecond: 50,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, false),
		Entry("unknown write rate disables the write-outage check",
			DisruptionWindow{
				StartTime:       time.Now().Add(-10 * time.Second),
				EndTime:         time.Now(),
				WriteFailures:   100000,
				WritesPerSecond: 0,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, false),
		Entry("active window also evaluated against MustRecoverWithin",
			DisruptionWindow{
				StartTime:       time.Now().Add(-2 * time.Minute),
				WritesPerSecond: 50,
				Policy:          OutagePolicy{MustRecoverWithin: time.Minute, MaxWriteOutage: time.Second},
			}, true),
	)

	It("DefaultOutagePolicy returns no zero-valued field", func() {
		p := DefaultOutagePolicy()
		Expect(p.MustRecoverWithin).NotTo(BeZero())
		Expect(p.MaxWriteOutage).NotTo(BeZero())
	})

	It("NoOutagePolicy grants the near-zero cushion and echoes recovery", func() {
		p := NoOutagePolicy(3 * time.Minute)
		Expect(p.MaxWriteOutage).To(Equal(NoOutageWriteOutageCushion))
		Expect(p.MustRecoverWithin).To(Equal(3 * time.Minute))
	})

	It("NoOutagePolicy is far tighter than DefaultOutagePolicy", func() {
		Expect(NoOutageWriteOutageCushion).To(BeNumerically("<", DefaultOutagePolicy().MaxWriteOutage))
	})
})
