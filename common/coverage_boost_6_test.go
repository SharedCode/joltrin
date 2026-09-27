package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func Test_ItemActionTracker_Get_TTL_CacheHit_SetsValue(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_ttl_hit",
		SlotLength:                4,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
		CacheConfig: &sop.StoreCacheConfig{
			IsValueDataCacheTTL:    true,
			ValueDataCacheDuration: time.Minute,
		},
	}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tr := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	// Prepare item and seed cache
	pk, pv := newPerson("tt", "l", "m", "e@x", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := mockRedisCache.SetStruct(ctx, formatItemKey(id.String()), &pv, time.Minute); err != nil {
		t.Fatalf("seed cache err: %v", err)
	}
	if err := tr.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected value set from cache and NeedsFetch=false")
	}
}

func Test_ItemActionTracker_Get_NonGlobal_FetchesFromBlob(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_blob",
		SlotLength:                4,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tr := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	pk, pv := newPerson("nb", "l", "m", "e@x", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}

	// Seed blob store with encoded value via tracker's Add path
	// Alternatively, marshal and call blob store directly
	// Use blob store directly for determinism
	ba, _ := encoding.BlobMarshaler.Marshal(pv)
	_ = mockNodeBlobStore.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: si.BlobTable, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: ba}}}})

	if err := tr.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected value set from blob and NeedsFetch=false")
	}
}

// Covers refetchAndMergeClosure version-mismatch detection path.
func Test_RefetchAndMerge_VersionMismatch_Error(t *testing.T) {
	ctx := context.Background()
	// Build store with value data in node segment so update path uses UpdateCurrentItem
	so := sop.StoreOptions{Name: "rfm_ver_mismatch", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	// Fresh global cache
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed an item and then simulate update with stale versionInDB
	pk, pv := newPerson("vu1", "vv1", "m", "v@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	// Track an update but with mismatching saved version
	stale := cur
	stale.Version = cur.Version // btree's current version
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &stale,
		versionInDB: cur.Version - 1, // older than current -> mismatch
	}

	// Seed MRU and Handle for root so repo.get can fetch after reset
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		cache.GetGlobalL1Cache(l2).SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		cache.GetGlobalL1Cache(l2).Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}
	// Sync StoreRepository
	upd := *ns
	upd.RootNodeID = b3.StoreInfo.RootNodeID
	upd.Count = b3.StoreInfo.Count
	_ = sr.Add(ctx, upd)

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected version mismatch error, got nil")
	}
}

// failingBlobStoreAdd returns an error on Add to exercise error path in commitUpdatedNodes.
type failingBlobStoreAdd struct{}

func (f failingBlobStoreAdd) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (f failingBlobStoreAdd) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return errors.New("blob add err")
}
func (f failingBlobStoreAdd) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (f failingBlobStoreAdd) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return nil
}

// Covers commitUpdatedNodes returning false on version mismatch.
func Test_CommitUpdatedNodes_VersionMismatch_ReturnsFalse_2(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), StoreRepository: sr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "upd_ver_mismatch", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.Version = 2 // backend version newer than node's version
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("reg add err: %v", err)
	}
	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}
	ok, handles, err := nr.commitUpdatedNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitUpdatedNodes err: %v", err)
	}
	if ok || handles != nil {
		t.Fatalf("expected ok=false and nil handles on version mismatch, got ok=%v handles=%v", ok, handles)
	}
}

