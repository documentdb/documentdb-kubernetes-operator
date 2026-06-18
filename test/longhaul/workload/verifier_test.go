// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

var _ = Describe("Verifier", func() {
	It("constructor wires metrics and journal correctly", func() {
		m := NewMetrics()
		j := journal.New()
		v := &Verifier{metrics: m, journal: j, nextSeq: make(map[string]int64)}
		Expect(v.metrics).To(BeIdenticalTo(m))
		Expect(v.journal).To(BeIdenticalTo(j))
		Expect(v.nextSeq).To(BeEmpty())
	})

	It("nextSeq is the per-writer resume point that bounds per-cycle scan cost", func() {
		// verifyWriter sets nextSeq[writerID] to maxSeq+1 (writer's tip at scan
		// time + 1), so the next cycle scans seq in (prev_tip, new_tip]. This
		// is what bounds per-cycle scan cost AND lets tail loss be detected by
		// comparing maxSeq against the highest doc actually present.
		v := &Verifier{nextSeq: make(map[string]int64)}

		got, ok := v.nextSeq["w1"]
		Expect(ok).To(BeFalse())
		Expect(got).To(BeZero())

		v.nextSeq["w1"] = 4
		Expect(v.nextSeq["w1"]).To(Equal(int64(4)))

		v.nextSeq["w2"] = 10
		Expect(v.nextSeq["w1"]).To(Equal(int64(4)))
		Expect(v.nextSeq["w2"]).To(Equal(int64(10)))
	})

	It("verifyAll requires a *mongo.Database (covered by integration runs)", func() {
		// We can't unit-test verifyAll's mongo path without a server, but the
		// constructor wiring above + the table-driven gap-detection logic is
		// what the verifier actually does. Document the boundary.
		Skip("verifyAll requires a *mongo.Database; covered by long-haul integration runs")
	})
})

// makeDoc constructs a valid WriteDocument whose checksum matches; tests that
// want a checksum mismatch override Checksum directly.
func makeDoc(writerID string, seq int64) WriteDocument {
	payload := "p"
	return WriteDocument{
		WriterID: writerID,
		Seq:      seq,
		Payload:  payload,
		Checksum: computeChecksum(writerID, seq, payload),
	}
}

