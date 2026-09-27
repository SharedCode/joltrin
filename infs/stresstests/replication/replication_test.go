//go:build stress
// +build stress

package replication

import (
	"context"
	"fmt"
	log "log/slog"
	"math/rand"
	"os"
	"path/filepath"

	"testing"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Redis config.
var redisConfig = redis.Options{
	Address:  "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
}

var transOptions sop.TransactionOptions

func getDataPath() string {
	s := os.Getenv("datapath")
	if s == "" {
		s = filepath.Join(os.TempDir(), "sop_data", "replication")
	}
	return s
}

var dataPath string = getDataPath()

func init() {
	initialize()
}

func initialize() {
	l := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{
		Level: log.LevelInfo,
	}))
	log.SetDefault(l) // configures log package to print with LevelInfo
	if _, err := redis.OpenConnection(redisConfig); err != nil {
		panic(err)
	}
	initErasureCoding()
	transOptions = sop.TransactionOptions{
		Mode:                 sop.ForWriting,
		MaxTime:              -1,
		RegistryHashModValue: fs.MinimumModValue,
		StoresFolders:        storesFolders,
		CacheType:            sop.Redis,
	}

	// For stress tests only
	sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
}

func TestReplicationFlow(t *testing.T) {
	tableName := "repltable2"
	_ = infs.RemoveBtree(context.Background(), tableName, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)
	setupBtreeWithOneItem(tableName, rand.Intn(50)+1, t)

	writeData(tableName, rand.Intn(50)+1, "foobar", t)
	fmt.Printf("No error here.\n")

	// Induce write failure on current active registry segment to trigger failover.
	makeActiveRegistryReadOnly(t, tableName)
	fmt.Printf("Error here! (simulated via permissions)\n")
	writeData(tableName, rand.Intn(50)+1, "bar bar", t)
	fmt.Printf("End of error\n")

	// Restore permissions so reinstate can proceed.
	restoreRegistryPermissions(t, tableName)
	// Also restore perms for other known tables that other stress suites may have toggled,
	// to avoid cross-test interference during reinstate (which scans all tables).
	restoreRegistryPermissions(t, "repltable")
	restoreRegistryPermissions(t, "repltable3")
	// Reinstate drive should succeed to reinstate the (failed) drives back to replication.
	reinstateDrive(t)

	fmt.Printf("Failed over and read foll. item Values: %v\n", readData(tableName, t))

	writeData(tableName, rand.Intn(50)+1, "hey hey", t)
	fmt.Printf("No error here.\n")

	fmt.Printf("Failed over and read foll. item Values: %v\n", readData(tableName, t))

	// Fail on reading.
	// cache := sop.NewCacheClient()
	// ctx := context.Background()
	// if err := cache.Clear(ctx); err != nil {
	// 	log.Warn(fmt.Sprintf("cache.Clear failed, details: %v", err))
	// }

	// Optionally, simulate read issue by making registry temporarily unreadable, then restore.
	makeActiveRegistryUnreadable(t, tableName)
	_ = readData(tableName, t) // may return empty on error paths
	restoreRegistryPermissions(t, tableName)

}

func TestReplicationFlow2(t *testing.T) {
	tableName := "repltable3"
	_ = infs.RemoveBtree(context.Background(), tableName, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)
	setupBtreeWithOneItem(tableName, rand.Intn(50)+1, t)

	writeData(tableName, rand.Intn(50)+1, "foobar", t)
	fmt.Printf("No error here.\n")

	// Induce write error (no failover classification in some paths).
	makeActiveRegistryReadOnly(t, tableName)
	fmt.Printf("Error here! (simulated via permissions)\n")
	writeData(tableName, rand.Intn(50)+1, "bar bar", t)
	fmt.Printf("End of error\n")
	restoreRegistryPermissions(t, tableName)

}

func setupBtreeWithOneItem(btreeName string, itemID int, t *testing.T) {
	// Take from global EC config the data paths & EC config details.
	ctx := context.Background()
	// Setup a good B-tree succeeding with adding one entry & committed.
	trans, err := infs.NewTransactionWithReplication(ctx, transOptions)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if err = trans.Begin(ctx); err != nil {
		t.Error(err)
		t.FailNow()
	}
	so := sop.StoreOptions{
		Name:                     btreeName,
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}
	b3, err := infs.NewBtreeWithReplication[int, string](ctx, so, trans, nil)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	_, err = b3.Add(ctx, itemID, "hello world")
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("expected no error but got: %v", err)
		t.FailNow()
	}
	fmt.Printf("GlobalReplication ActiveFolderToggler at End: %v\n", fs.GlobalReplicationDetails.ActiveFolderToggler)
}

