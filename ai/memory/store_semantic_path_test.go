package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestSemanticCategoryByPath_EmptyPathReturnsNil(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	s := NewStore[string]("semantic_kb", nil,
		cats.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		inmemory.NewBtree[DistanceKey, byte](false).Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])
	s.SetDomainReference([]float32{0.0, 0.0, 0.0})

	candidates, err := s.SemanticCategoryByPath(ctx, nil)
	if err != nil {
		t.Fatalf("SemanticCategoryByPath returned an unexpected error: %v", err)
	}
	if candidates != nil {
		t.Fatalf("expected nil candidates for an empty path, got %+v", candidates)
	}
}

func TestSemanticCategoryByPath_RequiresDomainReference(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	s := NewStore[string]("semantic_kb", nil,
		cats.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		inmemory.NewBtree[DistanceKey, byte](false).Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])

	_, err := s.SemanticCategoryByPath(ctx, [][]float32{{1.0, 0.0, 0.0}})
	if err == nil {
		t.Fatal("expected an error when domain reference is not set")
	}
	if !strings.Contains(err.Error(), "domain reference vector is not set") {
		t.Fatalf("expected domain-reference error, got %v", err)
	}
}

func TestDistanceIndex_CanLocateCategoryNeighbors(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	dist := inmemory.NewBtree[DistanceKey, byte](false)

	s := NewStore[string]("semantic_kb", nil,
		cats.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		dist.Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])
	s.SetDomainReference([]float32{0.0, 0.0, 0.0})

	root := &Category{ID: sop.NewUUID(), Name: "Root", CenterVector: []float32{1.0, 0.0, 0.0}}
	child := &Category{ID: sop.NewUUID(), Name: "Child", CenterVector: []float32{1.0, 1.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: root.ID}}}

	if _, err := s.AddCategory(ctx, root); err != nil {
		t.Fatalf("AddCategory(root) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, child); err != nil {
		t.Fatalf("AddCategory(child) failed: %v", err)
	}

	dist.Btree.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: root.ID, Distance: 1.0, ID: child.ID}, 0)

	count := dist.Btree.Count()
	if count != 2 {
		t.Fatalf("expected 2 distance-index entries, got %d", count)
	}

	found, err := dist.Btree.Find(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID}, false)
	if err != nil {
		t.Fatalf("distance lookup failed: %v", err)
	}
	if !found {
		t.Fatal("expected root distance entry to exist")
	}

	found, err = dist.Btree.Find(ctx, DistanceKey{ParentID: root.ID, Distance: 1.0, ID: child.ID}, false)
	if err != nil {
		t.Fatalf("child distance lookup failed: %v", err)
	}
	if !found {
		t.Fatal("expected child distance entry to exist")
	}
}

