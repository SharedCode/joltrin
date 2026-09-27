package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers transactionLogger.rollback branches for commitStoreInfo, commitAdded/Removed/Updated/NewRoot.
func Test_TransactionLogger_Rollback_Multiple_Phases(t *testing.T) {
	ctx := context.Background()
	// Wire minimal tx with caches, blob, store repo, registry, and backend repo required by rollback branches.
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	tx := &Transaction{l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_multi", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Attach logger to transaction so rollback helpers can read committedState, etc.
	tx.logger = tl

	// Build rollback log set; last key high to enable all earlier-phase rollbacks.
	// StoreInfo update
	sis := []sop.StoreInfo{{Name: si.Name}}
	// Added nodes payload
	addVids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{sop.NewUUID()}}}
	addBibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	addTuple := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: addVids, Second: addBibs}
	// Removed nodes payload
	rmVids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{sop.NewUUID()}}}
	// Updated nodes payload (inactive blob IDs to remove)
	updBibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	// New root nodes payload
	nrTuple := addTuple

	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitStoreInfo, Value: toByteArray(sis)},
		{Key: commitAddedNodes, Value: toByteArray(addTuple)},
		{Key: commitRemovedNodes, Value: toByteArray(rmVids)},
		{Key: commitUpdatedNodes, Value: toByteArray(updBibs)},
		{Key: commitNewRootNodes, Value: toByteArray(nrTuple)},
		{Key: deleteObsoleteEntries, Value: nil}, // highest stage to unlock earlier branches
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback multi-phase err: %v", err)
	}
}

// Covers rollback branch for commitUpdatedNodes where removeNodes is invoked and cache deletions may warn.
func Test_TransactionLogger_Rollback_CommitUpdated_RemoveNodes_Path(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, l1Cache: gc, blobStore: bs, registry: rg}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_rm", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}

	// Seed one inactive blob ID and a corresponding cached key to exercise deletion.
	blobID := sop.NewUUID()
	// Put a placeholder blob so remove sees it and deletes; cache key also present.
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: si.BlobTable, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: blobID, Value: []byte("x")}}}})
	_ = l2.SetStruct(ctx, nr.formatKey(blobID.String()), &struct{ A int }{A: 1}, time.Minute)

	// Build logs so that commitUpdatedNodes has a non-nil payload of blob IDs.
	updBibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{blobID}}}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitUpdatedNodes, Value: toByteArray(updBibs)},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback(commitUpdatedNodes removeNodes) err: %v", err)
	}
}

func Test_TransactionLogger_Rollback_CommitUpdated_RemoveNodes_DeleteErr_Propagates(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := deleteErrCache{L2Cache: base}
	gc := cache.GetGlobalL1Cache(base)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, l1Cache: gc, blobStore: bs, registry: rg}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_rm_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}

	// Seed a blob and also ensure the cache key exists so Delete is attempted.
	blobID := sop.NewUUID()
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: si.BlobTable, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: blobID, Value: []byte("x")}}}})
	_ = base.SetStruct(ctx, nr.formatKey(blobID.String()), &struct{ A int }{A: 1}, time.Minute)

	updBibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{blobID}}}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitUpdatedNodes, Value: toByteArray(updBibs)},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err == nil {
		t.Fatalf("expected error propagated from cache Delete during removeNodes")
	}
}

// Covers checkTrackedItems: (a) cache error path, (b) get-get compatibility with different LockIDs.
// getErrCache wraps Cache and returns an error from GetStruct.
type getErrCache struct{ sop.L2Cache }

func (g getErrCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return false, fmt.Errorf("getstruct err")
}

func (g getErrCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, duration time.Duration) ([]bool, error) {
	return nil, fmt.Errorf("getstruct err")
}

