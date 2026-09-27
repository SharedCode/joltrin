package database_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestDatabase_Standalone_Simple(t *testing.T) {
	storagePath := t.TempDir()

	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{storagePath},
		CacheType:     sop.InMemory,
	})

	ctx := context.Background()
	tx, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	store, err := database.NewBtree[string, string](ctx, db, "test_store", tx, nil)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}

	if _, err := store.Add(ctx, "key1", "value1"); err != nil {
		t.Errorf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestDatabase_Standalone_Replication(t *testing.T) {
	// Setup folders for replication
	basePath := t.TempDir()

	folders := []string{
		basePath + "/node1",
		basePath + "/node2",
	}
	for _, f := range folders {
		os.MkdirAll(f, 0755)
	}

	ecConfig := map[string]sop.ErasureCodingConfig{
		"test_store": {
			DataShardsCount:             1,
			ParityShardsCount:           1,
			BaseFolderPathsAcrossDrives: folders,
		},
	}

	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		StoresFolders: []string{folders[0], folders[1]},
		ErasureConfig: ecConfig,
		CacheType:     sop.InMemory,
	})

	ctx := context.Background()
	tx, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Note: Store name must match EC config key or be handled by default?
	// Usually EC config is per store.
	store, err := database.NewBtree[string, string](ctx, db, "test_store", tx, nil)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}

	if _, err := store.Add(ctx, "key1", "value1"); err != nil {
		t.Errorf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestDatabase_Cassandra_Construction(t *testing.T) {
	_, err := database.ValidateCassandraOptions(sop.DatabaseOptions{
		Keyspace:      "test_keyspace",
		StoresFolders: []string{t.TempDir()},
	})

	if err != nil {
		t.Fatal("ValidateCassandraOptions returned error")
	}
}

func TestDatabase_Cassandra_Transaction_Simple(t *testing.T) {
	db, _ := database.ValidateCassandraOptions(sop.DatabaseOptions{
		Keyspace:      "test_keyspace",
		StoresFolders: []string{t.TempDir()},
	})

	ctx := context.Background()
	// Expect error connecting to Cassandra
	_, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err == nil {
		t.Fatal("Expected error connecting to Cassandra, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestDatabase_Cassandra_Transaction_Replication(t *testing.T) {
	// Setup folders
	basePath := t.TempDir()

	folders := []string{
		basePath + "/node1",
		basePath + "/node2",
	}
	for _, f := range folders {
		os.MkdirAll(f, 0755)
	}

	ecConfig := map[string]sop.ErasureCodingConfig{
		"test_store": {
			DataShardsCount:             1,
			ParityShardsCount:           1,
			BaseFolderPathsAcrossDrives: folders,
		},
	}

	db, _ := database.ValidateCassandraOptions(sop.DatabaseOptions{
		Keyspace:      "test_keyspace",
		ErasureConfig: ecConfig,
	})

	ctx := context.Background()
	_, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err == nil {
		t.Fatal("Expected error connecting to Cassandra, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestDatabase_Setup_GetOptions(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()

	opts := sop.DatabaseOptions{
		StoresFolders: []string{path},
		Type:          sop.Standalone,
	}

	// 1. Test Setup (First run)
	savedOpts, err := database.Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if len(savedOpts.StoresFolders) != 1 {
		t.Errorf("Expected 1 store folder, got %d", len(savedOpts.StoresFolders))
	}

	// 2. Test GetOptions
	loadedOpts, err := database.GetOptions(ctx, path)
	if err != nil {
		t.Fatalf("GetOptions failed: %v", err)
	}
	if loadedOpts.Type != opts.Type {
		t.Errorf("Expected Type %v, got %v", opts.Type, loadedOpts.Type)
	}

	// 3. Test Setup (Second run - should return error because it's already in memory)
	opts2 := sop.DatabaseOptions{
		StoresFolders: []string{path},
		Type:          sop.Clustered,
	}
	_, err = database.Setup(ctx, opts2)
	if err == nil {
		t.Error("Expected Setup (2nd run) to fail with 'already setup', got nil")
	}
}

func TestDatabase_Setup_ExistingOnDisk(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()

	// Manually write dboptions.json
	opts := sop.DatabaseOptions{
		StoresFolders: []string{path},
		Type:          sop.Clustered,
	}
	b, _ := json.Marshal(opts)
	if err := os.WriteFile(filepath.Join(path, "dboptions.json"), b, 0644); err != nil {
		t.Fatalf("Failed to write options file: %v", err)
	}

	// Call Setup - should detect existing file and return it
	// Note: We pass different options (Standalone) to verify it returns the one from disk (Clustered)
	inputOpts := sop.DatabaseOptions{
		StoresFolders: []string{path},
		Type:          sop.Standalone,
	}

	loadedOpts, err := database.Setup(ctx, inputOpts)
	if err != nil {
		t.Fatalf("Setup failed on existing file: %v", err)
	}
	if loadedOpts.Type != sop.Clustered {
		t.Errorf("Expected Setup to return existing options (Clustered), got %v", loadedOpts.Type)
	}
}

func TestDatabase_Setup_MultipleFolders(t *testing.T) {
	ctx := context.Background()
	path1 := t.TempDir()
	path2 := t.TempDir()

	opts := sop.DatabaseOptions{
		StoresFolders: []string{path1, path2},
		Type:          sop.Standalone,
	}

	_, err := database.Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify file exists in both folders
	for _, p := range []string{path1, path2} {
		if _, err := database.GetOptions(ctx, p); err != nil {
			t.Errorf("GetOptions failed for path %s: %v", p, err)
		}
	}
}

func TestDatabase_Setup_Errors(t *testing.T) {
	ctx := context.Background()

	// Empty StoresFolders
	opts := sop.DatabaseOptions{
		StoresFolders: []string{},
	}
	_, err := database.Setup(ctx, opts)
	if err == nil {
		t.Error("Expected error for empty StoresFolders, got nil")
	}
}

func TestDatabase_Setup_ManualDeletion(t *testing.T) {
	storagePath := t.TempDir()
	opts := sop.DatabaseOptions{
		StoresFolders: []string{storagePath},
	}

	ctx := context.Background()

	// 1. First Setup
	_, err := database.Setup(ctx, opts)
	if err != nil {
		t.Fatalf("First setup failed: %v", err)
	}

	// 2. Verify "Already Setup" error if we try again immediately
	_, err = database.Setup(ctx, opts)
	if err == nil {
		t.Fatalf("Expected error for second setup, got nil")
	}

	// 3. Manually delete the file (simulate external deletion)
	optionsFile := filepath.Join(storagePath, "dboptions.json")
	if err := os.Remove(optionsFile); err != nil {
		t.Fatalf("Failed to remove file: %v", err)
	}

	// 4. Try Setup again - Should succeed now (with fix)
	_, err = database.Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup after manual deletion failed: %v", err)
	}
}

func TestDatabase_Remove_Replicated(t *testing.T) {
	// Setup folders for replication
	basePath := t.TempDir()
	folder1 := filepath.Join(basePath, "node1")
	folder2 := filepath.Join(basePath, "node2")

	opts := sop.DatabaseOptions{
		StoresFolders: []string{folder1, folder2},
		CacheType:     sop.InMemory,
	}

	ctx := context.Background()

	// 1. Setup
	if _, err := database.Setup(ctx, opts); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify folders exist
	if _, err := os.Stat(folder1); os.IsNotExist(err) {
		t.Errorf("Folder1 should exist after setup")
	}
	if _, err := os.Stat(folder2); os.IsNotExist(err) {
		t.Errorf("Folder2 should exist after setup")
	}

	// 2. Remove
	// We pass folder1 as the "dbPath" (primary)
	if err := database.Remove(ctx, folder1); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// 3. Verify BOTH are gone
	if _, err := os.Stat(folder1); !os.IsNotExist(err) {
		t.Errorf("Folder1 should be deleted")
	}
	if _, err := os.Stat(folder2); !os.IsNotExist(err) {
		t.Errorf("Folder2 should be deleted")
	}
}
