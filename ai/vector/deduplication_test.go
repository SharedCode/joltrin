package vector_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestDeduplicationDisabled_Optimize(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-dedupe-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// 1. Initial Populate & Optimize
	trans1, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 1 failed: %v", err)
	}
	idx1, _ := db.OpenVectorStore(ctx, "test_dedupe", trans1, vector.Config{
		UsageMode:             ai.BuildOnceQueryMany,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})

	// Add Item A
	idx1.Upsert(ctx, ai.Item[map[string]any]{
		ID:      "item-A",
		Vector:  []float32{1.0, 1.0},
		Payload: map[string]any{"ver": 1},
	})

	// Optimize to move to Vectors
	if err := idx1.Optimize(ctx); err != nil {
		t.Fatalf("Optimize 1 failed: %v", err)
	}
	// Optimize commits trans1

	// 2. Simulate "Ghost" Item in TempVectors
	// We want to add an item to TempVectors BUT keep Content pointing to the old optimized version.
	// This simulates a state where we have a duplicate candidate that would be skipped if dedupe is enabled.

	trans2, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}

	// We need access to internal architecture to bypass Upsert's Content update
	// We use version 1 to access Vectors (Optimize moved data there)
	arch, err := vector.OpenDomainStore(ctx, trans2, "test_dedupe", 1, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("Failed to open arch: %v", err)
	}

	// Verify Content points to a Centroid (not 0)
	found, _ := arch.Content.Find(ctx, ai.ContentKey{ItemID: "item-A"}, false)
	if !found {
		t.Fatalf("Item A not found in Content")
	}
	jsonStr, _ := arch.Content.GetCurrentValue(ctx)
	var stored map[string]any
	json.Unmarshal([]byte(jsonStr), &stored)

	currentKey := arch.Content.GetCurrentKey().Key
	cid := currentKey.CentroidID
	if currentKey.NextVersion == 1 {
		cid = currentKey.NextCentroidID
	}
	if cid == 0 {
		t.Fatalf("Expected Item A to be optimized (CentroidID != 0), got 0")
	}

	// Manually Add to Vectors (Duplicate)
	// We need to construct a key.
	// We'll use a different centroid/distance to ensure it's a different key.
	dupKey := ai.VectorKey{CentroidID: 2, DistanceToCentroid: 0.5, ItemID: "item-A"}
	if _, err := arch.Vectors.Add(ctx, dupKey, []float32{2.0, 2.0}); err != nil {
		t.Fatalf("Failed to add to Vectors: %v", err)
	}

	trans2.Commit(ctx)

	// 3. Optimize with Deduplication DISABLED
	trans3, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 3 failed: %v", err)
	}
	idx3, _ := db.OpenVectorStore(ctx, "test_dedupe", trans3, vector.Config{
		UsageMode:   ai.BuildOnceQueryMany,
		ContentSize: sop.MediumData,
	})

	// Disable Deduplication
	idx3.SetDeduplication(false)

	if err := idx3.Optimize(ctx); err != nil {
		t.Fatalf("Optimize 2 failed: %v", err)
	}

	// 4. Verify Duplicates in Vectors
	trans4, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 4 failed: %v", err)
	}
	// We need to access Vectors B-Tree directly to count.
	// idx4.Get() only returns one.

	// We can use idx4.Vectors() if exposed? Yes, I added it to interface/struct.
	// But wait, `idx` is `ai.VectorStore` interface. Does it have `Vectors()`?
	// The struct `domainIndex` has it. The interface `VectorStore` might not.
	// Let's check `ai/interfaces.go`.

	// If not in interface, we can cast.
	db.OpenVectorStore(ctx, "test_dedupe", trans4, vector.Config{})

	// We need to cast to something that exposes Vectors.
	// Or use `OpenDomainStore` again with version 2.
	arch4, err := vector.OpenDomainStore(ctx, trans4, "test_dedupe", 2, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("Failed to open version 2: %v", err)
	}

	// Count entries for "item-A" in Vectors
	// Iterate all vectors
	count := 0
	if ok, _ := arch4.Vectors.First(ctx); ok {
		for {
			item, _ := arch4.Vectors.GetCurrentItem(ctx)
			if item.Key.ItemID == "item-A" {
				count++
			}
			if ok, _ := arch4.Vectors.Next(ctx); !ok {
				break
			}
		}
	}

	if count != 2 {
		t.Errorf("Expected 2 copies of item-A (Dedupe Disabled), got %d", count)
	}

	trans4.Commit(ctx)
}
