package vector_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func BenchmarkVectorStoreCRUD(b *testing.B) {
	ctx := context.Background()
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sop.RegisterL2CacheFactory(sop.InMemory, func(options sop.TransactionOptions) sop.L2Cache {
		return cache.NewL2InMemoryCache()
	})

	options := core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	}
	db := database.NewDatabase(options)

	b.ResetTimer()

	// Seed for reproducibility
	rand.Seed(42)

	// We will run this benchmark for b.N iterations.
	// Each iteration will do a batch of operations: Insert/Update, Query, Delete, and a Commit.
	for i := 0; i < b.N; i++ {
		// Stop timer during setup to get accurate operational metrics
		b.StopTimer()

		trans, err := db.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			b.Fatalf("BeginTransaction failed: %v", err)
		}

		storeName := fmt.Sprintf("bench_store_%d", i%5) // Loop across 5 stores to simulate churn
		store, err := db.OpenVectorStore(ctx, storeName, trans, vector.Config{
			UsageMode: ai.DynamicWithVectorCountTracking,
		})
		if err != nil {
			b.Fatalf("OpenVectorStore failed: %v", err)
		}

		// Re-start timer for actual operations
		b.StartTimer()

		// 1. Batch Insert
		var items []ai.Item[map[string]any]
		for j := 0; j < 50; j++ {
			items = append(items, ai.Item[map[string]any]{
				ID:     fmt.Sprintf("item_%d_%d", i, j),
				Vector: []float32{rand.Float32(), rand.Float32(), rand.Float32()},
				Payload: map[string]any{
					"bench_iter": i,
					"data":       fmt.Sprintf("payload_%d", j),
				},
			})
		}
		if err := store.UpsertBatch(ctx, items); err != nil {
			b.Fatalf("UpsertBatch failed: %v", err)
		}

		// 2. Query
		targetVec := []float32{rand.Float32(), rand.Float32(), rand.Float32()}
		_, err = store.Query(ctx, targetVec, 5, nil)
		if err != nil {
			b.Fatalf("Query failed: %v", err)
		}

		// 3. Update an item
		if err := store.Upsert(ctx, ai.Item[map[string]any]{
			ID:     fmt.Sprintf("item_%d_%d", i, 0),
			Vector: []float32{1.0, 1.0, 1.0},
			Payload: map[string]any{
				"updated": true,
			},
		}); err != nil {
			b.Fatalf("Upsert update failed: %v", err)
		}

		// 4. Delete an item
		if err := store.Delete(ctx, fmt.Sprintf("item_%d_%d", i, 1)); err != nil {
			b.Fatalf("Delete failed: %v", err)
		}

		// 5. Commit
		if err := trans.Commit(ctx); err != nil {
			b.Fatalf("Commit failed: %v", err)
		}
	}
}
