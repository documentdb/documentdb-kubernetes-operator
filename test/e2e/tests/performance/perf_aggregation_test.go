package performance

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
)

// Aggregation threshold rationale
//
// A $group pipeline over a few thousand small documents on an indexed
// field is dominated by gateway + wire overhead, not planner work. On
// kind-on-laptop the pipeline completes in ~1–3s; the 45s budget is a
// generous upper bound that will only fail on a hard regression (e.g.,
// unexpected collection-scan fallback or planner bug).
var _ = Describe("DocumentDB performance — aggregation pipeline",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			copies     = 40 // seed.AggDataset * copies = 2,000 docs
			aggBudget  = 45 * time.Second
			batchWrite = 500
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("runs a $group aggregation within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("agg")

			// Replicate the canonical AggDataset so we stay within a
			// deterministic shape while reaching non-trivial size.
			base := seed.AggDataset()
			buf := make([]any, 0, batchWrite)
			id := 1
			flush := func() {
				if len(buf) == 0 {
					return
				}
				_, err := coll.InsertMany(ctx, buf)
				Expect(err).NotTo(HaveOccurred(), "seed agg")
				buf = buf[:0]
			}
			for c := 0; c < copies; c++ {
				for _, d := range base {
					cp := bson.M{}
					for k, v := range d {
						cp[k] = v
					}
					cp["_id"] = id
					id++
					buf = append(buf, cp)
					if len(buf) >= batchWrite {
						flush()
					}
				}
			}
			flush()

			pipeline := []bson.M{
				{"$group": bson.M{"_id": "$category", "total": bson.M{"$sum": "$value"}, "n": bson.M{"$sum": 1}}},
				{"$sort": bson.M{"_id": 1}},
			}

			start := time.Now()
			cur, err := coll.Aggregate(ctx, pipeline)
			Expect(err).NotTo(HaveOccurred(), "Aggregate")
			var out []bson.M
			Expect(cur.All(ctx, &out)).To(Succeed())
			elapsed := time.Since(start)
			logLatency("aggregate-group", elapsed)

			Expect(out).To(HaveLen(seed.AggDatasetGroups),
				"each AggDataset category should appear once")
			Expect(elapsed).To(BeNumerically("<", aggBudget),
				"$group pipeline should complete within %s", aggBudget)
		})
	})
