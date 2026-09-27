//go:build stress
// +build stress

package replication

import (
	"context"
	"errors"
	"fmt"
	log "log/slog"
	"os"
	"path/filepath"

	"testing"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

var dataPath = filepath.Join(os.TempDir(), "sop_data", "replication_misc")

// Redis config.
var redisConfig = redis.Options{
	Address:  "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
}

func init() {
	l := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{
		Level: log.LevelDebug,
	}))
	log.SetDefault(l) // configures log package to print with LevelInfo

	if _, err := redis.OpenConnection(redisConfig); err != nil {
		panic(err)
	}
	sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)

	// cache := sop.NewCacheClient()
	// log.Info("about to issue cache.Clear")
	// ctx := context.Background()
	// if err := cache.Clear(ctx); err != nil {
	// 	log.Error(fmt.Sprintf("cache.Clear failed, details: %v", err))
	// }
	initErasureCoding()
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

// openFailDirectIO implements fs.DirectIO and fails on Open to deterministically
// trigger DirectIO setup errors in tests without relying on filesystem permissions.
type openFailDirectIO struct{}

func (o *openFailDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	return nil, errors.New("simulated open failure")
}
func (o *openFailDirectIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return 0, errors.New("unreachable in this test")
}
func (o *openFailDirectIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return 0, errors.New("unreachable in this test")
}
func (o *openFailDirectIO) Close(file *os.File) error { return nil }

func TestDirectIOSetupNewFileFailure_NoReplication(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: sop.Redis}
	trans, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := infs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "norepltable",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		return
	}
	// Simulate DirectIO open failure using a test-only DirectIO shim to ensure deterministic error.
	// We expect commit to return an error when DirectIO cannot open the underlying registry file.
	// Swap in a simulated DirectIO that fails on Open.
	prev := fs.DirectIOSim
	fs.DirectIOSim = &openFailDirectIO{}
	defer func() { fs.DirectIOSim = prev }()
	b3.Add(ctx, 1, "hello world")
	if err := trans.Commit(ctx); err == nil {
		t.Error("expected error but none was returned")
		t.FailNow()
	}
}

func TestDirectIOSetupNewFileFailure_WithReplication(t *testing.T) {
	ctx := context.Background()
	// Take from global EC config the data paths & EC config details.
	to := sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, StoresFolders: storesFolders, CacheType: sop.Redis}

	_ = infs.RemoveBtree(ctx, "repltable", storesFolders, fs.GetGlobalErasureConfig(), sop.Redis)

	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	trans.Begin(ctx)
	so := sop.StoreOptions{
		Name:                     "repltable",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}
	b3, err := infs.NewBtreeWithReplication[int, string](ctx, so, trans, nil)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}

	fmt.Printf("GlobalReplication ActiveFolderToggler Before Fail: %v\n", fs.GlobalReplicationDetails.ActiveFolderToggler)

	// Simulate registry write failure on passive by toggling permissions.
	// Commit should still succeed (phase 2), but FailedToReplicate should be set.
	makeRegistryDirReadOnly(t, storesFolders, so.Name)
	b3.Add(ctx, 1, "hello world")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("expected no error on commit despite replication failure, got: %v", err)
		t.FailNow()
	}
	if fs.GlobalReplicationDetails == nil || !fs.GlobalReplicationDetails.FailedToReplicate {
		t.Errorf("expected FailedToReplicate true after replication error")
		t.FailNow()
	}
	// Restore permissions for subsequent success path
	restoreRegistryDirPerms(t, storesFolders, so.Name)

	fmt.Printf("GlobalReplication ActiveFolderToggler After Fail: %v\n", fs.GlobalReplicationDetails.ActiveFolderToggler)

	// Now, check whether transaction IO on new "active" target paths will be successful.

	ctx = context.Background()
	trans, err = infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	if err = trans.Begin(ctx); err != nil {
		t.Error(err)
		t.FailNow()
	}
	b3, err = infs.NewBtreeWithReplication[int, string](ctx, so, trans, nil)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	_, err = b3.Add(ctx, 1, "hello world")
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

func TestOpenBtree_TransWithRepl_failed(t *testing.T) {
	ctx := context.Background()
	// Take from global EC config the data paths & EC config details.
	to := sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, StoresFolders: storesFolders, CacheType: sop.Redis}

	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	trans.Begin(ctx)
	_, err = infs.OpenBtree[int, string](ctx, "repltable", trans, nil)
	if err == nil {
		t.Error("expected to fail but succeeded")
		t.FailNow()
	}
}

func TestOpenBtreeWithRepl_succeeded(t *testing.T) {
	ctx := context.Background()
	// Take from global EC config the data paths & EC config details.
	to := sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, StoresFolders: storesFolders, CacheType: sop.Redis}

	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	trans.Begin(ctx)
	_, err = infs.OpenBtreeWithReplication[int, string](ctx, "repltable", trans, nil)
	if err != nil {
		t.Errorf("expected to succeed but failed, details: %v", err)
		t.FailNow()
	}
}

// test helpers
func currentActive(stores []string) string {
	if fs.GlobalReplicationDetails != nil && fs.GlobalReplicationDetails.ActiveFolderToggler {
		return stores[1]
	}
	return stores[0]
}

func makeRegistryDirReadOnly(t *testing.T, stores []string, table string) {
	t.Helper()
	base := currentActive(stores)
	dir := filepath.Join(base, table)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.Chmod(dir, 0o555)
}

func restoreRegistryDirPerms(t *testing.T, stores []string, table string) {
	t.Helper()
	for _, b := range stores {
		dir := filepath.Join(b, table)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.Chmod(dir, 0o755)
	}
}
