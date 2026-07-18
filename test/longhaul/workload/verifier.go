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

// auditor is the incremental decision core of verifyWriter. Feed it the docs
// for a writer in ascending seq order via step(); it accumulates gap/checksum
// counters and findings while holding only the running expectedSeq — never the
// docs themselves. finish(maxSeq) applies trailing tail-loss detection and
// returns the aggregate. Keeping state O(1) (not O(docs)) is what lets the
// production scan stream a cursor instead of buffering a whole cycle's window,
// which after a disruption pause can be tens of thousands of docs.
//
// Invariants checked:
//   - For each doc, if doc.Seq > expectedSeq, the slots in [expectedSeq, doc.Seq)
//     are missing (internal gap).
//   - For each doc, checksum is recomputed and compared.
//   - After all docs, if expectedSeq <= maxSeq the trailing slots
//     [expectedSeq, maxSeq] are missing (tail loss).
//   - On finish, newExpectedSeq is always maxSeq+1 when maxSeq >= initial
//     expectedSeq, so the next cycle accounts for everything past maxSeq.
type auditor struct {
	writerID    string
	expectedSeq int64
	r           auditResult
}

func newAuditor(writerID string, expectedSeq int64) *auditor {
	return &auditor{writerID: writerID, expectedSeq: expectedSeq}
}

// step folds a single doc into the running audit state.
func (a *auditor) step(doc WriteDocument) {
	if doc.Seq > a.expectedSeq {
		gaps := doc.Seq - a.expectedSeq
		a.r.internalGaps += gaps
		a.r.findings = append(a.r.findings, finding{
			kind: findingGap, writerID: a.writerID,
			seq: a.expectedSeq, endSeq: doc.Seq, count: gaps,
		})
	}
	a.expectedSeq = doc.Seq + 1

	want := computeChecksum(doc.WriterID, doc.Seq, doc.Payload)
	if doc.Checksum != want {
		a.r.checksumErrors++
		a.r.findings = append(a.r.findings, finding{
			kind: findingChecksum, writerID: a.writerID,
			seq: doc.Seq, count: 1,
			stored: doc.Checksum, computed: want,
		})
	}
}

// finish applies tail-loss detection for [expectedSeq, maxSeq] and returns the
// aggregate result.
func (a *auditor) finish(maxSeq int64) auditResult {
	if a.expectedSeq <= maxSeq {
		tail := maxSeq - a.expectedSeq + 1
		a.r.tailLoss = tail
		a.r.findings = append(a.r.findings, finding{
			kind: findingTail, writerID: a.writerID,
			seq: a.expectedSeq, endSeq: maxSeq, count: tail,
		})
		a.expectedSeq = maxSeq + 1
	}
	a.r.newExpectedSeq = a.expectedSeq
	return a.r
}

// auditDocs is the pure, table-testable entry point: it replays a slice of docs
// through an auditor. Production code uses the streaming path in scanWriter,
// which feeds cursor-decoded docs to an auditor one at a time.
func auditDocs(writerID string, docs []WriteDocument, expectedSeq, maxSeq int64) auditResult {
	a := newAuditor(writerID, expectedSeq)
	for _, doc := range docs {
		a.step(doc)
	}
	return a.finish(maxSeq)
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

	r, err := v.scanWriter(ctx, writerID, expectedSeq, maxSeq)
	if err != nil {
		v.journal.Warn("verifier", fmt.Sprintf("query failed for writer %s: %v", writerID, err))
		return
	}

	v.metrics.VerifyGapsDetected.Add(r.internalGaps + r.tailLoss)
	v.metrics.ChecksumErrors.Add(r.checksumErrors)
	for _, f := range r.findings {
		v.logFinding(f)
	}

	v.nextSeq[writerID] = r.newExpectedSeq
}

// scanWriter streams all docs for writerID with seq in [expectedSeq, maxSeq]
// (sorted by seq ascending) through an auditor, holding only one decoded doc at
// a time. This keeps verify memory O(1) per cycle regardless of how many docs
// accumulated since the last tick — which can be tens of thousands after a
// disruption pause — instead of materialising the whole window in a slice.
func (v *Verifier) scanWriter(ctx context.Context, writerID string, expectedSeq, maxSeq int64) (auditResult, error) {
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
		return auditResult{}, err
	}
	defer cursor.Close(ctx)

	a := newAuditor(writerID, expectedSeq)
	for cursor.Next(ctx) {
		var doc WriteDocument
		if err := cursor.Decode(&doc); err != nil {
			// Decode errors are logged but skipped (the rest of the scan
			// continues; a skipped doc looks like a gap to the auditor).
			v.journal.Warn("verifier", fmt.Sprintf("decode error for writer %s: %v", writerID, err))
			continue
		}
		a.step(doc)
	}
	// Surface iteration errors rather than silently treating a truncated read as
	// tail loss, which would be a false positive.
	if err := cursor.Err(); err != nil {
		return auditResult{}, err
	}
	return a.finish(maxSeq), nil
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
