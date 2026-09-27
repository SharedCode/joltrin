package database

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func TestStore_Vectorize_NestedCategories(t *testing.T) {
	ctx := context.Background()

	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{StoresFolders: []string{dbDir}})

	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
	kb, _ := db.OpenKnowledgeBase(ctx, "nested_kb", tx, nil, &mockEmbedder{}, false)

	cats, _ := kb.Store.Categories(ctx)
	catsByPath, _ := kb.Store.CategoriesByPath(ctx)
	items, _ := kb.Store.Items(ctx)

	gpID := sop.NewUUID()
	gpCat := &memory.Category{ID: gpID, Name: "Level1", Description: "Grandparent"}
	cats.Add(ctx, gpID, gpCat)
	catsByPath.Add(ctx, "Level1", gpID)

	pID := sop.NewUUID()
	pCat := &memory.Category{ID: pID, Name: "Level2", Description: "Parent", ParentIDs: []memory.CategoryParent{{ParentID: gpID}}}
	cats.Add(ctx, pID, pCat)
	catsByPath.Add(ctx, "Level2", pID)

	cID := sop.NewUUID()
	cCat := &memory.Category{ID: cID, Name: "Level3", Description: "Child", ParentIDs: []memory.CategoryParent{{ParentID: pID}}}
	cats.Add(ctx, cID, cCat)
	catsByPath.Add(ctx, "Level3", cID)

	uuids := []sop.UUID{gpID, pID, cID}
	for _, catUUID := range uuids {
		for i := 0; i < 10; i++ {
			id := sop.NewUUID()
			items.Add(ctx, memory.ItemKey{CategoryID: catUUID, ItemID: id}, memory.Item[map[string]any]{
				ID:         id,
				CategoryID: catUUID,
				Summaries:  []string{"test chunk summary"},
				Data:       map[string]any{"index": i},
			})
		}
	}
	tx.Commit(ctx)

	err := db.Vectorize(ctx, "nested_kb", nil, &mockEmbedder{}, 10)
	if err != nil {
		t.Fatalf("Failed %v", err)
	}

	tx2, _ := db.BeginTransaction(ctx, sop.ForReading)
	kb2, _ := db.OpenKnowledgeBase(ctx, "nested_kb", tx2, nil, &mockEmbedder{}, false)

	catsDist, _ := kb2.Store.CategoriesByDistance(ctx)
	distCount := 0
	ok, _ := catsDist.First(ctx)
	for ok {
		distCount++
		cDK := catsDist.GetCurrentKey()
		k := cDK.Key
		if k.ParentID == sop.NilUUID {
			if k.Distance != 0 {
				t.Errorf("GP dist err: %v", k.Distance)
			}
		} else if k.ParentID.Compare(gpID) == 0 || k.ParentID.Compare(pID) == 0 {
			if k.Distance != 0 {
				t.Errorf("Nested dist err %v", k.Distance)
			}
		} else {
			t.Errorf("Unknown ParentID")
		}
		ok, _ = catsDist.Next(ctx)
	}

	if distCount != 3 {
		t.Errorf("Expected 3 categories in dist, got %d", distCount)
	}

	cats2, _ := kb2.Store.Categories(ctx)
	for _, c := range uuids {
		cats2.Find(ctx, c, false)
		cat, _ := cats2.GetCurrentValue(ctx)
		if len(cat.CenterVector) != 3 {
			t.Errorf("Expected center vector length 3")
		}
		if cat.ItemCount != 10 {
			t.Errorf("Expected item count 10")
		}
	}

	items2, _ := kb2.Store.Items(ctx)
	itemsCount := 0
	iok, _ := items2.First(ctx)
	for iok {
		item, _ := items2.GetCurrentValue(ctx)
		if !item.IsConfig() {
			if len(item.VectorHash) == 0 {
				t.Errorf("Item missing hash: %v", item.ID)
			}
			itemsCount++
		}
		iok, _ = items2.Next(ctx)
	}

	if itemsCount != 30 {
		t.Errorf("Expected 30 items, got %d", itemsCount)
	}

	tx2.Commit(ctx)
}