// Covers commitUpdatedNodes error from UpdateNoLocks and from blobStore.Add.
func Test_CommitUpdatedNodes_UpdateNoLocks_And_BlobAdd_Errors(t *testing.T) {
	ctx := context.Background()
	// Case 1: UpdateNoLocks error
	{
		l2 := mocks.NewMockClient()
		cache.GetGlobalL1Cache(l2)
		// Use a registry that always errors on UpdateNoLocks, regardless of allOrNothing flag
		regErr := &errAllUpdateNoLocks{Mock_vid_registry: &mocks.Mock_vid_registry{Lookup: make(map[sop.UUID]sop.Handle)}}
		bs := mocks.NewMockBlobStore()
		tx := &Transaction{registry: regErr, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
		si := sop.NewStoreInfo(sop.StoreOptions{Name: "upd_unl_err", SlotLength: 4})
		nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		h.Version = 1
		if err := regErr.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
			t.Fatalf("reg add err: %v", err)
		}
		n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
		nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}
		ok, _, err := nr.commitUpdatedNodes(ctx, nodes)
		if err == nil || ok {
			t.Fatalf("expected UpdateNoLocks error and ok=false, got ok=%v err=%v", ok, err)
		}
	}

	// Case 2: blobStore.Add error
	{
		l2 := mocks.NewMockClient()
		cache.GetGlobalL1Cache(l2)
		reg := mocks.NewMockRegistry(false)
		bs := failingBlobStoreAdd{}
		tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
		si := sop.NewStoreInfo(sop.StoreOptions{Name: "upd_blob_err", SlotLength: 4})
		nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		h.Version = 1
		if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
			t.Fatalf("reg add err: %v", err)
		}
		n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
		nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}
		ok, _, err := nr.commitUpdatedNodes(ctx, nodes)
		if err == nil || ok {
			t.Fatalf("expected blobStore.Add error and ok=false, got ok=%v err=%v", ok, err)
		}
	}
}

// errAllUpdateNoLocks wraps the default mock registry but forces UpdateNoLocks to fail
// to exercise the error path in commitUpdatedNodes regardless of the allOrNothing flag.
type errAllUpdateNoLocks struct{ *mocks.Mock_vid_registry }

func (e *errAllUpdateNoLocks) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return errors.New("induced error on UpdateNoLocks")
}

// Compose rollback logs to cover multiple branches where possible.
func Test_TransactionLogger_Rollback_With_DeleteObsoleteEntries(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	trLog := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tnx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: reg, blobStore: bs, logger: trLog, StoreRepository: sr}
	// Provide a minimal btreesBackend to satisfy calls during deleteObsoleteEntries
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_del_ob", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tnx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}
	tnx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	// Build payload for finalizeCommit that deleteObsoleteEntries uses
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{sop.NewUUID()}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	tracked := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: si.BlobTable, Blobs: []sop.UUID{}}}}
	fin := toByteArray(sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs},
		Second: tracked,
	})

	// lastCommittedFunctionLog must be deleteObsoleteEntries (place it last)
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitStoreInfo, Value: toByteArray([]sop.StoreInfo{*si})},
		{Key: finalizeCommit, Value: fin},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	if err := trLog.rollback(ctx, tnx, trLog.transactionID, logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

func Test_TransactionLogger_Rollback_Covers_Other_Branches(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	trLog := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tnx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: reg, blobStore: bs, logger: trLog, StoreRepository: sr}

	// Provide btreesBackend with a usable nodeRepository
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_other", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tnx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}
	tnx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	// Prepare payloads for different branches
	addV := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
		First:  []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_add", IDs: []sop.UUID{sop.NewUUID()}}},
		Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_add", Blobs: []sop.UUID{sop.NewUUID()}}},
	})
	rmV := toByteArray([]sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_rm", IDs: []sop.UUID{sop.NewUUID()}}})
	updV := toByteArray([]sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_upd", Blobs: []sop.UUID{sop.NewUUID()}}})
	rootV := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
		First:  []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root", IDs: []sop.UUID{sop.NewUUID()}}},
		Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_root", Blobs: []sop.UUID{sop.NewUUID()}}},
	})
	trkV := toByteArray([]sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "bt_i", Blobs: []sop.UUID{}}}})
	// finalize payload for deleteTrackedItemsValues branch
	fin := toByteArray(sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: []sop.RegistryPayload[sop.UUID]{}, Second: []sop.BlobsPayload[sop.UUID]{}},
		Second: []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: false, Second: sop.BlobsPayload[sop.UUID]{}}},
	})

	// Make the last entry deleteTrackedItemsValues so conditions (>) are satisfied for other branches
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitStoreInfo, Value: toByteArray([]sop.StoreInfo{*si})},
		{Key: commitAddedNodes, Value: addV},
		{Key: commitRemovedNodes, Value: rmV},
		{Key: commitUpdatedNodes, Value: updV},
		{Key: commitNewRootNodes, Value: rootV},
		{Key: commitTrackedItemsValues, Value: trkV},
		{Key: finalizeCommit, Value: fin},
		{Key: deleteTrackedItemsValues, Value: nil},
	}

	if err := trLog.rollback(ctx, tnx, trLog.transactionID, logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

// Covers addAction path in refetchAndMergeClosure when values are stored in-node (b3.Add).
func Test_RefetchAndMerge_AddItem_InNodeSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_add_innode", SlotLength: 4, IsValueDataInNodeSegment: true}
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
		t.Fatal(err)
	}

	// Seed a new item in tracker as addAction for in-node Add path
	pk, pv := newPerson("ain", "va", "m", "a@b", "p")
	it := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:       &it,
		persisted:  false,
	}

	// Ensure StoreRepository returns the store on refresh
	_ = sr.Add(ctx, *ns)

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the item is now present in btree (by key)
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatalf("expected item to be added to btree")
	}
}

