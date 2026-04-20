package seed

import (
	"fmt"
	"reflect"
	"testing"
)

func TestSmallDataset_Shape(t *testing.T) {
	t.Parallel()
	ds := SmallDataset()
	if len(ds) != SmallDatasetSize {
		t.Fatalf("want %d docs, got %d", SmallDatasetSize, len(ds))
	}
	for i, d := range ds {
		n := i + 1
		if d["_id"] != n {
			t.Fatalf("doc %d: _id=%v, want %d", i, d["_id"], n)
		}
		if d["name"] != fmt.Sprintf("doc-%d", n) {
			t.Fatalf("doc %d: name=%v", i, d["name"])
		}
		if d["score"] != n*10 {
			t.Fatalf("doc %d: score=%v, want %d", i, d["score"], n*10)
		}
	}
}

func TestMediumDataset_Size(t *testing.T) {
	t.Parallel()
	ds := MediumDataset()
	if len(ds) != MediumDatasetSize {
		t.Fatalf("want %d docs, got %d", MediumDatasetSize, len(ds))
	}
	// Spot-check first and last doc.
	if ds[0]["_id"] != 1 {
		t.Fatalf("first _id=%v", ds[0]["_id"])
	}
	if ds[MediumDatasetSize-1]["_id"] != MediumDatasetSize {
		t.Fatalf("last _id=%v", ds[MediumDatasetSize-1]["_id"])
	}
}

func TestSortDataset_DeterministicOrder(t *testing.T) {
	t.Parallel()
	a := SortDataset()
	b := SortDataset()
	if len(a) != SortDatasetSize {
		t.Fatalf("size=%d want %d", len(a), SortDatasetSize)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("SortDataset is not deterministic across calls")
	}
	// All _id values must be unique and in range [1, SortDatasetSize].
	seen := make(map[any]bool, SortDatasetSize)
	for _, d := range a {
		id := d["_id"]
		if seen[id] {
			t.Fatalf("duplicate _id=%v", id)
		}
		seen[id] = true
	}
}

func TestAggDataset_GroupCardinality(t *testing.T) {
	t.Parallel()
	ds := AggDataset()
	if len(ds) != AggDatasetSize {
		t.Fatalf("size=%d want %d", len(ds), AggDatasetSize)
	}
	cats := map[string]int{}
	for _, d := range ds {
		c, ok := d["category"].(string)
		if !ok {
			t.Fatalf("non-string category: %T", d["category"])
		}
		cats[c]++
	}
	if len(cats) != AggDatasetGroups {
		t.Fatalf("want %d distinct categories, got %d (%v)", AggDatasetGroups, len(cats), cats)
	}
	// Every category should have at least one document (round-robin
	// distribution guarantees this when size ≥ groups).
	for c, n := range cats {
		if n == 0 {
			t.Fatalf("category %s empty", c)
		}
	}
}

func TestAggDataset_Deterministic(t *testing.T) {
	t.Parallel()
	if !reflect.DeepEqual(AggDataset(), AggDataset()) {
		t.Fatalf("AggDataset not deterministic")
	}
}
