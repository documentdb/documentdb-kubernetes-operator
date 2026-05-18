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

// Sort threshold rationale
//
// With an ascending index on the sort field, a full collection sort
// over 5,000 docs is effectively a scan-of-index + cursor drain. On
// kind-on-laptop this completes in ~2s; the 60s cap absorbs cold index
// loads, port-forward warmup, and CI noise. A regression past the cap
// almost always means the sort fell back to in-memory post-processing.
var _ = Describe("DocumentDB performance — indexed sort",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			docCount   = 5_000
			sortBudget = 60 * time.Second
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("drains a sorted cursor using an index within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("sorted")

			// Seed in reverse order so a naive collection-scan sort
			// would be slower than an index-assisted one — makes the
			// index actually useful for the assertion.
			docs := make([]any, docCount)
			for i := 0; i < docCount; i++ {
				docs[i] = bson.M{"_id": i + 1, "score": docCount - i}
			}
			_, err := coll.InsertMany(ctx, docs)
			Expect(err).NotTo(HaveOccurred(), "seed sorted")

			_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
				Keys:    bson.D{{Key: "score", Value: 1}},
				Options: options.Index().SetName("idx_score"),
			})
			Expect(err).NotTo(HaveOccurred(), "create score index")

			findOpts := options.Find().SetSort(bson.D{{Key: "score", Value: 1}})

			start := time.Now()
			cur, err := coll.Find(ctx, bson.M{}, findOpts)
			Expect(err).NotTo(HaveOccurred(), "Find with sort")
			var last int32
			first := true
			count := 0
			for cur.Next(ctx) {
				var d struct {
					Score int32 `bson:"score"`
				}
				Expect(cur.Decode(&d)).To(Succeed())
				if !first {
					Expect(d.Score).To(BeNumerically(">=", last),
						"sort output must be non-decreasing")
				}
				last = d.Score
				first = false
				count++
			}
			Expect(cur.Err()).NotTo(HaveOccurred())
			Expect(cur.Close(ctx)).To(Succeed())
			elapsed := time.Since(start)
			logLatency("sort-index", elapsed)

			Expect(count).To(Equal(docCount))
			Expect(elapsed).To(BeNumerically("<", sortBudget),
				"indexed sort should complete within %s", sortBudget)
		})
	})
