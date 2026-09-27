package vector_test

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestAddCentroid(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-dynamic-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idx, err := db.OpenVectorStore(ctx, "test_dynamic_centroids", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// 1. Initial State
	vec1 := []float32{0.1, 0.1}
	item1 := ai.Item[map[string]any]{
		ID:      "item1",
		Vector:  vec1,
		Payload: map[string]any{"label": "A"},
	}
	if err := idx.Upsert(ctx, item1); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Verify we have 1 centroid (auto-created)
	// NOTE: In V0, Upsert goes to TempVectors, so NO centroids are created yet.
	// We must Optimize to transition to V1 where centroids exist.
	if err := idx.Optimize(ctx); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Start new transaction for V1 operations
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	idx, err = db.OpenVectorStore(ctx, "test_dynamic_centroids", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	// Verify we have 1 centroid (created by Optimize)
	// We can't easily check count, but we can check if item1 is in Vectors (implied by Get)
	item1Got, err := idx.Get(ctx, "item1")
	if err != nil {
		t.Fatalf("Get item1 failed: %v", err)
	}
	if item1Got.CentroidID == 0 {
		t.Fatal("Item1 should have assigned CentroidID after Optimize")
	}

	// 2. Add New Centroid
	vec2 := []float32{0.9, 0.9}
	newID, err := idx.AddCentroid(ctx, vec2)
	if err != nil {
		t.Fatalf("AddCentroid failed: %v", err)
	}
	// Optimize created 1 centroid (ID 1). So next should be 2.
	if newID != 2 {
		t.Errorf("Expected new centroid ID 2, got %d", newID)
	}

	// 3. Add Item to New Centroid
	// Upsert should now assign item2 to the new centroid because it's closer
	item2 := ai.Item[map[string]any]{
		ID:      "item2",
		Vector:  vec2,
		Payload: map[string]any{"label": "B"},
	}
	if err := idx.Upsert(ctx, item2); err != nil {
		t.Fatalf("Upsert item2 failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 4. Verify Assignment
	tx, err = db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	idx, err = vector.Open[map[string]any](ctx, tx, "test_dynamic_centroids", vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if _, err := idx.Get(ctx, "item2"); err != nil {
		t.Fatalf("Get item2 failed: %v", err)
	}
}
