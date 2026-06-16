// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	// CollectionName is the MongoDB collection used by the workload.
	CollectionName = "longhaul_writes"

	// writeInterval is the time between sequential writes per writer.
	writeInterval = 100 * time.Millisecond
)

// WriteDocument is the schema for data-plane durability tracking.
type WriteDocument struct {
	WriterID  string    `bson:"writer_id"`
	Seq       int64     `bson:"seq"`
	Payload   string    `bson:"payload"`
	Checksum  string    `bson:"checksum"`
	Timestamp time.Time `bson:"timestamp"`
}

// Writer performs sequential inserts to a MongoDB collection.
// Each writer has a unique ID and tracks its own sequence number.
type Writer struct {
	id         string
	seq        atomic.Int64
	metrics    *Metrics
	journal    *journal.Journal
	collection *mongo.Collection
}

// NewWriter creates a writer with the given ID connected to the specified database.
func NewWriter(id string, db *mongo.Database, metrics *Metrics, j *journal.Journal) *Writer {
	coll := db.Collection(CollectionName, options.Collection().
		SetWriteConcern(writeconcern.Majority()))
	return &Writer{
		id:         id,
		metrics:    metrics,
		journal:    j,
		collection: coll,
	}
}

// Run starts the writer loop. It blocks until the context is cancelled.
func (w *Writer) Run(ctx context.Context) {
	w.journal.Info("writer", fmt.Sprintf("writer %s started", w.id))
	defer w.journal.Info("writer", fmt.Sprintf("writer %s stopped", w.id))

	ticker := time.NewTicker(writeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.writeOne(ctx)
		}
	}
}

func (w *Writer) writeOne(ctx context.Context) {
	seq := w.seq.Add(1)
	payload := fmt.Sprintf("writer=%s seq=%d t=%d", w.id, seq, time.Now().UnixNano())
	checksum := computeChecksum(w.id, seq, payload)

	doc := WriteDocument{
		WriterID:  w.id,
		Seq:       seq,
		Payload:   payload,
		Checksum:  checksum,
		Timestamp: time.Now(),
	}

	w.metrics.WriteAttempted.Add(1)

	_, err := w.collection.InsertOne(ctx, doc)
	if err != nil {
		// Retryable writes are on by default in the v2 driver, so a network
		// blip during a disruption window can produce this sequence:
		//   1. driver sends InsertOne, server commits, ACK is dropped
		//   2. driver auto-retries the same _id, server returns code 11000
		//   3. InsertOne returns a duplicate-key error to us
		// The data is durably committed in case (3), so counting it as a write
		// failure (and feeding the policy/AllowedWriteFailures gate) would turn
		// successful writes into spurious FAIL verdicts. Treat dup-key as a
		// successful, idempotent ACK instead.
		if mongo.IsDuplicateKeyError(err) {
			w.metrics.WriteAcknowledged.Add(1)
			return
		}
		w.metrics.WriteFailed.Add(1)
		w.journal.RecordWriteFailure()
		return
	}
	w.metrics.WriteAcknowledged.Add(1)
}

// computeChecksum creates a deterministic hash of the write for verification.
func computeChecksum(writerID string, seq int64, payload string) string {
	data := fmt.Sprintf("%s:%d:%s", writerID, seq, payload)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8])
}

// StartWriters launches n writers and returns a cancel function to stop them.
func StartWriters(ctx context.Context, n int, db *mongo.Database, metrics *Metrics, j *journal.Journal) []*Writer {
	writers := make([]*Writer, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("w%03d", i)
		writers[i] = NewWriter(id, db, metrics, j)
		go writers[i].Run(ctx)
	}
	return writers
}

// EnsureIndexes creates the necessary indexes on the workload collection.
func EnsureIndexes(ctx context.Context, db *mongo.Database) error {
	coll := db.Collection(CollectionName)
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "writer_id", Value: 1},
			{Key: "seq", Value: 1},
		},
		Options: options.Index().SetUnique(true),
	})
	return err
}
