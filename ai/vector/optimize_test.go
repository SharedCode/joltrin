package vector_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestOptimize(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-optimize-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize Database
	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	tx, err := db.BeginTransaction(context.Background(), sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idx, err := db.OpenVectorStore(context.Background(), "test_optimize", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	// dIdx := idx.(*domainIndex[map[string]any])

	// 1. Initial Population (2 Clusters)
	// Cluster 1: (0,0)
	// Cluster 2: (10,10)
	var items []ai.Item[map[string]any]
	for i := 0; i < 10; i++ {
		items = append(items, ai.Item[map[string]any]{
			ID:      fmt.Sprintf("c1-%d", i),
			Vector:  []float32{0.1 * float32(i), 0.1 * float32(i)},
			Payload: map[string]any{"cluster": 1},
		})
	}
	for i := 0; i < 10; i++ {
		items = append(items, ai.Item[map[string]any]{
			ID:      fmt.Sprintf("c2-%d", i),
			Vector:  []float32{10.0 + 0.1*float32(i), 10.0 + 0.1*float32(i)},
			Payload: map[string]any{"cluster": 2},
		})
	}

	if err := idx.UpsertBatch(context.Background(), items); err != nil {
		t.Fatalf("UpsertBatch failed: %v", err)
	}

	// Verify initial state (should have ~4 centroids due to sqrt(20))
	// But we can't easily check centroid count without peeking.
	// Let's assume it's fine.

	// 2. Add a NEW Cluster (20,20) via individual Upserts
	// This simulates drift. The new items will be forced into existing centroids (likely Cluster 2's).
	for i := 0; i < 10; i++ {
		err := idx.Upsert(context.Background(), ai.Item[map[string]any]{
			ID:      fmt.Sprintf("c3-%d", i),
			Vector:  []float32{20.0 + 0.1*float32(i), 20.0 + 0.1*float32(i)},
			Payload: map[string]any{"cluster": 3},
		})
		if err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}
	}

	// 3. Optimize
	// This should detect the new cluster and create a centroid for it.
	if err := idx.Optimize(context.Background()); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Start new transaction for verification
	tx2, err := db.BeginTransaction(context.Background(), sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	idx2, err := db.OpenVectorStore(context.Background(), "test_optimize", tx2, vector.Config{
		UsageMode: ai.Dynamic,
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	// 4. Verify
	// Query near the new cluster (20,20)
	hits, err := idx2.Query(context.Background(), []float32{20, 20}, 5, nil)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Query returned no hits")
	}
	// Should be from Cluster 3
	clusterVal := hits[0].Payload["cluster"]
	if v, ok := clusterVal.(float64); ok {
		if int(v) != 3 {
			t.Errorf("Expected cluster 3, got %v", clusterVal)
		}
	} else if v, ok := clusterVal.(int); ok {
		if v != 3 {
			t.Errorf("Expected cluster 3, got %v", clusterVal)
		}
	} else {
		t.Errorf("Unexpected type for cluster: %T", clusterVal)
	}

	// Check if metadata was updated in Content store
	// We can check by getting an item and inspecting internal fields if we could,
	// but Get() returns clean meta.
	// However, if Optimize failed to update Content, Delete might fail or leave ghosts?
	// Let's try to delete an item from the new cluster.
	if err := idx2.Delete(context.Background(), "c3-0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// Verify it's gone
	if _, err := idx2.Get(context.Background(), "c3-0"); err == nil {
		t.Error("Expected error after delete, got nil")
	}

	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}
