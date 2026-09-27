package model_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func TestModelStore(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-model-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// 1. Populate
	t.Log("--- Populating Model Store ---")
	trans1, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 1 failed: %v", err)
	}

	store, err := db.OpenModelStore(ctx, "test_models", trans1)
	if err != nil {
		t.Fatalf("OpenModelStore failed: %v", err)
	}

	categories := []string{"cat1", "cat2", "cat3", "cat4", "cat5"}
	itemsPerCat := 20

	for _, cat := range categories {
		for i := 0; i < itemsPerCat; i++ {
			name := fmt.Sprintf("model-%d", i)
			data := map[string]any{
				"id":    i,
				"cat":   cat,
				"value": fmt.Sprintf("val-%s-%d", cat, i),
			}
			if err := store.Save(ctx, cat, name, data); err != nil {
				t.Fatalf("Save failed for %s/%s: %v", cat, name, err)
			}
		}
	}

	if err := trans1.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 2. Verify
	t.Log("--- Verifying Model Store ---")
	trans2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	store2, err := db.OpenModelStore(ctx, "test_models", trans2)
	if err != nil {
		t.Fatalf("OpenModelStore 2 failed: %v", err)
	}

	for _, cat := range categories {
		names, err := store2.List(ctx, cat)
		if err != nil {
			t.Fatalf("List failed for %s: %v", cat, err)
		}
		if len(names) != itemsPerCat {
			t.Errorf("Category %s: expected %d items, got %d", cat, itemsPerCat, len(names))
		}

		// Verify a random item
		targetName := "model-10"
		var targetData map[string]any
		if err := store2.Load(ctx, cat, targetName, &targetData); err != nil {
			t.Errorf("Load failed for %s/%s: %v", cat, targetName, err)
		}
		expectedValue := fmt.Sprintf("val-%s-10", cat)
		if targetData["value"] != expectedValue {
			t.Errorf("Data mismatch for %s/%s: expected %s, got %v", cat, targetName, expectedValue, targetData["value"])
		}
	}

	// Verify cross-contamination (List shouldn't return items from other cats)
	emptyNames, err := store2.List(ctx, "non-existent")
	if err != nil {
		t.Fatalf("List non-existent failed: %v", err)
	}
	if len(emptyNames) != 0 {
		t.Errorf("Expected 0 items for non-existent category, got %d", len(emptyNames))
	}

	trans2.Commit(ctx)

	// 3. Delete
	t.Log("--- Deleting from Model Store ---")
	trans3, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 3 failed: %v", err)
	}
	store3, err := db.OpenModelStore(ctx, "test_models", trans3)
	if err != nil {
		t.Fatalf("OpenModelStore 3 failed: %v", err)
	}

	if err := store3.Delete(ctx, "cat1", "model-0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion within same transaction
	var dummy map[string]any
	if err := store3.Load(ctx, "cat1", "model-0", &dummy); err == nil {
		t.Error("Load should fail after delete")
	}

	if err := trans3.Commit(ctx); err != nil {
		t.Fatalf("Commit 3 failed: %v", err)
	}
}
