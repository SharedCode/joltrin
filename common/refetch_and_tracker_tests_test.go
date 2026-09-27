package common

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers addAction error branch in refetchAndMergeClosure when values are out-of-node and key duplicates on unique tree.
func Test_RefetchAndMerge_Add_SeparateSegment_DuplicateKey_ReturnsError(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_add_dup_out", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatalf("btree.New error: %v", err)
	}

	// Seed an item.
	pk, pv := newPerson("od", "k", "f", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: ok=%v err=%v", ok, err)
	}
	// Refresh StoreInfo for refetch.
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Clear tracker and enqueue duplicate add in separate segment path.
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items = map[sop.UUID]cacheItem[PersonKey, Person]{}
	dup := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[dup.ID] = cacheItem[PersonKey, Person]{
		lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:       &dup,
	}

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err == nil {
		t.Fatalf("expected duplicate error for separate segment add, got nil")
	}
}

// Covers itemActionTracker.Get path that fetches value from blob store and caches it when ValueNeedsFetch is true.
func Test_ItemActionTracker_Get_FetchesFromBlob_AndCaches(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_get_fetch", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](ns, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Prepare an item that needs fetch and store its value in blob.
	p := Person{Gender: "m", Email: "e", Phone: "p", SSN: "s"}
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: PersonKey{Firstname: "f", Lastname: "l"}, Version: 1, ValueNeedsFetch: true}
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: ns.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: it.ID, Value: toByteArray(p)}},
	}}); err != nil {
		t.Fatalf("blob add err: %v", err)
	}

	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("iat.Get err: %v", err)
	}
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected value loaded and ValueNeedsFetch=false")
	}
}

// Covers addAction path when values are in separate segment (IsValueDataInNodeSegment=false): uses AddItem.
func Test_RefetchAndMerge_AddAction_SeparateSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	// Use separate segment for values to exercise AddItem branch.
	so := sop.StoreOptions{Name: "rfm_add_sep", SlotLength: 4, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a new item to be added during refetch
	id := sop.NewUUID()
	pk, pv := newPerson("ka", "va", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv, Version: 0}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        it,
		versionInDB: it.Version,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch add separate segment err: %v", err)
	}
	// The item should be present in the B-tree after merge.
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatalf("expected item added and found")
	}
}

// Covers removeAction path error when item cannot be found after refetch.
func Test_RefetchAndMerge_RemoveAction_FailFind_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_remove_fail", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed a removal for a non-existent item; FindWithID will fail and return error.
	id := sop.NewUUID()
	pk, _ := newPerson("kr", "vr", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, Version: 1}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        it,
		versionInDB: it.Version,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error from remove path when item not found")
	}
}
