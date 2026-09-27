package memory

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestFindClosestCategory_Nested(t *testing.T) {
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
	st.domainReference = []float32{0.0, 0.0, 0.0}

	addCat := func(id sop.UUID, parentID sop.UUID, parentAnchor []float32, name string, vec []float32) *Category {
		cat := &Category{ID: id, Name: name, CenterVector: vec}
		st.categories.Add(ctx, id, cat)

		dist := EuclideanDistance(parentAnchor, vec)
		st.categoriesByDistance.Add(ctx, DistanceKey{ParentID: parentID, Distance: dist, ID: id}, 0)
		return cat
	}

	catL1A := addCat(sop.NewUUID(), sop.NilUUID, st.domainReference, "L1A", []float32{1.0, 0.0, 0.0})
	catL2A := addCat(sop.NewUUID(), catL1A.ID, catL1A.CenterVector, "L2A", []float32{1.1, 0.0, 0.0})
	_ = addCat(sop.NewUUID(), catL1A.ID, catL1A.CenterVector, "L2B", []float32{1.5, 0.0, 0.0})
	catL3A := addCat(sop.NewUUID(), catL2A.ID, catL2A.CenterVector, "L3A", []float32{1.1, 0.1, 0.0})
	catL4A := addCat(sop.NewUUID(), catL3A.ID, catL3A.CenterVector, "L4A", []float32{1.1, 0.1, 0.1})

	// PRINTS!
	ok, _ := st.categoriesByDistance.First(ctx)
	for ok {
		k := st.categoriesByDistance.GetCurrentKey().Key
		t.Logf("TREE ITEM: Parent=%s Dist=%v ID=%s", k.ParentID.String(), k.Distance, k.ID.String())
		ok, _ = st.categoriesByDistance.Next(ctx)
	}

	// Query exactly to L4A
	qVec := []float32{1.11, 0.11, 0.11}

	closest, dist, err := st.FindClosestCategory(ctx, qVec)
	if err != nil {
		t.Fatalf("FindClosestCategory failed: %v", err)
	}

	if closest == nil {
		t.Fatalf("Expected to find a category, got nil")
	}

	if closest.ID != catL4A.ID {
		t.Errorf("Expected closest category to be %s (L4A), got %s", catL4A.Name, closest.Name)
	}

	t.Logf("Found deepest leaf accurately: %s with dist %v", closest.Name, dist)
}

func TestFindClosestCategory_NoMatches(t *testing.T) {
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
	st.domainReference = []float32{0.0, 0.0, 0.0}

	qVec := []float32{1.11, 0.11, 0.11}

	closest, dist, err := st.FindClosestCategory(ctx, qVec)
	if err != nil {
		t.Fatalf("FindClosestCategory failed unexpectedly: %v", err)
	}

	if closest != nil {
		t.Fatalf("Expected nil closest category on empty database")
	}
	if dist != -1.0 {
		t.Fatalf("Expected default distance -1.0 on match failure, got %v", dist)
	}
}
func TestFindClosestCategory_MultipleSiblings(t *testing.T) {
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
	st.domainReference = []float32{0.0, 0.0, 0.0}

	addCat := func(id sop.UUID, parentID sop.UUID, parentAnchor []float32, name string, vec []float32) *Category {
		cat := &Category{ID: id, Name: name, CenterVector: vec}
		st.categories.Add(ctx, id, cat)

		dist := EuclideanDistance(parentAnchor, vec)
		st.categoriesByDistance.Add(ctx, DistanceKey{ParentID: parentID, Distance: dist, ID: id}, 0)
		return cat
	}

	catRoot := addCat(sop.NewUUID(), sop.NilUUID, st.domainReference, "Root", []float32{1.0, 0.0, 0.0})

	var targetSibling *Category
	for i := 0; i < 10; i++ {
		vec := []float32{1.0, float32(i) * 0.1, 0.0}
		c := addCat(sop.NewUUID(), catRoot.ID, catRoot.CenterVector, "Sibling", vec)
		if i == 8 {
			targetSibling = c
		}
	}

	qVec := []float32{1.0, 0.81, 0.0}
	closest, _, err := st.FindClosestCategory(ctx, qVec)
	if err != nil {
		t.Fatalf("FindClosestCategory failed: %v", err)
	}

	if closest == nil || closest.ID != targetSibling.ID {
		t.Errorf("Expected closest category to be Sibling 8, got %v", closest)
	}
}
