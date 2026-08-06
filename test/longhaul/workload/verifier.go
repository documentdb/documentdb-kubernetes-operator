// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
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

	// seeded[writerID] records whether the verifier has performed the one-time
	// startup seed of expectedSeq from the collection's current minimum seq.
	// Only touched from the verifier goroutine.
	seeded map[string]bool

	// floor[writerID] is the highest fully-verified seq for that writer
	// (nextSeq-1), published as an atomic so the retention pruner can read it
	// concurrently without racing the verifier goroutine. Everything at or
	// below the floor has been accounted for and is safe to prune.
	floor map[string]*atomic.Int64
}

// NewVerifier creates a verifier. writers is the set of writers whose tips
// the verifier will compare against the DB for tail-loss detection; pass nil
// to disable tail-loss checks (useful in unit tests).
func NewVerifier(db *mongo.Database, writers []*Writer, metrics *Metrics, j *journal.Journal) *Verifier {
	coll := db.Collection(CollectionName, options.Collection().
		SetReadConcern(readconcern.Majority()))
	floor := make(map[string]*atomic.Int64, len(writers))
	for _, w := range writers {
		floor[w.id] = &atomic.Int64{}
	}
	return &Verifier{
		metrics:    metrics,
		journal:    j,
		collection: coll,
		writers:    writers,
		nextSeq:    make(map[string]int64),
		seeded:     make(map[string]bool),
		floor:      floor,
	}
}

// ConfirmedFloor returns the highest seq the verifier has fully accounted for
// for writerID (0 if none yet). Every seq at or below this value has been
// scanned, so a pruner may safely delete documents below it without the
// verifier ever re-reading them (steady state) or misreading them as gaps
// (restart, thanks to the startup DB-min seed). Safe for concurrent reads.
func (v *Verifier) ConfirmedFloor(writerID string) int64 {
	if a, ok := v.floor[writerID]; ok {
		return a.Load()
	}
	return 0
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

	// One-time startup seed: retention pruning deletes already-verified
	// documents below the confirmed floor, so after a pod restart (which
	// resets the in-memory nextSeq to 1) a naive scan from seq 1 would see the
	// pruned prefix as one enormous gap — a false data-loss verdict. Advance
	// expectedSeq to the collection's current minimum seq so the verifier only
	// audits documents that still exist. Real gaps above the surviving floor
	// are still detected. Done once per writer; steady-state cycles never
	// re-read below nextSeq.
	if !v.seeded[writerID] {
		// Only mark seeded on success: a transient minSeq error (network blip,
		// majority-read timeout) must retry next cycle, otherwise the seed is
		// skipped forever and the very next scan runs from seq 1 against a
		// pruned collection — turning the surviving prefix into one giant false
		// gap. minSeq returning (0, nil) for an empty collection is a success.
		newExpected, ok, err := seedExpectedSeq(expectedSeq, func() (int64, error) {
			return v.minSeq(ctx, writerID)
		})
		if err != nil {
			v.journal.Warn("verifier", fmt.Sprintf("min-seq seed failed for writer %s (will retry): %v", writerID, err))
		} else {
			expectedSeq = newExpected
			v.seeded[writerID] = ok
		}
	}

	if maxSeq < expectedSeq {
		// Nothing new committed since last cycle.
		v.publishFloor(writerID, expectedSeq-1)
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
	v.publishFloor(writerID, r.newExpectedSeq-1)
}

// publishFloor records the highest fully-verified seq for writerID so the
// retention pruner can read it concurrently. No-op for writers not registered
// at construction (e.g. unit tests that pass nil writers).
func (v *Verifier) publishFloor(writerID string, floor int64) {
	if a, ok := v.floor[writerID]; ok {
		a.Store(floor)
	}
}

// seedExpectedSeq performs the one-time startup min-seq seed decision for a
// single writer. lookup resolves the collection's current minimum seq for that
// writer (v.minSeq in production). It returns the possibly-advanced expectedSeq
// and whether the seed succeeded; the caller must only mark the writer seeded
// when ok is true, so a transient lookup error retries on the next cycle rather
// than being skipped forever (which would scan a pruned collection from seq 1
// and register a false gap). An empty collection — lookup returning (0, nil) —
// counts as success and leaves expectedSeq unchanged.
func seedExpectedSeq(expectedSeq int64, lookup func() (int64, error)) (int64, bool, error) {
	minSeq, err := lookup()
	if err != nil {
		return expectedSeq, false, err
	}
	if minSeq > expectedSeq {
		expectedSeq = minSeq
	}
	return expectedSeq, true, nil
}

// minSeq returns the lowest seq currently stored for writerID, or 0 if the
// writer has no documents.
func (v *Verifier) minSeq(ctx context.Context, writerID string) (int64, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "seq", Value: 1}})
	var doc WriteDocument
	err := v.collection.FindOne(ctx, bson.M{"writer_id": writerID}, opts).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		return 0, err
	}
	return doc.Seq, nil
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
