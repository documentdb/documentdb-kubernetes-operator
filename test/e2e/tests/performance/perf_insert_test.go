package performance

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// Insert threshold rationale
//
// Seeding 10,000 small documents via InsertMany is the bulk-write
// tripwire. On a kind-on-laptop baseline the operation typically
// completes in 10–20s; CI nodes add variance. The 2-minute bound is a
// generous ~8x multiplier intended to catch catastrophic regressions
// (e.g., accidental per-document round-trips, gateway CPU starvation)
// rather than to grade performance.
var _ = Describe("DocumentDB performance — bulk insert",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			docCount       = 10_000
			insertBudget   = 2 * time.Minute
			perInsertBatch = 1_000
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("bulk-inserts 10k documents within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("bulk_insert")

			// Build the payload outside the timed region so we measure
			// server-side insert latency rather than Go allocations.
			batches := make([][]any, 0, docCount/perInsertBatch)
			for b := 0; b < docCount/perInsertBatch; b++ {
				docs := make([]any, perInsertBatch)
				base := b * perInsertBatch
				for i := 0; i < perInsertBatch; i++ {
					n := base + i + 1
					docs[i] = bson.M{
						"_id":   n,
						"kind":  "perf",
						"value": n,
					}
				}
				batches = append(batches, docs)
			}

			opCtx, cancel := context.WithTimeout(ctx, insertBudget)
			defer cancel()

			start := time.Now()
			for _, batch := range batches {
				_, err := coll.InsertMany(opCtx, batch)
				Expect(err).NotTo(HaveOccurred(), "InsertMany")
			}
			elapsed := time.Since(start)
			logLatency("insert-10k", elapsed)

			Expect(elapsed).To(BeNumerically("<", insertBudget),
				"bulk insert of %d docs should complete within %s", docCount, insertBudget)

			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(BeEquivalentTo(docCount))
		})
	})
