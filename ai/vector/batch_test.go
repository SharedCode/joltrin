package vector

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestUpsertBatchCentroidPopulation(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-batch-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, _ := core_database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idx, err := Open[map[string]any](ctx, tx, "test_batch", Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Create a batch of items that form 2 clusters
	var items []ai.Item[map[string]any]
	// Cluster 1: around (0,0)
	for i := 0; i < 10; i++ {
		items = append(items, ai.Item[map[string]any]{
			ID:      fmt.Sprintf("c1-%d", i),
			Vector:  []float32{0.1 * float32(i), 0.1 * float32(i)},
			Payload: map[string]any{"cluster": 1},
		})
	}
	// Cluster 2: around (10,10)
	for i := 0; i < 10; i++ {
		items = append(items, ai.Item[map[string]any]{
			ID:      fmt.Sprintf("c2-%d", i),
			Vector:  []float32{10.0 + 0.1*float32(i), 10.0 + 0.1*float32(i)},
			Payload: map[string]any{"cluster": 2},
		})
	}

	// UpsertBatch should trigger K-Means (k ~ sqrt(20) = 4)
	if err := idx.UpsertBatch(ctx, items); err != nil {
		t.Fatalf("UpsertBatch failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify Centroids were created
	// We can't easily access centroids directly from here without opening the store manually,
	// but we can check if Query works effectively.

	tx, err = core_database.BeginTransaction(ctx, db, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer tx.Rollback(ctx)

	idx, err = Open[map[string]any](ctx, tx, "test_batch", Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Query near Cluster 1
	hits, err := idx.Query(ctx, []float32{0, 0}, 5, nil)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Query returned no hits")
	}
	// Should be from Cluster 1
	clusterVal := hits[0].Payload["cluster"]
	if v, ok := clusterVal.(float64); ok {
		if int(v) != 1 {
			t.Errorf("Expected cluster 1, got %v", clusterVal)
		}
	} else if v, ok := clusterVal.(int); ok {
		if v != 1 {
			t.Errorf("Expected cluster 1, got %v", clusterVal)
		}
	} else {
		t.Errorf("Unexpected type for cluster: %T", clusterVal)
	}

	// Query near Cluster 2
	hits2, err := idx.Query(ctx, []float32{10, 10}, 5, nil)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(hits2) == 0 {
		t.Fatal("Query returned no hits")
	}
	// Should be from Cluster 2
	clusterVal2 := hits2[0].Payload["cluster"]
	if v, ok := clusterVal2.(float64); ok {
		if int(v) != 2 {
			t.Errorf("Expected cluster 2, got %v", clusterVal2)
		}
	} else if v, ok := clusterVal2.(int); ok {
		if v != 2 {
			t.Errorf("Expected cluster 2, got %v", clusterVal2)
		}
	} else {
		t.Errorf("Unexpected type for cluster: %T", clusterVal2)
	}
}
