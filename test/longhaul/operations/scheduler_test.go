// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

// fakeOp is a minimal Operation for scheduler tests.
type fakeOp struct {
	name      string
	weight    int
	available bool
	executed  int
	err       error
}

func (f *fakeOp) Name() string { return f.name }
func (f *fakeOp) Weight() int  { return f.weight }
func (f *fakeOp) Precondition(_ context.Context) (bool, string) {
	if f.available {
		return true, ""
	}
	return false, "precondition not met"
}
func (f *fakeOp) Execute(_ context.Context) error {
	f.executed++
	return f.err
}
func (f *fakeOp) OutagePolicy() journal.OutagePolicy { return journal.DefaultOutagePolicy() }

func newSchedulerForTest(ops ...Operation) *Scheduler {
	return &Scheduler{
		operations: ops,
		journal:    journal.New(),
		cooldown:   time.Hour,
	}
}

var _ = Describe("Scheduler", func() {
	Describe("selectOperation", func() {
		It("returns nil when no candidates pass precondition", func() {
			s := newSchedulerForTest(&fakeOp{name: "a", weight: 1, available: false})
			Expect(s.selectOperation(context.Background())).To(BeNil())
		})

		It("returns nil when total weight is zero", func() {
			a := &fakeOp{name: "a", weight: 0, available: true}
			s := newSchedulerForTest(a)
			Expect(s.selectOperation(context.Background())).To(BeNil())
		})

		It("picks only operations whose precondition passes", func() {
			a := &fakeOp{name: "a", weight: 1, available: false}
			b := &fakeOp{name: "b", weight: 1, available: true}
			s := newSchedulerForTest(a, b)
			for i := 0; i < 50; i++ {
				got := s.selectOperation(context.Background())
				Expect(got).NotTo(BeNil(), "iter %d", i)
				Expect(got.Name()).To(Equal("b"))
			}
		})

		It("respects relative weights (a:1, b:9 -> b ~ 90%)", func() {
			a := &fakeOp{name: "a", weight: 1, available: true}
			b := &fakeOp{name: "b", weight: 9, available: true}
			s := newSchedulerForTest(a, b)

			const trials = 2000
			bCount := 0
			for i := 0; i < trials; i++ {
				if s.selectOperation(context.Background()).Name() == "b" {
					bCount++
				}
			}
			// Expected 1800; allow generous +/-10% (180) for randomness.
			Expect(bCount).To(BeNumerically(">=", 1620))
			Expect(bCount).To(BeNumerically("<=", 1980))
		})
	})

	Describe("executeOp", func() {
		It("opens and closes a disruption window around the call", func() {
			op := &fakeOp{name: "op", weight: 1, available: true}
			s := newSchedulerForTest(op)
			s.executeOp(context.Background(), op)
			Expect(op.executed).To(Equal(1))
			Expect(s.journal.ActiveWindow()).To(BeNil())
			closed := s.journal.DisruptionWindows()
			Expect(closed).To(HaveLen(1))
			Expect(closed[0].OperationName).To(Equal("op"))
		})

		It("records an ERROR event when Execute fails", func() {
			op := &fakeOp{name: "boom", weight: 1, available: true, err: errors.New("kaboom")}
			s := newSchedulerForTest(op)
			s.executeOp(context.Background(), op)

			var sawError bool
			for _, e := range s.journal.Events() {
				if e.Level == journal.LevelError && e.Component == "scheduler" {
					sawError = true
				}
			}
			Expect(sawError).To(BeTrue(), "expected scheduler ERROR event on Execute failure")
		})
	})

	It("OpsExecuted mirrors the internal counter", func() {
		s := newSchedulerForTest()
		Expect(s.OpsExecuted()).To(Equal(0))
		s.opsExecuted = 7
		Expect(s.OpsExecuted()).To(Equal(7))
	})
})
