package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/search"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

// Compare implementation needed for ordered sorting
func (k VectorKey) Compare(other interface{}) int {
	o := other.(VectorKey)
	if k.CategoryID.Compare(o.CategoryID) != 0 {
		return k.CategoryID.Compare(o.CategoryID)
	}
	if k.DistanceToCategory < o.DistanceToCategory {
		return -1
	} else if k.DistanceToCategory > o.DistanceToCategory {
		return 1
	}
	return k.VectorID.Compare(o.VectorID)
}

func TestDynamicStore_Upsert(t *testing.T) {
	ctx := context.Background()
	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](false)
	items := inmemory.NewBtree[ItemKey, Item[string]](false)

	s := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)

	err := s.Upsert(ctx, Item[string]{
		ID:   sop.NewUUID(),
		Data: "LLM Thought One",
	}, []float32{0.1, 0.2, 0.3})
	if err != nil {
		t.Fatalf("Failed to upsert item: %v", err)
	}

	count := categories.Count()
	if count != 1 {
		t.Fatalf("Expected 1 category, found %v", count)
	}

	vc := vectors.Count()
	if vc != 1 {
		t.Fatalf("Expected 1 vector, found %v", vc)
	}

	cc := items.Count()
	if cc != 1 {
		t.Fatalf("Expected 1 content, found %v", cc)
	}

	err = s.Upsert(ctx, Item[string]{
		ID:   sop.NewUUID(),
		Data: "LLM Thought Two",
	}, []float32{0.15, 0.21, 0.33})
	if err != nil {
		t.Fatalf("Failed to upsert second item: %v", err)
	}

	if vectors.Count() != 2 {
		t.Fatalf("Expected exactly 2 vectors, found %v", vectors.Count())
	}
}

type MockTextIndex struct {
	data map[string]string
}

func (m *MockTextIndex) Add(ctx context.Context, docID string, text string) error {
	if m.data == nil {
		m.data = make(map[string]string)
	}
	m.data[docID] = text
	return nil
}

func (m *MockTextIndex) Search(ctx context.Context, query string) ([]search.TextSearchResult, error) {
	var res []search.TextSearchResult
	for k, v := range m.data {
		if v == query || strings.Contains(v, query) {
			res = append(res, search.TextSearchResult{DocID: k, Score: 1.0})
		}
	}
	return res, nil
}

func (m *MockTextIndex) Delete(ctx context.Context, docID string) error {
	delete(m.data, docID)
	return nil
}

func TestDynamicStore_SimulateLLMSleepCycle(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	ds := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	s := ds.(*store[string])

	s.SetTextIndex(&MockTextIndex{data: make(map[string]string)})

	newRootCat := &Category{
		ID:           sop.NewUUID(),
		Name:         "Fruits",
		CenterVector: []float32{0.1, 0.2, 0.3},
	}
	_, err := s.categories.Add(ctx, newRootCat.ID, newRootCat)
	if err != nil {
		t.Fatalf("Failed to add new category: %v", err)
	}

	item1 := Item[string]{
		ID:         sop.NewUUID(),
		CategoryID: newRootCat.ID,
		Data:       "Apple is a fruit",
	}

	err = s.Upsert(ctx, item1, []float32{0.1, 0.2, 0.3})
	if err != nil {
		t.Fatalf("Failed to upsert item1: %v", err)
	}

	err = s.DeleteItem(ctx, ItemKey{CategoryID: item1.CategoryID, ItemID: item1.ID})
	if err != nil {
		t.Fatalf("Failed to delete item: %v", err)
	}
	if mockIdx, ok := s.textIndex.(*MockTextIndex); ok {
		mockIdx.Delete(ctx, fmt.Sprintf("%v,%v", item1.CategoryID.String(), item1.ID.String()))
	}

	vecCount := vectors.Count()
	if vecCount != 0 {
		t.Errorf("Expected 0 vectors after delete, got %v", vecCount)
	}

	err = s.Upsert(ctx, item1, []float32{0.11, 0.21, 0.31})
	if err != nil {
		t.Fatalf("Failed to re-upsert: %v", err)
	}

	hits, err := s.Query(ctx, []float32{0.11, 0.21, 0.31}, &SearchOptions[string]{Limit: 5})
	if err != nil {
		t.Fatalf("Failed to query: %v", err)
	}

	if len(hits) != 1 {
		t.Errorf("Expected 1 hit, got %d", len(hits))
	} else if hits[0].Payload != "Apple is a fruit" {
		t.Errorf("Expected hit payload 'Apple is a fruit', got %v", hits[0].Payload)
	}

	textHits, err := ds.QueryText(ctx, "Apple is a fruit", &SearchOptions[string]{Limit: 5})
	if err != nil {
		t.Fatalf("Failed to text search: %v", err)
	}

	if len(textHits) != 1 {
		t.Errorf("Expected 1 text hit, got %d", len(textHits))
	} else if textHits[0].Payload != "Apple is a fruit" {
		t.Errorf("Expected text hit payload 'Apple is a fruit', got %v", textHits[0].Payload)
	}
}

func TestDynamicStore_PublicAPIs(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)
	s := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)

	err := s.UpsertBatch(ctx, []Item[string]{
		{ID: sop.NewUUID(), Data: "1"},
		{ID: sop.NewUUID(), Data: "2"},
	}, [][]float32{
		{0.1}, {0.2},
	})
	if err != nil {
		t.Fatalf("UpsertBatch failed: %v", err)
	}

	count, _ := s.Count(ctx)
	if count != 2 {
		t.Errorf("Expected Count=2, got %v", count)
	}

	itemTree, _ := s.Items(ctx)
	ok, err := itemTree.First(ctx)
	if !ok || err != nil {
		t.Fatalf("No items found")
	}
	firstItem, _ := itemTree.GetCurrentValue(ctx)
	firstItemKey := ItemKey{CategoryID: firstItem.CategoryID, ItemID: firstItem.ID}
	_, err = s.Get(ctx, firstItemKey)
	if err != nil {
		t.Errorf("Get API failed or returned wrong item: %v, key: %v", err, firstItemKey)
	}

	err = s.Delete(ctx, firstItemKey)
	if err != nil {
		t.Errorf("Delete API failed: %v", err)
	}

	_, err = s.Get(ctx, firstItemKey)
	if err == nil {
		t.Errorf("Expected error fetching deleted item")
	}

	count, _ = s.Count(ctx)
	if count != 1 {
		t.Errorf("Expected Count=1 after delete, got %v", count)
	}

	s.Consolidate(ctx)
	s.UpdateEmbedderInfo(ctx, "mock", "1.0", 3)
	vecs, _ := s.Vectors(ctx)
	if vecs == nil {
		t.Errorf("Vectors() should return tree")
	}
	v, _ := s.Version(ctx)
	if v != 0 {
		t.Errorf("Version() should return 0")
	}
}
