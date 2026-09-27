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
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestOptimizeWithTempVectors(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-temp-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize Database
	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	ctx := context.Background()

	// 1. Manually populate TempVectors
	// We need to open a transaction and get the architecture
	trans, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// Open Store (Version 0)
	arch, err := vector.OpenDomainStore(ctx, trans, "test_temp", 0, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("Failed to open domain store: %v", err)
	}

	// Add items to TempVectors
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("item-%d", i)
		vec := []float32{float32(i), float32(i)}
		if _, err := arch.TempVectors.Add(ctx, id, vec); err != nil {
			t.Fatalf("Failed to add to TempVectors: %v", err)
		}

		payload := map[string]any{"val": i}
		// CentroidID/Distance are 0/0 initially

		data, _ := json.Marshal(payload)
		if _, err := arch.Content.Add(ctx, ai.ContentKey{ItemID: id}, string(data)); err != nil {
			t.Fatalf("Failed to add to Content: %v", err)
		}
	}

	if err := trans.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit setup: %v", err)
	}

	// 2. Run Optimize
	trans2, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to create transaction 2: %v", err)
	}

	idx, err := db.OpenVectorStore(ctx, "test_temp", trans2, vector.Config{
		UsageMode:             ai.Dynamic,
		ContentSize:           sop.MediumData,
		EnableIngestionBuffer: true,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := idx.Optimize(ctx); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Transaction is committed by Optimize, so we don't need to commit here.

	// Verify TempVectors folder is gone immediately after Optimize commit
	tempVectorsPath := filepath.Join(tmpDir, "test_temp_tmp_vecs")
	if _, err := os.Stat(tempVectorsPath); !os.IsNotExist(err) {
		t.Errorf("TempVectors folder still exists at %s after Optimize", tempVectorsPath)
	}

	// 3. Verify Vectors are populated
	trans3, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to create transaction 3: %v", err)
	}

	idx3, err := db.OpenVectorStore(ctx, "test_temp", trans3, vector.Config{
		UsageMode:   ai.Dynamic,
		ContentSize: sop.MediumData,
	})
	if err != nil {
		t.Fatalf("Open 3 failed: %v", err)
	}

	// We can use Query or Get
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("item-%d", i)
		item, err := idx3.Get(ctx, id)
		if err != nil {
			t.Errorf("Get(%s) failed: %v", id, err)
		} else {
			if item.CentroidID == 0 {
				t.Errorf("Item %s has CentroidID 0 (not assigned)", id)
			}
			if len(item.Vector) == 0 {
				t.Errorf("Item %s has empty vector", id)
			}
		}
	}
	trans3.Commit(ctx)
}
