// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package journal

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Journal", func() {
	Describe("Record and Events", func() {
		It("preserves levels and length", func() {
			j := New()
			j.Info("test", "first")
			j.Warn("test", "second")
			j.Error("test", "third")

			events := j.Events()
			Expect(events).To(HaveLen(3))
			Expect(events[0].Level).To(Equal(LevelInfo))
			Expect(events[1].Level).To(Equal(LevelWarn))
			Expect(events[2].Level).To(Equal(LevelError))
			Expect(j.Len()).To(Equal(3))
		})

		It("returns only events after a cutoff", func() {
			j := New()
			j.Info("test", "before")
			cutoff := time.Now()
			time.Sleep(2 * time.Millisecond)
			j.Info("test", "after1")
			j.Info("test", "after2")

			Expect(j.EventsSince(cutoff)).To(HaveLen(2))
		})
	})

	Describe("DisruptionWindow lifecycle", func() {
		It("opens, records failures, and closes correctly", func() {
			j := New()
			policy := OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 10}

			Expect(j.ActiveWindow()).To(BeNil())

			j.OpenDisruptionWindow("scale-up", policy)
			w := j.ActiveWindow()
			Expect(w).NotTo(BeNil())
			Expect(w.OperationName).To(Equal("scale-up"))
			Expect(w.IsActive()).To(BeTrue())

			j.RecordWriteFailure()
			j.RecordWriteFailure()
			j.RecordWriteFailure()
			Expect(j.ActiveWindow().WriteFailures).To(Equal(int64(3)))

			j.CloseDisruptionWindow()
			Expect(j.ActiveWindow()).To(BeNil())
			closed := j.DisruptionWindows()
			Expect(closed).To(HaveLen(1))
			Expect(closed[0].WriteFailures).To(Equal(int64(3)))
			Expect(closed[0].IsActive()).To(BeFalse())
		})

		It("opening a new window closes the previous active window", func() {
			j := New()
			j.OpenDisruptionWindow("op1", DefaultOutagePolicy())
			j.OpenDisruptionWindow("op2", DefaultOutagePolicy())
			Expect(j.ActiveWindow().OperationName).To(Equal("op2"))
			closed := j.DisruptionWindows()
			Expect(closed).To(HaveLen(1))
			Expect(closed[0].OperationName).To(Equal("op1"))
		})

		It("RecordWriteFailure without an active window is a no-op", func() {
			j := New()
			Expect(func() { j.RecordWriteFailure() }).NotTo(Panic())
		})
	})

	Describe("HasPolicyViolation", func() {
		It("returns false on empty journal", func() {
			Expect(New().HasPolicyViolation()).To(BeFalse())
		})

		It("returns false on a closed window within budget", func() {
			j := New()
			j.OpenDisruptionWindow("op", OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 10})
			j.CloseDisruptionWindow()
			Expect(j.HasPolicyViolation()).To(BeFalse())
		})

		It("returns true on a closed window over write-failure budget", func() {
			j := New()
			j.OpenDisruptionWindow("op", OutagePolicy{MustRecoverWithin: time.Minute, AllowedWriteFailures: 1})
			j.RecordWriteFailure()
			j.RecordWriteFailure()
			j.CloseDisruptionWindow()
			Expect(j.HasPolicyViolation()).To(BeTrue())
		})

		It("returns true on an active window over time budget", func() {
			j := New()
			j.OpenDisruptionWindow("op", OutagePolicy{MustRecoverWithin: time.Nanosecond, AllowedWriteFailures: 10})
			time.Sleep(1 * time.Millisecond)
			Expect(j.HasPolicyViolation()).To(BeTrue())
		})
	})

	It("appends concurrently without races (run with -race)", func() {
		j := New()
		var wg sync.WaitGroup
		const writers = 8
		const perWriter = 100
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < perWriter; k++ {
					j.Info("c", "x")
				}
			}()
		}
		wg.Wait()
		Expect(j.Len()).To(Equal(writers * perWriter))
	})
})
