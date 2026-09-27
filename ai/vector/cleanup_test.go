package vector_test

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestOptimizeCleansUpSoftDeletedItems(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	path, _ := os.MkdirTemp("", "sop-ai-test-cleanup")
	defer os.RemoveAll(path)

	db, _ := core_database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{path},
	})

	t1, _ := core_database.BeginTransaction(ctx, db, sop.ForWriting)

	cfg := vector.Config{
		UsageMode: ai.DynamicWithVectorCountTracking,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{path},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	}

	store, err := vector.Open[map[string]any](ctx, t1, "cleanup_test", cfg)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// 2. Add Item
	id := "item1"
	if err := store.Upsert(ctx, ai.Item[map[string]any]{
		ID:      id,
		Vector:  []float32{1, 2, 3},
		Payload: map[string]any{"data": "payload"},
	}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	t1.Commit(ctx)

	// 3. Delete Item (Soft Delete)
	t2, _ := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	store, _ = vector.Open[map[string]any](ctx, t2, "cleanup_test", cfg)

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	t2.Commit(ctx)

	// Verify Soft Delete
	t3, _ := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	store, _ = vector.Open[map[string]any](ctx, t3, "cleanup_test", cfg)

	// Should not be found via Get
	if _, err := store.Get(ctx, id); err == nil {
		t.Errorf("Item should be not found after delete")
	}

	// But should exist in Content with Deleted=true
	contentStore, _ := store.Content(ctx)
	if found, _ := contentStore.Find(ctx, ai.ContentKey{ItemID: id}, false); !found {
		t.Errorf("Item should still exist in Content (soft deleted)")
	} else {
		k := contentStore.GetCurrentKey()
		if !k.Key.Deleted {
			t.Errorf("Item should be marked Deleted")
		}
	}
	t3.Commit(ctx)

	// 4. Run Optimize
	t4, _ := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	store, err = vector.Open[map[string]any](ctx, t4, "cleanup_test", cfg)
	if err != nil {
		t.Fatalf("Open failed (4): %v", err)
	}

	if err := store.Optimize(ctx); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// 5. Verify Physical Deletion
	t5, _ := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	store, err = vector.Open[map[string]any](ctx, t5, "cleanup_test", cfg)
	if err != nil {
		t.Fatalf("Open failed (5): %v", err)
	}
	contentStore, err = store.Content(ctx)
	if err != nil {
		t.Fatalf("Content failed: %v", err)
	}

	if found, _ := contentStore.Find(ctx, ai.ContentKey{ItemID: id}, false); found {
		t.Errorf("Item should have been physically deleted from Content after Optimize")
	}

	t5.Commit(ctx)
}
