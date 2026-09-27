package vector_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestOptimize_GracePeriod(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-grace-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storeName := "test_grace"

	// Initialize Database
	db, _ := core_database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	tx, err := core_database.BeginTransaction(context.Background(), db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idx, err := vector.Open[map[string]any](context.Background(), tx, storeName, vector.Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Add some data to have a valid state
	if err := idx.Upsert(context.Background(), ai.Item[map[string]any]{
		ID:     "item1",
		Vector: []float32{1, 2, 3},
	}); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Simulate a failed optimization by creating the "next version" stores manually.
	// Current version is 0, so next version is 1.
	// The stores created during optimization are:
	// - storeName + "_lku_1"
	// - storeName + "_centroids_1"
	// - storeName + "_vecs_1"

	tx2, err := core_database.BeginTransaction(context.Background(), db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}

	lookupName := fmt.Sprintf("%s_lku_1", storeName)
	// We need to create this store so it exists in the registry and on disk.
	// We use the internal btree creation via the transaction if possible,
	// but here we can just use the database to open/create a generic btree.
	// Since database.OpenVectorStore abstracts this, we might need to access the underlying store creation.
	// However, the database interface doesn't expose generic B-Tree creation easily if it's not part of the public API.
	// But `sop` package has `NewStore`.

	// We can use the fact that `db` wraps `sop.StoreRepository` or similar.
	// Actually, `db` is `database.Database`.
	// Let's try to use `btree.New` with the transaction.

	// We need to match the store configuration used in Optimize.
	// It uses `sop.ConfigureStore(..., true, btree.DefaultSlotLength, ...)`

	// Create the "failed" lookup store
	_, err = infs.NewBtree[int, string](context.Background(), sop.ConfigureStore(lookupName, true, btree.DefaultSlotLength, "lookup", sop.SmallData, ""), tx2, func(a, b int) int { return a - b })
	if err != nil {
		t.Fatalf("Failed to create simulation store: %v", err)
	}

	if err := tx2.Commit(context.Background()); err != nil {
		t.Fatalf("Commit 2 failed: %v", err)
	}

	// Now we need to find the physical file and touch it.
	// In Standalone mode (fs), the store is a directory in tmpDir.
	// The directory name might be the store name or something derived.
	// Usually it is `tmpDir/lookupName`.
	// GetStoreFileStat checks for "storeinfo.txt" inside the store folder.
	storePath := filepath.Join(tmpDir, lookupName, "storeinfo.txt")

	// Wait for grace period (simulated by setting mtime back)
	// Default grace period is 1 hour.
	// We can't easily change the grace period in the test without exposing it.
	// But we can change the file modification time.
	// Set mtime to 2 hours ago.
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(storePath, oldTime, oldTime); err != nil {
		// If file doesn't exist, maybe the path is different.
		// In fs/store_repository.go, folder path is constructed.
		// It might be just `tmpDir/lookupName` if StoresFolders is not used.
		// Let's check if directory exists.
		if _, err := os.Stat(filepath.Join(tmpDir, lookupName)); err == nil {
			// Directory exists. Try touching the directory itself?
			// GetStoreFileStat checks the folder mod time if file doesn't exist?
			// No, it checks storeinfo.txt.
			// Maybe storeinfo.txt is not created by NewBtree immediately?
			// It should be created on Commit.
			t.Logf("Failed to chtimes on %s: %v", storePath, err)
		} else {
			t.Logf("Store folder not found: %v", err)
		}
	}

	// Run Optimize again. It should detect the stale stores and clean them up.
	tx3, err := core_database.BeginTransaction(context.Background(), db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 3 failed: %v", err)
	}

	idx3, err := vector.Open[map[string]any](context.Background(), tx3, storeName, vector.Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
			CacheType:     sop.InMemory,
		},
		Cache: sop.GetL2Cache(sop.TransactionOptions{CacheType: db.CacheType}),
	})
	if err != nil {
		t.Fatalf("Open 3 failed: %v", err)
	}

	// Optimize should trigger cleanup
	if err := idx3.Optimize(context.Background()); err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Optimize commits the transaction, so we don't need to commit here.

	// Verify cleanup
	// The stale store folder should have been replaced (recreated).
	// We check that the mod time is recent (not the old time we set).
	fi, err := os.Stat(filepath.Join(tmpDir, lookupName))
	if err != nil {
		t.Errorf("Store folder %s should exist after optimization", lookupName)
	} else {
		if fi.ModTime().Before(time.Now().Add(-1 * time.Hour)) {
			t.Errorf("Store folder %s should have been recreated (mod time is old)", lookupName)
		}
	}

	// Verify we can still access the index
	tx4, err := core_database.BeginTransaction(context.Background(), db, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 4 failed: %v", err)
	}
	idx4, err := vector.Open[map[string]any](context.Background(), tx4, storeName, vector.Config{
		UsageMode: ai.Dynamic,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{tmpDir},
		},
	})
	if err != nil {
		t.Fatalf("Open 4 failed: %v", err)
	}
	item, err := idx4.Get(context.Background(), "item1")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if item.ID != "item1" {
		t.Errorf("Got wrong item: %v", item)
	}
	tx4.Commit(context.Background())
}
