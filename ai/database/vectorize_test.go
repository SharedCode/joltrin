package database

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai/memory"

	"github.com/sharedcode/joltrin/v5"
)

type mockEmbedder struct{}

type promptAwareEmbedder struct{}

func (m *promptAwareEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	res := make([][]float32, len(texts))
	for i, text := range texts {
		switch text {
		case "Finance knowledge base":
			res[i] = []float32{9.0, 9.0, 9.0}
		case "finance_kb":
			res[i] = []float32{8.0, 8.0, 8.0}
		case "You are the finance KB":
			res[i] = []float32{9.0, 9.0, 9.0}
		default:
			res[i] = []float32{1.0, 2.0, 3.0}
		}
	}
	return res, nil
}

func (m *promptAwareEmbedder) Dim() int {
	return 3
}

func (m *promptAwareEmbedder) Name() string {
	return "prompt-aware-embedder"
}

func (m *mockEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	res := make([][]float32, len(texts))
	for i := range texts {
		res[i] = []float32{1.0, 2.0, 3.0}
	}
	return res, nil
}

func (m *mockEmbedder) Dim() int {
	return 3
}

func (m *mockEmbedder) Name() string {
	return "mock-embedder"
}

func TestVectorize_PrefersKBNameForDomainReference(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{StoresFolders: []string{dbDir}})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	kb, err := db.OpenKnowledgeBase(ctx, "finance_kb", tx, nil, &promptAwareEmbedder{}, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg == nil {
		cfg = &memory.KnowledgeBaseConfig{}
	}
	cfg.Description = "Finance knowledge base"
	cfg.SystemPrompt = "You are the finance KB"
	if err := kb.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	catID := sop.NewUUID()
	cat := &memory.Category{ID: catID, Name: "Finance", Description: "Finance category"}
	cats, _ := kb.Store.Categories(ctx)
	cats.Add(ctx, catID, cat)
	catsByPath, _ := kb.Store.CategoriesByPath(ctx)
	catsByPath.Add(ctx, "Finance", catID)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := db.Vectorize(ctx, "finance_kb", nil, &promptAwareEmbedder{}, 1); err != nil {
		t.Fatalf("Vectorize failed: %v", err)
	}

	tx2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction(read) failed: %v", err)
	}
	defer tx2.Commit(ctx)

	kb2, err := db.OpenKnowledgeBase(ctx, "finance_kb", tx2, nil, &promptAwareEmbedder{}, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase(read) failed: %v", err)
	}
	cfg2, err := kb2.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig(read) failed: %v", err)
	}
	if len(cfg2.DomainReference) != 3 || cfg2.DomainReference[0] != 8 || cfg2.DomainReference[1] != 8 || cfg2.DomainReference[2] != 8 {
		t.Fatalf("expected DomainReference to prefer the KB name over the description, got %v", cfg2.DomainReference)
	}
}

func TestVectorize_FallsBackToKBNameWhenDescriptionIsTooLong(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{StoresFolders: []string{dbDir}})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	kb, err := db.OpenKnowledgeBase(ctx, "finance_kb", tx, nil, &promptAwareEmbedder{}, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg == nil {
		cfg = &memory.KnowledgeBaseConfig{}
	}
	cfg.Description = strings.Repeat("This description is intentionally much longer than the threshold so the anchor logic must fall back to the KB name instead of the long description. ", 3)
	if err := kb.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := db.Vectorize(ctx, "finance_kb", nil, &promptAwareEmbedder{}, 1); err != nil {
		t.Fatalf("Vectorize failed: %v", err)
	}

	tx2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction(read) failed: %v", err)
	}
	defer tx2.Commit(ctx)

	kb2, err := db.OpenKnowledgeBase(ctx, "finance_kb", tx2, nil, &promptAwareEmbedder{}, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase(read) failed: %v", err)
	}
	cfg2, err := kb2.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig(read) failed: %v", err)
	}
	if len(cfg2.DomainReference) != 3 || cfg2.DomainReference[0] != 8 || cfg2.DomainReference[1] != 8 || cfg2.DomainReference[2] != 8 {
		t.Fatalf("expected DomainReference to fall back to KB name, got %v", cfg2.DomainReference)
	}
}

func TestStore_Vectorize_Batches(t *testing.T) {
	ctx := context.Background()

	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbDir},
	})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	kb, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx, nil, &mockEmbedder{}, false)

	catID := sop.NewUUID()
	cat := &memory.Category{
		ID:          catID,
		Name:        "Test Category",
		Description: "A generic testing category",
	}

	cats, _ := kb.Store.Categories(ctx)
	cats.Add(ctx, catID, cat)

	catsByPath, _ := kb.Store.CategoriesByPath(ctx)
	catsByPath.Add(ctx, "Test Category", catID)

	items, _ := kb.Store.Items(ctx)
	// Create 25 items
	for i := 0; i < 25; i++ {
		id := sop.NewUUID()
		items.Add(ctx, memory.ItemKey{CategoryID: catID, ItemID: id}, memory.Item[map[string]any]{
			ID:         id,
			CategoryID: catID,
			Summaries:  []string{fmt.Sprintf("Summary %d", i)},
		})
	}

	tx.Commit(ctx)

	err = db.Vectorize(ctx, "test_kb", nil, &mockEmbedder{}, 10)
	if err != nil {
		t.Fatalf("Failed to vectorize batch: %v", err)
	}

	tx2, _ := db.BeginTransaction(ctx, sop.ForReading)
	kb2, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx2, nil, &mockEmbedder{}, false)
	catsDist, _ := kb2.Store.CategoriesByDistance(ctx)

	found, err := catsDist.First(ctx)
	if err != nil {
		t.Fatalf("Failed to B-Tree first: %v", err)
	}
	if !found {
		t.Fatalf("categoriesByDistance was completely empty after Vectorize!")
	}

	// Also verify that the category's ItemCount was updated
	cats2, _ := kb2.Store.Categories(ctx)
	catFound, _ := cats2.Find(ctx, catID, false)
	if !catFound {
		t.Fatalf("Category was missing after Vectorize!")
	}
	updatedCat, _ := cats2.GetCurrentValue(ctx)
	if updatedCat.ItemCount != 25 {
		t.Errorf("Expected ItemCount to be 25, got %d", updatedCat.ItemCount)
	}

	tx2.Commit(ctx)
}

