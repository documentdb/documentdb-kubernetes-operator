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
		v.journal.Warn("verifier", fmt.Sprintf("query failed for writer %s: %v", writerID, err))
		return
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var doc WriteDocument
		if err := cursor.Decode(&doc); err != nil {
			v.journal.Warn("verifier", fmt.Sprintf("decode error for writer %s: %v", writerID, err))
			continue
		}

		// Internal gap: missing seq numbers between two observed docs.
		if doc.Seq > expectedSeq {
			gaps := doc.Seq - expectedSeq
			v.metrics.VerifyGapsDetected.Add(gaps)
			v.journal.Error("verifier", fmt.Sprintf(
				"gap detected: writer=%s expected_seq=%d got_seq=%d (missing %d)",
				writerID, expectedSeq, doc.Seq, gaps))
		}
		expectedSeq = doc.Seq + 1

		// Verify checksum.
		expected := computeChecksum(doc.WriterID, doc.Seq, doc.Payload)
		if doc.Checksum != expected {
			v.metrics.ChecksumErrors.Add(1)
			v.journal.Error("verifier", fmt.Sprintf(
				"checksum mismatch: writer=%s seq=%d stored=%s computed=%s",
				writerID, doc.Seq, doc.Checksum, expected))
		}
	}

	// Tail loss: writer acked through maxSeq but DB has nothing in
	// (expectedSeq-1, maxSeq]. This catches the case where the most recent
	// acked writes vanished and no later writes have arrived to expose the
	// gap via the per-doc check above.
	if expectedSeq <= maxSeq {
		tail := maxSeq - expectedSeq + 1
		v.metrics.VerifyGapsDetected.Add(tail)
		v.journal.Error("verifier", fmt.Sprintf(
			"tail loss: writer=%s expected_seq=%d acked_tip=%d (missing %d)",
			writerID, expectedSeq, maxSeq, tail))
	}

	// We've accounted for every seq up to maxSeq; advance the resume point.
	v.nextSeq[writerID] = maxSeq + 1
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
