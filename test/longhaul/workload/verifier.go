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

// Verifier periodically scans the workload collection to detect
// sequence gaps and checksum mismatches in acknowledged writes.
type Verifier struct {
	id         string
	metrics    *Metrics
	journal    *journal.Journal
	collection *mongo.Collection
}

// NewVerifier creates a verifier with the given ID.
func NewVerifier(id string, db *mongo.Database, metrics *Metrics, j *journal.Journal) *Verifier {
	coll := db.Collection(CollectionName, options.Collection().
		SetReadConcern(readconcern.Majority()))
	return &Verifier{
		id:         id,
		metrics:    metrics,
		journal:    j,
		collection: coll,
	}
}

// Run starts the verifier loop. It blocks until the context is cancelled.
func (v *Verifier) Run(ctx context.Context) {
	v.journal.Info("verifier", fmt.Sprintf("verifier %s started", v.id))
	defer v.journal.Info("verifier", fmt.Sprintf("verifier %s stopped", v.id))

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
	// Query all documents for this writer, ordered by sequence.
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}})
	cursor, err := v.collection.Find(ctx, bson.D{{Key: "writer_id", Value: writerID}}, opts)
	if err != nil {
		v.journal.Warn("verifier", fmt.Sprintf("query failed for writer %s: %v", writerID, err))
		return
	}
	defer cursor.Close(ctx)

	var expectedSeq int64 = 1
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
}

// StartVerifiers launches n verifiers and returns them.
func StartVerifiers(ctx context.Context, n int, db *mongo.Database, metrics *Metrics, j *journal.Journal) []*Verifier {
	verifiers := make([]*Verifier, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("v%03d", i)
		verifiers[i] = NewVerifier(id, db, metrics, j)
		go verifiers[i].Run(ctx)
	}
	return verifiers
}
