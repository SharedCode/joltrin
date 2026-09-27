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

func TestVectorStore(t *testing.T) {
	// Setup temporary directory
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize Database
	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	// 1. Test Upsert
	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	index, err := db.OpenVectorStore(ctx, "test_vectors", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Test Data
	id1 := "vec-1"
	vec1 := []float32{1.0, 0.0, 0.0}
	meta1 := map[string]any{"type": "A"}

	id2 := "vec-2"
	vec2 := []float32{0.0, 1.0, 0.0}
	meta2 := map[string]any{"type": "B"}

	if err := index.Upsert(ctx, ai.Item[map[string]any]{ID: id1, Vector: vec1, Payload: meta1}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if err := index.Upsert(ctx, ai.Item[map[string]any]{ID: id2, Vector: vec2, Payload: meta2}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Test Get & Query (Read Transaction)
	tx, err = db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	index, err = db.OpenVectorStore(ctx, "test_vectors", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	item, err := index.Get(ctx, id1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if item.ID != id1 {
		t.Errorf("Expected ID %s, got %s", id1, item.ID)
	}
	if len(item.Vector) != 3 {
		t.Errorf("Expected vector length 3, got %d", len(item.Vector))
	}

	// 3. Test Query (Exact Match)
	hits, err := index.Query(ctx, vec1, 1, nil)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Query returned no hits")
	}
	if hits[0].ID != id1 {
		t.Errorf("Expected top hit %s, got %s", id1, hits[0].ID)
	}
	if hits[0].Score < 0.99 {
		t.Errorf("Expected score ~1.0, got %f", hits[0].Score)
	}

	// 4. Test Query (Filter)
	hits, err = index.Query(ctx, vec1, 10, func(item map[string]any) bool {
		return item["type"] == "B"
	})
	if err != nil {
		t.Fatalf("Query with filter failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Query with filter returned no hits")
	}
	if hits[0].ID != id2 {
		t.Errorf("Expected filtered hit %s, got %s", id2, hits[0].ID)
	}

	// 5. Test Delete (Write Transaction)
	tx.Rollback(ctx) // End read transaction

	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	index, err = db.OpenVectorStore(ctx, "test_vectors", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := index.Delete(ctx, id1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify Delete
	tx, err = db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	index, err = db.OpenVectorStore(ctx, "test_vectors", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	_, err = index.Get(ctx, id1)
	if err == nil {
		t.Error("Expected error after delete, got nil")
	}

	// End read transaction
	tx.Rollback(ctx)

	// 6. Test Domains Isolation
	// We need a write transaction to create new stores
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	doctor, err := db.OpenVectorStore(ctx, "doctor", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open doctor failed: %v", err)
	}
	mechanic, err := db.OpenVectorStore(ctx, "mechanic", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open mechanic failed: %v", err)
	}

	// We need a write transaction for upsert
	tx.Rollback(ctx)
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	doctor, err = db.OpenVectorStore(ctx, "doctor", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open doctor failed: %v", err)
	}
	mechanic, err = db.OpenVectorStore(ctx, "mechanic", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open mechanic failed: %v", err)
	}

	// Upsert to doctor
	if err := doctor.Upsert(ctx, ai.Item[map[string]any]{ID: "flu", Vector: []float32{1.0, 0.0, 0.0}, Payload: map[string]any{"desc": "flu"}}); err != nil {
		t.Fatalf("Doctor upsert failed: %v", err)
	}

	// Upsert to mechanic
	if err := mechanic.Upsert(ctx, ai.Item[map[string]any]{ID: "engine", Vector: []float32{0.0, 1.0, 0.0}, Payload: map[string]any{"desc": "engine"}}); err != nil {
		t.Fatalf("Mechanic upsert failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Query (Read Transaction)
	tx, err = db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	doctor, err = db.OpenVectorStore(ctx, "doctor", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open doctor failed: %v", err)
	}
	mechanic, err = db.OpenVectorStore(ctx, "mechanic", tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		t.Fatalf("Open mechanic failed: %v", err)
	}

	// Query doctor
	docHits, err := doctor.Query(ctx, []float32{1.0, 0.0, 0.0}, 10, nil)
	if err != nil {
		t.Fatalf("Doctor query failed: %v", err)
	}
	if len(docHits) != 1 || docHits[0].ID != "flu" {
		t.Errorf("Doctor query expected 'flu', got %v", docHits)
	}

	// Query mechanic
	mechHits, err := mechanic.Query(ctx, []float32{0.0, 1.0, 0.0}, 10, nil)
	if err != nil {
		t.Fatalf("Mechanic query failed: %v", err)
	}
	if len(mechHits) != 1 || mechHits[0].ID != "engine" {
		t.Errorf("Mechanic query expected 'engine', got %v", mechHits)
	}
}

func TestDeleteUpdatesCentroidCount(t *testing.T) {
	// Setup temporary directory
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-delete-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize Database
	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	index, err := db.OpenVectorStore(ctx, "test_delete_count", tx, vector.Config{
		UsageMode: ai.DynamicWithVectorCountTracking,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
		},
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	id := "vec-1"
	vec := []float32{1.0, 0.0, 0.0}
	meta := map[string]any{"type": "A"}

	// 1. Upsert
	if err := index.Upsert(ctx, ai.Item[map[string]any]{ID: id, Vector: vec, Payload: meta}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Optimize to move to Vectors (Version 1) so we have Centroids
	if err := index.Optimize(ctx); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Optimize commits the transaction, so we need a new one
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}

	index, err = db.OpenVectorStore(ctx, "test_delete_count", tx, vector.Config{
		UsageMode: ai.DynamicWithVectorCountTracking,
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	// Helper to get centroid count
	getCentroidCount := func(centroidID int) int {
		centroidsStore, err := index.Centroids(ctx)
		if err != nil {
			t.Fatalf("Failed to get centroids store: %v", err)
		}

		found, err := centroidsStore.Find(ctx, centroidID, false)
		if err != nil {
			t.Fatalf("Failed to find centroid: %v", err)
		}
		if !found {
			t.Fatalf("Centroid %d not found", centroidID)
		}

		c, err := centroidsStore.GetCurrentValue(ctx)
		if err != nil {
			t.Fatalf("Failed to get centroid value: %v", err)
		}
		return c.VectorCount
	}

	// 2. Verify Count is 1
	// The first centroid created is usually ID 1
	count := getCentroidCount(1)
	if count != 1 {
		t.Errorf("Expected centroid count 1, got %d", count)
	}

	// 3. Delete
	// Note: Delete marks the item as deleted (Tombstone) and decrements the centroid count immediately.
	// The physical removal of the item happens during the next Optimize call.
	if err := index.Delete(ctx, id); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 4. Verify Count is 0
	count = getCentroidCount(1)
	if count != 0 {
		t.Errorf("Expected centroid count 0, got %d", count)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}
