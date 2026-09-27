package database

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func TestVectorize_PopulatesCategoryByDistance(t *testing.T) {
	ctx := context.Background()

	embedder := &mockEmbedder{}

	dbDir := t.TempDir()
	db := NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbDir},
	})

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	kb, err := db.OpenKnowledgeBase(ctx, "test_kb", tx, nil, embedder, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

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

	kb.Store.SetDomainReference([]float32{0, 0, 0})
	cfg, err := kb.GetConfig(ctx)
	if cfg == nil {
		cfg = &memory.KnowledgeBaseConfig{EmbedderDimension: 3}
	}
	cfg.DomainReference = []float32{0, 0, 0}
	kb.SetConfig(ctx, cfg)

	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Failed to commit prep tx: %v", err)
	}

	err = db.Vectorize(ctx, "test_kb", nil, embedder, 3)
	if err != nil {
		t.Fatalf("Failed to vectorize: %v", err)
	}

	// Verify categoriesByDistance
	tx2, _ := db.BeginTransaction(ctx, sop.ForReading)
	kb2, _ := db.OpenKnowledgeBase(ctx, "test_kb", tx2, nil, embedder, false)

	catsDist, _ := kb2.Store.CategoriesByDistance(ctx)

	found, err := catsDist.First(ctx)
	if err != nil {
		t.Fatalf("Failed to B-Tree first: %v", err)
	}
	if !found {
		t.Fatalf("categoriesByDistance was completely empty after Vectorize!")
	}

	currKey := catsDist.GetCurrentKey()

	if currKey.Key.ID != catID {
		t.Errorf("Expected DistanceKey.ID to be %v, got %v", catID, currKey.Key.ID)
	}

	if currKey.Key.Distance == 0 {
		t.Errorf("Expected non-zero distance, got 0")
	}

	t.Logf("Successfully populated DistanceKey map! ID: %v, Computed Distance: %f", currKey.Key.ID, currKey.Key.Distance)

	tx2.Commit(ctx)
}
