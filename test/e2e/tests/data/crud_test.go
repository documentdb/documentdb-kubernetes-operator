package data

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"

	e2e "github.com/documentdb/documentdb-operator/test/e2e"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
)

var _ = Describe("DocumentDB data — CRUD",
	Ordered,
	Label(e2e.DataLabel, e2e.BasicLabel),
	e2e.MediumLevelLabel,
	func() {
		var (
			ctx    context.Context
			handle *emongo.Handle
			dbName string
		)

		BeforeAll(func() {
			ctx = context.Background()
			handle, dbName = connectSharedRO(ctx)
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("inserts a document and finds it", func() {
			coll := handle.Database(dbName).Collection("crud_insert_find")
			_, err := coll.InsertOne(ctx, bson.M{"_id": 1, "name": "alice", "score": 10})
			Expect(err).NotTo(HaveOccurred())
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": 1}).Decode(&got)).To(Succeed())
			Expect(got["name"]).To(Equal("alice"))
		})

		It("bulk inserts the small dataset and counts documents", func() {
			coll := handle.Database(dbName).Collection("crud_bulk")
			docs := seed.SmallDataset()
			any := make([]any, len(docs))
			for i := range docs {
				any[i] = docs[i]
			}
			_, err := coll.InsertMany(ctx, any)
			Expect(err).NotTo(HaveOccurred())
			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)))
		})

		It("updates a document in place", func() {
			coll := handle.Database(dbName).Collection("crud_update")
			_, err := coll.InsertOne(ctx, bson.M{"_id": 1, "status": "new"})
			Expect(err).NotTo(HaveOccurred())
			res, err := coll.UpdateOne(ctx, bson.M{"_id": 1}, bson.M{"$set": bson.M{"status": "done"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.ModifiedCount).To(Equal(int64(1)))
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": 1}).Decode(&got)).To(Succeed())
			Expect(got["status"]).To(Equal("done"))
		})

		It("deletes a document and observes the decrement", func() {
			coll := handle.Database(dbName).Collection("crud_delete")
			docs := []any{bson.M{"_id": 1}, bson.M{"_id": 2}, bson.M{"_id": 3}}
			_, err := coll.InsertMany(ctx, docs)
			Expect(err).NotTo(HaveOccurred())
			res, err := coll.DeleteOne(ctx, bson.M{"_id": 2})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedCount).To(Equal(int64(1)))
			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(2)))
		})
	},
)
