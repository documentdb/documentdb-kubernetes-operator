package data

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	e2e "github.com/documentdb/documentdb-operator/test/e2e"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
)

var _ = Describe("DocumentDB data — aggregation",
	Ordered,
	Label(e2e.DataLabel),
	e2e.MediumLevelLabel,
	func() {
		var (
			ctx    context.Context
			handle *emongo.Handle
			dbName string
			coll   *mongo.Collection
		)

		BeforeAll(func() {
			ctx = context.Background()
			handle, dbName = connectSharedRO(ctx)
			coll = handle.Database(dbName).Collection("agg")
			docs := seed.AggDataset()
			any := make([]any, len(docs))
			for i := range docs {
				any[i] = docs[i]
			}
			_, err := coll.InsertMany(ctx, any)
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("groups documents by category and counts per-group cardinality", func() {
			pipe := mongo.Pipeline{
				{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: "$category"},
					{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
				}}},
			}
			cur, err := coll.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var results []bson.M
			Expect(cur.All(ctx, &results)).To(Succeed())
			Expect(results).To(HaveLen(seed.AggDatasetGroups))
			var total int64
			for _, r := range results {
				switch v := r["count"].(type) {
				case int32:
					total += int64(v)
				case int64:
					total += v
				default:
					Fail("unexpected count type")
				}
			}
			Expect(total).To(Equal(int64(seed.AggDatasetSize)))
		})

		It("filters with $match before grouping", func() {
			pipe := mongo.Pipeline{
				{{Key: "$match", Value: bson.D{{Key: "category", Value: "alpha"}}}},
				{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: "$category"},
					{Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}},
				}}},
			}
			cur, err := coll.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var results []bson.M
			Expect(cur.All(ctx, &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			Expect(results[0]["_id"]).To(Equal("alpha"))
		})

		It("projects selected fields with $project", func() {
			pipe := mongo.Pipeline{
				{{Key: "$match", Value: bson.D{{Key: "_id", Value: 1}}}},
				{{Key: "$project", Value: bson.D{
					{Key: "_id", Value: 0},
					{Key: "category", Value: 1},
				}}},
			}
			cur, err := coll.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var results []bson.M
			Expect(cur.All(ctx, &results)).To(Succeed())
			Expect(results).To(HaveLen(1))
			// _id was explicitly excluded; only category remains.
			Expect(results[0]).NotTo(HaveKey("_id"))
			Expect(results[0]).To(HaveKey("category"))
		})
	},
)
