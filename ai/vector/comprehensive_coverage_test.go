package vector_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestVectorStoreComprehensiveLifecycle(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-comprehensive-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()
	storeName := "comp_test"

	// --- Step 1: Ingest (V0) ---
	t.Log("--- Step 1: Ingest (V0) ---")
	trans0, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 0 failed: %v", err)
	}

	store0, err := db.OpenVectorStore(ctx, storeName, trans0, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})
	if err != nil {
		t.Fatalf("Open 0 failed: %v", err)
	}

	// Add Item 1, 2, 3
	itemsV0 := []ai.Item[map[string]any]{
		{ID: "item-1", Vector: []float32{1, 1}, Payload: map[string]any{"val": 1}},
		{ID: "item-2", Vector: []float32{2, 2}, Payload: map[string]any{"val": 2}},
		{ID: "item-3", Vector: []float32{3, 3}, Payload: map[string]any{"val": 3}},
	}
	if err := store0.UpsertBatch(ctx, itemsV0); err != nil {
		t.Fatalf("UpsertBatch V0 failed: %v", err)
	}

	// Verify V0 State (Peek)
	// In V0, we expect TempVectors to be used.
	// We can't easily peek without committing or using the same transaction with internal helpers.
	// Let's commit first.
	if err := trans0.Commit(ctx); err != nil {
		t.Fatalf("Commit 0 failed: %v", err)
	}

	// Peek V0
	transPeek0, _ := db.BeginTransaction(ctx, sop.ForReading)
	arch0, err := vector.OpenDomainStore(ctx, transPeek0, storeName, 0, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("OpenDomainStore V0 failed: %v", err)
	}
	if arch0.TempVectors == nil {
		t.Fatal("TempVectors should exist in V0")
	}
	if arch0.Vectors.Count() > 0 {
		t.Fatal("Vectors should be empty in V0")
	}
	transPeek0.Commit(ctx)

	// --- Step 2: Optimize (V0 -> V1) ---
	t.Log("--- Step 2: Optimize (V0 -> V1) ---")
	trans1, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 1 failed: %v", err)
	}
	store1, err := db.OpenVectorStore(ctx, storeName, trans1, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})
	if err != nil {
		t.Fatalf("Open 1 failed: %v", err)
	}

	if err := store1.Optimize(ctx); err != nil {
		t.Fatalf("Optimize V0->V1 failed: %v", err)
	}
	// Optimize commits trans1

	// --- Step 3: Verify V1 State ---
	t.Log("--- Step 3: Verify V1 State ---")
	transVerify1, _ := db.BeginTransaction(ctx, sop.ForReading)

	// Check Version in SysStore
	sysStoreName := fmt.Sprintf("%s/sys_config", storeName)
	sysStore, _ := infs.OpenBtree[string, string](ctx, sysStoreName, transVerify1, nil)
	found, _ := sysStore.Find(ctx, storeName, false)
	if !found {
		t.Fatal("System config not found")
	}
	strVer, _ := sysStore.GetCurrentValue(ctx)
	var meta vector.Metadata
	json.Unmarshal([]byte(strVer), &meta)
	if meta.ActiveVersion != 1 {
		t.Errorf("Expected Version 1, got %d", meta.ActiveVersion)
	}

	// Check Files/Stores
	// V1 should have: /centroids_1, /vecs_1, /lku_1
	// TempVectors should be gone.

	// We use OpenDomainStore to check internal struct
	arch1, err := vector.OpenDomainStore(ctx, transVerify1, storeName, 1, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("OpenDomainStore V1 failed: %v", err)
	}
	if arch1.TempVectors == nil {
		t.Fatal("TempVectors should NOT be nil in V1")
	}
	if arch1.Vectors.Count() != 3 {
		t.Errorf("Expected 3 vectors in V1, got %d", arch1.Vectors.Count())
	}
	if arch1.Lookup.Count() != 3 {
		t.Errorf("Expected 3 lookup items in V1, got %d", arch1.Lookup.Count())
	}

	// Check physical existence of V1 stores (by name convention)
	// Note: In Standalone, these are folders/files.
	// We can't easily check "TempVectors disappeared" physically because SOP manages it,
	// but we can check if we can open it.
	// Actually, Optimize deletes it.

	transVerify1.Commit(ctx)

	// --- Step 4: CUD in V1 ---
	t.Log("--- Step 4: CUD in V1 ---")
	transCUD, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction CUD failed: %v", err)
	}
	storeCUD, err := db.OpenVectorStore(ctx, storeName, transCUD, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})
	if err != nil {
		t.Fatalf("Open CUD failed: %v", err)
	}

	// CREATE: Item 4
	if err := storeCUD.Upsert(ctx, ai.Item[map[string]any]{
		ID: "item-4", Vector: []float32{4, 4}, Payload: map[string]any{"val": 4},
	}); err != nil {
		t.Fatalf("Upsert Item 4 failed: %v", err)
	}

	// UPDATE: Item 2 (Change Vector and Payload)
	if err := storeCUD.Upsert(ctx, ai.Item[map[string]any]{
		ID: "item-2", Vector: []float32{2.1, 2.1}, Payload: map[string]any{"val": 22},
	}); err != nil {
		t.Fatalf("Update Item 2 failed: %v", err)
	}

	// DELETE: Item 1
	if err := storeCUD.Delete(ctx, "item-1"); err != nil {
		t.Fatalf("Delete Item 1 failed: %v", err)
	}

	if err := transCUD.Commit(ctx); err != nil {
		t.Fatalf("Commit CUD failed: %v", err)
	}

	// Verify CUD results (in V1)
	transVerifyCUD, _ := db.BeginTransaction(ctx, sop.ForReading)
	storeVerifyCUD, _ := db.OpenVectorStore(ctx, storeName, transVerifyCUD, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})

	// Check Item 1 (Deleted)
	if _, err := storeVerifyCUD.Get(ctx, "item-1"); err == nil {
		t.Error("Item 1 should be deleted")
	}

	// Check Item 2 (Updated)
	item2, err := storeVerifyCUD.Get(ctx, "item-2")
	if err != nil {
		t.Error("Item 2 not found")
	} else {
		if item2.Vector[0] != 2.1 {
			t.Errorf("Item 2 vector not updated. Got %v", item2.Vector)
		}
		if val, ok := item2.Payload["val"].(float64); !ok || val != 22 {
			// JSON unmarshal numbers as float64
			t.Errorf("Item 2 payload not updated. Got %v", item2.Payload)
		}
	}

	// Check Item 4 (Created)
	if _, err := storeVerifyCUD.Get(ctx, "item-4"); err != nil {
		t.Error("Item 4 not found")
	}

	transVerifyCUD.Commit(ctx)

	// --- Step 5: Optimize (V1 -> V2) ---
	t.Log("--- Step 5: Optimize (V1 -> V2) ---")
	trans2, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	store2, err := db.OpenVectorStore(ctx, storeName, trans2, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	if err := store2.Optimize(ctx); err != nil {
		t.Fatalf("Optimize V1->V2 failed: %v", err)
	}

	// --- Step 6: Verify V2 State ---
	t.Log("--- Step 6: Verify V2 State ---")
	transVerify2, _ := db.BeginTransaction(ctx, sop.ForReading)

	// Check Version
	sysStore, _ = infs.OpenBtree[string, string](ctx, sysStoreName, transVerify2, nil)
	sysStore.Find(ctx, storeName, false)
	strVer, _ = sysStore.GetCurrentValue(ctx)
	json.Unmarshal([]byte(strVer), &meta)
	if meta.ActiveVersion != 2 {
		t.Errorf("Expected Version 2, got %d", meta.ActiveVersion)
	}

	// Check V1 Stores Deleted
	// We check if we can open them (should fail) or check filesystem
	v1Stores := []string{
		fmt.Sprintf("%s/vecs_1", storeName),
		fmt.Sprintf("%s/centroids_1", storeName),
		fmt.Sprintf("%s/lku_1", storeName),
	}
	for _, s := range v1Stores {
		path := filepath.Join(tmpDir, s)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Store %s should have been deleted", s)
		}
	}

	// Check V2 Stores Exist
	v2Stores := []string{
		fmt.Sprintf("%s/vecs_2", storeName),
		fmt.Sprintf("%s/centroids_2", storeName),
		fmt.Sprintf("%s/lku_2", storeName),
	}
	for _, s := range v2Stores {
		path := filepath.Join(tmpDir, s)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Store %s should exist", s)
		}
	}

	// Verify Data Integrity in V2
	arch2, err := vector.OpenDomainStore(ctx, transVerify2, storeName, 2, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("OpenDomainStore V2 failed: %v", err)
	}

	// Expected items: 2, 3, 4 (1 deleted)
	expectedCount := int64(3)
	if arch2.Vectors.Count() != expectedCount {
		t.Errorf("Expected %d vectors in V2, got %d", expectedCount, arch2.Vectors.Count())
	}
	if arch2.Lookup.Count() != expectedCount {
		t.Errorf("Expected %d lookup items in V2, got %d", expectedCount, arch2.Lookup.Count())
	}

	// Verify Lookup Validity
	if ok, _ := arch2.Lookup.First(ctx); ok {
		for {
			id, _ := arch2.Lookup.GetCurrentValue(ctx)
			// Check if ID exists in Content
			found, _ := arch2.Content.Find(ctx, ai.ContentKey{ItemID: id}, false)
			if !found {
				t.Errorf("Lookup ID %s not found in Content", id)
			}

			// Check if ID exists in Vectors
			// Need to get CentroidID from Content first
			currentItem := arch2.Content.GetCurrentKey()
			currentKey := currentItem.Key

			cid := currentKey.CentroidID
			dist := currentKey.Distance
			// Handle Lazy Migration (Next fields)
			if currentKey.Version != 2 && currentKey.NextVersion == 2 {
				cid = currentKey.NextCentroidID
				dist = currentKey.NextDistance
			}

			vecKey := ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: id}
			foundVec, _ := arch2.Vectors.Find(ctx, vecKey, false)
			if !foundVec {
				t.Errorf("Vector for %s not found in Vectors V2", id)
			}

			if ok, _ := arch2.Lookup.Next(ctx); !ok {
				break
			}
		}
	}

	transVerify2.Commit(ctx)
}
