package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func TestStore_CursorPaginationAndIsolation(t *testing.T) {
	ctx := context.Background()

	st := NewStore[string]("test_cursor", nil,
		inmemory.NewBtree[sop.UUID, *Category](false).Btree,
		inmemory.NewBtree[string, sop.UUID](false).Btree,
		inmemory.NewBtree[DistanceKey, byte](false).Btree,
		inmemory.NewBtree[VectorKey, Vector](false).Btree,
		inmemory.NewBtree[ItemKey, Item[string]](false).Btree,
		inmemory.NewBtree[sop.UUID, Document](false).Btree,
	).(*store[string])

	catID := sop.NewUUID()
	cat := &Category{ID: catID, Name: "CursorCat"}
	st.categories.Add(ctx, catID, cat)

	const boundarySize = 1000

	itemsMap := make(map[sop.UUID]bool)

	// Linear scale ingestion
	for i := 0; i < boundarySize; i++ {
		id := sop.NewUUID()
		item := Item[string]{
			ID:         id,
			CategoryID: catID,
			Data:       fmt.Sprintf("Payload %d", i),
		}

		err := st.UpsertByCategoryID(ctx, catID, nil, item, [][]float32{{float32(i), float32(i)}}, nil)
		if err != nil {
			t.Fatalf("Upsert failed at %d: %v", i, err)
		}
		itemsMap[id] = false
	}

	// Validate Count
	count, _ := st.Count(ctx)
	if count != int64(boundarySize) {
		t.Errorf("Expected count %d, got %d", boundarySize, count)
	}

	// Cursor Pagination using First/Next
	iok, err := st.items.First(ctx)
	if err != nil || !iok {
		t.Fatalf("Items first failed: %v", err)
	}

	traversedCount := 0
	for iok {
		item, _ := st.items.GetCurrentValue(ctx)

		if _, exists := itemsMap[item.ID]; !exists {
			if !item.IsConfig() {
				t.Errorf("Found alien item ID in tree: %v", item.ID)
			}
		} else {
			itemsMap[item.ID] = true
			traversedCount++
		}

		iok, _ = st.items.Next(ctx)
	}

	if traversedCount != boundarySize {
		t.Errorf("Expected to traverse %d items, got %d", boundarySize, traversedCount)
	}

	// Double check isolation of mapping tracking
	for id, visited := range itemsMap {
		if !visited {
			t.Errorf("Item %v was theoretically added but missed by Next() cursor pagination", id)
			break // Only print once so logs don't blow up
		}
	}
}
