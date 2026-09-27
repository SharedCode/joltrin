package memory

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestCategoryByDistanceSearch(t *testing.T) {
	ctx := context.Background()

	MockSTree := inmemory.NewBtree[sop.UUID, *Category](false)
	MockDTree := inmemory.NewBtree[DistanceKey, byte](false)

	st := NewStore[string]("test_kb", nil,
		MockSTree.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		MockDTree.Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])
	st.domainReference = []float32{1.0, 1.0, 1.0}

	cat1 := &Category{ID: sop.NewUUID(), Name: "First", CenterVector: []float32{0.9, 0.9, 0.9}}
	cat2 := &Category{ID: sop.NewUUID(), Name: "Second", CenterVector: []float32{0.2, 0.2, 0.2}}

	st.categories.Add(ctx, cat1.ID, cat1)
	st.categories.Add(ctx, cat2.ID, cat2)

	dist1 := EuclideanDistance(st.domainReference, cat1.CenterVector)
	dist2 := EuclideanDistance(st.domainReference, cat2.CenterVector)

	st.categoriesByDistance.Add(ctx, DistanceKey{Distance: dist1, ID: cat1.ID}, 0)
	st.categoriesByDistance.Add(ctx, DistanceKey{Distance: dist2, ID: cat2.ID}, 0)

	queryVec := []float32{0.85, 0.85, 0.85}
	qDist := EuclideanDistance(st.domainReference, queryVec)

	t.Logf("Cat1 Dist: %v, Cat2 Dist: %v, Query Dist: %v", dist1, dist2, qDist)

	found, err := st.categoriesByDistance.Find(ctx, DistanceKey{Distance: qDist, ID: sop.NilUUID}, false)
	if err != nil {
		t.Fatalf("Find error: %v", err)
	}

	var closestKey DistanceKey
	if found {
		currKey := st.categoriesByDistance.GetCurrentKey()
		closestKey = currKey.Key
	} else {
		// Cursor landed on an adjacent node. We need to check current and previous (if any)
		currKey := st.categoriesByDistance.GetCurrentKey()
		bestKey := currKey.Key
		bestDiff := absDist(qDist, bestKey.Distance)

		// Try Previous
		ok, err := st.categoriesByDistance.Previous(ctx)
		if err == nil && ok {
			prevKey := st.categoriesByDistance.GetCurrentKey()
			prevDiff := absDist(qDist, prevKey.Key.Distance)
			if prevDiff < bestDiff {
				bestKey = prevKey.Key
			}
		}

		closestKey = bestKey
		t.Logf("Exact dist not found. Closest distance found: %v", closestKey.Distance)
	}

	t.Logf("Closest Cat ID from Distance Tree: %v", closestKey.ID)
	if closestKey.ID != cat1.ID {
		t.Errorf("Expected Cat1 ID to be closest, got %v", closestKey.ID)
	}
}

func absDist(a, b float32) float32 {
	if a > b {
		return a - b
	}
	return b - a
}