func TestSemanticCategoryByPath_VariousHierarchyFlavors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		build func(t *testing.T, s *store[string], dist *inmemory.BtreeInterface[DistanceKey, byte]) (path [][]float32, wantID sop.UUID)
	}{
		{
			name: "root to leaf",
			build: func(t *testing.T, s *store[string], dist *inmemory.BtreeInterface[DistanceKey, byte]) ([][]float32, sop.UUID) {
				root := &Category{ID: sop.NewUUID(), Name: "Root", CenterVector: []float32{0.0, 0.0, 0.0}}
				leaf := &Category{ID: sop.NewUUID(), Name: "Leaf", CenterVector: []float32{0.1, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: root.ID}}}
				if _, err := s.AddCategory(ctx, root); err != nil {
					t.Fatalf("AddCategory(root) failed: %v", err)
				}
				if _, err := s.AddCategory(ctx, leaf); err != nil {
					t.Fatalf("AddCategory(leaf) failed: %v", err)
				}
				if ok, err := dist.Btree.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID}, 0); err != nil || !ok {
					t.Fatalf("add root distance failed: ok=%v err=%v", ok, err)
				}
				if ok, err := dist.Btree.Add(ctx, DistanceKey{ParentID: root.ID, Distance: 0.1, ID: leaf.ID}, 0); err != nil || !ok {
					t.Fatalf("add leaf distance failed: ok=%v err=%v", ok, err)
				}
				return [][]float32{{1.0, 0.0, 0.0}, {0.1, 0.0, 0.0}}, leaf.ID
			},
		},
		{
			name: "root to subcategory to leaf",
			build: func(t *testing.T, s *store[string], dist *inmemory.BtreeInterface[DistanceKey, byte]) ([][]float32, sop.UUID) {
				root := &Category{ID: sop.NewUUID(), Name: "Root", CenterVector: []float32{0.0, 0.0, 0.0}}
				sub := &Category{ID: sop.NewUUID(), Name: "Subcategory", CenterVector: []float32{0.2, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: root.ID}}}
				leaf := &Category{ID: sop.NewUUID(), Name: "Leaf", CenterVector: []float32{0.3, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: sub.ID}}}
				for _, cat := range []*Category{root, sub, leaf} {
					if _, err := s.AddCategory(ctx, cat); err != nil {
						t.Fatalf("AddCategory(%s) failed: %v", cat.Name, err)
					}
				}
				for _, entry := range []DistanceKey{
					{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID},
					{ParentID: root.ID, Distance: 0.2, ID: sub.ID},
					{ParentID: sub.ID, Distance: 0.1, ID: leaf.ID},
				} {
					if ok, err := dist.Btree.Add(ctx, entry, 0); err != nil || !ok {
						t.Fatalf("add distance entry failed: ok=%v err=%v", ok, err)
					}
				}
				return [][]float32{{1.0, 0.0, 0.0}, {0.2, 0.0, 0.0}, {0.1, 0.0, 0.0}}, leaf.ID
			},
		},
		{
			name: "deep hierarchy leaf",
			build: func(t *testing.T, s *store[string], dist *inmemory.BtreeInterface[DistanceKey, byte]) ([][]float32, sop.UUID) {
				root := &Category{ID: sop.NewUUID(), Name: "Root", CenterVector: []float32{0.0, 0.0, 0.0}}
				branch := &Category{ID: sop.NewUUID(), Name: "Branch", CenterVector: []float32{0.2, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: root.ID}}}
				sub := &Category{ID: sop.NewUUID(), Name: "Subbranch", CenterVector: []float32{0.4, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: branch.ID}}}
				leaf := &Category{ID: sop.NewUUID(), Name: "Leaf", CenterVector: []float32{0.6, 0.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: sub.ID}}}
				for _, cat := range []*Category{root, branch, sub, leaf} {
					if _, err := s.AddCategory(ctx, cat); err != nil {
						t.Fatalf("AddCategory(%s) failed: %v", cat.Name, err)
					}
				}
				for _, entry := range []DistanceKey{
					{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID},
					{ParentID: root.ID, Distance: 0.2, ID: branch.ID},
					{ParentID: branch.ID, Distance: 0.2, ID: sub.ID},
					{ParentID: sub.ID, Distance: 0.2, ID: leaf.ID},
				} {
					if ok, err := dist.Btree.Add(ctx, entry, 0); err != nil || !ok {
						t.Fatalf("add distance entry failed: ok=%v err=%v", ok, err)
					}
				}
				return [][]float32{{1.0, 0.0, 0.0}, {0.2, 0.0, 0.0}, {0.2, 0.0, 0.0}, {0.2, 0.0, 0.0}}, leaf.ID
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cats := inmemory.NewBtree[sop.UUID, *Category](true)
			dist := inmemory.NewBtree[DistanceKey, byte](false)
			s := NewStore[string]("semantic_kb", nil,
				cats.Btree,
				inmemory.NewBtree[string, sop.UUID](false).Btree,
				dist.Btree,
				inmemory.NewBtree[VectorKey, Vector](false).Btree,
				inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
				inmemory.NewBtree[sop.UUID, Document](false).Btree,
			).(*store[string])
			s.SetDomainReference([]float32{0.0, 0.0, 0.0})

			pathVectors, wantID := tt.build(t, s, &dist)
			candidates, err := s.SemanticCategoryByPath(ctx, pathVectors)
			if err != nil {
				t.Fatalf("SemanticCategoryByPath failed: %v", err)
			}
			if len(candidates) == 0 {
				t.Fatal("expected at least one semantic candidate")
			}
			found := false
			for _, cat := range candidates {
				if cat != nil && cat.ID == wantID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected leaf category %s to appear among candidates, got %+v", wantID, candidates)
			}
		})
	}
}