func writeData(btreeName string, itemID int, msg string, t *testing.T) {
	ctx := context.Background()
	trans, err := infs.NewTransactionWithReplication(ctx, transOptions)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if err = trans.Begin(ctx); err != nil {
		t.Error(err)
		t.FailNow()
	}

	b3, err := infs.OpenBtreeWithReplication[int, string](ctx, btreeName, trans, nil)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	_, err = b3.Add(ctx, itemID, msg)
	if err != nil {
		t.Errorf("expected no error but got: %v", err)
		t.FailNow()
	}
	if err := trans.Commit(ctx); err != nil {
		fmt.Printf("got error: %v\n", err)
	}
	fmt.Printf("GlobalReplication ActiveFolderToggler at End: %v\n", fs.GlobalReplicationDetails.ActiveFolderToggler)
}

func readData(btreeName string, t *testing.T) []sop.KeyValuePair[int, string] {
	ctx := context.Background()
	trans, err := infs.NewTransactionWithReplication(ctx, transOptions)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if err = trans.Begin(ctx); err != nil {
		t.Error(err)
		t.FailNow()
	}

	b3, err := infs.OpenBtreeWithReplication[int, string](ctx, btreeName, trans, nil)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	result := make([]sop.KeyValuePair[int, string], 0)
	b3.First(ctx)
	for {
		itm, err := b3.GetCurrentItem(ctx)
		if err != nil {
			return result
		}
		o := sop.KeyValuePair[int, string]{
			Key:   itm.Key,
			Value: *itm.Value,
		}
		result = append(result, o)
		if ok, _ := b3.Next(ctx); !ok {
			break
		}
	}
	trans.Commit(ctx)
	return result
}

func TestDirectIOCloseFileFailure(t *testing.T) {
}

func TestStoreRepositoryCreateStoreFailure(t *testing.T) {
	ctx := context.Background()
	base := activeBaseFolder()
	// Make base folder read-only to prevent mkdir
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatalf("Failed to chmod base folder: %v", err)
	}
	defer os.Chmod(base, 0o755)

	trans, err := infs.NewTransactionWithReplication(ctx, transOptions)
	if err != nil {
		t.Fatalf("NewTransactionWithReplication failed: %v", err)
	}
	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	so := sop.StoreOptions{
		Name:                     "fail_create_store",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}
	_ = infs.RemoveBtree(ctx, so.Name, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)
	_, err = infs.NewBtreeWithReplication[int, string](ctx, so, trans, nil)
	if err == nil {
		t.Error("Expected error when creating store in read-only directory, got nil")
	} else {
		t.Logf("Got expected error: %v", err)
	}
	_ = trans.Rollback(ctx)
}

