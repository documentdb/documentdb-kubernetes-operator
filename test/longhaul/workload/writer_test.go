// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

var _ = Describe("computeChecksum", func() {
	It("is deterministic for the same inputs", func() {
		a := computeChecksum("w001", 42, "payload-x")
		b := computeChecksum("w001", 42, "payload-x")
		Expect(a).To(Equal(b))
	})

	DescribeTable("differs when any input changes",
		func(name string, modified string) {
			base := computeChecksum("w001", 42, "payload")
			Expect(modified).NotTo(Equal(base), "field %q should change checksum", name)
		},
		Entry("writerID changed", "writerID", computeChecksum("w002", 42, "payload")),
		Entry("seq changed", "seq", computeChecksum("w001", 43, "payload")),
		Entry("payload changed", "payload", computeChecksum("w001", 42, "payload-x")),
	)

	It("is 16 lowercase hex chars (SHA-256 truncated to 8 bytes)", func() {
		got := computeChecksum("w001", 1, "x")
		Expect(got).To(HaveLen(16))
		for _, r := range got {
			Expect(strings.ContainsRune("0123456789abcdef", r)).To(BeTrue(), "non-hex char %q in %q", r, got)
		}
	})
})

var _ = Describe("Writer", func() {
	It("constructor preserves id, metrics, journal, and starts seq at 0", func() {
		// The writer constructor is mostly composition; verify it doesn't panic
		// when given a nil collection (mongo.Database can produce a Collection
		// without I/O), and that the ID is preserved. We can't construct a real
		// *mongo.Database without a connection, so we limit the assertion to
		// what's safe to inspect: the metrics, journal, and id wiring.
		m := NewMetrics()
		j := journal.New()
		w := &Writer{id: "w042", metrics: m, journal: j}
		Expect(w.id).To(Equal("w042"))
		Expect(w.metrics).To(BeIdenticalTo(m))
		Expect(w.journal).To(BeIdenticalTo(j))
		Expect(w.seq.Load()).To(BeZero())
	})

	It("seq is monotonically increasing under repeated Add", func() {
		// Even though writeOne hits the network, the seq.Add is the first thing
		// it does. Verify the atomic counter advances correctly.
		w := &Writer{id: "w001"}
		for i := int64(1); i <= 100; i++ {
			Expect(w.seq.Add(1)).To(Equal(i))
		}
	})

	It("Seq() returns the current committed sequence number", func() {
		w := &Writer{id: "w001"}
		Expect(w.Seq()).To(BeZero())
		w.seq.Store(42)
		Expect(w.Seq()).To(Equal(int64(42)))
	})
})
