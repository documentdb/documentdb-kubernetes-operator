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

// pipeline_test.go exercises more complex aggregation pipelines:
// $lookup (joins), $unwind (array flattening), and $group. Data is
// seeded inline because seed.AggDataset does not model cross-collection
// relationships.
var _ = Describe("DocumentDB data — complex pipelines",
	Ordered,
	Label(e2e.DataLabel),
	e2e.MediumLevelLabel,
	func() {
		var (
			ctx      context.Context
			handle   *emongo.Handle
			dbName   string
			orders   *mongo.Collection
			products *mongo.Collection
		)

		BeforeAll(func() {
			ctx = context.Background()
			handle, dbName = connectSharedRO(ctx)
			orders = handle.Database(dbName).Collection("orders")
			products = handle.Database(dbName).Collection("products")

			_, err := products.InsertMany(ctx, []any{
				bson.M{"_id": "p1", "name": "pen", "category": "office"},
				bson.M{"_id": "p2", "name": "book", "category": "office"},
				bson.M{"_id": "p3", "name": "lamp", "category": "home"},
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = orders.InsertMany(ctx, []any{
				bson.M{"_id": 1, "customer": "alice", "items": bson.A{"p1", "p2"}},
				bson.M{"_id": 2, "customer": "bob", "items": bson.A{"p2", "p3"}},
				bson.M{"_id": 3, "customer": "alice", "items": bson.A{"p3"}},
			})
			Expect(err).NotTo(HaveOccurred())
		})
		AfterAll(func() {
			if handle != nil {
				_ = handle.Client().Database(dbName).Drop(ctx)
				_ = handle.Close(ctx)
			}
		})

		It("performs $unwind on the items array", func() {
			pipe := mongo.Pipeline{
				{{Key: "$unwind", Value: "$items"}},
			}
			cur, err := orders.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var out []bson.M
			Expect(cur.All(ctx, &out)).To(Succeed())
			// 2 + 2 + 1 = 5 unwound rows from 3 source orders.
			Expect(out).To(HaveLen(5))
		})

		It("joins orders with products via $lookup + $unwind", func() {
			pipe := mongo.Pipeline{
				{{Key: "$unwind", Value: "$items"}},
				{{Key: "$lookup", Value: bson.D{
					{Key: "from", Value: "products"},
					{Key: "localField", Value: "items"},
					{Key: "foreignField", Value: "_id"},
					{Key: "as", Value: "product"},
				}}},
				{{Key: "$unwind", Value: "$product"}},
				{{Key: "$match", Value: bson.D{{Key: "customer", Value: "alice"}}}},
			}
			cur, err := orders.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var out []bson.M
			Expect(cur.All(ctx, &out)).To(Succeed())
			// alice has orders {1,3} with items {p1,p2,p3} → 3 rows.
			Expect(out).To(HaveLen(3))
			for _, doc := range out {
				Expect(doc["customer"]).To(Equal("alice"))
				product, ok := doc["product"].(bson.M)
				Expect(ok).To(BeTrue(), "product should be an embedded doc post-lookup")
				Expect(product).To(HaveKey("name"))
			}
		})

		It("aggregates per-customer item counts with $group", func() {
			pipe := mongo.Pipeline{
				{{Key: "$unwind", Value: "$items"}},
				{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: "$customer"},
					{Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}},
				}}},
			}
			cur, err := orders.Aggregate(ctx, pipe)
			Expect(err).NotTo(HaveOccurred())
			defer cur.Close(ctx)
			var out []bson.M
			Expect(cur.All(ctx, &out)).To(Succeed())
			counts := map[string]int{}
			for _, r := range out {
				counts[r["_id"].(string)] = toInt(r["n"])
			}
			Expect(counts).To(HaveKeyWithValue("alice", 3))
			Expect(counts).To(HaveKeyWithValue("bob", 2))
		})
	},
)
