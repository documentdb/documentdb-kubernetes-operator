package data

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	e2e "github.com/documentdb/documentdb-operator/test/e2e"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
)

var _ = Describe("DocumentDB data — update operators",
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
			coll = handle.Database(dbName).Collection("update_ops")
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("applies $set to add and mutate a field", func() {
			id := "set-1"
			_, err := coll.InsertOne(ctx, bson.M{"_id": id, "name": "alpha"})
			Expect(err).NotTo(HaveOccurred())
			_, err = coll.UpdateOne(ctx, bson.M{"_id": id},
				bson.M{"$set": bson.M{"name": "alpha-2", "enabled": true}})
			Expect(err).NotTo(HaveOccurred())
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": id}).Decode(&got)).To(Succeed())
			Expect(got["name"]).To(Equal("alpha-2"))
			Expect(got["enabled"]).To(BeTrue())
		})

		It("applies $inc to a numeric field", func() {
			id := "inc-1"
			_, err := coll.InsertOne(ctx, bson.M{"_id": id, "count": 10})
			Expect(err).NotTo(HaveOccurred())
			_, err = coll.UpdateOne(ctx, bson.M{"_id": id},
				bson.M{"$inc": bson.M{"count": 5}})
			Expect(err).NotTo(HaveOccurred())
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": id}).Decode(&got)).To(Succeed())
			Expect(toInt(got["count"])).To(Equal(15))
		})

		It("applies $unset to remove a field", func() {
			id := "unset-1"
			_, err := coll.InsertOne(ctx, bson.M{"_id": id, "tmp": "x", "keep": "y"})
			Expect(err).NotTo(HaveOccurred())
			_, err = coll.UpdateOne(ctx, bson.M{"_id": id},
				bson.M{"$unset": bson.M{"tmp": ""}})
			Expect(err).NotTo(HaveOccurred())
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": id}).Decode(&got)).To(Succeed())
			Expect(got).NotTo(HaveKey("tmp"))
			Expect(got).To(HaveKey("keep"))
		})

		It("applies $push to append to an array", func() {
			id := "push-1"
			_, err := coll.InsertOne(ctx, bson.M{"_id": id, "tags": bson.A{"a"}})
			Expect(err).NotTo(HaveOccurred())
			_, err = coll.UpdateOne(ctx, bson.M{"_id": id},
				bson.M{"$push": bson.M{"tags": "b"}})
			Expect(err).NotTo(HaveOccurred())
			var got bson.M
			Expect(coll.FindOne(ctx, bson.M{"_id": id}).Decode(&got)).To(Succeed())
			tags, ok := got["tags"].(bson.A)
			Expect(ok).To(BeTrue(), "tags should decode as bson.A")
			Expect(tags).To(ConsistOf("a", "b"))
		})
	},
)
