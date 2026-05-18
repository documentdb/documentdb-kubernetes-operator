package performance

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// Count/range threshold rationale
//
// After seeding 5,000 documents and creating an index on `value`, a
// half-range query (value >= midpoint) should hit the index and return
// ~2,500 documents quickly — well under a second on a hot kind cluster.
// We allow 30s to absorb port-forward warmup + cold-cache index
// traversal on busy CI nodes. Any regression past 30s likely means the
// planner stopped using the index.
var _ = Describe("DocumentDB performance — count with range + index",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			docCount    = 5_000
			countBudget = 30 * time.Second
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("counts half the range using an index within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("range_count")

			docs := make([]any, docCount)
			for i := 0; i < docCount; i++ {
				docs[i] = bson.M{"_id": i + 1, "value": i + 1}
			}
			_, err := coll.InsertMany(ctx, docs)
			Expect(err).NotTo(HaveOccurred(), "seed range_count")

			_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
				Keys:    bson.D{{Key: "value", Value: 1}},
				Options: options.Index().SetName("idx_value"),
			})
			Expect(err).NotTo(HaveOccurred(), "create value index")

			filter := bson.M{"value": bson.M{"$gte": docCount / 2}}

			start := time.Now()
			n, err := coll.CountDocuments(ctx, filter)
			elapsed := time.Since(start)
			logLatency("count-range", elapsed)

			Expect(err).NotTo(HaveOccurred(), "CountDocuments range")
			Expect(n).To(BeEquivalentTo(docCount/2 + 1))
			Expect(elapsed).To(BeNumerically("<", countBudget),
				"indexed range count should complete within %s", countBudget)
		})
	})
