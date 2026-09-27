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

func TestVectorStoreStructure(t *testing.T) {
	// 1. Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-structure-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	t.Log("--- Ingesting Data ---")
	trans1, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 1 failed: %v", err)
	}

	store, err := db.OpenVectorStore(ctx, "structure_test", trans1, vector.Config{
		UsageMode:   ai.Dynamic,
		ContentSize: sop.MediumData,
	})
	if err != nil {
		t.Fatalf("OpenVectorStore failed: %v", err)
	}

	// Generate 100 items in 5 distinct clusters
	// Cluster centers: (0,0), (10,10), (20,20), (30,30), (40,40)
	const numClusters = 5
	const itemsPerCluster = 20
	var items []ai.Item[map[string]any]

	for c := 0; c < numClusters; c++ {
		centerX := float32(c * 10)
		centerY := float32(c * 10)
		for i := 0; i < itemsPerCluster; i++ {
			id := fmt.Sprintf("item-%d-%d", c, i)
			// Small random noise around center
			vec := []float32{centerX + 0.1*float32(i), centerY + 0.1*float32(i)}
			items = append(items, ai.Item[map[string]any]{
				ID:     id,
				Vector: vec,
				Payload: map[string]any{
					"cluster": c,
					"index":   i,
				},
			})
		}
	}

	if err := store.UpsertBatch(ctx, items); err != nil {
		t.Fatalf("UpsertBatch failed: %v", err)
	}

	// 2. Optimize (Force structure creation)
	t.Log("--- Optimizing ---")
	if err := store.Optimize(ctx); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Optimize commits the transaction, so we don't need to commit trans1 here.

	// 3. Verify Structure
	t.Log("--- Verifying Internal Structure ---")
	trans2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}

	store2, err := db.OpenVectorStore(ctx, "structure_test", trans2, vector.Config{
		UsageMode:   ai.Dynamic,
		ContentSize: sop.MediumData,
	})
	if err != nil {
		t.Fatalf("OpenVectorStore 2 failed: %v", err)
	}

	// A. Verify Centroids
	centroidsTree, err := store2.Centroids(ctx)
	if err != nil {
		t.Fatalf("Failed to get Centroids tree: %v", err)
	}

	centroidCount := centroidsTree.Count()
	t.Logf("Centroids Count: %d", centroidCount)

	// We expect roughly 5 centroids.
	// Since we used K-Means (or similar) inside Optimize/Upsert, it might vary slightly depending on initialization,
	// but with such distinct clusters, it should be very close to 5.
	// Note: UpsertBatch with auto-init uses sqrt(N) = sqrt(100) = 10 centroids if not careful,
	// but Optimize re-calculates.
	// Let's just assert it's > 0 and <= 20.
	if centroidCount < 2 || centroidCount > 20 {
		t.Errorf("Unexpected centroid count: %d (expected around 5-10)", centroidCount)
	}

	// Collect valid Centroid IDs
	validCentroids := make(map[int]bool)
	if ok, _ := centroidsTree.First(ctx); ok {
		for {
			k := centroidsTree.GetCurrentKey()
			validCentroids[k.Key] = true
			if ok, _ := centroidsTree.Next(ctx); !ok {
				break
			}
		}
	}

	// B. Verify Vectors
	vectorsTree, err := store2.Vectors(ctx)
	if err != nil {
		t.Fatalf("Failed to get Vectors tree: %v", err)
	}

	vectorCount := vectorsTree.Count()
	t.Logf("Vectors Count: %d", vectorCount)
	if vectorCount != int64(len(items)) {
		t.Errorf("Expected %d vectors, got %d", len(items), vectorCount)
	}

	// Check Vector Keys
	if ok, _ := vectorsTree.First(ctx); ok {
		for {
			item, _ := vectorsTree.GetCurrentItem(ctx)
			key := item.Key

			// 1. Check CentroidID validity
			if !validCentroids[key.CentroidID] {
				t.Errorf("Vector %s assigned to invalid centroid %d", key.ItemID, key.CentroidID)
			}

			// 2. Check Distance (should be >= 0)
			if key.DistanceToCentroid < 0 {
				t.Errorf("Vector %s has negative distance %f", key.ItemID, key.DistanceToCentroid)
			}

			if ok, _ := vectorsTree.Next(ctx); !ok {
				break
			}
		}
	}

	// C. Verify Content
	contentTree, err := store2.Content(ctx)
	if err != nil {
		t.Fatalf("Failed to get Content tree: %v", err)
	}

	contentCount := contentTree.Count()
	t.Logf("Content Count: %d", contentCount)
	if contentCount != int64(len(items)) {
		t.Errorf("Expected %d content items, got %d", len(items), contentCount)
	}

	// Check a sample content
	item, err := store2.Get(ctx, "item-0-0")
	if err != nil {
		t.Errorf("Failed to Get item-0-0: %v", err)
	} else {
		// Check Payload
		if item.Payload["cluster"] != float64(0) && item.Payload["cluster"] != 0 {
			t.Errorf("Payload mismatch. Expected cluster 0, got %v", item.Payload["cluster"])
		}

		// Check Linkage (CentroidID in Content should match a valid centroid)
		// store.Get() handles the logical resolution of CentroidID (handling Lazy Migration internally),
		// so we just check if the returned CentroidID is valid.
		if !validCentroids[item.CentroidID] {
			t.Errorf("Content points to invalid centroid %d", item.CentroidID)
		}
	}

	trans2.Commit(ctx)
}
