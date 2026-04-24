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

// This spec writes to its per-spec Mongo database only — the shared
// read-only CR is not mutated, honoring fixture contracts. The "RO" in
// SharedRO means the Kubernetes custom resource is read-only; data-plane
// writes into isolated databases are permitted.
var _ = Describe("DocumentDB data — delete operators",
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
			coll = handle.Database(dbName).Collection("delete_ops")
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		BeforeEach(func() {
			// Reset state between Its so counts are deterministic.
			_, err := coll.DeleteMany(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			seedSmall(ctx, coll)
		})

		It("deleteOne removes exactly one matching document", func() {
			res, err := coll.DeleteOne(ctx, bson.M{"score": bson.M{"$gte": 30}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedCount).To(Equal(int64(1)))
			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize - 1)))
		})

		It("deleteMany removes every matching document", func() {
			// SmallDataset scores are 10..100. >= 50 → ids 5..10 → 6 docs.
			res, err := coll.DeleteMany(ctx, bson.M{"score": bson.M{"$gte": 50}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedCount).To(Equal(int64(6)))
			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(seed.SmallDatasetSize - 6)))
		})

		It("deleteMany with empty filter removes all documents", func() {
			res, err := coll.DeleteMany(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.DeletedCount).To(Equal(int64(seed.SmallDatasetSize)))
			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(0)))
		})
	},
)
