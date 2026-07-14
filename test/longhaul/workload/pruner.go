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
)

const (
	// pruneInterval is how often the pruner trims old documents. A long-haul
	// run writes ~10 docs/sec/writer, so a few thousand rows accumulate per
	// writer between cycles — a small, index-backed DeleteMany each time.
	pruneInterval = 5 * time.Minute
)

// floorProvider reports the highest fully-verified seq per writer. *Verifier
// satisfies this; the pruner only ever deletes strictly below the floor, so it
// can never remove a document the verifier has not already accounted for.
type floorProvider interface {
	ConfirmedFloor(writerID string) int64
}

// pruneBackend abstracts the delete so Pruner can be unit-tested without a
// live collection. Production uses docdbPruneBackend.
type pruneBackend interface {
	// deleteThrough removes all documents for writerID with seq <= throughSeq
	// and returns the number deleted.
	deleteThrough(ctx context.Context, writerID string, throughSeq int64) (int64, error)
}

// docdbPruneBackend adapts *mongo.Collection to pruneBackend. The delete filter
// rides the existing unique (writer_id, seq) index, so it is a bounded range
// delete rather than a collection scan.
type docdbPruneBackend struct {
	coll *mongo.Collection
}

func (m docdbPruneBackend) deleteThrough(ctx context.Context, writerID string, throughSeq int64) (int64, error) {
	res, err := m.coll.DeleteMany(ctx, bson.D{
		{Key: "writer_id", Value: writerID},
		{Key: "seq", Value: bson.D{{Key: "$lte", Value: throughSeq}}},
	})
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// Pruner bounds the workload collection by keeping only the most recent
// retainPerWriter documents per writer. It deletes strictly below the
// verifier's confirmed floor, so pruning never affects the durability verdict:
// every removed document was already scanned, and the verifier's startup
// DB-min seed keeps a post-restart scan from misreading the pruned prefix as a
// gap.
type Pruner struct {
	writers         []*Writer
	floor           floorProvider
	backend         pruneBackend
	retainPerWriter int64
	metrics         *Metrics
	journal         *journal.Journal
}

// NewPruner constructs a Pruner. retainPerWriter must be > 0; callers gate on
// that (0 disables pruning entirely) before constructing.
func NewPruner(coll *mongo.Collection, writers []*Writer, floor floorProvider, retainPerWriter int64, metrics *Metrics, j *journal.Journal) *Pruner {
	return &Pruner{
		writers:         writers,
		floor:           floor,
		backend:         docdbPruneBackend{coll: coll},
		retainPerWriter: retainPerWriter,
		metrics:         metrics,
		journal:         j,
	}
}

// Run starts the prune loop. It blocks until the context is cancelled.
func (p *Pruner) Run(ctx context.Context) {
	p.journal.Info("pruner", fmt.Sprintf("pruner started (retain %d docs/writer)", p.retainPerWriter))
	defer p.journal.Info("pruner", "pruner stopped")

	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pruneAll(ctx)
		}
	}
}

func (p *Pruner) pruneAll(ctx context.Context) {
	for _, w := range p.writers {
		p.pruneWriter(ctx, w.id)
	}
}

// pruneWriter deletes the writer's documents older than the retention window.
// The delete boundary is (confirmed floor - retainPerWriter): everything at or
// below it is both fully verified and outside the retained tail, so removing it
// is safe and bounds the writer's footprint to ~retainPerWriter documents.
func (p *Pruner) pruneWriter(ctx context.Context, writerID string) {
	floor := p.floor.ConfirmedFloor(writerID)
	throughSeq := floor - p.retainPerWriter
	if throughSeq < 1 {
		// Not enough verified history yet to prune anything.
		return
	}

	deleted, err := p.backend.deleteThrough(ctx, writerID, throughSeq)
	if err != nil {
		p.journal.Warn("pruner", fmt.Sprintf("prune failed for writer %s: %v", writerID, err))
		return
	}
	if deleted > 0 {
		p.metrics.DocsPruned.Add(deleted)
		p.journal.Info("pruner", fmt.Sprintf("pruned %d docs for writer %s (seq <= %d)", deleted, writerID, throughSeq))
	}
}

// StartPruner launches a single pruner goroutine and returns it.
func StartPruner(ctx context.Context, coll *mongo.Collection, writers []*Writer, floor floorProvider, retainPerWriter int64, metrics *Metrics, j *journal.Journal) *Pruner {
	p := NewPruner(coll, writers, floor, retainPerWriter, metrics, j)
	go p.Run(ctx)
	return p
}
