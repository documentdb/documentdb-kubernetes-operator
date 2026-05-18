package performance

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// Delete+drop threshold rationale
//
// DeleteMany of half a collection followed by a Collection.Drop() is a
// cheap metadata-bounded path on the DocumentDB gateway. Kind-on-laptop
// baseline is ~1–3s for both combined; 60s is a generous 20x guard
// aimed at catching pathologic regressions such as tombstone fanout or
// table-rewrite fallback.
var _ = Describe("DocumentDB performance — bulk delete and drop",
	Label(e2e.PerformanceLabel, e2e.SlowLabel), e2e.HighLevelLabel,
	Ordered, Serial, func() {

		const (
			docCount     = 5_000
			deleteBudget = 60 * time.Second
		)

		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.High) })

		It("bulk-deletes half the collection and drops it within the smoke threshold", func(ctx SpecContext) {
			conn := connectSharedRO(ctx)
			DeferCleanup(conn.Stop)

			coll := conn.Client.Database(conn.DB).Collection("delete_drop")

			docs := make([]any, docCount)
			for i := 0; i < docCount; i++ {
				// Even-ids are deletable, odd-ids are survivors. This
				// exercises a real matching predicate rather than a
				// fast-path {} delete.
				docs[i] = bson.M{"_id": i + 1, "even": (i+1)%2 == 0}
			}
			_, err := coll.InsertMany(ctx, docs)
			Expect(err).NotTo(HaveOccurred(), "seed delete_drop")

			start := time.Now()
			delRes, err := coll.DeleteMany(ctx, bson.M{"even": true})
			Expect(err).NotTo(HaveOccurred(), "DeleteMany")
			Expect(delRes.DeletedCount).To(BeEquivalentTo(docCount / 2))

			// Drop the collection — the operation should complete
			// quickly even on a large collection because it is a
			// metadata-only truncate on the server.
			Expect(coll.Drop(ctx)).To(Succeed(), "Drop collection")
			elapsed := time.Since(start)
			logLatency("delete-drop", elapsed)

			Expect(elapsed).To(BeNumerically("<", deleteBudget),
				"delete + drop should complete within %s", deleteBudget)

			n, err := coll.CountDocuments(ctx, bson.M{})
			Expect(err).NotTo(HaveOccurred(),
				"CountDocuments on a dropped collection should return 0, not error")
			Expect(n).To(BeEquivalentTo(0))
		})
	})
