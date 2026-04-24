package data

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	e2e "github.com/documentdb/documentdb-operator/test/e2e"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
)

var _ = Describe("DocumentDB data — sort/limit/skip",
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
			coll = handle.Database(dbName).Collection("sort_cursor")
			docs := seed.SortDataset()
			docsAny := make([]any, len(docs))
			for i := range docs {
				docsAny[i] = docs[i]
			}
			_, err := coll.InsertMany(ctx, docsAny)
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("sorts ascending by _id", func() {
			cur, err := coll.Find(ctx, bson.M{},
				options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(5))
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var results []bson.M
			Expect(cur.All(ctx, &results)).To(Succeed())
			Expect(results).To(HaveLen(5))
			Expect(results[0]["_id"]).To(BeEquivalentTo(1))
			// Strictly ascending.
			for i := 1; i < len(results); i++ {
				prev := toInt(results[i-1]["_id"])
				cur := toInt(results[i]["_id"])
				Expect(cur).To(BeNumerically(">", prev))
			}
		})

		It("sorts descending by _id", func() {
			cur, err := coll.Find(ctx, bson.M{},
				options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(3))
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var results []bson.M
			Expect(cur.All(ctx, &results)).To(Succeed())
			Expect(results).To(HaveLen(3))
			Expect(toInt(results[0]["_id"])).To(Equal(seed.SortDatasetSize))
		})

		It("limits and skips consistently", func() {
			// Full page 1 (no skip) of 10 results sorted by _id asc.
			page1, err := coll.Find(ctx, bson.M{},
				options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(10))
			Expect(err).NotTo(HaveOccurred())
			defer page1.Close(ctx)
			var page1Docs []bson.M
			Expect(page1.All(ctx, &page1Docs)).To(Succeed())
			Expect(page1Docs).To(HaveLen(10))

			// Page 2 is Skip(5) → first doc of page2 equals 6th of page1.
			page2, err := coll.Find(ctx, bson.M{},
				options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetSkip(5).SetLimit(5))
			Expect(err).NotTo(HaveOccurred())
			defer page2.Close(ctx)
			var page2Docs []bson.M
			Expect(page2.All(ctx, &page2Docs)).To(Succeed())
			Expect(page2Docs).To(HaveLen(5))
			Expect(page2Docs[0]["_id"]).To(Equal(page1Docs[5]["_id"]))
		})
	},
)

// toInt coerces numeric BSON values (int32/int64/int) to int for test
// comparisons. Panics on unexpected types so failure is obvious.
func toInt(v any) int {
	switch n := v.(type) {
	case int32:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	default:
		Fail("unexpected numeric type in _id")
		return 0
	}
}
