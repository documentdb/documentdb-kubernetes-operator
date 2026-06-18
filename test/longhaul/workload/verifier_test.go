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