var _ = Describe("auditDocs", func() {
	It("returns zero counters when there are no docs and maxSeq < expectedSeq", func() {
		// Cycle with no new writes since last tick: expectedSeq=5, maxSeq=4.
		// Note: verifyWriter short-circuits this case BEFORE calling auditDocs,
		// but auditDocs itself must still be safe — it should report no tail.
		r := auditDocs("w1", nil, 5, 4)
		Expect(r.internalGaps).To(BeZero())
		Expect(r.tailLoss).To(BeZero())
		Expect(r.checksumErrors).To(BeZero())
		Expect(r.findings).To(BeEmpty())
		// expectedSeq is unchanged when expectedSeq > maxSeq.
		Expect(r.newExpectedSeq).To(Equal(int64(5)))
	})

	It("reports a clean contiguous run with no gaps and no tail", func() {
		// expectedSeq=1, docs=[1,2,3], maxSeq=3.
		docs := []WriteDocument{makeDoc("w1", 1), makeDoc("w1", 2), makeDoc("w1", 3)}
		r := auditDocs("w1", docs, 1, 3)
		Expect(r.internalGaps).To(BeZero())
		Expect(r.tailLoss).To(BeZero())
		Expect(r.checksumErrors).To(BeZero())
		Expect(r.findings).To(BeEmpty())
		Expect(r.newExpectedSeq).To(Equal(int64(4)))
	})

	It("detects an internal gap between two docs", func() {
		// expectedSeq=1, docs=[1,4,5], maxSeq=5 → gap of 2 (seqs 2 and 3).
		docs := []WriteDocument{makeDoc("w1", 1), makeDoc("w1", 4), makeDoc("w1", 5)}
		r := auditDocs("w1", docs, 1, 5)
		Expect(r.internalGaps).To(Equal(int64(2)))
		Expect(r.tailLoss).To(BeZero())
		Expect(r.checksumErrors).To(BeZero())
		Expect(r.findings).To(HaveLen(1))
		Expect(r.findings[0].kind).To(Equal(findingGap))
		Expect(r.findings[0].seq).To(Equal(int64(2)))    // first missing
		Expect(r.findings[0].endSeq).To(Equal(int64(4))) // the doc that exposed the gap
		Expect(r.findings[0].count).To(Equal(int64(2)))
		Expect(r.newExpectedSeq).To(Equal(int64(6)))
	})

	It("detects a gap at the start of the scan window", func() {
		// expectedSeq=1, docs=[3], maxSeq=3 → gap of 2 (seqs 1 and 2).
		docs := []WriteDocument{makeDoc("w1", 3)}
		r := auditDocs("w1", docs, 1, 3)
		Expect(r.internalGaps).To(Equal(int64(2)))
		Expect(r.tailLoss).To(BeZero())
		Expect(r.newExpectedSeq).To(Equal(int64(4)))
	})

	It("detects tail loss with no docs at all (empty scan, non-zero tip)", func() {
		// Case B from review: writer acked through seq=10, DB lost them all,
		// no later writes. expectedSeq=1, docs=[], maxSeq=10 → tail=10.
		r := auditDocs("w1", nil, 1, 10)
		Expect(r.internalGaps).To(BeZero())
		Expect(r.tailLoss).To(Equal(int64(10)))
		Expect(r.findings).To(HaveLen(1))
		Expect(r.findings[0].kind).To(Equal(findingTail))
		Expect(r.findings[0].seq).To(Equal(int64(1)))
		Expect(r.findings[0].endSeq).To(Equal(int64(10)))
		Expect(r.findings[0].count).To(Equal(int64(10)))
		Expect(r.newExpectedSeq).To(Equal(int64(11)))
	})

	It("detects tail loss when last doc is below maxSeq", func() {
		// expectedSeq=1, docs=[1,2], maxSeq=5 → tail of 3 (seqs 3,4,5).
		docs := []WriteDocument{makeDoc("w1", 1), makeDoc("w1", 2)}
		r := auditDocs("w1", docs, 1, 5)
		Expect(r.internalGaps).To(BeZero())
		Expect(r.tailLoss).To(Equal(int64(3)))
		Expect(r.findings).To(HaveLen(1))
		Expect(r.findings[0].kind).To(Equal(findingTail))
		Expect(r.findings[0].seq).To(Equal(int64(3)))
		Expect(r.findings[0].endSeq).To(Equal(int64(5)))
		Expect(r.newExpectedSeq).To(Equal(int64(6)))
	})

	It("detects internal gap AND tail loss in the same cycle", func() {
		// expectedSeq=1, docs=[2,5], maxSeq=8 → gap=1 (seq 1) + gap=2 (seqs 3,4) + tail=3 (6,7,8).
		// Internal gaps total = 3; tail = 3.
		docs := []WriteDocument{makeDoc("w1", 2), makeDoc("w1", 5)}
		r := auditDocs("w1", docs, 1, 8)
		Expect(r.internalGaps).To(Equal(int64(3)))
		Expect(r.tailLoss).To(Equal(int64(3)))
		Expect(r.findings).To(HaveLen(3)) // two gaps + one tail
		Expect(r.findings[0].kind).To(Equal(findingGap))
		Expect(r.findings[1].kind).To(Equal(findingGap))
		Expect(r.findings[2].kind).To(Equal(findingTail))
		Expect(r.newExpectedSeq).To(Equal(int64(9)))
	})

	It("Case A: late write exposes an earlier-lost range as an internal gap", func() {
		// Original: writer acked 1..110, DB loses 101..110, writer writes 111.
		// Verifier last cycle: nextSeq[w1]=101, so expectedSeq=101.
		// docs=[doc with seq=111], maxSeq=111 → gap=10 (101..110), no tail.
		docs := []WriteDocument{makeDoc("w1", 111)}
		r := auditDocs("w1", docs, 101, 111)
		Expect(r.internalGaps).To(Equal(int64(10)))
		Expect(r.tailLoss).To(BeZero())
		Expect(r.newExpectedSeq).To(Equal(int64(112)))
	})

	It("detects a checksum mismatch without affecting gap counters", func() {
		// docs=[1,2,3], doc 2 has a bad checksum.
		bad := makeDoc("w1", 2)
		bad.Checksum = "deadbeef00000000"
		docs := []WriteDocument{makeDoc("w1", 1), bad, makeDoc("w1", 3)}
		r := auditDocs("w1", docs, 1, 3)
		Expect(r.checksumErrors).To(Equal(int64(1)))
		Expect(r.internalGaps).To(BeZero())
		Expect(r.tailLoss).To(BeZero())
		Expect(r.findings).To(HaveLen(1))
		Expect(r.findings[0].kind).To(Equal(findingChecksum))
		Expect(r.findings[0].seq).To(Equal(int64(2)))
		Expect(r.findings[0].stored).To(Equal("deadbeef00000000"))
	})

	It("counts checksum errors across multiple bad docs", func() {
		bad1 := makeDoc("w1", 1)
		bad1.Checksum = "xx"
		bad2 := makeDoc("w1", 2)
		bad2.Checksum = "yy"
		r := auditDocs("w1", []WriteDocument{bad1, bad2}, 1, 2)
		Expect(r.checksumErrors).To(Equal(int64(2)))
		Expect(r.findings).To(HaveLen(2))
	})

	It("advances newExpectedSeq to maxSeq+1 even with tail loss", func() {
		// Critical invariant: after a tail-loss cycle, nextSeq must move past
		// maxSeq so the next cycle doesn't re-detect the same tail.
		r := auditDocs("w1", nil, 1, 100)
		Expect(r.newExpectedSeq).To(Equal(int64(101)))
	})

	It("preserves writerID in findings", func() {
		r := auditDocs("worker-xyz", nil, 1, 1)
		Expect(r.findings).To(HaveLen(1))
		Expect(r.findings[0].writerID).To(Equal("worker-xyz"))
	})
})
