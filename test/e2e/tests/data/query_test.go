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

// seedSmall inserts seed.SmallDataset into coll and returns a convenience
// view over the dataset size.
func seedSmall(ctx context.Context, coll *mongo.Collection) int {
	docs := seed.SmallDataset()
	any := make([]any, len(docs))
	for i := range docs {
		any[i] = docs[i]
	}
	_, err := coll.InsertMany(ctx, any)
	Expect(err).NotTo(HaveOccurred())
	return len(docs)
}

var _ = Describe("DocumentDB data — query filters",
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
			coll = handle.Database(dbName).Collection("query_filters")
			seedSmall(ctx, coll)
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("filters with $eq", func() {
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"score": bson.M{"$eq": 50}}).Decode(&got)).To(Succeed())
			Expect(got["_id"]).To(BeEquivalentTo(5))
		})

		It("filters with $gt", func() {
			n, err := coll.CountDocuments(ctx, bson.M{"score": bson.M{"$gt": 50}})
			Expect(err).NotTo(HaveOccurred())
			// SmallDataset scores are N*10 for N in [1..10] → strictly > 50 means 6..10.
			Expect(n).To(Equal(int64(5)))
		})

		It("filters with $in", func() {
			n, err := coll.CountDocuments(ctx, bson.M{"_id": bson.M{"$in": []int{1, 3, 5, 99}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(3)))
		})

		It("filters with $and", func() {
			n, err := coll.CountDocuments(ctx, bson.M{"$and": []bson.M{
				{"score": bson.M{"$gte": 30}},
				{"score": bson.M{"$lte": 70}},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(5)))
		})

		It("filters with $or", func() {
			n, err := coll.CountDocuments(ctx, bson.M{"$or": []bson.M{
				{"_id": 1},
				{"_id": 10},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(2)))
		})

		It("filters with $regex on name", func() {
			// SmallDataset names are "doc-N" so all documents match "^doc-".
			n, err := coll.CountDocuments(ctx, bson.M{"name": bson.M{"$regex": "^doc-"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)))
		})
	},
)
