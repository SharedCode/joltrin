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

func TestConfigurationMethods(t *testing.T) {
	// Test Config struct
	cfg := vector.Config{
		UsageMode: ai.Dynamic,
	}
	if cfg.UsageMode != ai.Dynamic {
		t.Errorf("Expected UsageMode to be Dynamic, got %v", cfg.UsageMode)
	}
}

func TestArchitectureDirectMethods(t *testing.T) {
	// These methods (Add, Search) in Architecture struct seem to be helpers or demo code
	// but since they are exported, we should test them or at least cover them.

	tmpDir, err := os.MkdirTemp("", "sop-ai-test-arch-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// We need to manually setup the environment to call Architecture methods directly
	// This is a bit involved because Architecture expects a transaction.
	// We can reuse the Database helper to get a transaction.

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	ctx := context.Background()
	trans, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer trans.Rollback(ctx)

	arch, err := vector.OpenDomainStore(ctx, trans, "test_arch", 1, vector.Config{ContentSize: sop.MediumData, EnableIngestionBuffer: true})
	if err != nil {
		t.Fatalf("OpenDomainStore failed: %v", err)
	}

	// Test Add
	if err := arch.Add(ctx, "arch-item-1", []float32{1, 2}, `{"foo":"bar"}`); err != nil {
		t.Fatalf("Architecture.Add failed: %v", err)
	}

	// Test Search
	// Search expects centroids to be populated?
	// The Add method hardcodes centroidID := 1.
	// The Search method hardcodes targetCentroid := 1.
	// So it should work if we just Add then Search.

	results, err := arch.Search(ctx, []float32{1, 2}, 1)
	if err != nil {
		t.Fatalf("Architecture.Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
	if results[0] != `{"foo":"bar"}` {
		t.Errorf("Expected data, got %s", results[0])
	}
}
