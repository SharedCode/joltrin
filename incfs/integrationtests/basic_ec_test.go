//go:build integration
// +build integration

package integrationtests

import (
	"cmp"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/incfs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

func initErasureCoding() {
	// Erasure Coding configuration lookup table (map).
	ec := make(map[string]sop.ErasureCodingConfig)

	// Ensure data paths exist
	for i := 1; i <= 3; i++ {
		path := fmt.Sprintf("%s%cdisk%d", dataPath, os.PathSeparator, i)
		if err := os.MkdirAll(path, 0755); err != nil {
			panic(fmt.Sprintf("Failed to create directory %s: %v", path, err))
		}
		fmt.Printf("Created directory: %s\n", path)
	}

	// Erasure Coding config for "barstoreec" table uses three base folder paths that mimicks three disks.
	// Two data shards and one parity shard.
	ec["barstoreec"] = sop.ErasureCodingConfig{
		DataShardsCount:   2,
		ParityShardsCount: 1,
		BaseFolderPathsAcrossDrives: []string{
			fmt.Sprintf("%s%cdisk1", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk2", dataPath, os.PathSeparator),
			fmt.Sprintf("%s%cdisk3", dataPath, os.PathSeparator),
		},
		RepairCorruptedShards: true,
	}
	fs.SetGlobalErasureConfig(ec)
}

func Test_TransactionStory_OpenVsNewBTreeEC(t *testing.T) {
	// Cleanup potential stale data from previous runs
	_ = incfs.RemoveBtree(ctx, "barstoreec", sop.Redis)

	trans, err := incfs.NewTransactionWithReplication(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "barstoreec",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, trans, cmp.Compare)
	if err != nil {
		t.Error(err)
		return
	}
	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Errorf("Add(1, 'hello world') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}

	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		trans.Rollback(ctx)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

func Test_Basic_EC_Get(t *testing.T) {
	trans, err := incfs.NewTransactionWithReplication(ctx, sop.TransactionOptions{Mode: sop.ForReading, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "barstoreec",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, trans, cmp.Compare)
	if err != nil {
		t.Error(err)
		return
	}
	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		trans.Rollback(ctx)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}
