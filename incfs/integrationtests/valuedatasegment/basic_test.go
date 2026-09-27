//go:build integration
// +build integration

package valuedatasegment

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/incfs"
	"github.com/sharedcode/joltrin/v5"
)

var cassConfig = cassandra.Config{
	ClusterHosts: []string{"localhost:9042"},
	Keyspace:     "btree",
}
var redisConfig = redis.Options{
	Address:  "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
}

var ctx = context.Background()

var dataPath string = "/tmp/sop_data_vds"

func init() {
	if dp := os.Getenv("datapath"); dp != "" {
		dataPath = dp
	}
	incfs.Initialize(cassConfig, redisConfig)

	// Register Redis L2Cache for use in Integration tests.
	sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
}

func Test_TransactionStory_OpenVsNewBTree(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooStore1",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
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
	if _, err := incfs.OpenBtree[int, string](ctx, "fooStore22", trans, nil); err == nil {
		t.Logf("OpenBtree('fooStore', trans) failed, got nil want error.")
	}
}

func Test_TransactionStory_SingleBTree(t *testing.T) {
	// 1. Open a transaction
	// 2. Instantiate a BTree
	// 3. Do CRUD on BTree
	// 4. Commit Transaction
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooStore2",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
		BlobStoreBaseFolderPath:  dataPath,
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

func Test_ByteArrayValue(t *testing.T) {
	incfs.RemoveBtree(ctx, "baStore", sop.Redis)
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, []byte](ctx, sop.StoreOptions{
		Name:                    "baStore",
		SlotLength:              8,
		LeafLoadBalancing:       true,
		Description:             "",
		BlobStoreBaseFolderPath: dataPath,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		return
	}
	value := []byte("hello world")
	if ok, err := b3.Add(ctx, 1, value); !ok || err != nil {
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
	if v, err := b3.GetCurrentValue(ctx); !bytes.Equal(v, value) || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = %v, nil.", v, err, value)
		return
	}
	t.Logf("Successfully added & found item with key 1.")
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

func Test_ByteArrayValueGet(t *testing.T) {
	trans, err := incfs.NewTransaction(ctx, sop.TransactionOptions{Mode: sop.NoCheck, MaxTime: -1, CacheType: sop.Redis})
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, err := incfs.NewBtree[int, []byte](ctx, sop.StoreOptions{
		Name:                    "baStore",
		SlotLength:              8,
		LeafLoadBalancing:       true,
		Description:             "",
		BlobStoreBaseFolderPath: dataPath,
	}, trans, nil)
	if err != nil {
		t.Error(err)
		return
	}
	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Errorf("FindOne(1,false) failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	value := []byte("hello world")
	if k := b3.GetCurrentKey().Key; k != 1 {
		t.Errorf("GetCurrentKey() failed, got = %v, want = 1.", k)
		trans.Rollback(ctx)
		return
	}
	if v, err := b3.GetCurrentValue(ctx); !bytes.Equal(v, value) || err != nil {
		t.Errorf("GetCurrentValue() failed, got = %v, %v, want = %v, nil.", v, err, value)
		return
	}
	trans.Commit(ctx)
}
