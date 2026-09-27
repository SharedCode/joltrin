package vector_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestVectorStoreLifecycle(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-lifecycle-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, _ := core_database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// --- Stage 0: Initial Ingestion (V0) ---
	t.Log("--- Stage 0: Initial Ingestion ---")
	trans0, err := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 0 failed: %v", err)
	}

	// Use BuildOnceQueryMany to force TempVectors usage if logic depends on it,
	// but our new logic says TempVectors is used if it exists (Version 0).
	// Let's use Dynamic to prove it works for general case too if Version 0.
	// Wait, the code says:
	// if arch.TempVectors != nil { ... }
	// And OpenDomainStore creates TempVectors ONLY if version == 0.
	// So any UsageMode should work for V0 ingestion into TempVectors.
	idx0, err := vector.Open[map[string]any](ctx, trans0, "lifecycle", vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open 0 failed: %v", err)
	}

	// Upsert Item A
	if err := idx0.Upsert(ctx, ai.Item[map[string]any]{
		ID:      "item-A",
		Vector:  []float32{1.0, 1.0},
		Payload: map[string]any{"ver": 0},
	}); err != nil {
		t.Fatalf("Upsert A failed: %v", err)
	}

	// Verify it is in TempVectors (by checking CentroidID == 0)
	itemA, err := idx0.Get(ctx, "item-A")
	if err != nil {
		t.Fatalf("Get A failed: %v", err)
	}
	if itemA.CentroidID != 0 {
		t.Errorf("Item A should have CentroidID 0 (in TempVectors), got %d", itemA.CentroidID)
	}

	// Also verify we can't find it in Vectors if we were to look (but we can't look easily without opening arch).
	// But CentroidID=0 confirms it came from TempVectors path in Get().

	if err := trans0.Commit(ctx); err != nil {
		t.Fatalf("Commit 0 failed: %v", err)
	}

	// --- Stage 1: Optimize (V0 -> V1) ---
	t.Log("--- Stage 1: Optimize (V0 -> V1) ---")
	trans1, err := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 1 failed: %v", err)
	}
	idx1, err := vector.Open[map[string]any](ctx, trans1, "lifecycle", vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open 1 failed: %v", err)
	}

	if err := idx1.Optimize(ctx); err != nil {
		t.Fatalf("Optimize 1 failed: %v", err)
	}
	// Optimize commits trans1

	// --- Stage 2a: Verify V1 State ---
	t.Log("--- Stage 2a: Verify V1 State ---")
	trans2a, err := core_database.BeginTransaction(ctx, db, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 2a failed: %v", err)
	}

	// Open Arch V1 to verify
	arch1, err := vector.OpenDomainStore(ctx, trans2a, "lifecycle", 1, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("Failed to open arch 1: %v", err)
	}
	if arch1.TempVectors == nil {
		t.Fatal("TempVectors should NOT be nil in Version 1")
	}

	// Verify Item A is in Vectors (V1)
	found, err := arch1.Content.Find(ctx, ai.ContentKey{ItemID: "item-A"}, false)
	if !found {
		t.Fatal("Item A not found in Content V1")
	}
	jsonStr, _ := arch1.Content.GetCurrentValue(ctx)
	var storedA map[string]any
	json.Unmarshal([]byte(jsonStr), &storedA)

	currentKeyA := arch1.Content.GetCurrentKey().Key
	cid := currentKeyA.CentroidID
	dist := currentKeyA.Distance
	if currentKeyA.NextVersion == 1 {
		cid = currentKeyA.NextCentroidID
		dist = currentKeyA.NextDistance
	}

	if cid == 0 {
		t.Fatal("Item A should have assigned CentroidID in V1")
	}

	// Check Vectors
	vecKeyA := ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: "item-A"}
	foundVec, err := arch1.Vectors.Find(ctx, vecKeyA, false)
	if !foundVec {
		t.Fatal("Item A should be in Vectors V1")
	}
	trans2a.Commit(ctx)

	// --- Stage 2b: Ingestion into V1 ---
	t.Log("--- Stage 2b: Ingestion into V1 ---")
	trans2b, err := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2b failed: %v", err)
	}

	// Upsert Item B (Should go to Vectors V1 directly)
	idx2, err := vector.Open[map[string]any](ctx, trans2b, "lifecycle", vector.Config{
		UsageMode:   ai.Dynamic,
		ContentSize: sop.MediumData,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	if err := idx2.Upsert(ctx, ai.Item[map[string]any]{
		ID:      "item-B",
		Vector:  []float32{2.0, 2.0},
		Payload: map[string]any{"ver": 1},
	}); err != nil {
		t.Fatalf("Upsert B failed: %v", err)
	}

	// Verify Item B is in Vectors V1 (using Get to avoid manual open issues)
	itemB, err := idx2.Get(ctx, "item-B")
	if err != nil {
		t.Fatalf("Get B failed: %v", err)
	}
	if itemB.CentroidID == 0 {
		t.Fatal("Item B should have assigned CentroidID in V1")
	}

	if err := trans2b.Commit(ctx); err != nil {
		t.Fatalf("Commit 2b failed: %v", err)
	}

	// --- Stage 3: Optimize (V1 -> V2) ---
	t.Log("--- Stage 3: Optimize (V1 -> V2) ---")
	trans3, err := core_database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 3 failed: %v", err)
	}
	idx3, err := vector.Open[map[string]any](ctx, trans3, "lifecycle", vector.Config{
		UsageMode:   ai.Dynamic,
		ContentSize: sop.MediumData,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open 3 failed: %v", err)
	}

	if err := idx3.Optimize(ctx); err != nil {
		t.Fatalf("Optimize 2 failed: %v", err)
	}
	// Optimize commits trans3

	// --- Stage 4: Verify V2 State ---
	t.Log("--- Stage 4: Verify V2 State ---")
	trans4, err := core_database.BeginTransaction(ctx, db, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 4 failed: %v", err)
	}

	// Open Arch V2
	arch2, err := vector.OpenDomainStore(ctx, trans4, "lifecycle", 2, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("Failed to open arch 2: %v", err)
	}
	if arch2.TempVectors == nil {
		t.Fatal("TempVectors should NOT be nil in Version 2")
	}

	// Verify Item A is in Vectors V2
	found, err = arch2.Content.Find(ctx, ai.ContentKey{ItemID: "item-A"}, false)
	if !found {
		t.Fatal("Item A not found in Content V2")
	}
	jsonStr, _ = arch2.Content.GetCurrentValue(ctx)
	json.Unmarshal([]byte(jsonStr), &storedA)

	currentKeyA = arch2.Content.GetCurrentKey().Key
	cid = currentKeyA.CentroidID
	dist = currentKeyA.Distance
	if currentKeyA.NextVersion == 2 {
		cid = currentKeyA.NextCentroidID
		dist = currentKeyA.NextDistance
	}
	vecKeyA = ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: "item-A"}
	foundVec, err = arch2.Vectors.Find(ctx, vecKeyA, false)
	if !foundVec {
		t.Fatal("Item A should be in Vectors V2")
	}

	// Verify Item B is in Vectors V2
	found, err = arch2.Content.Find(ctx, ai.ContentKey{ItemID: "item-B"}, false)
	if !found {
		t.Fatal("Item B not found in Content V2")
	}
	jsonStr, _ = arch2.Content.GetCurrentValue(ctx)
	var storedB map[string]any
	json.Unmarshal([]byte(jsonStr), &storedB)

	currentKeyB := arch2.Content.GetCurrentKey().Key
	cidB := currentKeyB.CentroidID
	distB := currentKeyB.Distance
	if currentKeyB.NextVersion == 2 {
		cidB = currentKeyB.NextCentroidID
		distB = currentKeyB.NextDistance
	}
	vecKeyB := ai.VectorKey{CentroidID: cidB, DistanceToCentroid: distB, ItemID: "item-B"}
	foundVec, err = arch2.Vectors.Find(ctx, vecKeyB, false)
	if !foundVec {
		t.Fatal("Item B should be in Vectors V2")
	}

	// Verify V1 stores are gone
	// We can check if the folders exist in the temp dir.
	// The folder names are constructed as: {tmpDir}/{domain}_{store}_{version}
	// e.g. lifecycle/vecs_1
	// But the store path is managed by sop.
	// We can try to open them and expect failure, or check file system.
	// Let's check file system.
	v1Stores := []string{"lifecycle/vecs_1", "lifecycle/centroids_1", "lifecycle/lku_1"}
	for _, storeName := range v1Stores {
		// The folder path is usually {tmpDir}/{storeName} in Standalone mode if not configured otherwise.
		// But createStore logs show: .../T/sop-ai-test-lifecycle-.../lifecycle/vecs_1
		storePath := filepath.Join(tmpDir, storeName)
		if _, err := os.Stat(storePath); !os.IsNotExist(err) {
			t.Errorf("Store %s should have been deleted after Optimize V1->V2", storeName)
		}
	}

	trans4.Commit(ctx)
}
