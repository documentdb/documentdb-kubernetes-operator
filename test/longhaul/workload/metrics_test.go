// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Metrics", func() {
	It("NewMetrics records StartTime in the present", func() {
		before := time.Now()
		m := NewMetrics()
		after := time.Now()
		Expect(m.StartTime).To(BeTemporally(">=", before))
		Expect(m.StartTime).To(BeTemporally("<=", after))
	})

	It("Snapshot reads all counters and reports a positive elapsed", func() {
		m := NewMetrics()
		m.WriteAttempted.Store(10)
		m.WriteAcknowledged.Store(8)
		m.WriteFailed.Store(2)
		m.VerifyPasses.Store(3)
		m.VerifyGapsDetected.Store(1)
		m.ChecksumErrors.Store(0)

		time.Sleep(2 * time.Millisecond)
		s := m.Snapshot()
		Expect(s.WriteAttempted).To(Equal(int64(10)))
		Expect(s.WriteAcknowledged).To(Equal(int64(8)))
		Expect(s.WriteFailed).To(Equal(int64(2)))
		Expect(s.VerifyPasses).To(Equal(int64(3)))
		Expect(s.GapsDetected).To(Equal(int64(1)))
		Expect(s.ChecksumErrors).To(Equal(int64(0)))
		Expect(s.Elapsed).To(BeNumerically(">", 0))
	})

	DescribeTable("WriteSuccessRate",
		func(attempted, acked int64, want float64) {
			s := MetricsSnapshot{WriteAttempted: attempted, WriteAcknowledged: acked}
			Expect(s.WriteSuccessRate()).To(Equal(want))
		},
		Entry("no attempts returns 1.0", int64(0), int64(0), 1.0),
		Entry("all succeeded", int64(100), int64(100), 1.0),
		Entry("none succeeded", int64(100), int64(0), 0.0),
		Entry("half succeeded", int64(100), int64(50), 0.5),
		Entry("acked exceeds attempted (sanity, not clamped)", int64(10), int64(12), 1.2),
	)

	DescribeTable("HasDataLoss",
		func(gaps, checksums int64, want bool) {
			s := MetricsSnapshot{GapsDetected: gaps, ChecksumErrors: checksums}
			Expect(s.HasDataLoss()).To(Equal(want))
		},
		Entry("clean", int64(0), int64(0), false),
		Entry("one gap", int64(1), int64(0), true),
		Entry("one checksum mismatch", int64(0), int64(1), true),
		Entry("both", int64(5), int64(7), true),
	)

	It("counters are atomic-safe under concurrent increment + Snapshot", func() {
		// All counter mutations happen across goroutines in production
		// (writer + verifier + scheduler). Verify the atomic.Int64 fields
		// don't race under concurrent increment + Snapshot reads.
		m := NewMetrics()
		const writers = 8
		const perWriter = 1000

		var wg sync.WaitGroup
		wg.Add(writers + 1)

		for i := 0; i < writers; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < perWriter; j++ {
					m.WriteAttempted.Add(1)
					m.WriteAcknowledged.Add(1)
				}
			}()
		}
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_ = m.Snapshot()
			}
		}()
		wg.Wait()

		s := m.Snapshot()
		want := int64(writers * perWriter)
		Expect(s.WriteAttempted).To(Equal(want))
		Expect(s.WriteAcknowledged).To(Equal(want))
	})
})
