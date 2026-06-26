// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"context"
	"fmt"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
)

const (
	// verifyInterval is how often the verifier scans for gaps.
	verifyInterval = 10 * time.Second
)

// Verifier periodically scans the workload collection to detect sequence
// gaps, tail loss, and checksum mismatches in acknowledged writes.
//
// Per-cycle scan cost is bounded by nextSeq: each scan starts at the
// highest-checked seq+1 per writer, so cycle cost is O(new docs since last
// tick), not O(full history). Without this, a 100ms writer would accumulate
// ~864k docs/day per writer and re-reading the whole collection every 10s
// would dominate cluster load.
type Verifier struct {
	metrics    *Metrics
	journal    *journal.Journal
	collection *mongo.Collection
	writers    []*Writer

	// nextSeq[writerID] is the seq we'll start the next scan from for that
	// writer — set to (snapshotted writer.Seq() + 1) at the end of each cycle.
	// Consequence: any seq <= that snapshot is accounted for exactly once;
	// a late-arriving fill at a missing seq is not re-checked.
	// Only mutated from the verifier goroutine, so no lock is needed.
	nextSeq map[string]int64
}

// NewVerifier creates a verifier. writers is the set of writers whose tips
// the verifier will compare against the DB for tail-loss detection; pass nil
// to disable tail-loss checks (useful in unit tests).
func NewVerifier(db *mongo.Database, writers []*Writer, metrics *Metrics, j *journal.Journal) *Verifier {
	coll := db.Collection(CollectionName, options.Collection().
		SetReadConcern(readconcern.Majority()))
	return &Verifier{
		metrics:    metrics,
		journal:    j,
		collection: coll,
		writers:    writers,
		nextSeq:    make(map[string]int64),
	}
}

// Run starts the verifier loop. It blocks until the context is cancelled.
func (v *Verifier) Run(ctx context.Context) {
	v.journal.Info("verifier", "verifier started")
	defer v.journal.Info("verifier", "verifier stopped")

	ticker := time.NewTicker(verifyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.verifyAll(ctx)
		}
	}
}

func (v *Verifier) verifyAll(ctx context.Context) {
	for _, w := range v.writers {
		v.verifyWriter(ctx, w)
	}
	v.metrics.VerifyPasses.Add(1)
}

// findingKind labels what the verifier observed at a particular seq.
type findingKind int

const (
	findingGap findingKind = iota
	findingChecksum
	findingTail
)

// finding describes a single anomaly the verifier observed; auditDocs returns
// these so the caller can log them with full context without coupling the
// math to the journal.
type finding struct {
	kind     findingKind
	writerID string
	seq      int64  // doc.Seq for gap/checksum; expectedSeq for tail
	endSeq   int64  // for gap: doc.Seq; for tail: maxSeq; unused for checksum
	count    int64  // number of missing seqs (gap/tail), 1 for checksum
	stored   string // for checksum only
	computed string // for checksum only
}

// auditResult is the aggregate counts from one verifyWriter cycle. Pure —
// no I/O — so it's table-testable without a database.
type auditResult struct {
	newExpectedSeq int64
	internalGaps   int64
	tailLoss       int64
	checksumErrors int64
	findings       []finding
}

// auditDocs is the pure decision core of verifyWriter. Given the docs the
// verifier read (sorted by seq ascending) plus the writer's expected starting
// seq and current tip, it returns the new expected seq, the gap/checksum/tail
// counters, and a list of findings for the caller to log.
//
// Invariants checked:
//   - For each doc, if doc.Seq > expectedSeq, the slots in [expectedSeq, doc.Seq)
//     are missing (internal gap).
//   - For each doc, checksum is recomputed and compared.
//   - After processing all docs, if expectedSeq <= maxSeq the trailing slots
//     [expectedSeq, maxSeq] are missing (tail loss).
//   - On exit, newExpectedSeq is always maxSeq+1 when maxSeq >= initial
//     expectedSeq, so the next cycle accounts for everything past maxSeq.
func auditDocs(writerID string, docs []WriteDocument, expectedSeq, maxSeq int64) auditResult {
	var r auditResult
	for _, doc := range docs {
		if doc.Seq > expectedSeq {
			gaps := doc.Seq - expectedSeq
			r.internalGaps += gaps
			r.findings = append(r.findings, finding{
				kind: findingGap, writerID: writerID,
				seq: expectedSeq, endSeq: doc.Seq, count: gaps,
			})
		}
		expectedSeq = doc.Seq + 1

		want := computeChecksum(doc.WriterID, doc.Seq, doc.Payload)
		if doc.Checksum != want {
			r.checksumErrors++
			r.findings = append(r.findings, finding{
				kind: findingChecksum, writerID: writerID,
				seq: doc.Seq, count: 1,
				stored: doc.Checksum, computed: want,
			})
		}
	}

	if expectedSeq <= maxSeq {
		tail := maxSeq - expectedSeq + 1
		r.tailLoss = tail
		r.findings = append(r.findings, finding{
			kind: findingTail, writerID: writerID,
			seq: expectedSeq, endSeq: maxSeq, count: tail,
		})
		expectedSeq = maxSeq + 1
	}

	r.newExpectedSeq = expectedSeq
	return r
}

