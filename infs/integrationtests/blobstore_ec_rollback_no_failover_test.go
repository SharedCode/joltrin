//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/fs"
)

// ecFailFileIO simulates shard write failures for BlobStoreWithEC by failing WriteFile
// for configured shard indices and path prefix. It returns a non-failover sop.Error code.
type ecFailFileIO struct {
	base       fs.FileIO
	pathPrefix string
	failIdx    map[int]struct{}
}

func (e ecFailFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	if strings.Contains(name, e.pathPrefix) {
		// file names end with _<shardIndex>
		idx := -1
		for i := len(name) - 1; i >= 0; i-- {
			if name[i] == '_' {
				// parse suffix as int; if parsing fails, ignore
				var n int
				_, _ = fmt.Sscanf(name[i+1:], "%d", &n)
				idx = n
				break
			}
		}
		if idx >= 0 {
			if _, ok := e.failIdx[idx]; ok {
				// Return a non-failover-qualified SOP error to ensure no failover.
				return sop.Error{Code: sop.FileIOError, Err: fmt.Errorf("simulated shard write failure idx=%d", idx)}
			}
		}
	}
	return e.base.WriteFile(ctx, name, data, perm)
}
func (e ecFailFileIO) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return e.base.ReadFile(ctx, name)
}
func (e ecFailFileIO) Remove(ctx context.Context, name string) error { return e.base.Remove(ctx, name) }
func (e ecFailFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	return e.base.Stat(ctx, path)
}
func (e ecFailFileIO) Exists(ctx context.Context, p string) bool     { return e.base.Exists(ctx, p) }
func (e ecFailFileIO) RemoveAll(ctx context.Context, p string) error { return e.base.RemoveAll(ctx, p) }
func (e ecFailFileIO) MkdirAll(ctx context.Context, p string, perm os.FileMode) error {
	return e.base.MkdirAll(ctx, p, perm)
}
func (e ecFailFileIO) ReadDir(ctx context.Context, d string) ([]os.DirEntry, error) {
	return e.base.ReadDir(ctx, d)
}

