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

// fakeBackend is a controllable writeBackend stub for writer state-machine tests.
type fakeBackend struct {
	insertErrs       []error // returned in order; if exhausted, returns last
	dupClassifier    func(error) bool
	insertedDocs     []WriteDocument
	highestSeqReturn int64
	highestSeqErr    error
	highestSeqCalls  int
}

func (f *fakeBackend) insert(_ context.Context, doc WriteDocument) error {
	f.insertedDocs = append(f.insertedDocs, doc)
	if len(f.insertErrs) == 0 {
		return nil
	}
	err := f.insertErrs[0]
	if len(f.insertErrs) > 1 {
		f.insertErrs = f.insertErrs[1:]
	}
	return err
}

func (f *fakeBackend) isDuplicate(err error) bool {
	if f.dupClassifier == nil {
		return false
	}
	return f.dupClassifier(err)
}

func (f *fakeBackend) highestSeq(_ context.Context, _ string) (int64, error) {
	f.highestSeqCalls++
	return f.highestSeqReturn, f.highestSeqErr
}

func newTestWriter(b writeBackend) *Writer {
	return &Writer{
		id:      "w001",
		metrics: NewMetrics(),
		journal: journal.New(),
		backend: b,
	}
}

var errDup = errors.New("dup-key")
var errTransient = errors.New("transient network error")

var _ = Describe("Writer.writeOne", func() {
	It("on success: advances seq, increments Attempted+Acknowledged, sends correct doc", func() {
		b := &fakeBackend{}
		w := newTestWriter(b)
		w.writeOne(context.Background())

		Expect(w.Seq()).To(Equal(int64(1)))
		Expect(w.metrics.WriteAttempted.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteAcknowledged.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteFailed.Load()).To(BeZero())

		Expect(b.insertedDocs).To(HaveLen(1))
		got := b.insertedDocs[0]
		Expect(got.WriterID).To(Equal("w001"))
		Expect(got.Seq).To(Equal(int64(1)))
		// Checksum must match what computeChecksum would produce for this payload.
		Expect(got.Checksum).To(Equal(computeChecksum(got.WriterID, got.Seq, got.Payload)))
	})

	It("on DupKey error: advances seq + Acknowledged, NOT WriteFailed (retryable-write ack)", func() {
		b := &fakeBackend{
			insertErrs:    []error{errDup},
			dupClassifier: func(err error) bool { return errors.Is(err, errDup) },
		}
		w := newTestWriter(b)
		w.writeOne(context.Background())

		Expect(w.Seq()).To(Equal(int64(1)), "DupKey is an idempotent ack; seq must advance")
		Expect(w.metrics.WriteAcknowledged.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteFailed.Load()).To(BeZero(), "DupKey must not count as a failure")
	})

	It("on non-DupKey error: seq does NOT advance, WriteFailed++, journal records failure", func() {
		b := &fakeBackend{
			insertErrs:    []error{errTransient},
			dupClassifier: func(err error) bool { return errors.Is(err, errDup) },
		}
		w := newTestWriter(b)
		w.writeOne(context.Background())

		Expect(w.Seq()).To(BeZero(), "seq must not advance on non-DupKey error")
		Expect(w.metrics.WriteAttempted.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteFailed.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteAcknowledged.Load()).To(BeZero())
		// Next tick must retry the same seq=1 (since seq is still 0, next call computes 0+1).
	})

	It("retries the same seq after a non-DupKey failure", func() {
		// First call: transient error -> seq stays 0.
		// Second call: success -> seq becomes 1 (retry of the same logical write).
		b := &fakeBackend{
			insertErrs:    []error{errTransient, nil},
			dupClassifier: func(err error) bool { return false },
		}
		w := newTestWriter(b)
		w.writeOne(context.Background())
		w.writeOne(context.Background())

		Expect(w.Seq()).To(Equal(int64(1)))
		Expect(b.insertedDocs).To(HaveLen(2))
		Expect(b.insertedDocs[0].Seq).To(Equal(int64(1)))
		Expect(b.insertedDocs[1].Seq).To(Equal(int64(1)), "second attempt must reuse seq=1")
		Expect(w.metrics.WriteFailed.Load()).To(Equal(int64(1)))
		Expect(w.metrics.WriteAcknowledged.Load()).To(Equal(int64(1)))
	})

	It("advances seq monotonically across N successful writes", func() {
		b := &fakeBackend{}
		w := newTestWriter(b)
		for i := 0; i < 5; i++ {
			w.writeOne(context.Background())
		}
		Expect(w.Seq()).To(Equal(int64(5)))
		Expect(b.insertedDocs).To(HaveLen(5))
		for i, doc := range b.insertedDocs {
			Expect(doc.Seq).To(Equal(int64(i + 1)))
		}
	})
})

var _ = Describe("Writer.Resume", func() {
	It("on empty collection: returns 0 and leaves seq at 0", func() {
		b := &fakeBackend{highestSeqReturn: 0}
		w := newTestWriter(b)
		got, err := w.Resume(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeZero())
		Expect(w.Seq()).To(BeZero())
	})

	It("with existing data: seeds seq from the highest persisted seq", func() {
		b := &fakeBackend{highestSeqReturn: 42}
		w := newTestWriter(b)
		got, err := w.Resume(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(int64(42)))
		Expect(w.Seq()).To(Equal(int64(42)), "subsequent writeOne will compute seq=43")
	})

	It("on backend error: returns the error and leaves seq untouched", func() {
		boom := errors.New("network down")
		b := &fakeBackend{highestSeqErr: boom}
		w := newTestWriter(b)
		w.seq.Store(99) // pretend something was already there

		got, err := w.Resume(context.Background())
		Expect(err).To(MatchError(boom))
		Expect(got).To(BeZero())
		Expect(w.Seq()).To(Equal(int64(99)), "Resume must not clobber seq on error")
	})
})
