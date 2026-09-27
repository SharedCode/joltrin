package memory

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestAddCategory(t *testing.T) {
	ctx := context.Background()
	inmem := inmemory.NewBtree[sop.UUID, *Category](true)
	s := &store[string]{
		categories: inmem.Btree,
	}

	// 1. Nil category
	if _, err := s.AddCategory(ctx, nil); err == nil {
		t.Errorf("expected error for nil category")
	}

	// 2. Successful Add without Parents
	c1 := &Category{
		ID:   sop.NewUUID(),
		Name: "Root",
	}
	id1, err := s.AddCategory(ctx, c1)
	if err != nil {
		t.Fatalf("failed to add root category: %v", err)
	}
	if id1.IsNil() {
		t.Errorf("expected valid ID")
	}

	// 3. Already Exists
	if _, err := s.AddCategory(ctx, c1); err == nil {
		t.Errorf("expected error for duplicate category ID")
	}

	// 4. Add with Parents
	c2 := &Category{
		ID:   sop.NewUUID(),
		Name: "Child",
		ParentIDs: []CategoryParent{
			{ParentID: id1, UseCase: "Child of Root"},
		},
	}

	id2, err := s.AddCategory(ctx, c2)
	if err != nil {
		t.Fatalf("failed to add child category: %v", err)
	}

	// Validate child link
	ok, err := s.categories.Find(ctx, id1, false)
	if err != nil || !ok {
		t.Fatalf("failed to find root category again")
	}
	rootVal, err := s.categories.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("failed to get root category value: %v", err)
	}

	foundChild := false
	for _, cid := range rootVal.ChildrenIDs {
		if cid == id2 {
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Errorf("expected root category to have child ID in ChildrenIDs, got %v", rootVal.ChildrenIDs)
	}
}
func TestAddCategoryParent(t *testing.T) {
	ctx := context.Background()
	inmem := inmemory.NewBtree[sop.UUID, *Category](true)
	s := &store[string]{
		categories: inmem.Btree,
	}

	rootID := sop.NewUUID()
	childID := sop.NewUUID()
	otherRootID := sop.NewUUID()

	_, _ = s.AddCategory(ctx, &Category{ID: rootID, Name: "Root"})
	_, _ = s.AddCategory(ctx, &Category{ID: childID, Name: "Child", ParentIDs: []CategoryParent{{ParentID: rootID, UseCase: "Primary"}}})
	_, _ = s.AddCategory(ctx, &Category{ID: otherRootID, Name: "OtherRoot"})

	// 1. Invalid IDs
	if err := s.AddCategoryParent(ctx, sop.UUID{}, CategoryParent{ParentID: otherRootID}); err == nil {
		t.Errorf("expected error for empty category ID")
	}
	if err := s.AddCategoryParent(ctx, childID, CategoryParent{}); err == nil {
		t.Errorf("expected error for empty parent ID")
	}

	// 2. target category not found
	if err := s.AddCategoryParent(ctx, sop.NewUUID(), CategoryParent{ParentID: otherRootID}); err == nil {
		t.Errorf("expected error for non-existent target category")
	}

	// 3. parent category not found
	err := s.AddCategoryParent(ctx, childID, CategoryParent{ParentID: sop.NewUUID(), UseCase: "Fail"})
	if err == nil {
		t.Errorf("expected error for non-existent parent category")
	}

	// 4. Valid linking
	err = s.AddCategoryParent(ctx, childID, CategoryParent{ParentID: otherRootID, UseCase: "Secondary Parent"})
	if err != nil {
		t.Fatalf("failed to add second parent: %v", err)
	}

	s.categories.Find(ctx, childID, false)
	val, _ := s.categories.GetCurrentValue(ctx)
	if len(val.ParentIDs) < 2 || val.ParentIDs[1].ParentID != otherRootID {
		t.Errorf("expected child to have second parent")
	}

	s.categories.Find(ctx, otherRootID, false)
	oRootVal, _ := s.categories.GetCurrentValue(ctx)
	if len(oRootVal.ChildrenIDs) == 0 || oRootVal.ChildrenIDs[0] != childID {
		t.Errorf("expected other root to have child")
	}

	// 5. Update Existing UseCase
	err = s.AddCategoryParent(ctx, childID, CategoryParent{ParentID: otherRootID, UseCase: "Updated UseCase"})
	if err != nil {
		t.Fatalf("expected no error during duplicate insert/update")
	}
	s.categories.Find(ctx, childID, false)
	val, _ = s.categories.GetCurrentValue(ctx)
	found := false
	for _, p := range val.ParentIDs {
		if p.ParentID == otherRootID && p.UseCase == "Updated UseCase" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected use case to be updated")
	}
}