func TestStoreRepositoryRemoveStoreFailure(t *testing.T) {
	ctx := context.Background()
	name := "fail_remove_store"
	_ = infs.RemoveBtree(ctx, name, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)

	// 1. Create store successfully
	setupBtreeWithOneItem(name, 1, t)

	// 2. Make base folder read-only so we can't remove the store directory
	base := activeBaseFolder()
	if err := os.Chmod(base, 0o555); err != nil {
		t.Fatalf("Failed to chmod base folder: %v", err)
	}
	defer os.Chmod(base, 0o755)

	// 3. Try to remove using StoreRepository directly
	cache := sop.GetL2Cache(transOptions)
	rt, err := fs.NewReplicationTracker(ctx, storesFolders, true, cache)
	if err != nil {
		t.Fatalf("NewReplicationTracker failed: %v", err)
	}
	sr, err := fs.NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository failed: %v", err)
	}

	err = sr.Remove(ctx, name)
	if err == nil {
		t.Error("Expected error when removing store with read-only parent, got nil")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestStoreRepositoryWriteFileFailure(t *testing.T) {
	ctx := context.Background()
	name := "fail_write_file"
	_ = infs.RemoveBtree(ctx, name, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)
	setupBtreeWithOneItem(name, 1, t)

	// Use the file that exists in the active folder, or fallback to the other if active is empty (which would be a bug in test setup/understanding)
	base := activeBaseFolder()
	storeDir := filepath.Join(base, name)
	infoFile := filepath.Join(storeDir, "storeinfo.txt")

	// Check if file exists
	if _, err := os.Stat(infoFile); os.IsNotExist(err) {
		// Try other folder
		if base == storesFolders[0] {
			base = storesFolders[1]
		} else {
			base = storesFolders[0]
		}
		storeDir = filepath.Join(base, name)
		infoFile = filepath.Join(storeDir, "storeinfo.txt")
	}

	// Use StoreRepository directly
	cache := sop.GetL2Cache(transOptions)
	rt, err := fs.NewReplicationTracker(ctx, storesFolders, true, cache)
	if err != nil {
		t.Fatalf("NewReplicationTracker failed: %v", err)
	}
	sr, err := fs.NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository failed: %v", err)
	}

	// Get store info to ensure it is in cache
	stores, err := sr.Get(ctx, name)
	if err != nil || len(stores) == 0 {
		t.Fatalf("Failed to get store: %v", err)
	}

	// Rename file and make dir read-only in BOTH folders so write (create) fails
	for _, base := range storesFolders {
		storeDir := filepath.Join(base, name)
		infoFile := filepath.Join(storeDir, "storeinfo.txt")
		if _, err := os.Stat(infoFile); err == nil {
			if err := os.Rename(infoFile, filepath.Join(storeDir, "storeinfo.bak")); err != nil {
				t.Fatalf("Failed to rename info file: %v", err)
			}
		}
		if err := os.Chmod(storeDir, 0o555); err != nil {
			t.Fatalf("Failed to chmod store dir: %v", err)
		}
		defer os.Chmod(storeDir, 0o755)
	}

	// Update
	stores[0].CountDelta = 1
	_, err = sr.Update(ctx, stores)

	if err == nil {
		t.Error("Expected error when writing to read-only store directory, got nil")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestStoreRepositoryReadFileFailure(t *testing.T) {
	ctx := context.Background()
	name := "fail_read_file"
	_ = infs.RemoveBtree(ctx, name, storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)
	setupBtreeWithOneItem(name, 1, t)

	// Delete store info file in BOTH folders
	for _, base := range storesFolders {
		infoFile := filepath.Join(base, name, "storeinfo.txt")
		if err := os.Remove(infoFile); err != nil && !os.IsNotExist(err) {
			t.Fatalf("Failed to remove info file: %v", err)
		}
		if _, err := os.Stat(infoFile); !os.IsNotExist(err) {
			t.Fatalf("File %s still exists after remove!", infoFile)
		}
	}

	// Clear cache to force read from disk.
	cache := sop.GetL2Cache(transOptions)
	cache.Delete(ctx, []string{fmt.Sprintf("%s:%s", storesFolders[0], name)})
	cache.Delete(ctx, []string{fmt.Sprintf("%s:%s", storesFolders[1], name)})

	// Try to OpenBtree
	trans, _ := infs.NewTransactionWithReplication(ctx, transOptions)
	trans.Begin(ctx)
	_, err := infs.OpenBtreeWithReplication[int, string](ctx, name, trans, nil)

	if err == nil {
		t.Error("Expected error when reading missing store info file, got nil")
		// Debug why it succeeded
		rt, _ := fs.NewReplicationTracker(ctx, storesFolders, true, cache)
		sr, _ := fs.NewStoreRepository(ctx, rt, nil, cache, 0)
		stores, _ := sr.Get(ctx, name)
		t.Logf("Store found: %+v", stores)
	} else {
		t.Logf("Got expected error: %v", err)
	}
	trans.Rollback(ctx)
}

// Helpers to simulate IO failures by toggling permissions on registry segment files.
func activeBaseFolder() string {
	if fs.GlobalReplicationDetails != nil && fs.GlobalReplicationDetails.ActiveFolderToggler {
		return storesFolders[1]
	}
	return storesFolders[0]
}

func registrySegmentPath(base, table string) string {
	return filepath.Join(base, table, fmt.Sprintf("%s-1.reg", table))
}

func ensureTableDir(t *testing.T, base, table string) string {
	t.Helper()
	dir := filepath.Join(base, table)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func makeActiveRegistryReadOnly(t *testing.T, table string) {
	t.Helper()
	base := activeBaseFolder()
	ensureTableDir(t, base, table)
	seg := registrySegmentPath(base, table)
	// Best-effort chmod; if file doesn't exist yet, directory perms will block creation.
	_ = os.Chmod(seg, 0o444)
	_ = os.Chmod(filepath.Dir(seg), 0o555)
}

func makeActiveRegistryUnreadable(t *testing.T, table string) {
	t.Helper()
	base := activeBaseFolder()
	ensureTableDir(t, base, table)
	seg := registrySegmentPath(base, table)
	_ = os.Chmod(seg, 0o000)
}

func restoreRegistryPermissions(t *testing.T, table string) {
	t.Helper()
	for _, b := range storesFolders {
		dir := ensureTableDir(t, b, table)
		_ = os.Chmod(dir, 0o755)
		seg := registrySegmentPath(b, table)
		_ = os.Chmod(seg, 0o644)
	}
}

func initErasureCoding() {
	// Erasure Coding configuration lookup table (map).
	ec := make(map[string]sop.ErasureCodingConfig)

	// Erasure Coding config for "barstoreec" table uses three base folder paths that mimicks three disks.
	// Two data shards and one parity shard.
	ec[""] = sop.ErasureCodingConfig{
		DataShardsCount:   2,
		ParityShardsCount: 1,
		BaseFolderPathsAcrossDrives: []string{
			fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk10", dataPath, os.PathSeparator),
		},
		RepairCorruptedShards: true,
	}
	fs.SetGlobalErasureConfig(ec)
}

var storesFolders = []string{
	fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator),
	fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator),
}

// Test to issue Reinstate of failed drives. But only works if the replcation flag is true and the replication status
// FailedToReplicate = true.
func reinstateDrive(t *testing.T) {
	ctx := context.Background()
	if err := infs.ReinstateFailedDrives(ctx, storesFolders, sop.Redis); err != nil {
		t.Error(err)
		t.FailNow()
	}
	if fs.GlobalReplicationDetails.ActiveFolderToggler {
		fmt.Printf("Active Folder is: %s\n", storesFolders[0])
		return
	}
	fmt.Printf("Active Folder is: %s\n", storesFolders[1])
}
