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
// gaps and checksum mismatches in acknowledged writes.
//
// Per-cycle scan cost is bounded by nextSeq: each scan starts at the
// highest-seen seq+1 per writer, so the cycle cost is O(new docs since last
// tick), not O(full history). Without this, a 100ms writer would accumulate
// ~864k docs/day per writer and re-reading the whole collection every 10s
// would dominate cluster load.
type Verifier struct {
	metrics    *Metrics
	journal    *journal.Journal
	collection *mongo.Collection

	// nextSeq[writerID] is highest-observed seq + 1 for that writer; documents
	// below this point are skipped on subsequent cycles. Consequence: a gap is
	// counted exactly once when we step past it — a late-arriving fill at the
	// missing seq is not re-checked.
	// Only mutated from the verifier goroutine, so no lock is needed.
	nextSeq map[string]int64
}

// NewVerifier creates a verifier.
func NewVerifier(db *mongo.Database, metrics *Metrics, j *journal.Journal) *Verifier {
	coll := db.Collection(CollectionName, options.Collection().
		SetReadConcern(readconcern.Majority()))
	return &Verifier{
		metrics:    metrics,
		journal:    j,
		collection: coll,
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
	// Get distinct writer IDs using aggregation (v2 API compatible).
	pipeline := bson.A{
		bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$writer_id"}}}},
	}
	cursor, err := v.collection.Aggregate(ctx, pipeline)
	if err != nil {
		v.journal.Warn("verifier", fmt.Sprintf("failed to get writer IDs: %v", err))
		return
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID string `bson:"_id"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		v.journal.Warn("verifier", fmt.Sprintf("failed to decode writer IDs: %v", err))
		return
	}

	for _, r := range results {
		v.verifyWriter(ctx, r.ID)
	}

	v.metrics.VerifyPasses.Add(1)
}

func (v *Verifier) verifyWriter(ctx context.Context, writerID string) {
	// Resume from where the previous cycle left off. First-ever scan starts at 1.
	expectedSeq := v.nextSeq[writerID]
	if expectedSeq == 0 {
		expectedSeq = 1
	}

	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}})
	filter := bson.D{
		{Key: "writer_id", Value: writerID},
		{Key: "seq", Value: bson.D{{Key: "$gte", Value: expectedSeq}}},
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

		// Check for gaps in the sequence.
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

	// Persist the resume point. Note: if no rows were returned, expectedSeq
	// is unchanged; if a gap was crossed, expectedSeq is past it, so a late
	// fill at the missing seq will be filtered out by seq >= nextSeq next cycle.
	v.nextSeq[writerID] = expectedSeq
}

// StartVerifier launches a single verifier goroutine and returns it.
//
// Only one verifier runs. Each verifier scans the full collection and writes
// to the shared Metrics.VerifyGapsDetected counter, so running multiple
// verifiers would multi-count every real gap by N and double the cluster
// read load. One verifier is sufficient because the per-writer nextSeq map
// bounds each cycle to new documents.
func StartVerifier(ctx context.Context, db *mongo.Database, metrics *Metrics, j *journal.Journal) *Verifier {
	v := NewVerifier(db, metrics, j)
	go v.Run(ctx)
	return v
}