// Covers updateAction path in refetchAndMergeClosure when values are in a separate segment (UpdateCurrentNodeItem).
func Test_RefetchAndMerge_Update_SeparateSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_upd_sep", SlotLength: 4, IsValueDataInNodeSegment: false}
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
		t.Fatal(err)
	}

	// Seed an item in the tree
	pk, pv := newPerson("us1", "v1", "m", "u@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)

	// Prepare tracker with updateAction for separate segment path:
	// - Map key is current(item) ID (uuid) so FindWithID succeeds
	// - Item is a new inflight item (new ID) with updated value
	newV := pv
	newV.Phone = "upd-sep"
	inflight := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &newV}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &inflight,
		versionInDB: cur.Version,
		persisted:   false,
	}

	// Seed MRU and handle for root so repo.get can refetch after reset
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		cache.GetGlobalL1Cache(l2).SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		cache.GetGlobalL1Cache(l2).Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}
	// Sync StoreRepository to current state
	upd := *ns
	upd.RootNodeID = b3.StoreInfo.RootNodeID
	upd.Count = b3.StoreInfo.Count
	_ = sr.Add(ctx, upd)

	// Execute closure
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate updated value applied
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("item missing after update")
	}
	got, _ := b3.GetCurrentItem(ctx)
	if got.Value == nil || got.Value.Phone != "upd-sep" {
		t.Fatalf("expected updated value 'upd-sep', got %+v", got.Value)
	}
	// Validate tracker bookkeeping: persisted true for inflight item and forDeletion contains old id
	iat := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person])
	ci, ok := iat.items[inflight.ID]
	if !ok || !ci.persisted {
		t.Fatalf("expected inflight item persisted in tracker, ok=%v persisted=%v", ok, ci.persisted)
	}
	if len(iat.forDeletionItems) == 0 || iat.forDeletionItems[0] != cur.ID {
		t.Fatalf("expected old item ID queued for deletion, got %#v", iat.forDeletionItems)
	}
}

// Covers error path when FindWithID fails in refetchAndMergeClosure (non-add action with missing uuid).
func Test_RefetchAndMerge_FindWithID_Fails_ReturnsError(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_find_fail", SlotLength: 4, IsValueDataInNodeSegment: true}
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
		t.Fatal(err)
	}

	// Seed an item in the tree
	pk, pv := newPerson("ff1", "fv1", "m", "f@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)

	// Create a bogus uuid to fail FindWithID; action must not be addAction
	bogus := sop.NewUUID()
	// Reuse current value and mark versionInDB to match, but mismatch uuid
	ci := cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &cur,
		versionInDB: cur.Version,
	}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items = map[sop.UUID]cacheItem[PersonKey, Person]{bogus: ci}

	// Seed MRU and handle for root so repo.get can refetch after reset
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		cache.GetGlobalL1Cache(l2).SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		cache.GetGlobalL1Cache(l2).Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}
	// Sync StoreRepository
	upd := *ns
	upd.RootNodeID = b3.StoreInfo.RootNodeID
	upd.Count = b3.StoreInfo.Count
	_ = sr.Add(ctx, upd)

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error when FindWithID fails with bogus uuid")
	}
}