func Test_ItemActionTracker_CheckTrackedItems_Error_And_GetCompat(t *testing.T) {
	ctx := context.Background()

	// (a) error path
	errCache := struct{ sop.L2Cache }{L2Cache: mocks.NewMockClient()}
	si1 := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_chk_err", SlotLength: 2})
	trk1 := newItemActionTracker[PersonKey, Person](si1, getErrCache{L2Cache: errCache.L2Cache}, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id1 := sop.NewUUID()
	pk1, p1 := newPerson("a", "b", "m", "a@x", "p")
	it1 := &btree.Item[PersonKey, Person]{ID: id1, Key: pk1, Value: &p1, Version: 1}
	_ = trk1.Update(ctx, it1)
	if err := trk1.checkTrackedItems(ctx); err == nil {
		t.Fatalf("expected error from GetStruct in checkTrackedItems")
	}

	// (b) get-get compatibility
	l2 := mocks.NewMockClient()
	si2 := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_chk_getget", SlotLength: 2})
	trk2 := newItemActionTracker[PersonKey, Person](si2, l2, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id2 := sop.NewUUID()
	pk2, p2 := newPerson("c", "d", "m", "c@x", "p")
	it2 := &btree.Item[PersonKey, Person]{ID: id2, Key: pk2, Value: &p2, Version: 1}
	// Track as get
	it2.ValueNeedsFetch = false
	if err := trk2.Get(ctx, it2); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	// Seed redis with a different owner but action getAction
	lr := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	_ = l2.SetStruct(ctx, l2.FormatLockKey(id2.String()), &lr, time.Minute)
	if err := trk2.checkTrackedItems(ctx); err != nil {
		t.Fatalf("get-get compatibility should pass, err: %v", err)
	}
}

// Covers refetchAndMergeClosure updateAction separate-segment success path.
func Test_RefetchAndMerge_Update_SeparateSegment_Succeeds_Alt(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_upd_sep", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatalf("btree.New error: %v", err)
	}

	// Seed an item then prepare an update with a new item ID (separate segment behavior)
	pk, pv := newPerson("uu", "vv", "m", "u@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	// Persist StoreInfo refresh
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	// Seed MRU and Handle for root so repo.get can fetch after refetch resets caches
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		gc.SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		gc.Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}
	// New value under new item ID to be merged
	_, pv2 := newPerson("uu", "vv", "m", "u2@x", "p")
	newID := sop.NewUUID()
	ci := cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &btree.Item[PersonKey, Person]{ID: newID, Key: pk, Value: &pv2, Version: cur.Version},
		versionInDB: cur.Version,
	}
	// Use the existing current item ID as map key so FindWithID(uuid) finds the record.
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = ci

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err != nil {
		t.Fatalf("refetch update separate-segment err: %v", err)
	}
	// Expect tracker to have migrated to newID and mark persisted
	got, ok := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[newID]
	if !ok || !got.persisted {
		t.Fatalf("expected persisted=true under newID after merge; ok=%v persisted=%v", ok, got.persisted)
	}
}

// removeErrTL is a TransactionLog stub whose Remove returns an error to cover rollback's final Remove path.
type removeErrTL struct{}

func (removeErrTL) PriorityLog() sop.TransactionPriorityLog { return noOpPrioLog{} }
func (removeErrTL) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return nil
}
func (removeErrTL) Remove(ctx context.Context, tid sop.UUID) error {
	return fmt.Errorf("forced remove error")
}
func (removeErrTL) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (removeErrTL) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (removeErrTL) NewUUID() sop.UUID { return sop.NewUUID() }

// Ensures rollback returns the error from final TransactionLog.Remove when no branch returns earlier.
func Test_TransactionLogger_Rollback_FinalRemove_Error_Returned(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(removeErrTL{}, true)
	// Arrange minimal logs that don't trigger early returns and don't touch tx fields.
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitTrackedItemsValues, Value: nil},
	}
	if err := tl.rollback(ctx, &Transaction{}, sop.NewUUID(), logs); err == nil || err.Error() != "forced remove error" {
		t.Fatalf("expected forced remove error, got %v", err)
	}
}

// Covers priorityRollback happy path: non-empty payload -> UpdateNoLocks succeeds -> Remove called.
func Test_TransactionLogger_PriorityRollback_Success_RemovesLog(t *testing.T) {
	ctx := context.Background()
	tid := sop.NewUUID()
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	pl := &stubPriorityLog2{payload: map[string][]sop.RegistryPayload[sop.Handle]{
		tid.String(): {{RegistryTable: "rt", IDs: []sop.Handle{h}}},
	}}
	tl := &transactionLog{TransactionLog: stubTLog2{pl: pl}, logging: true}
	tx := &Transaction{registry: mocks.NewMockRegistry(false)}

	if err := tl.priorityRollback(ctx, tx.registry, tid); err != nil {
		t.Fatalf("priorityRollback err: %v", err)
	}
	if pl.removed[tid.String()] == 0 {
		t.Fatalf("expected Remove called on priority log")
	}
}

