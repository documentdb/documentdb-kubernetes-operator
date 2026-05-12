// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

var _ = Describe("LeakDetector", func() {
	It("returns zero slope when sample count is below the floor", func() {
		d := NewLeakDetector(journal.New(), 10, 5)
		d.AddSample(ResourceSample{Timestamp: time.Now(), MemoryMB: 100})
		a := d.Analyze()
		Expect(a.HasLeak).To(BeFalse())
		Expect(a.MemorySlopeMB).To(BeZero())
		Expect(a.SampleCount).To(Equal(1))
	})

	It("does not flag a leak when memory is flat", func() {
		d := NewLeakDetector(journal.New(), 1.0, 3)
		t0 := time.Now()
		for i := 0; i < 10; i++ {
			d.AddSample(ResourceSample{
				Timestamp: t0.Add(time.Duration(i) * time.Second),
				MemoryMB:  100,
				CPUCores:  0.5,
			})
		}
		a := d.Analyze()
		Expect(a.HasLeak).To(BeFalse())
		Expect(absf(a.MemorySlopeMB)).To(BeNumerically("<", 0.001))
	})

	It("flags a leak when growth far exceeds the threshold", func() {
		// Threshold 100 MB/h; growth 1 MB/sec = 3600 MB/h.
		d := NewLeakDetector(journal.New(), 100.0, 3)
		t0 := time.Now()
		for i := 0; i < 60; i++ {
			d.AddSample(ResourceSample{
				Timestamp: t0.Add(time.Duration(i) * time.Second),
				MemoryMB:  100 + float64(i),
			})
		}
		a := d.Analyze()
		Expect(a.HasLeak).To(BeTrue())
		Expect(a.MemorySlopeMB).To(BeNumerically("~", 3600, 100))
	})

	It("does not flag a leak when growth stays below threshold", func() {
		// 0.01 MB/sec = 36 MB/h, threshold 100 MB/h.
		d := NewLeakDetector(journal.New(), 100.0, 3)
		t0 := time.Now()
		for i := 0; i < 60; i++ {
			d.AddSample(ResourceSample{
				Timestamp: t0.Add(time.Duration(i) * time.Second),
				MemoryMB:  100 + 0.01*float64(i),
			})
		}
		Expect(d.Analyze().HasLeak).To(BeFalse())
	})

	It("enforces the minSamples floor of 3", func() {
		d := NewLeakDetector(journal.New(), 10, 1) // request 1, floored to 3
		d.AddSample(ResourceSample{Timestamp: time.Now(), MemoryMB: 100})
		d.AddSample(ResourceSample{Timestamp: time.Now(), MemoryMB: 100})
		Expect(d.Analyze().MemorySlopeMB).To(BeZero())
	})
})

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