func TestSemanticCategoryByPath_ReturnsExpectedLeaf(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	dist := inmemory.NewBtree[DistanceKey, byte](false)

	s := NewStore[string]("semantic_kb", nil,
		cats.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		dist.Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])
	s.SetDomainReference([]float32{0.0, 0.0, 0.0})

	root := &Category{ID: sop.NewUUID(), Name: "Root", CenterVector: []float32{1.0, 0.0, 0.0}}
	branch := &Category{ID: sop.NewUUID(), Name: "Branch", CenterVector: []float32{1.0, 0.2, 0.0}, ParentIDs: []CategoryParent{{ParentID: root.ID}}}
	leaf := &Category{ID: sop.NewUUID(), Name: "Leaf", CenterVector: []float32{1.0, 0.25, 0.0}, ParentIDs: []CategoryParent{{ParentID: branch.ID}}}

	if _, err := s.AddCategory(ctx, root); err != nil {
		t.Fatalf("AddCategory(root) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, branch); err != nil {
		t.Fatalf("AddCategory(branch) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, leaf); err != nil {
		t.Fatalf("AddCategory(leaf) failed: %v", err)
	}

	dist.Btree.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: root.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: root.ID, Distance: 0.2, ID: branch.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: root.ID, Distance: 0.25, ID: leaf.ID}, 0)

	candidates, err := s.SemanticCategoryByPath(ctx, [][]float32{{1.0, 0.0, 0.0}, {1.0, 0.25, 0.0}})
	if err != nil {
		t.Fatalf("SemanticCategoryByPath failed: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one semantic candidate")
	}
	t.Logf("candidates=%#v", candidates)
	if candidates[0] == nil {
		t.Fatal("expected a non-nil semantic candidate")
	}
	if candidates[0].ID != branch.ID && candidates[0].ID != leaf.ID {
		t.Fatalf("expected a semantic candidate from the distance index, got %s", candidates[0].ID)
	}
}

func TestSemanticCategoryByPath_ReturnsSingleBestCandidate(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	dist := inmemory.NewBtree[DistanceKey, byte](false)

	s := NewStore[string]("semantic_kb", nil,
		cats.Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		dist.Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])
	s.SetDomainReference([]float32{0.0, 0.0, 0.0})

	left := &Category{ID: sop.NewUUID(), Name: "Left", CenterVector: []float32{1.0, 0.0, 0.0}}
	right := &Category{ID: sop.NewUUID(), Name: "Right", CenterVector: []float32{0.0, 1.0, 0.0}}
	leftLeaf := &Category{ID: sop.NewUUID(), Name: "LeftLeaf", CenterVector: []float32{1.0, 0.1, 0.0}, ParentIDs: []CategoryParent{{ParentID: left.ID}}}
	rightLeaf := &Category{ID: sop.NewUUID(), Name: "RightLeaf", CenterVector: []float32{0.1, 1.0, 0.0}, ParentIDs: []CategoryParent{{ParentID: right.ID}}}

	if _, err := s.AddCategory(ctx, left); err != nil {
		t.Fatalf("AddCategory(left) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, right); err != nil {
		t.Fatalf("AddCategory(right) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, leftLeaf); err != nil {
		t.Fatalf("AddCategory(leftLeaf) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, rightLeaf); err != nil {
		t.Fatalf("AddCategory(rightLeaf) failed: %v", err)
	}

	dist.Btree.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: left.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: 1.0, ID: right.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: left.ID, Distance: 0.1, ID: leftLeaf.ID}, 0)
	dist.Btree.Add(ctx, DistanceKey{ParentID: right.ID, Distance: 0.1, ID: rightLeaf.ID}, 0)

	candidates, err := s.SemanticCategoryByPath(ctx, [][]float32{{0.5, 0.5, 0.0}, {0.5, 0.5, 0.0}})
	if err != nil {
		t.Fatalf("SemanticCategoryByPath failed: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected both tied candidates to be returned, got %d", len(candidates))
	}
	ids := map[sop.UUID]bool{}
	for _, cat := range candidates {
		if cat == nil {
			continue
		}
		ids[cat.ID] = true
	}
	if !ids[leftLeaf.ID] || !ids[rightLeaf.ID] {
		t.Fatalf("expected both tied leaves to remain, got %+v", candidates)
	}
}