// Covers itemActionTracker.commitTrackedItemsValues value persistence and global cache update.
func Test_ItemActionTracker_CommitTrackedItemsValues_AddsAndCaches(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_commit_vals", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](ns, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Seed tracker with one item for add/update with a value present.
	id := sop.NewUUID()
	pk, pv := newPerson("cf", "cl", "m", "e@c", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv, Version: 1}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        it,
		versionInDB: it.Version,
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	// Value should be in blob store and cache.
	if ba, _ := bs.GetOne(ctx, ns.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob persisted for item")
	}
	var got Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &got); !ok {
		t.Fatalf("expected value cached in global cache")
	}
}

// Covers Add actively-persisted branch: logs pre-commit, persists value, and updates cache.
func Test_ItemActionTracker_Add_ActivelyPersisted_PersistsAndCaches_Alt(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_add_active", SlotLength: 4, IsUnique: true, IsValueDataActivelyPersisted: true, IsValueDataGloballyCached: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rec := &tlRecorder{tid: sop.NewUUID()}
	tl := &transactionLog{TransactionLog: rec, logging: true, transactionID: rec.tid}
	trk := newItemActionTracker[PersonKey, Person](ns, l2, bs, tl)

	pk, pv := newPerson("aa", "bb", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 1}

	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Should have logged addActivelyPersistedItem
	hit := false
	for _, kv := range rec.added {
		if kv.Key == addActivelyPersistedItem {
			hit = true
			break
		}
	}
	if !hit {
		t.Fatalf("expected addActivelyPersistedItem to be logged")
	}
	// Blob should exist and cache should have the value.
	if ba, _ := bs.GetOne(ctx, ns.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob stored for actively persisted Add")
	}
	var got Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &got); !ok {
		t.Fatalf("expected cache to be set for actively persisted Add")
	}
}

// Covers Update path when item already tracked: version increments when equal to versionInDB and active persist occurs.
func Test_ItemActionTracker_Update_Existing_IncrementsVersion(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_upd_active", SlotLength: 4, IsUnique: true, IsValueDataActivelyPersisted: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](ns, l2, bs, tl)

	// Seed existing tracked item with versionInDB matching the item's version.
	id := sop.NewUUID()
	pk, pv := newPerson("uu", "vv", "m", "e@u", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv, Version: 2}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: getAction},
		item:        it,
		versionInDB: 2,
	}

	// Call Update; since item.Version == versionInDB, it should increment.
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if it.Version != 3 {
		t.Fatalf("expected version increment to 3, got %d", it.Version)
	}
}

// Early return when IsValueDataInNodeSegment = true.
func Test_ItemActionTracker_CommitTrackedItemsValues_InNodeSegment_EarlyReturn(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_vals_node_segment", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](ns, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	// Seed one item; should be ignored due to early-return.
	id := sop.NewUUID()
	pk, pv := newPerson("er", "ns", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv}
	trk.items[id] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction}, item: it}
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues early-return err: %v", err)
	}
}

// Early return when IsValueDataActivelyPersisted = true.
func Test_ItemActionTracker_CommitTrackedItemsValues_ActivelyPersisted_EarlyReturn(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_vals_active", SlotLength: 4, IsValueDataActivelyPersisted: true}
	ns := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](ns, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues active early-return err: %v", err)
	}
}

// Update when item is not tracked yet and IsValueDataActivelyPersisted=true: increments version and persists.
func Test_ItemActionTracker_Update_Untracked_ActivelyPersisted_Persists(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_upd_untracked_active", SlotLength: 4, IsValueDataActivelyPersisted: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](ns, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), true))

	pk, pv := newPerson("ux", "uy", "m", "e@u", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 0}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if it.Version == 0 {
		t.Fatalf("expected version to increment on update of untracked item")
	}
	if ba, _ := bs.GetOne(ctx, ns.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob persisted for updated item")
	}
}

// Add when not actively persisted: only increments version and does not persist value.
func Test_ItemActionTracker_Add_NotActivelyPersisted_NoBlob(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_add_non_active", SlotLength: 4, IsValueDataActivelyPersisted: false}
	ns := sop.NewStoreInfo(so)
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](ns, mocks.NewMockClient(), bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("na", "nb", "m", "e@a", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 1}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if it.Version != 2 {
		t.Fatalf("expected version increment to 2, got %d", it.Version)
	}
	if ba, _ := bs.GetOne(ctx, ns.BlobTable, it.ID); len(ba) != 0 {
		t.Fatalf("expected no blob persisted when not actively persisted")
	}
}