// Test_EC_BlobStore_ShardsExceedParity_Rollback_NoFailover ensures that when EC shard writes fail
// beyond parity tolerance, the transaction rolls back and no failover event is generated (since
// only Registry/StoreRepository IO can trigger failover).
func Test_EC_BlobStore_ShardsExceedParity_Rollback_NoFailover(t *testing.T) {
	ctx := context.Background()

	// Isolated replication base folders for this test (same as other isolated tests):
	isolatedStores := []string{
		fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator),
		fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator),
	}
	// EC config: 2 data + 2 parity across disk10..disk13
	ecCfg := map[string]sop.ErasureCodingConfig{
		"": {
			DataShardsCount:   2,
			ParityShardsCount: 2,
			BaseFolderPathsAcrossDrives: []string{
				fmt.Sprintf("%s%cdisk10", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk11", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk12", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk13", dataPath, os.PathSeparator),
			},
			RepairCorruptedShards: true,
		},
	}

	// Table unique to this test; ensure isolation.
	table := "ec_blob_failoverfree_it"

	// Clean environment for isolated disks.
	sanitizeIsolatedReplicationBases(t)
	cleanupStoreEverywhere(table)
	cleanupECShards(table)
	cleanupStoreRepository(table, isolatedStores)

	// Build components mirroring NewTwoPhaseCommitTransactionWithReplication but with a custom BlobStore fileIO.
	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
	rt, err := fs.NewReplicationTracker(ctx, isolatedStores, true, cache)
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// ManageStore + StoreRepository
	mbsf := fs.NewManageStoreFolder(fs.NewFileIO())
	sr, err := fs.NewStoreRepository(ctx, rt, mbsf, cache, fs.MinimumModValue)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}

	// Registry (readWrite=true)
	reg := fs.NewRegistry(true, fs.MinimumModValue, rt, cache)

	// BlobStoreWithEC with failing FileIO: fail 3 shards (indices 0,1,2) to exceed parity (2)
	baseFileIO := fs.NewFileIO()
	// prefix where blob shards for this table will be written; matches DefaultToFilePath layout
	// baseFolderPath + os.PathSeparator + table
	// We'll target all EC drive roots by checking for table name in path for simplicity.
	failPrefix := string(os.PathSeparator) + table + string(os.PathSeparator)
	injected := ecFailFileIO{base: baseFileIO, pathPrefix: failPrefix, failIdx: map[int]struct{}{0: {}, 1: {}, 2: {}}}
	bs, err := fs.NewBlobStoreWithEC(fs.DefaultToFilePath, injected, ecCfg)
	if err != nil {
		t.Fatalf("NewBlobStoreWithEC: %v", err)
	}

	// Transaction log and 2PC transaction
	tl := fs.NewTransactionLog(cache, rt)
	twoPT, err := common.NewTwoPhaseCommitTransaction(sop.ForWriting, -1, bs, sr, reg, cache, tl)
	if err != nil {
		t.Fatalf("NewTwoPhaseCommitTransaction: %v", err)
	}
	// Wire failover handler and set TID for sector locks
	twoPT.HandleReplicationRelatedError = rt.HandleReplicationRelatedError
	rt.SetTransactionID(twoPT.GetID())

	// Wrap into sop.Transaction to use higher-level helpers
	tx, err := sop.NewTransaction(sop.ForWriting, twoPT)
	if err != nil {
		t.Fatalf("sop.NewTransaction: %v", err)
	}

	// Ensure store exists via StoreRepository to avoid dependency on helper options.
	// Use DisableBlobStoreFormatting so blob table equals store name (matches failPrefix).
	si := sop.NewStoreInfo(sop.StoreOptions{
		Name:                           table,
		SlotLength:                     8,
		IsValueDataInNodeSegment:       false,
		IsValueDataActivelyPersisted:   true,
		DisableRegistryStoreFormatting: true,
		DisableBlobStoreFormatting:     true,
	})
	// Add is idempotent for unique names; ignore error if already present.
	_ = sr.Add(ctx, *si)

	initialActive := false
	if fs.GlobalReplicationDetails != nil {
		initialActive = fs.GlobalReplicationDetails.ActiveFolderToggler
	}

	// Perform a write that will cause EC Add to fail > parity and trigger rollback.
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// Open the store; it should exist now.
	btr, err := common.OpenBtree[int, string](ctx, table, tx, nil)
	if err != nil {
		t.Fatalf("OpenBtree: %v", err)
	}
	_, upErr := btr.Upsert(ctx, 100, "blob-data-100")
	// If active persistence fails during Upsert (BlobStore.Add error), roll back to clean staged blobs.
	if upErr != nil {
		_ = tx.Rollback(ctx)
	} else {
		// Otherwise, commit should fail due to EC write failures exceeding parity.
		if err := tx.Commit(ctx); err == nil {
			t.Fatalf("expected commit error due to EC shard write failures exceeding parity")
		}
	}

	// Assert no failover occurred and FailedToReplicate did not flip.
	if fs.GlobalReplicationDetails != nil {
		if fs.GlobalReplicationDetails.ActiveFolderToggler != initialActive {
			t.Fatalf("unexpected failover toggler flip for blob IO error; got %+v", *fs.GlobalReplicationDetails)
		}
		if fs.GlobalReplicationDetails.FailedToReplicate {
			t.Fatalf("unexpected FailedToReplicate=true for blob IO error; got %+v", *fs.GlobalReplicationDetails)
		}
	}

	// Ensure no shard files were written for the blob (clean rollback) under any EC drive.
	// Quick scan for files with the table folder containing the blob UUID as prefix.
	for i := 10; i <= 13; i++ {
		base := fmt.Sprintf("%s%cdisk%d", dataPath, os.PathSeparator, i)
		dir := filepath.Join(base, table)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		// Directory may exist due to attempted writes; ensure there are no files for this blob key.
		des, _ := os.ReadDir(dir)
		for _, de := range des {
			if strings.Contains(de.Name(), "_") { // shard naming format
				t.Fatalf("unexpected shard file present after rollback: %s", filepath.Join(dir, de.Name()))
			}
		}
	}
}