func (v *Verifier) verifyWriter(ctx context.Context, w *Writer) {
	writerID := w.id

	// Snapshot the writer's tip BEFORE scanning. Writes that commit after this
	// point land above maxSeq and the scan filter excludes them, so they're
	// accounted for in the next cycle. Reading w.Seq() first (vs. CountDocuments
	// first) guarantees expected <= what's-in-DB modulo real loss, so no false
	// positives from in-flight writes.
	maxSeq := w.Seq()

	expectedSeq := v.nextSeq[writerID]
	if expectedSeq == 0 {
		expectedSeq = 1
	}
	if maxSeq < expectedSeq {
		// Nothing new committed since last cycle.
		return
	}

	docs, err := v.fetchDocs(ctx, writerID, expectedSeq, maxSeq)
	if err != nil {
		v.journal.Warn("verifier", fmt.Sprintf("query failed for writer %s: %v", writerID, err))
		return
	}

	r := auditDocs(writerID, docs, expectedSeq, maxSeq)
	v.metrics.VerifyGapsDetected.Add(r.internalGaps + r.tailLoss)
	v.metrics.ChecksumErrors.Add(r.checksumErrors)
	for _, f := range r.findings {
		v.logFinding(f)
	}

	v.nextSeq[writerID] = r.newExpectedSeq
}

// fetchDocs reads all docs for writerID with seq in [expectedSeq, maxSeq],
// sorted by seq ascending. Decode errors are logged but skipped (the rest of
// the scan continues; a skipped doc looks like a gap to auditDocs).
func (v *Verifier) fetchDocs(ctx context.Context, writerID string, expectedSeq, maxSeq int64) ([]WriteDocument, error) {
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}})
	filter := bson.D{
		{Key: "writer_id", Value: writerID},
		{Key: "seq", Value: bson.D{
			{Key: "$gte", Value: expectedSeq},
			{Key: "$lte", Value: maxSeq},
		}},
	}
	cursor, err := v.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var out []WriteDocument
	for cursor.Next(ctx) {
		var doc WriteDocument
		if err := cursor.Decode(&doc); err != nil {
			v.journal.Warn("verifier", fmt.Sprintf("decode error for writer %s: %v", writerID, err))
			continue
		}
		out = append(out, doc)
	}
	return out, nil
}

func (v *Verifier) logFinding(f finding) {
	switch f.kind {
	case findingGap:
		v.journal.Error("verifier", fmt.Sprintf(
			"gap detected: writer=%s expected_seq=%d got_seq=%d (missing %d)",
			f.writerID, f.seq, f.endSeq, f.count))
	case findingTail:
		v.journal.Error("verifier", fmt.Sprintf(
			"tail loss: writer=%s expected_seq=%d acked_tip=%d (missing %d)",
			f.writerID, f.seq, f.endSeq, f.count))
	case findingChecksum:
		v.journal.Error("verifier", fmt.Sprintf(
			"checksum mismatch: writer=%s seq=%d stored=%s computed=%s",
			f.writerID, f.seq, f.stored, f.computed))
	}
}

// StartVerifier launches a single verifier goroutine and returns it.
//
// Only one verifier runs. Each verifier writes to the shared
// Metrics.VerifyGapsDetected counter, so running multiple would multi-count
// every real gap by N and double the cluster read load. One is sufficient
// because the per-writer nextSeq map bounds each cycle to new documents.
func StartVerifier(ctx context.Context, db *mongo.Database, writers []*Writer, metrics *Metrics, j *journal.Journal) *Verifier {
	v := NewVerifier(db, writers, metrics, j)
	go v.Run(ctx)
	return v
}