// Covers the version-mismatch error branch in refetchAndMergeClosure (after FindWithID + GetCurrentItem).
func Test_RefetchAndMerge_Update_VersionMismatch_ReturnsError(t *testing.T) {
	ctx := context.Background()
	// In-node value storage to keep things simple
	so := sop.StoreOptions{Name: "rfm_ver_mismatch", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)

	// Store repo must return the store during refresh
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	// Minimal transaction wiring
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	// Store interface and btree
	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatalf("btree.New error: %v", err)
	}

	// Seed one item into the B-tree so FindWithID succeeds later
	pk, pv := newPerson("vm", "x", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: ok=%v err=%v", ok, err)
	}
	// Persist refreshed StoreInfo so refetch uses the correct RootNodeID/Count
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)
	// Get current item to capture its ID and version
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem error: %v", err)
	}

	// Clear any Add action recorded by the tracker from the seed Add
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items = make(map[sop.UUID]cacheItem[PersonKey, Person])

	// Prepare an updateAction with mismatching versionInDB to trigger the error path
	stale := cur // use same ID/key/value
	// Make versionInDB intentionally different from the current item.Version
	mismatchedVersion := cur.Version + 1
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &stale,
		versionInDB: mismatchedVersion,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected version-mismatch error, got nil")
	}
}

// Covers the addAction error branch in refetchAndMergeClosure when B-tree is unique and key already exists (b3.Add returns false).
func Test_RefetchAndMerge_Add_InNode_DuplicateKey_ReturnsError(t *testing.T) {
	ctx := context.Background()
	// Unique keys and in-node values
	so := sop.StoreOptions{Name: "rfm_add_dup", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true}
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

	// Seed an item with key K so a subsequent addAction with the same key fails on unique tree
	pk, pv := newPerson("du", "k", "f", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: ok=%v err=%v", ok, err)
	}
	// Persist refreshed StoreInfo so refetch sees existing key at the new root
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)
	// Clear tracker items recorded by the seed Add
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items = make(map[sop.UUID]cacheItem[PersonKey, Person])

	// Prepare a different item ID but same key to trigger duplicate detection during Add
	dup := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[dup.ID] = cacheItem[PersonKey, Person]{
		lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:       &dup,
		persisted:  false,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error from duplicate-key add, got nil")
	}
}

// unlockErrCache wraps a cache to force Unlock errors.
type unlockErrCache struct{ sop.L2Cache }

func (c unlockErrCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return fmt.Errorf("unlock error")
}

// Ensures doPriorityRollbacks executes the unlock-warning branch when Unlock returns an error.
func Test_TransactionLogger_DoPriorityRollbacks_UnlockWarning_Path(t *testing.T) {
	ctx := context.Background()

	// Setup registry with matching versions so UpdateNoLocks succeeds.
	reg := mocks.NewMockRegistry(false)
	// Create one TID with one handle to restore.
	tid := sop.NewUUID()
	lid := sop.NewUUID()
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
		{RegistryTable: "rt", IDs: []sop.Handle{{LogicalID: lid, Version: 1}}},
	})

	// Priority log returns a single batch entry.
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{
		Key:   tid,
		Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{{LogicalID: lid, Version: 1}}}},
	}}}

	// Wrap mock cache to force Unlock error, to hit the log.Warn branch.
	l2 := unlockErrCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2, registry: reg}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error from doPriorityRollbacks: %v", err)
	}
	if !consumed {
		t.Fatalf("expected consumed=true after processing batch")
	}
}

