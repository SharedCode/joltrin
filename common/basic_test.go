package common

import (
	"cmp"
	"os"
	"testing"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/v5"
)

func init() {
	l := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{
		Level: log.LevelDebug,
	}))
	log.SetDefault(l) // configures log package to print with LevelInfo
}

func Test_OpenVsNewBTree(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooStore",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, trans, cmp.Compare)
	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Logf("Add(1, 'hello world') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}
	if _, err := OpenBtree[int, string](ctx, "fooStore22", trans, nil); err == nil {
		t.Logf("OpenBtree('fooStore', trans) failed, got nil want error.")
	}
}

func Test_SingleBTree(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooStore",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, trans, cmp.Compare)
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

// This test exercise & demonstrate to use B-Tree that is unique on Keys.
// Adding an item with a key matching an existing item in the trie will fail.
func Test_UniqueKeyBTree(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooWorld",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, trans, nil)
	b3.Add(ctx, 1, "hello world")
	b3.Add(ctx, 2, "foo bar")

	if ok, _ := b3.Add(ctx, 1, "this one will fail"); ok {
		t.Errorf("Add(1) failed, got true, want false, as key 1 exists.")
	}

	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

func Test_UniqueKeyBTreeAcrossCommits(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooWorld2",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, cmp.Compare)
	b3.Add(ctx, 1, "hello world")
	b3.Add(ctx, 2, "foo bar")

	if err := t1.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}

	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)
	// Open the same trie created above.
	b32, _ := OpenBtree[int, string](ctx, "fooWorld2", t2, nil)
	if ok, _ := b32.Add(ctx, 1, "hello world"); ok {
		t.Errorf("Add(1) failed, got true, want false, as key 1 exists.")
	}

	if err := t2.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
}

// Fail 2nd commit as item key 1 was added in 1st and is also being added in 2nd.
func Test_UniqueKeyBTreeOnMultipleCommits(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooWorld3",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, cmp.Compare)
	b3.Add(ctx, 1, "hello world")
	b3.Add(ctx, 2, "foo bar")

	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)
	// Open the same trie created above.
	b32, _ := OpenBtree[int, string](ctx, "fooWorld3", t2, nil)
	b32.Add(ctx, 1, "hello world")

	if err := t1.Commit(ctx); err != nil {
		t.Errorf("Commit returned error, details: %v.", err)
	}
	if err := t2.Commit(ctx); err == nil {
		t.Errorf("Commit got nil, want error.")
	} else {
		t.Log(err)
	}
}

func Test_StoreCachingMinRuleCheck(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "storecachingminrule",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: true,
		Description:              "",
		CacheConfig: &sop.StoreCacheConfig{
			RegistryCacheDuration:  time.Duration(1 * time.Second),
			NodeCacheDuration:      time.Duration(30 * time.Minute),
			StoreInfoCacheDuration: time.Duration(1 * time.Second),
		},
	}, trans, cmp.Compare)

	// Check if minimum duration times were applied by SOP.
	if b3.GetStoreInfo().CacheConfig.RegistryCacheDuration < time.Duration(10*time.Minute) {
		t.Errorf("Minimum cache duration of 10mins enforcement failed for RegistryCacheDuration.")
	}
	if b3.GetStoreInfo().CacheConfig.NodeCacheDuration < time.Duration(5*time.Minute) {
		t.Errorf("Minimum cache duration of 5mins enforcement failed for NodeCacheDuration.")
	}
	if b3.GetStoreInfo().CacheConfig.StoreInfoCacheDuration < time.Duration(5*time.Minute) {
		t.Errorf("Minimum cache duration of 5mins enforcement failed for StoreInfoCacheDuration.")
	}
}

func Test_StoreCachingDefaultCacheApplied(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "storecachingdefault",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: true,
		Description:              "",
	}, trans, cmp.Compare)

	if b3.GetStoreInfo().CacheConfig.RegistryCacheDuration != sop.GetDefaultCacheConfig().RegistryCacheDuration {
		t.Errorf("Default cache check failed for RegistryCacheDuration.")
	}
	if b3.GetStoreInfo().CacheConfig.NodeCacheDuration != sop.GetDefaultCacheConfig().NodeCacheDuration {
		t.Errorf("Default cache check failed for NodeCacheDuration.")
	}
	if b3.GetStoreInfo().CacheConfig.StoreInfoCacheDuration != sop.GetDefaultCacheConfig().StoreInfoCacheDuration {
		t.Errorf("Default cache check failed for StoreInfoCacheDuration.")
	}
	if b3.GetStoreInfo().CacheConfig.ValueDataCacheDuration != sop.GetDefaultCacheConfig().ValueDataCacheDuration {
		t.Errorf("Default cache check failed for ValueDataCacheDuration.")
	}
}