func TestStore_VectorizeItems(t *testing.T) {
	ctx := context.Background()

	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbDir},
	})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	kb, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx, nil, &mockEmbedder{}, false)

	catID := sop.NewUUID()
	cat := &memory.Category{
		ID:          catID,
		Name:        "Test Category",
		Description: "A generic testing category",
	}

	cats, _ := kb.Store.Categories(ctx)
	cats.Add(ctx, catID, cat)

	catsByPath, _ := kb.Store.CategoriesByPath(ctx)
	catsByPath.Add(ctx, "Test Category", catID)

	items, _ := kb.Store.Items(ctx)

	var specificItems []sop.UUID

	// Create 25 items
	for i := 0; i < 25; i++ {
		id := sop.NewUUID()
		items.Add(ctx, memory.ItemKey{CategoryID: catID, ItemID: id}, memory.Item[map[string]any]{
			ID:         id,
			CategoryID: catID,
			Summaries:  []string{fmt.Sprintf("Summary %d", i)},
		})

		if i%5 == 0 {
			specificItems = append(specificItems, id)
		}
	}

	tx.Commit(ctx)

	// We only process specific items
	err = db.VectorizeItems(ctx, "test_kb", nil, &mockEmbedder{}, 10, catID, specificItems)
	if err != nil {
		t.Fatalf("Failed to vectorize batch: %v", err)
	}

	tx2, _ := db.BeginTransaction(ctx, sop.ForReading)
	kb2, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx2, nil, &mockEmbedder{}, false)

	// Only 5 items should have vectors assigned/updated

	items2, _ := kb2.Store.Items(ctx)

	hitCount := 0
	for _, expectedID := range specificItems {
		found, err := items2.Find(ctx, memory.ItemKey{CategoryID: catID, ItemID: expectedID}, false)
		if err != nil || !found {
			t.Fatalf("Item %v missing", expectedID)
		}

		item, _ := items2.GetCurrentValue(ctx)
		if len(item.VectorHash) > 0 {
			hitCount++
		}
	}

	if hitCount != len(specificItems) {
		t.Errorf("Expected %d items to be vectorized, but found vectors for %d", len(specificItems), hitCount)
	}

	tx2.Commit(ctx)
}

func TestStore_VectorizeItems_ByCategory(t *testing.T) {
	ctx := context.Background()

	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbDir},
	})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	kb, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx, nil, &mockEmbedder{}, false)

	catID := sop.NewUUID()
	cat := &memory.Category{
		ID:          catID,
		Name:        "Test Category",
		Description: "A generic testing category",
	}

	cats, _ := kb.Store.Categories(ctx)
	cats.Add(ctx, catID, cat)

	catsByPath, _ := kb.Store.CategoriesByPath(ctx)
	catsByPath.Add(ctx, "Test Category", catID)

	items, _ := kb.Store.Items(ctx)

	// Create 25 items
	for i := 0; i < 25; i++ {
		id := sop.NewUUID()
		items.Add(ctx, memory.ItemKey{CategoryID: catID, ItemID: id}, memory.Item[map[string]any]{
			ID:         id,
			CategoryID: catID,
			Summaries:  []string{fmt.Sprintf("Summary %d", i)},
		})
	}

	tx.Commit(ctx)

	// We pass nil for itemIDs to vectorize the whole category
	err = db.VectorizeItems(ctx, "test_kb", nil, &mockEmbedder{}, 10, catID, nil)
	if err != nil {
		t.Fatalf("Failed to vectorize batch: %v", err)
	}

	tx2, _ := db.BeginTransaction(ctx, sop.ForReading)
	kb2, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx2, nil, &mockEmbedder{}, false)

	// All 25 items should have vectors assigned/updated

	items2, _ := kb2.Store.Items(ctx)
	found, err := items2.First(ctx)
	if err != nil {
		t.Fatalf("Failed to find first item: %v", err)
	}

	hitCount := 0
	for found {
		item, _ := items2.GetCurrentValue(ctx)
		if len(item.VectorHash) > 0 {
			hitCount++
		}
		found, _ = items2.Next(ctx)
	}

	if hitCount != 25 {
		t.Errorf("Expected 25 items to be vectorized, but found vectors for %d", hitCount)
	}

	tx2.Commit(ctx)
}