// errIsLockedCache13 wraps a cache and forces IsLocked to return an error to hit acquireLocks' error branch.
type errIsLockedCache13 struct {
	inner sop.L2Cache
	err   error
}

func (c errIsLockedCache13) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c errIsLockedCache13) Set(ctx context.Context, key, value string, expiration time.Duration) error {
	return c.inner.Set(ctx, key, value, expiration)
}
func (c errIsLockedCache13) Get(ctx context.Context, key string) (bool, string, error) {
	return c.inner.Get(ctx, key)
}
func (c errIsLockedCache13) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return c.inner.GetEx(ctx, key, expiration)
}
func (c errIsLockedCache13) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.inner.SetStruct(ctx, key, value, expiration)
}
func (c errIsLockedCache13) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.inner.SetStructs(ctx, keys, values, expiration)
}
func (c errIsLockedCache13) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return c.inner.GetStruct(ctx, key, target)
}
func (c errIsLockedCache13) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return c.inner.GetStructEx(ctx, key, target, expiration)
}
func (c errIsLockedCache13) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.inner.GetStructs(ctx, keys, targets, expiration)
}
func (c errIsLockedCache13) Delete(ctx context.Context, keys []string) (bool, error) {
	return c.inner.Delete(ctx, keys)
}
func (c errIsLockedCache13) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c errIsLockedCache13) FormatLockKey(k string) string  { return c.inner.FormatLockKey(k) }
func (c errIsLockedCache13) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.inner.CreateLockKeys(keys)
}
func (c errIsLockedCache13) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.inner.CreateLockKeysForIDs(keys)
}
func (c errIsLockedCache13) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return c.inner.IsLockedTTL(ctx, duration, lockKeys)
}
func (c errIsLockedCache13) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return c.inner.Lock(ctx, duration, lockKeys)
}
func (c errIsLockedCache13) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := c.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := c.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (c errIsLockedCache13) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, c.err
}
func (c errIsLockedCache13) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return c.inner.IsLockedByOthers(ctx, lockKeyNames)
}
func (c errIsLockedCache13) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return c.inner.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (c errIsLockedCache13) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return c.inner.Unlock(ctx, lockKeys)
}
func (c errIsLockedCache13) Clear(ctx context.Context) error { return c.inner.Clear(ctx) }
func (c errIsLockedCache13) IsRestarted(ctx context.Context) bool {
	return c.inner.IsRestarted(ctx)
}

// errGetExCache13 wraps a cache and forces GetEx to error to hit acquireLocks' takeover GetEx error path.
type errGetExCache13 struct {
	inner sop.L2Cache
	err   error
}

func (c errGetExCache13) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c errGetExCache13) Set(ctx context.Context, key, value string, expiration time.Duration) error {
	return c.inner.Set(ctx, key, value, expiration)
}
func (c errGetExCache13) Get(ctx context.Context, key string) (bool, string, error) {
	return c.inner.Get(ctx, key)
}
func (c errGetExCache13) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return false, "", c.err
}
func (c errGetExCache13) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.inner.SetStruct(ctx, key, value, expiration)
}
func (c errGetExCache13) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.inner.SetStructs(ctx, keys, values, expiration)
}
func (c errGetExCache13) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return c.inner.GetStruct(ctx, key, target)
}
func (c errGetExCache13) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return c.inner.GetStructEx(ctx, key, target, expiration)
}
func (c errGetExCache13) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.inner.GetStructs(ctx, keys, targets, expiration)
}
func (c errGetExCache13) Delete(ctx context.Context, keys []string) (bool, error) {
	return c.inner.Delete(ctx, keys)
}
func (c errGetExCache13) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c errGetExCache13) FormatLockKey(k string) string  { return c.inner.FormatLockKey(k) }
func (c errGetExCache13) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.inner.CreateLockKeys(keys)
}
func (c errGetExCache13) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.inner.CreateLockKeysForIDs(keys)
}
func (c errGetExCache13) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return c.inner.IsLockedTTL(ctx, duration, lockKeys)
}
func (c errGetExCache13) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return c.inner.Lock(ctx, duration, lockKeys)
}
func (c errGetExCache13) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := c.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := c.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (c errGetExCache13) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return c.inner.IsLocked(ctx, lockKeys)
}
func (c errGetExCache13) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return c.inner.IsLockedByOthers(ctx, lockKeyNames)
}
func (c errGetExCache13) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return c.inner.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (c errGetExCache13) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return c.inner.Unlock(ctx, lockKeys)
}
func (c errGetExCache13) Clear(ctx context.Context) error { return c.inner.Clear(ctx) }
func (c errGetExCache13) IsRestarted(ctx context.Context) bool {
	return c.inner.IsRestarted(ctx)
}

