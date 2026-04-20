// Package seed provides deterministic, canonical datasets used by the
// DocumentDB E2E suite. All generators are pure functions with no
// external dependencies; they return freshly-allocated slices of
// bson.M so callers may mutate them safely.
package seed

import (
	"fmt"
	"math/rand/v2"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SmallDatasetSize is the number of documents returned by SmallDataset.
const SmallDatasetSize = 10

// MediumDatasetSize is the number of documents returned by MediumDataset.
const MediumDatasetSize = 1000

// SortDatasetSize is the number of documents returned by SortDataset.
const SortDatasetSize = 100

// AggDatasetSize is the number of documents returned by AggDataset.
const AggDatasetSize = 50

// AggDatasetGroups is the number of distinct category values emitted by
// AggDataset. Callers asserting group cardinality in aggregation tests
// should use this constant.
const AggDatasetGroups = 5

// SmallDataset returns exactly SmallDatasetSize documents with
// predictable identity and score fields, suitable for quick insert /
// count round-trips. Shape: {_id: N, name: "doc-N", score: N*10} for
// N in [1, SmallDatasetSize].
func SmallDataset() []bson.M {
	out := make([]bson.M, SmallDatasetSize)
	for i := 0; i < SmallDatasetSize; i++ {
		n := i + 1
		out[i] = bson.M{
			"_id":   n,
			"name":  fmt.Sprintf("doc-%d", n),
			"score": n * 10,
		}
	}
	return out
}

// MediumDataset returns MediumDatasetSize documents following the same
// shape as SmallDataset, used to validate bulk insert, count, and
// indexing behaviour under non-trivial sizes.
func MediumDataset() []bson.M {
	out := make([]bson.M, MediumDatasetSize)
	for i := 0; i < MediumDatasetSize; i++ {
		n := i + 1
		out[i] = bson.M{
			"_id":   n,
			"name":  fmt.Sprintf("doc-%d", n),
			"score": n * 10,
		}
	}
	return out
}

// sortDatasetSeed is the deterministic seed used by SortDataset so that
// identical Go runs produce identical document order — this is what
// makes sort assertions in tests reproducible.
var sortDatasetSeed = [32]byte{
	0xd0, 0xc8, 0xd8, 0x53, 's', 'o', 'r', 't',
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}

// SortDataset returns SortDatasetSize documents with varied string
// names and numeric scores suitable for validating $sort semantics.
// Output order is intentionally scrambled but deterministic — running
// the function repeatedly yields the same slice so assertions can
// compare to a known ordering.
func SortDataset() []bson.M {
	r := rand.New(rand.NewChaCha8(sortDatasetSeed))
	// Generate names from a 12-letter alphabet so sort validations see
	// meaningful string comparisons rather than trivial N-indexed names.
	const alphabet = "abcdefghijkl"
	indices := r.Perm(SortDatasetSize)
	out := make([]bson.M, SortDatasetSize)
	for i := 0; i < SortDatasetSize; i++ {
		n := indices[i] + 1
		// Two-letter name derived from the permutation for variety.
		b := []byte{alphabet[n%len(alphabet)], alphabet[(n*7)%len(alphabet)]}
		out[i] = bson.M{
			"_id":   n,
			"name":  string(b),
			"score": (n * 37) % 1000,
		}
	}
	return out
}

// aggDatasetSeed is the deterministic seed used by AggDataset.
var aggDatasetSeed = [32]byte{
	0xa6, 0x67, 0x67, 'a', 'g', 'g', 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}

// AggDataset returns AggDatasetSize documents spread across exactly
// AggDatasetGroups distinct `category` values. Every document has a
// unique numeric _id and a per-category `value` field suitable for
// $group / $sum aggregations.
func AggDataset() []bson.M {
	r := rand.New(rand.NewChaCha8(aggDatasetSeed))
	categories := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	if len(categories) != AggDatasetGroups {
		// Compile-time invariant guarded by the const.
		panic("seed: AggDatasetGroups mismatch")
	}
	out := make([]bson.M, AggDatasetSize)
	// Round-robin to guarantee every category appears at least once,
	// then perturb value fields with the deterministic RNG.
	for i := 0; i < AggDatasetSize; i++ {
		cat := categories[i%len(categories)]
		out[i] = bson.M{
			"_id":      i + 1,
			"category": cat,
			"value":    r.IntN(1000),
		}
	}
	return out
}
