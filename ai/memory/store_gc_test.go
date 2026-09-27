package memory

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestStore_GarbageCollection(t *testing.T) {
	ctx := context.Background()
	st := NewStore[string]("test_gc", nil,
		inmemory.NewBtree[sop.UUID, *Category](false).Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		inmemory.NewBtree[DistanceKey, byte](false).Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])

	catID := sop.NewUUID()
	cat := &Category{ID: catID, Name: "GCCat"}
	st.categories.Add(ctx, catID, cat)

	itemID1 := sop.NewUUID()
	item1 := Item[string]{ID: itemID1, CategoryID: catID, Data: "I have 3 vectors"}

	err := st.UpsertByCategoryID(ctx, catID, nil, item1, [][]float32{{1.0, 1.0}, {2.0, 2.0}, {3.0, 3.0}}, nil)
	if err != nil {
		t.Fatalf("Upsert item1 failed: %v", err)
	}

	itemID2 := sop.NewUUID()
	item2 := Item[string]{ID: itemID2, CategoryID: catID, Data: "I have 1 vector"}

	err = st.UpsertByCategoryID(ctx, catID, nil, item2, [][]float32{{4.0, 4.0}}, nil)
	if err != nil {
		t.Fatalf("Upsert item2 failed: %v", err)
	}

	vecsCount := st.vectors.Count()
	if vecsCount != 4 {
		t.Errorf("Expected 4 vectors, got %d", vecsCount)
	}

	// Overwrite Item 1 with just 1 vector, ensuring 2 vectors are garbage collected
	err = st.UpsertByCategoryID(ctx, catID, nil, item1, [][]float32{{1.1, 1.1}}, nil)
	if err != nil {
		t.Fatalf("Re-upsert item1 failed: %v", err)
	}

	vecsCount = st.vectors.Count()
	if vecsCount != 2 {
		t.Errorf("Expected 2 vectors after partial overwrite GC, got %d", vecsCount)
	}

	// Delete Item 2
	err = st.DeleteItem(ctx, ItemKey{CategoryID: catID, ItemID: itemID2})
	if err != nil {
		t.Fatalf("Delete item2 failed: %v", err)
	}

	vecsCount = st.vectors.Count()
	if vecsCount != 1 {
		t.Errorf("Expected 1 vector after deleting item2, got %d", vecsCount)
	}

	// Iterate vectors B-Tree to ensure cursor is sound and only Item 1's vector remains
	iok, _ := st.vectors.First(ctx)
	iterCount := 0
	for iok {
		iterCount++
		iok, _ = st.vectors.Next(ctx)
	}

	if iterCount != 1 {
		t.Errorf("Cursor iteration found %d vectors, expected 1", iterCount)
	}
}