func Test_TransactionLogger_AcquireLocks_IsLocked_Error_UnlocksAndReturns(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := errIsLockedCache13{inner: base, err: errors.New("islocked failed")}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{l2Cache: l2}

	// Two IDs to build locks for
	id1, id2 := sop.NewUUID(), sop.NewUUID()
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(id1), sop.NewHandle(id2)}}}
	// Force Lock to succeed so IsLocked is reached, then error returned.
	if _, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores); err == nil || err.Error() != "islocked failed" {
		t.Fatalf("expected IsLocked error returned, got: %v", err)
	}
}

func Test_TransactionLogger_AcquireLocks_GetEx_Error_Returned(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := errGetExCache13{inner: base, err: errors.New("getex boom")}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{l2Cache: l2}

	// Prepare two IDs and seed lock ownership to the same tid so takeover path runs and calls GetEx.
	tid := sop.NewUUID()
	id1, id2 := sop.NewUUID(), sop.NewUUID()
	_ = base.Set(ctx, base.FormatLockKey(id1.String()), tid.String(), time.Minute)
	_ = base.Set(ctx, base.FormatLockKey(id2.String()), tid.String(), time.Minute)

	stores := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(id1), sop.NewHandle(id2)}}}
	if _, err := tl.acquireLocks(ctx, tx, tid, stores); err == nil || err.Error() != "getex boom" {
		t.Fatalf("expected GetEx error returned, got: %v", err)
	}
}

// plGetErr simulates PriorityLog.Get error.
type plGetErr struct{ e error }

func (p plGetErr) IsEnabled() bool                                             { return true }
func (p plGetErr) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (p plGetErr) Remove(ctx context.Context, tid sop.UUID) error              { return nil }
func (p plGetErr) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, p.e
}
func (p plGetErr) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (p plGetErr) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (p plGetErr) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}

// plRemoveErr returns payload but Remove returns error to propagate.
type plRemoveErr struct {
	payload []sop.RegistryPayload[sop.Handle]
	e       error
}

func (p plRemoveErr) IsEnabled() bool                                             { return true }
func (p plRemoveErr) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (p plRemoveErr) Remove(ctx context.Context, tid sop.UUID) error              { return p.e }
func (p plRemoveErr) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return p.payload, nil
}
func (p plRemoveErr) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (p plRemoveErr) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (p plRemoveErr) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}

func Test_TransactionLogger_PriorityRollback_Get_Error_Propagated(t *testing.T) {
	ctx := context.Background()
	baseTL := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	tl := newTransactionLogger(tlWithPL{inner: baseTL, pl: plGetErr{e: errors.New("get failed")}}, true)
	if err := tl.priorityRollback(ctx, nil, sop.NewUUID()); err == nil || err.Error() != "get failed" {
		t.Fatalf("expected get failed error, got: %v", err)
	}
}

func Test_TransactionLogger_PriorityRollback_Remove_Error_Propagated(t *testing.T) {
	ctx := context.Background()
	// Build a payload with one handle so UpdateNoLocks succeeds on a working registry.
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	payload := []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}
	baseTL := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	tl := newTransactionLogger(tlWithPL{inner: baseTL, pl: plRemoveErr{payload: payload, e: errors.New("remove failed")}}, true)
	tx := &Transaction{registry: mocks.NewMockRegistry(false)}
	if err := tl.priorityRollback(ctx, tx.registry, sop.NewUUID()); err == nil || err.Error() != "remove failed" {
		t.Fatalf("expected remove failed error, got: %v", err)
	}
}

// failingBlobStoreAddIAT returns an error from Add to exercise commitTrackedItemsValues error propagation.
type failingBlobStoreAddIAT struct{ sop.BlobStore }

