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

func TestDirectIngestion(t *testing.T) {
	// 1. Setup Environment
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-skip-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// 2. Configure Store with EnableIngestionBuffer = false (Default)
	// This means we skip Stage 0 (TempVectors) and write directly to the Index (Vectors).
	cfg := vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: false, // Explicitly set to false for clarity, though it's the default
	}

	// 3. Open Store
	trans, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	store, err := db.OpenVectorStore(ctx, "skip_stage_demo", trans, cfg)
	if err != nil {
		t.Fatalf("OpenVectorStore failed: %v", err)
	}

	// 4. Upsert an Item
	// Since we are skipping Stage 0, this item will go directly to the Vectors B-Tree.
	// If we don't provide a CentroidID, one will be auto-generated/assigned.
	item := ai.Item[map[string]any]{
		ID:      "item-direct",
		Vector:  []float32{1.0, 2.0, 3.0},
		Payload: map[string]any{"info": "direct insert"},
	}

	if err := store.Upsert(ctx, item); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// Commit to persist changes
	if err := trans.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 5. Verify Internal State (Architecture)
	transReadArch, _ := db.BeginTransaction(ctx, sop.ForReading)

	// Open internal architecture to inspect
	arch, err := vector.OpenDomainStore(ctx, transReadArch, "skip_stage_demo", 0, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: false})
	if err != nil {
		t.Fatalf("OpenDomainStore failed: %v", err)
	}

	// Assertion 1: TempVectors should be nil
	if arch.TempVectors != nil {
		t.Error("Expected TempVectors to be nil when EnableIngestionBuffer is false")
	}

	// Assertion 2: Item should be in Vectors B-Tree
	// We need to find it. Since we didn't specify CentroidID, it was auto-assigned (likely 1).
	// We can check Content first to get the assigned CentroidID.
	found, err := arch.Content.Find(ctx, ai.ContentKey{ItemID: "item-direct"}, false)
	if !found {
		t.Fatal("Item not found in Content store")
	}
	transReadArch.Commit(ctx)

	// 6. Verify Public API
	transReadAPI, _ := db.BeginTransaction(ctx, sop.ForReading)

	// Assertion 3: Verify it is retrievable via standard Get
	storeRead, _ := db.OpenVectorStore(ctx, "skip_stage_demo", transReadAPI, cfg)
	fetchedItem, err := storeRead.Get(ctx, "item-direct")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if fetchedItem.CentroidID == 0 {
		t.Error("Fetched item has CentroidID 0, implying it came from TempVectors (unexpected)")
	}

	t.Logf("Success: Item inserted directly into Centroid %d", fetchedItem.CentroidID)

	transReadAPI.Commit(ctx)
}
