// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

// fakeFloor is a controllable floorProvider.
type fakeFloor map[string]int64

func (f fakeFloor) ConfirmedFloor(writerID string) int64 { return f[writerID] }

// fakePruneBackend records deleteThrough calls and returns scripted results.
type fakePruneBackend struct {
	// deletedThrough[writerID] is the highest throughSeq requested.
	calls []pruneCall
	// perWriterDeleted maps writerID -> count returned by deleteThrough.
	perWriterDeleted map[string]int64
	err              error
}

type pruneCall struct {
	writerID   string
	throughSeq int64
}

func (f *fakePruneBackend) deleteThrough(_ context.Context, writerID string, throughSeq int64) (int64, error) {
	f.calls = append(f.calls, pruneCall{writerID: writerID, throughSeq: throughSeq})
	if f.err != nil {
		return 0, f.err
	}
	if f.perWriterDeleted != nil {
		return f.perWriterDeleted[writerID], nil
	}
	return 0, nil
}

func newTestPruner(b pruneBackend, floor floorProvider, retain int64, m *Metrics) *Pruner {
	return &Pruner{
		writers:         []*Writer{{id: "w000"}, {id: "w001"}},
		floor:           floor,
		backend:         b,
		retainPerWriter: retain,
		metrics:         m,
		journal:         journal.New(),
	}
}

var _ = Describe("Pruner.pruneWriter", func() {
	It("deletes through (floor - retainPerWriter) when there is enough history", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 500}}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 10_000}, 2_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(HaveLen(1))
		Expect(b.calls[0]).To(Equal(pruneCall{writerID: "w000", throughSeq: 8_000}))
		Expect(m.DocsPruned.Load()).To(Equal(int64(500)))
	})

	It("does not delete when the floor is below the retention window", func() {
		b := &fakePruneBackend{}
		m := NewMetrics()
		// floor 1500, retain 2000 => throughSeq = -500 < 1 => no delete.
		p := newTestPruner(b, fakeFloor{"w000": 1_500}, 2_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(BeEmpty())
		Expect(m.DocsPruned.Load()).To(BeZero())
	})

	It("does not delete when the floor is exactly at the retention window", func() {
		b := &fakePruneBackend{}
		m := NewMetrics()
		// floor 2000, retain 2000 => throughSeq = 0 < 1 => no delete.
		p := newTestPruner(b, fakeFloor{"w000": 2_000}, 2_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(BeEmpty())
	})

	It("prunes the first document once the floor advances one past the window", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 1}}
		m := NewMetrics()
		// floor 2001, retain 2000 => throughSeq = 1 => delete seq <= 1.
		p := newTestPruner(b, fakeFloor{"w000": 2_001}, 2_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(HaveLen(1))
		Expect(b.calls[0].throughSeq).To(Equal(int64(1)))
	})

	It("does not count deletions or advance metrics when the backend errors", func() {
		b := &fakePruneBackend{err: errors.New("delete failed")}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 10_000}, 2_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(HaveLen(1))
		Expect(m.DocsPruned.Load()).To(BeZero())
	})

	It("never deletes at or above the confirmed floor (safety invariant)", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 3}}
		m := NewMetrics()
		floor := int64(5_000)
		p := newTestPruner(b, fakeFloor{"w000": floor}, 1_000, m)

		p.pruneWriter(context.Background(), "w000")

		Expect(b.calls).To(HaveLen(1))
		Expect(b.calls[0].throughSeq).To(BeNumerically("<", floor),
			"pruner must only delete strictly below the verifier's confirmed floor")
	})
})

var _ = Describe("Pruner.pruneAll", func() {
	It("prunes every writer using its own floor", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 10, "w001": 20}}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 5_000, "w001": 9_000}, 1_000, m)

		p.pruneAll(context.Background())

		Expect(b.calls).To(ConsistOf(
			pruneCall{writerID: "w000", throughSeq: 4_000},
			pruneCall{writerID: "w001", throughSeq: 8_000},
		))
		Expect(m.DocsPruned.Load()).To(Equal(int64(30)))
	})

	It("freezes pruning when a checksum error has been detected (preserve evidence)", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 10, "w001": 20}}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 5_000, "w001": 9_000}, 1_000, m)
		m.ChecksumErrors.Add(1)

		p.pruneAll(context.Background())

		Expect(b.calls).To(BeEmpty(), "no deletes must be issued once data loss is detected")
		Expect(m.DocsPruned.Load()).To(BeZero())
		Expect(p.frozen).To(BeTrue())
	})

	It("freezes pruning when a gap has been detected", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 10}}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 5_000}, 1_000, m)
		m.VerifyGapsDetected.Add(1)

		p.pruneAll(context.Background())

		Expect(b.calls).To(BeEmpty())
	})

	It("stays frozen on later cycles even though the failure counters are unchanged", func() {
		b := &fakePruneBackend{perWriterDeleted: map[string]int64{"w000": 10}}
		m := NewMetrics()
		p := newTestPruner(b, fakeFloor{"w000": 5_000}, 1_000, m)
		m.ChecksumErrors.Add(1)

		p.pruneAll(context.Background())
		p.pruneAll(context.Background())

		Expect(b.calls).To(BeEmpty())
		Expect(p.frozen).To(BeTrue())
	})
})
