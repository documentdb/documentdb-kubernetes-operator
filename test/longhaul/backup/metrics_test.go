// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Metrics", func() {
	It("snapshots counters atomically", func() {
		m := NewMetrics()
		m.Scheduled.Add(3)
		m.Completed.Add(2)
		m.Failed.Add(1)
		m.RetentionViolations.Add(1)
		m.GCViolations.Add(1)
		m.VerifyCycles.Add(5)
		m.LastChildCount.Store(7)
		now := time.Now()
		m.LastScheduledUnix.Store(now.Unix())

		snap := m.Snapshot()
		Expect(snap.Scheduled).To(Equal(int64(3)))
		Expect(snap.Completed).To(Equal(int64(2)))
		Expect(snap.Failed).To(Equal(int64(1)))
		Expect(snap.RetentionViolations).To(Equal(int64(1)))
		Expect(snap.GCViolations).To(Equal(int64(1)))
		Expect(snap.VerifyCycles).To(Equal(int64(5)))
		Expect(snap.LastChildCount).To(Equal(int64(7)))
		Expect(snap.LastScheduled.Unix()).To(Equal(now.Unix()))
	})

	It("reports zero LastScheduled when never scheduled", func() {
		snap := NewMetrics().Snapshot()
		Expect(snap.LastScheduled.IsZero()).To(BeTrue())
	})

	DescribeTable("HasRetentionFailure",
		func(retention, gc int64, want bool) {
			m := NewMetrics()
			m.RetentionViolations.Add(retention)
			m.GCViolations.Add(gc)
			Expect(m.Snapshot().HasRetentionFailure()).To(Equal(want))
		},
		Entry("clean", int64(0), int64(0), false),
		Entry("retention violated", int64(1), int64(0), true),
		Entry("gc violated", int64(0), int64(1), true),
		Entry("both", int64(2), int64(3), true),
	)
})
