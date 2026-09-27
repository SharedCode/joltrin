package vector

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestOptimizeRollingVersion(t *testing.T) {
	// 1. Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-rolling-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use core database to manage transactions
	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// 2. Insert Item (Version 0)
	tx1, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction #1 failed: %v", err)
	}

	// Call Open directly
	cfg := Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	}
	idx1, err := Open[map[string]any](ctx, tx1, "rolling_test", cfg)
	if err != nil {
		t.Fatalf("Open #1 failed: %v", err)
	}

	item := ai.Item[map[string]any]{
		ID:      "item1",
		Vector:  []float32{1.0, 2.0, 3.0},
		Payload: map[string]any{"val": 1},
	}
	if err := idx1.Upsert(ctx, item); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	tx1.Commit(ctx)

	// 3. Optimize #1 (Moves to Version 1)
	tx2, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction #2 failed: %v", err)
	}
	idx2, err := Open[map[string]any](ctx, tx2, "rolling_test", cfg)
	if err != nil {
		t.Fatalf("Open #2 failed: %v", err)
	}
	if err := idx2.Optimize(ctx); err != nil {
		t.Fatalf("Optimize #1 failed: %v", err)
	}

	// 3b. Verify Access after Optimize #1 (Active Version = 1)
	// The item should be accessible via NextVersion logic (Version=0, NextVersion=1)
	tx2a, _ := database.BeginTransaction(ctx, db, sop.ForReading)
	idx2a, _ := Open[map[string]any](ctx, tx2a, "rolling_test", cfg)

	got1, err := idx2a.Get(ctx, "item1")
	if err != nil {
		t.Fatalf("Get failed after Optimize #1: %v", err)
	}
	if got1.ID != "item1" {
		t.Errorf("Expected ID item1, got %s", got1.ID)
	}
	if len(got1.Vector) != 3 || got1.Vector[0] != 1.0 {
		t.Errorf("Vector mismatch after Optimize #1: %v", got1.Vector)
	}

	// Verify Query works
	hits1, err := idx2a.Query(ctx, []float32{1.0, 2.0, 3.0}, 1, nil)
	if err != nil {
		t.Fatalf("Query failed after Optimize #1: %v", err)
	}
	if len(hits1) == 0 || hits1[0].ID != "item1" {
		t.Errorf("Query failed to find item after Optimize #1")
	}
	tx2a.Commit(ctx)

	// 4. Optimize #2 (Moves to Version 2)
	tx3, _ := database.BeginTransaction(ctx, db, sop.ForWriting)
	idx3, _ := Open[map[string]any](ctx, tx3, "rolling_test", cfg)
	if err := idx3.Optimize(ctx); err != nil {
		t.Fatalf("Optimize #2 failed: %v", err)
	}

	// 4b. Verify Access after Optimize #2 (Active Version = 2)
	// The item should be accessible. Internally, it should have rolled (Version=1, NextVersion=2).
	tx3a, _ := database.BeginTransaction(ctx, db, sop.ForReading)
	idx3a, _ := Open[map[string]any](ctx, tx3a, "rolling_test", cfg)

	got2, err := idx3a.Get(ctx, "item1")
	if err != nil {
		t.Fatalf("Get failed after Optimize #2: %v", err)
	}
	if got2.ID != "item1" {
		t.Errorf("Expected ID item1, got %s", got2.ID)
	}
	if len(got2.Vector) != 3 || got2.Vector[0] != 1.0 {
		t.Errorf("Vector mismatch after Optimize #2: %v", got2.Vector)
	}

	// Verify Query works
	hits2, err := idx3a.Query(ctx, []float32{1.0, 2.0, 3.0}, 1, nil)
	if err != nil {
		t.Fatalf("Query failed after Optimize #2: %v", err)
	}
	if len(hits2) == 0 || hits2[0].ID != "item1" {
		t.Errorf("Query failed to find item after Optimize #2")
	}
	tx3a.Commit(ctx)

	// 5. Verify Internal State
	tx4, _ := database.BeginTransaction(ctx, db, sop.ForReading)
	idx4, _ := Open[map[string]any](ctx, tx4, "rolling_test", cfg)

	di := idx4.(*domainIndex[map[string]any])

	activeVer, _ := di.getActiveVersion(ctx)
	if activeVer != 2 {
		t.Errorf("Expected active version 2, got %d", activeVer)
	}

	arch, err := di.getArchitecture(ctx, activeVer)
	if err != nil {
		t.Fatalf("Failed to get architecture: %v", err)
	}

	found, err := arch.Content.Find(ctx, ai.ContentKey{ItemID: "item1"}, false)
	if err != nil {
		t.Fatalf("Content.Find failed: %v", err)
	}
	if !found {
		t.Fatal("Item not found in Content store")
	}

	currentKey := arch.Content.GetCurrentKey().Key

	t.Logf("Item State: Version=%d, NextVersion=%d", currentKey.Version, currentKey.NextVersion)

	if currentKey.Version != 1 {
		t.Errorf("Rolling logic failed! Expected Version=1, got %d", currentKey.Version)
	}
	if currentKey.NextVersion != 2 {
		t.Errorf("Expected NextVersion=2, got %d", currentKey.NextVersion)
	}

	tx4.Commit(ctx)
}