func (f failingBlobStoreAddIAT) Add(ctx context.Context, payload []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return errors.New("blob add err")
}

func Test_ItemActionTracker_CommitTrackedItemsValues_Add_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	// Store with values not in node segment and not actively persisted, so commitTrackedItemsValues runs.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_commit_err", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), failingBlobStoreAddIAT{}, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Seed one item with a value so it will be added to itemsForAdd.
	pk, p := newPerson("cb14", "c", "m", "a@b", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p, Version: 1}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}

	if err := trk.commitTrackedItemsValues(ctx); err == nil || err.Error() != "blob add err" {
		t.Fatalf("expected blob add err, got: %v", err)
	}
}

// Ensures finalizeCommit with nil value and lastCommittedFunctionLog < deleteObsoleteEntries hits the continue branch
// (no immediate Remove), and function completes without error.
func Test_TransactionLogger_Rollback_FinalizeNil_ContinuePath(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tnx := &Transaction{}

	// Build logs where lastCommittedFunctionLog is commitAddedNodes (< deleteObsoleteEntries),
	// and include a finalizeCommit with nil value to exercise the continue path.
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: nil},
		{Key: commitAddedNodes, Value: nil}, // lastCommittedFunctionLog
	}

	if err := tl.rollback(ctx, tnx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("unexpected error in rollback finalize-nil continue path: %v", err)
	}
}

// errStoreRepo wraps a StoreRepository and forces Update to return an error.
type errStoreRepo2 struct{ inner sop.StoreRepository }

func (e errStoreRepo2) Add(ctx context.Context, stores ...sop.StoreInfo) error {
	return e.inner.Add(ctx, stores...)
}
func (e errStoreRepo2) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, fmt.Errorf("update repo err")
}
func (e errStoreRepo2) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return e.inner.Get(ctx, names...)
}
func (e errStoreRepo2) GetAll(ctx context.Context) ([]string, error) { return e.inner.GetAll(ctx) }
func (e errStoreRepo2) GetWithTTL(ctx context.Context, isCacheTTL bool, cacheDuration time.Duration, names ...string) ([]sop.StoreInfo, error) {
	// The mocks use time.Duration; adapt using seconds for compatibility if needed.
	return e.inner.GetWithTTL(ctx, isCacheTTL, 0, names...)
}
func (e errStoreRepo2) Remove(ctx context.Context, names ...string) error {
	return e.inner.Remove(ctx, names...)
}
func (e errStoreRepo2) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error {
	return e.inner.Replicate(ctx, storesInfo)
}

func Test_TransactionLogger_Rollback_CommitStoreInfo_Update_Error(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	// Wire a transaction with a StoreRepository whose Update errors.
	tx := &Transaction{StoreRepository: errStoreRepo2{inner: mocks.NewMockStoreRepository()}}

	sis := []sop.StoreInfo{*sop.NewStoreInfo(sop.StoreOptions{Name: "csinfo_err", SlotLength: 2})}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitStoreInfo, Value: toByteArray(sis)},
		{Key: finalizeCommit, Value: nil}, // make lastCommittedFunctionLog > commitStoreInfo to trigger Update
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err == nil || err.Error() != "update repo err" {
		t.Fatalf("expected update repo err, got: %v", err)
	}
}

// Ensure finalize-with-payload where lastCommittedFunctionLog == deleteTrackedItemsValues propagates error from deleteTrackedItemsValues.
func Test_TransactionLogger_Rollback_FinalizeWithPayload_DeletesTrackedItemsValues_Error(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Use errBlobStore2 to make blob Remove fail; also provide l1 cache and registry for deleteObsoleteEntries call.
	l2 := mocks.NewMockClient()
	l1 := cache.NewL1Cache(l2, cache.DefaultMinCapacity, cache.DefaultMaxCapacity)
	tx := &Transaction{blobStore: errBlobStore2{BlobStore: mocks.NewMockBlobStore()}, l2Cache: l2, l1Cache: l1, registry: mocks.NewMockRegistry(false)}

	items := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
		{First: false, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}}},
	}
	pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{},
		Second: items,
	}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: toByteArray(pl)},
		{Key: deleteTrackedItemsValues, Value: nil},
	}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err == nil || err.Error() != "forced remove error" {
		t.Fatalf("expected forced remove error, got: %v", err)
	}
}