// Validates the timeout path in doPriorityRollbacks returns consumed=true without error.
func Test_TransactionLogger_DoPriorityRollbacks_TimedOut_ReturnsTrue(t *testing.T) {
	ctx := context.Background()

	// Seed a minimal batch so loop runs at least once.
	tid := sop.NewUUID()
	lid := sop.NewUUID()
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{
		Key:   tid,
		Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{{LogicalID: lid, Version: 1}}}},
	}}}

	// Registry aligned to make UpdateNoLocks a no-op success.
	reg := mocks.NewMockRegistry(false)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{{LogicalID: lid, Version: 1}}}})

	// Override sop.Now to simulate elapsed time beyond maxDuration.
	origNow := sop.Now
	fixed := time.Now()
	sop.Now = func() time.Time { return fixed.Add(10 * time.Minute) }
	defer func() { sop.Now = origNow }()

	tx := &Transaction{l2Cache: mocks.NewMockClient(), registry: reg}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !consumed {
		t.Fatalf("expected consumed=true due to timeout")
	}
}

// Ensures doPriorityRollbacks returns consumed=false when the coordinator lock (Prbs) is already held.
func Test_TransactionLogger_DoPriorityRollbacks_PrbsLockHeld_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	// Pre-lock the exact key used internally: FormatLockKey applied twice (matching implementation).
	prbsKey := l2.FormatLockKey(l2.FormatLockKey(coordinatorLockName))
	_ = l2.Set(ctx, prbsKey, sop.NewUUID().String(), time.Minute)

	tl := newTransactionLogger(stubTLog{pl: &stubPriorityLog{}}, true)
	tx := &Transaction{l2Cache: l2, registry: mocks.NewMockRegistry(false)}

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if consumed {
		t.Fatalf("expected consumed=false when Prbs lock is held")
	}
}

// Covers itemActionTracker.Add when actively-persisted is enabled but item has nil Value:
// should skip blob add/log and only bump version and track the item.
func Test_ItemActionTracker_Add_ActivelyPersisted_NoValue_SkipsPersist(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	so := sop.StoreOptions{Name: "iat_add_skip", SlotLength: 4, IsValueDataActivelyPersisted: true, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, l2, mocks.NewMockBlobStore(), tl)

	// Item with nil Value triggers manage() to return nil, so no blob add/log occurs.
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: PersonKey{Lastname: "x"}, Version: 1, Value: nil}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Version should still bump.
	if it.Version != 2 {
		t.Fatalf("expected version increment to 2, got %d", it.Version)
	}
	// Ensure item is tracked with action addAction.
	ci, ok := trk.items[it.ID]
	if !ok || ci.Action != addAction {
		t.Fatalf("expected tracked addAction, ok=%v action=%v", ok, ci.Action)
	}
}

// Covers itemActionTracker.Update when an existing tracked item is addAction: it should actively persist
// and update global cache when configured.
func Test_ItemActionTracker_Update_OnExistingAdd_ActivelyPersisted_Caches(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	so := sop.StoreOptions{Name: "iat_upd_on_add", SlotLength: 4, IsValueDataActivelyPersisted: true, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	// Seed tracker with an addAction entry containing a non-nil value so manage() yields a blob to add.
	id := sop.NewUUID()
	val := Person{Email: "a"}
	it0 := &btree.Item[PersonKey, Person]{ID: id, Key: PersonKey{Lastname: "k"}, Version: 1, Value: &val}
	trk.items[id] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction}, item: it0, versionInDB: it0.Version}

	// Call Update with a separate item instance; code path should use the cached addAction entry.
	it := &btree.Item[PersonKey, Person]{ID: id, Key: it0.Key, Version: 1, Value: &val}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	// Value should be placed in global cache keyed by the item ID; verify presence.
	var got Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(id.String()), &got); !ok || got.Email != "a" {
		t.Fatalf("expected cached value after Update on addAction; ok=%v got=%+v", ok, got)
	}
}

