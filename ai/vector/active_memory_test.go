package vector_test

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
)

func TestActiveMemory_RollingAverage(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "sop-ai-test-rolling-*")
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(sop.DatabaseOptions{StoresFolders: []string{tmpDir}})
	ctx := context.Background()

	cfg := vector.Config{UsageMode: ai.DynamicWithVectorCountTracking}
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ := db.OpenVectorStore(ctx, "rolling_avg_test", tx, cfg)

	cID, _ := idx.AddCentroid(ctx, []float32{1.0, 1.0})
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ = db.OpenVectorStore(ctx, "rolling_avg_test", tx, cfg)

	_ = idx.Upsert(ctx, ai.Item[map[string]any]{
		ID:         "item1",
		CentroidID: cID,
		Vector:     []float32{1.0, 1.0},
		Payload:    map[string]any{"v": 1},
	})
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ = db.OpenVectorStore(ctx, "rolling_avg_test", tx, cfg)

	_ = idx.Upsert(ctx, ai.Item[map[string]any]{
		ID:         "item2",
		CentroidID: cID,
		Vector:     []float32{3.0, 3.0},
		Payload:    map[string]any{"v": 2},
	})
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForReading)
	idx, _ = db.OpenVectorStore(ctx, "rolling_avg_test", tx, cfg)

	cStore, _ := idx.Centroids(ctx)
	cStore.Find(ctx, cID, false)
	c, _ := cStore.GetCurrentValue(ctx)

	if c.VectorCount != 2 {
		t.Errorf("Expected VectorCount 2, got %d", c.VectorCount)
	}

	const epsilon = 0.001
	if math.Abs(float64(c.Vector[0]-2.0)) > epsilon || math.Abs(float64(c.Vector[1]-2.0)) > epsilon {
		t.Errorf("Expected Vector to be roughly {2.0, 2.0}, got %v", c.Vector)
	}
}

func TestActiveMemory_SplitCentroid(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "sop-ai-test-split-*")
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(sop.DatabaseOptions{StoresFolders: []string{tmpDir}})
	ctx := context.Background()

	cfg := vector.Config{UsageMode: ai.DynamicWithVectorCountTracking}
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ := db.OpenVectorStore(ctx, "split_test", tx, cfg)

	cID, _ := idx.AddCentroid(ctx, []float32{5.0, 5.0})
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ = db.OpenVectorStore(ctx, "split_test", tx, cfg)

	groupA := [][]float32{
		{1.0, 1.0}, {1.1, 0.9}, {0.9, 1.1}, {1.0, 1.2},
	}
	groupB := [][]float32{
		{9.0, 9.0}, {9.1, 8.9}, {8.9, 9.1}, {9.0, 9.2},
	}

	for i, vec := range groupA {
		_ = idx.Upsert(ctx, ai.Item[map[string]any]{
			ID:         "A" + string(rune(i+'0')),
			CentroidID: cID,
			Vector:     vec,
			Payload:    map[string]any{"v": "A"},
		})
	}
	for i, vec := range groupB {
		_ = idx.Upsert(ctx, ai.Item[map[string]any]{
			ID:         "B" + string(rune(i+'0')),
			CentroidID: cID,
			Vector:     vec,
			Payload:    map[string]any{"v": "B"},
		})
	}
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ = db.OpenVectorStore(ctx, "split_test", tx, cfg)

	err := idx.SplitCentroid(ctx, cID)
	if err != nil {
		t.Fatalf("SplitCentroid failed: %v", err)
	}
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForReading)
	idx, _ = db.OpenVectorStore(ctx, "split_test", tx, cfg)

	cStore, _ := idx.Centroids(ctx)

	count := 0
	ok, _ := cStore.First(ctx)
	for ok {
		count++
		ok, _ = cStore.Next(ctx)
	}

	if count < 2 {
		t.Errorf("Expected at least 2 centroids after split, got %d", count)
	}
}
