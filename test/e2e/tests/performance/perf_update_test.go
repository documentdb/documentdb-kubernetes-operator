package performance

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// Update threshold rationale
//
// A single UpdateMany over 5,000 docs that sets a new field on every
// document is one round-trip per call, so the wall-clock cost is
// dominated by server-side write amplification + WAL. Kind-on-laptop
// baseline is ~2–5s; 90s is a generous tripwire that catches pathologic
// regressions (e.g., accidentally rewriting $set as per-doc upserts).
var _ = Describe("DocumentDB performance — bulk update",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			docCount     = 5_000
			updateBudget = 90 * time.Second
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("bulk-updates every document within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("bulk_update")

			docs := make([]any, docCount)
			for i := 0; i < docCount; i++ {
				docs[i] = bson.M{"_id": i + 1, "touched": false, "value": i}
			}
			_, err := coll.InsertMany(ctx, docs)
			Expect(err).NotTo(HaveOccurred(), "seed bulk_update")

			start := time.Now()
			res, err := coll.UpdateMany(ctx,
				bson.M{"touched": false},
				bson.M{"$set": bson.M{"touched": true, "stamp": "perf"}},
			)
			elapsed := time.Since(start)
			logLatency("update-5k", elapsed)

			Expect(err).NotTo(HaveOccurred(), "UpdateMany")
			Expect(res.MatchedCount).To(BeEquivalentTo(docCount))
			Expect(res.ModifiedCount).To(BeEquivalentTo(docCount))
			Expect(elapsed).To(BeNumerically("<", updateBudget),
				"bulk update should complete within %s", updateBudget)

			remaining, err := coll.CountDocuments(ctx, bson.M{"touched": false})
			Expect(err).NotTo(HaveOccurred())
			Expect(remaining).To(BeEquivalentTo(0))
		})
	})
