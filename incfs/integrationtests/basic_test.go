//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"fmt"
	log "log/slog"
	"os"
	"testing"
	"time"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/incfs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Cassandra config.
var cassConfig = cas.Config{
	ClusterHosts: []string{"localhost:9042"},
	Keyspace:     "btree",
}

// Redis config.
var redisConfig = redis.Options{
	Address:  "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
}

var dataPath string = "/tmp/sop_data"

func init() {
	if dp := os.Getenv("datapath"); dp != "" {
		dataPath = dp
	}
	l := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{
		Level: log.LevelInfo,
	}))
	log.SetDefault(l) // configures log package to print with LevelInfo

	incfs.Initialize(cassConfig, redisConfig)

	// Initialize Erasure Coding (EC) for the EC tests.
	initErasureCoding()

	// Register Redis L2Cache for use in Integration tests.
	sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
}

var ctx = context.Background()

func Test_GetStoreList(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	stores, _ := trans.GetPhasedTransaction().GetStores(ctx)

	log.Info(fmt.Sprintf("Store count: %d", len(stores)))
}

// Create an empty store on 1st run, add one item(max) on succeeding runs.
func Test_CreateEmptyStore(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)

	b3, err := incfs.OpenBtree[int, string](ctx, "emptyStore", trans, nil)
	if err == nil {
		if b3.Count() == 0 {
			if ok, err := b3.Add(ctx, 123, "foobar"); !ok || err != nil {
				t.Errorf("Failed, w/ error: %v", err)
				return
			}
		}
		trans.Commit(ctx)
		return
	}
	trans, _ = incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	trans.Begin(ctx)

	b3, err = incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "emptyStore",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		BlobStoreBaseFolderPath:  dataPath,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		return
	}
	if b3.Count() != 0 {
		t.Error("b3 not empty, expected Count = 0")
	}
	trans.Commit(ctx)
}

func Test_TransactionStory_OpenVsNewBTree(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "barstore2",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		BlobStoreBaseFolderPath:  dataPath,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		return
	}
	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Logf("Add(1, 'hello world') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	if _, err := incfs.OpenBtree[int, string](ctx, "barStore22", trans, nil); err == nil {
		t.Logf("OpenBtree('barStore', trans) failed, got nil want error.")
	}
}

func Test_TransactionStory_SingleBTree_Get(t *testing.T) {
	// 1. Open a transaction
	// 2. Instantiate a BTree
	// 3. Do CRUD on BTree
	// 4. Commit Transaction
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForReading, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "barstore1",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		BlobStoreBaseFolderPath:  dataPath,
	}, trans, nil)

	defer trans.Rollback(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if b3.Count() == 0 {
		return
	}
	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		return
	}
}

// My File path formatter, given a base path & a GUID.
// basePath parameter contains the blob store BlobStore Base FolderPath + the blob store (file) name.
// id parameter is the GUID of the blob to be written to the disk.
func MyToFilePath(basePath string, id sop.UUID) string {
	if len(basePath) > 0 && basePath[len(basePath)-1] == os.PathSeparator {
		return fmt.Sprintf("%s%s", basePath, fs.Apply4LevelHierarchy(id))
	}
	return fmt.Sprintf("%s%c%s", basePath, os.PathSeparator, fs.Apply4LevelHierarchy(id))
}

func Test_TransactionStory_SingleBTree(t *testing.T) {

	// Demo NewTransactionExt specifying custom "to file path" lambda function.
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "barstore1",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		BlobStoreBaseFolderPath:  dataPath,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		trans.Rollback(ctx)
		return
	}
	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Errorf("Add(1, 'hello world') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		trans.Rollback(ctx)
		return
	}

	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		trans.Rollback(ctx)
		return
	}
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		trans.Rollback(ctx)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		trans.Rollback(ctx)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

func Test_RegistryZeroDurationCache(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	so := sop.StoreCacheConfig{}
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "regnotcached",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		BlobStoreBaseFolderPath:  dataPath,
		CacheConfig:              &so,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		trans.Rollback(ctx)
		return
	}
	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Errorf("Add(1, 'hello world') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		trans.Rollback(ctx)
		return
	}

	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		trans.Rollback(ctx)
		return
	}
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		trans.Rollback(ctx)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		trans.Rollback(ctx)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

func Test_StoreCaching(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "storecaching",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		BlobStoreBaseFolderPath:  dataPath,
		CacheConfig:              sop.NewStoreCacheConfig(time.Duration(30*time.Minute), false),
	}, trans, nil)
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

func Test_StoreCachingTTL(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "storecachingttl",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		BlobStoreBaseFolderPath:  dataPath,
		CacheConfig:              sop.NewStoreCacheConfig(time.Duration(30*time.Minute), true),
	}, trans, nil)
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
	if v, err := b3.GetCurrentValue(ctx); v != "hello world" || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = 1, nil.", v, err)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}