// Increases coverage for Phase1Commit wrapper by forcing rollback to return an error
// (unlockTrackedItems returns error when committedState >= lockTrackedItems), so wrapper
// includes "rollback error:" in the returned message.
func Test_Phase1Commit_Wrapper_Appends_RollbackError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tx := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: rg, logger: tl, maxTime: time.Minute}

	// Backend that has tracked items, lock fails to trigger early error, and unlock returns error for rollback.
	bx := btreeBackend{
		nodeRepository: &nodeRepositoryBackend{localCache: make(map[sop.UUID]cachedNode)},
		getStoreInfo: func() *sop.StoreInfo {
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_rb_err", SlotLength: 4})
			return si
		},
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return assertError("lock fail") },
		unlockTrackedItems:               func(context.Context) error { return assertError("unlock fail") },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}
	tx.btreesBackend = []btreeBackend{bx}

	if err := tx.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	err := tx.Phase1Commit(ctx)
	if err == nil {
		t.Fatalf("expected error from Phase1Commit")
	}
	// Should include "rollback error" from wrapper formatting when rollback fails.
	if !strings.Contains(err.Error(), "rollback error:") {
		t.Fatalf("expected rollback error appended, got: %v", err)
	}
	if tx.HasBegun() {
		t.Fatalf("expected transaction to be ended after failure")
	}
}

// Helper to construct quick errors without importing fmt anew.
func assertError(msg string) error { return &simpleErr{s: msg} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// Covers rollbackRemovedNodes when nodesAreLocked=false to exercise the Update() path.
func Test_NodeRepository_RollbackRemovedNodes_UnlockedPath(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	rg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_rm_nodes_unlocked", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}

	// Seed registry with deleted/timestamped handles to create a non-empty handlesForRollback.
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.IsDeleted = true
	h.WorkInProgressTimestamp = sop.Now().UnixMilli()
	_ = rg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	if err := nr.rollbackRemovedNodes(ctx, false, vids); err != nil {
		t.Fatalf("rollbackRemovedNodes (unlocked) err: %v", err)
	}
}

// stubTLRemoveErr2 returns an error on Remove to exercise error propagation at end of rollback.
type stubTLRemoveErr2 struct{ mocks.MockTransactionLog }

func (s *stubTLRemoveErr2) Remove(ctx context.Context, tid sop.UUID) error {
	return fmt.Errorf("rm err")
}

// Covers finalizeCommit branch with non-nil payload when lastCommittedFunctionLog < deleteTrackedItemsValues:
// should not delete tracked items or obsolete entries, and perform final Remove at the end.
func Test_TransactionLogger_Rollback_FinalizeWithPayload_Continue_NoDeletes(t *testing.T) {
	ctx := context.Background()
	tl := &stubTLRemove{}
	logger := newTransactionLogger(tl, true)
	tx := &Transaction{logger: logger}

	// Prepare a non-empty finalize payload but make the last log a lower stage to force the "continue" path.
	// Structure: Tuple{ First: Tuple{ []RegistryPayload[UUID], []BlobsPayload[UUID] }, Second: []Tuple[bool, BlobsPayload[UUID]] }
	lids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{sop.NewUUID()}}}
	bids := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}}}
	tracked := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: bids[0]}}
	pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: lids, Second: bids},
		Second: tracked,
	}

	tid := sop.NewUUID()
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(finalizeCommit), Value: toByteArray(pl)},
		// Make the last committed function a lower stage so finalize payload branch only continues.
		{Key: int(commitAddedNodes), Value: nil},
	}

	if err := logger.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("rollback returned error: %v", err)
	}
	if len(tl.removed) != 1 || tl.removed[0].Compare(tid) != 0 {
		t.Fatalf("expected Remove called for tid at end-of-loop, got %v", tl.removed)
	}
}

// Ensures rollback returns error when final Remove(tid) fails and no early return occurs.
func Test_TransactionLogger_Rollback_EndRemove_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	tl := &stubTLRemoveErr2{}
	logger := newTransactionLogger(tl, true)
	tx := &Transaction{logger: logger}

	tid := sop.NewUUID()
	// Provide a simple log that doesn't early-return so the trailing Remove is executed.
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(commitAddedNodes), Value: nil},
	}

	if err := logger.rollback(ctx, tx, tid, logs); err == nil {
		t.Fatalf("expected error propagated from final Remove")
	}
}
