package vector

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
)

// TestPartialOptimizationState verifies that the system behaves correctly
// when an item is in a "migrating" state (NextVersion set).
func TestPartialOptimizationState(t *testing.T) {
	// Setup Temp Dir
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-partial-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	c := cache.NewL2InMemoryCache()
	t.Logf("Cache created: %T", c)

	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	trans.Begin(ctx)

	// 1. Setup Store
	config := Config{
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		UsageMode: ai.DynamicWithVectorCountTracking,
	}
	store, err := Open[string](ctx, trans, "partial_test", config)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// 2. Add an item (Version 0)
	item := ai.Item[string]{
		ID:         "item1",
		Vector:     []float32{1, 0, 0},
		Payload:    "payload1",
		CentroidID: 1,
	}
	if err := store.Upsert(ctx, item); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	// 3. Manually simulate "Broken Partial State" (Current Bug)
	// The item has Version=1 (Future), but ActiveVersion is 0.
	// This simulates what happens if Optimize overwrites Version before switching ActiveVersion.
	di := store.(*domainIndex[string])
	arch, _ := di.getArchitecture(ctx, 0)

	// Update Key to Version 1
	found, _ := arch.Content.Find(ctx, ai.ContentKey{ItemID: "item1"}, false)
	if !found {
		t.Fatal("Item not found in Content")
	}
	currentKey := arch.Content.GetCurrentKey().Key
	currentKey.Version = 1 // Future version
	val, _ := arch.Content.GetCurrentValue(ctx)
	arch.Content.RemoveCurrentItem(ctx)
	arch.Content.Add(ctx, currentKey, val)

	// 4. Verify Get fails (or behaves unexpectedly)
	// Because ActiveVersion is 0, but Item says Version 1.
	got, err := store.Get(ctx, "item1")
	if err != nil {
		t.Logf("Get failed as expected in broken state: %v", err)
	} else {
		// If it succeeds, it's because the vector is still in Version 0 store and CentroidID matches.
		// But logically, the item claims to be Version 1.
		t.Logf("Get succeeded (unexpectedly?) in broken state: %v", got)
	}

	// 5. Manually simulate "Correct Partial State" (Proposed Fix)
	// Reset item to Version 0, but set NextVersion=1.
	found, _ = arch.Content.Find(ctx, ai.ContentKey{ItemID: "item1"}, false)
	currentKey = arch.Content.GetCurrentKey().Key
	currentKey.Version = 0
	currentKey.NextVersion = 1
	currentKey.NextCentroidID = 2 // Assume it moves to Centroid 2
	currentKey.NextDistance = 0.5
	arch.Content.RemoveCurrentItem(ctx)
	arch.Content.Add(ctx, currentKey, val)

	// 6. Verify Get works (ActiveVersion = 0)
	// It should ignore NextVersion and use Version 0 data.
	got, err = store.Get(ctx, "item1")
	if err != nil {
		t.Fatalf("Get failed in correct partial state (Active=0): %v", err)
	}
	if got.CentroidID != 1 {
		t.Errorf("Expected CentroidID 1, got %d", got.CentroidID)
	}

	// 7. Simulate Switch to ActiveVersion = 1
	// Now ActiveVersion becomes 1.
	// We need to mock getActiveVersion or just update sysStore.
	// Note: sysStore is empty initially, so we use Add.
	m := Metadata{
		ActiveVersion: 1,
		Embedder: EmbedderInfo{
			Provider:   "mock",
			Model:      "mock-model",
			Dimensions: 5,
		},
	}
	b, _ := json.Marshal(m)
	foundSys, _ := di.sysStore.Find(ctx, di.name, false)
	if foundSys {
		_, err := di.sysStore.UpdateCurrentItem(ctx, di.name, string(b))
		if err != nil {
			t.Fatalf("sysStore.UpdateCurrentItem failed: %v", err)
		}
	} else {
		if _, err := di.sysStore.Add(ctx, di.name, string(b)); err != nil {
			t.Fatalf("sysStore.Add failed: %v", err)
		}
	}
	// Clear cache to force reload
	// di.archCache = nil // Commented out: We inject manually below

	// Create Version 1 Vectors store
	// We need to pass the transaction and comparer.
	// We can use the helper from store.go but it's private.
	// We'll use infs.NewBtree directly.
	// Use sop.ConfigureStore to match OpenDomainStore configuration (IsValueDataInNodeSegment=true for SmallData)
	v1Vectors, err := infs.NewBtree[ai.VectorKey, []float32](ctx, sop.ConfigureStore("partial_test/vecs_1", true, 10000, "Vectors", sop.SmallData, ""), trans, compositeKeyComparer)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}

	// Add vector
	if _, err := v1Vectors.Add(ctx, ai.VectorKey{CentroidID: 2, DistanceToCentroid: 0.5, ItemID: "item1"}, []float32{1, 0, 0}); err != nil {
		t.Fatalf("v1Vectors.Add failed: %v", err)
	}

	// Inject into archCache to avoid "store already in transaction" error
	// We need to create the full architecture or at least what Get needs.
	// Get needs Content (shared) and Vectors (v1).
	// We can reuse the existing Content store from arch 0.
	di.archCache = make(map[int64]*Architecture)
	di.archCache[1] = &Architecture{
		Content: arch.Content,
		Vectors: v1Vectors,
		// Centroids and Lookup are not used in this specific Get path, but good to have if needed.
		// For this test, Get only needs Content and Vectors.
		Version: 1,
	}

	// Now Get should work and return Centroid 2
	got, err = store.Get(ctx, "item1")
	if err != nil {
		t.Fatalf("Get failed in correct partial state (Active=1): %v", err)
	}
	if got.CentroidID != 2 {
		t.Errorf("Expected CentroidID 2 (from Next), got %d", got.CentroidID)
	}

	trans.Commit(ctx)
}
