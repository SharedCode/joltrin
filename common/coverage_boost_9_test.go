package common

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// fetchedNilRepo wraps NodeRepository and clears node slots right after Fetched is called,
// so subsequent UpdateCurrentNodeItem sees a nil slot and returns false without error.
type fetchedNilRepo struct {
	inner   btree.NodeRepository[PersonKey, Person]
	backend *nodeRepositoryBackend
}

func (t *fetchedNilRepo) Add(n *btree.Node[PersonKey, Person])    { t.inner.Add(n) }
func (t *fetchedNilRepo) Update(n *btree.Node[PersonKey, Person]) { t.inner.Update(n) }
func (t *fetchedNilRepo) Remove(id sop.UUID)                      { t.inner.Remove(id) }
func (t *fetchedNilRepo) Get(ctx context.Context, id sop.UUID) (*btree.Node[PersonKey, Person], error) {
	return t.inner.Get(ctx, id)
}
func (t *fetchedNilRepo) Fetched(id sop.UUID) {
	t.inner.Fetched(id)
	if cn, ok := t.backend.localCache[id]; ok && cn.node != nil {
		if n, ok2 := cn.node.(*btree.Node[PersonKey, Person]); ok2 {
			for i := range n.Slots {
				n.Slots[i] = btree.Item[PersonKey, Person]{}
			}
			n.Count = 0
		}
	}
}

// Triggers refetchAndMerge updateAction path for separate segment and forces UpdateCurrentNodeItem
// to return false (no error) so the closure surfaces a merge failure error.
func Test_RefetchAndMerge_Update_SeparateSegment_FalseWithoutError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_update_sep_false", SlotLength: 4, IsValueDataInNodeSegment: false}
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

	// Seed one item and persist root so refetch can load from backend.
	pk, pv := newPerson("u3", "v3", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Locate and prepare a tracked update with a new inflight ID.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newID := sop.NewUUID()
	inflight := &btree.Item[PersonKey, Person]{ID: newID, Key: pk, Value: nil, Version: cur.Version}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        inflight,
		versionInDB: cur.Version,
	}

	// Swap NodeRepository with a wrapper that clears slots right after Fetched during GetCurrentItem.
	si.NodeRepository = &fetchedNilRepo{inner: si.NodeRepository, backend: si.backendNodeRepository}

	// Execute refetch and merge; expect merge update error due to UpdateCurrentNodeItem=false.
	err = refetchAndMergeClosure(&si, b3, sr)(ctx)
	if err == nil || !strings.Contains(err.Error(), "failed to merge update item") {
		t.Fatalf("expected update merge failure, got: %v", err)
	}
}

// Same as above but for in-node segment: force UpdateCurrentItem to return false (no error)
// by clearing the node slots after Fetched, then expect merge update error.
func Test_RefetchAndMerge_Update_InNodeSegment_FalseWithoutError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_update_innode_false", SlotLength: 4, IsValueDataInNodeSegment: true}
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

	// Seed one item and persist root so refetch can load from backend.
	pk, pv := newPerson("u4", "v4", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Track an update with in-node value present.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newVal := pv
	newVal.Phone = "nv"
	inflight := &btree.Item[PersonKey, Person]{ID: cur.ID, Key: pk, Value: &newVal, Version: cur.Version}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        inflight,
		versionInDB: cur.Version,
	}

	// Wrap NodeRepository so Fetched clears slots before UpdateCurrentItem.
	si.NodeRepository = &fetchedNilRepo{inner: si.NodeRepository, backend: si.backendNodeRepository}

	err = refetchAndMergeClosure(&si, b3, sr)(ctx)
	if err == nil || !strings.Contains(err.Error(), "failed to merge update item") {
		t.Fatalf("expected update merge failure (in-node), got: %v", err)
	}
}

// Ensures rollback processes finalizeCommit with non-nil payload and when
// lastCommittedFunctionLog >= deleteObsoleteEntries, it executes deleteObsoleteEntries
// and removes the transaction logs without error.
func Test_TransactionLogger_Rollback_Finalize_DeleteObsoleteEntries_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{
		blobStore:       mocks.NewMockBlobStore(),
		l2Cache:         l2,
		l1Cache:         cache.GetGlobalL1Cache(l2),
		registry:        mocks.NewMockRegistry(false),
		StoreRepository: mocks.NewMockStoreRepository(),
	}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tid := sop.NewUUID()

	// Build finalize payload: registry IDs and unused node IDs lists (non-empty)
	storesForDelete := []sop.RegistryPayload[sop.UUID]{
		{RegistryTable: "rt", IDs: []sop.UUID{sop.NewUUID()}},
	}
	nodesForDelete := []sop.BlobsPayload[sop.UUID]{
		{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}},
	}
	fin := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First: sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
			First:  storesForDelete,
			Second: nodesForDelete,
		},
	}

	// Mark lastCommittedFunctionLog as deleteObsoleteEntries by ordering logs accordingly.
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: toByteArray(fin)},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	if err := tl.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("expected rollback to succeed, got: %v", err)
	}
}

// Ensures rollback processes a committed commitTrackedItemsValues log by deleting
// referenced blobs and cache entries (when globally cached flag is true).
func Test_TransactionLogger_Rollback_Committed_CommitTrackedItemsValues_Deletes(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: mocks.NewMockRegistry(false), StoreRepository: mocks.NewMockStoreRepository()}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	// Seed a blob and a cached value for the same ID
	id := sop.NewUUID()
	bt := "bt"
	payload := []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: bt, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("x")}}}}
	if err := bs.Add(ctx, payload); err != nil {
		t.Fatalf("seed blob add err: %v", err)
	}
	if err := l2.SetStruct(ctx, formatItemKey(id.String()), &Person{Email: "e"}, time.Minute); err != nil {
		t.Fatalf("seed cache set err: %v", err)
	}

	// Build commitTrackedItemsValues log as the last committed function
	cfv := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: bt, Blobs: []sop.UUID{id}}}}
	logs := []sop.KeyValuePair[int, []byte]{{Key: commitTrackedItemsValues, Value: toByteArray(cfv)}}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}

	// Verify cache entry was deleted
	var p Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(id.String()), &p); ok {
		t.Fatalf("expected cache deletion for %s", id.String())
	}
	// Verify blob was removed
	if ba, _ := bs.GetOne(ctx, bt, id); ba != nil {
		t.Fatalf("expected blob deletion for %s", id.String())
	}
}

// Ensures refetchAndMergeClosure returns an error when RemoveCurrentItem returns false (no error)
// during removeAction replay by clearing node slots after Fetched.
func Test_RefetchAndMerge_Remove_FalseWithoutError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_remove_false", SlotLength: 4, IsValueDataInNodeSegment: true}
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

	// Seed one item and persist root so refetch can load from backend.
	pk, pv := newPerson("rm", "sp", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Track a remove action for the existing item.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        &cur,
		versionInDB: cur.Version,
	}

	// Wrap NodeRepository so Fetched clears slots before RemoveCurrentItem is invoked.
	si.NodeRepository = &fetchedNilRepo{inner: si.NodeRepository, backend: si.backendNodeRepository}

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err == nil || !strings.Contains(err.Error(), "failed to merge remove item") {
		t.Fatalf("expected remove merge failure error, got: %v", err)
	}
}

// Ensures rollback processes finalizeCommit with non-nil payload when the
// last committed function is deleteTrackedItemsValues; it should delete cached
// value entries and blobs referenced in the payload and remove logs.
func Test_TransactionLogger_Rollback_Finalize_DeleteTrackedItemsValues_Deletes(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: mocks.NewMockRegistry(false), StoreRepository: mocks.NewMockStoreRepository()}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	id := sop.NewUUID()
	bt := "bt2"
	// Seed blob and cache
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: bt, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("x")}}}})
	_ = l2.SetStruct(ctx, formatItemKey(id.String()), &Person{Email: "y"}, time.Minute)

	// Build finalize payload where Second contains tracked items for deletion
	delTracked := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: bt, Blobs: []sop.UUID{id}}}}
	fin := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		Second: delTracked,
	}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: toByteArray(fin)},
		{Key: deleteTrackedItemsValues, Value: nil},
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	var p Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(id.String()), &p); ok {
		t.Fatalf("expected cache deletion for %s", id.String())
	}
	if ba, _ := bs.GetOne(ctx, bt, id); ba != nil {
		t.Fatalf("expected blob deletion for %s", id.String())
	}
}

// Early-return: IsValueDataInNodeSegment true should no-op in commitTrackedItemsValues.
func Test_CommitTrackedItemsValues_EarlyReturn_InNodeSegment_NoOp(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "cv_innode_noop", SlotLength: 2, IsValueDataInNodeSegment: true})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Seed an add/update tracked item; since early return, it shouldn't be processed.
	pk, pv := newPerson("ei", "vv", "m", "a@b", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 1}
	_ = trk.Add(ctx, it)
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Value should remain intact (not moved to blob) because no-op occurred.
	got := trk.items[it.ID]
	if got.item.Value == nil {
		t.Fatalf("expected value to remain; early return should not nullify")
	}
}

// Early-return: IsValueDataActivelyPersisted true should no-op in commitTrackedItemsValues.
func Test_CommitTrackedItemsValues_EarlyReturn_ActivelyPersisted_NoOp(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "cv_act_noop", SlotLength: 2, IsValueDataActivelyPersisted: true})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Nothing to process; commitTrackedItemsValues should short-circuit and return nil.
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// RefetchAndMerge: getAction branch should no-op and return nil.
func Test_RefetchAndMerge_GetAction_NoOp_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_get_noop", SlotLength: 4, IsValueDataInNodeSegment: true}
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

	// Seed item and persist root.
	pk, pv := newPerson("gg", "vv", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{cn.node}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Track a get action.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cur.ValueNeedsFetch = false
	if err := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).Get(ctx, &cur); err != nil {
		t.Fatalf("tracker.Get err: %v", err)
	}

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err != nil {
		t.Fatalf("unexpected error on get-action merge: %v", err)
	}
}

// Ensures commitTrackedItemsValues writes blobs and caches values when globally cached is true.
func Test_CommitTrackedItemsValues_WritesBlobs_And_Caches(t *testing.T) {
	ctx := context.Background()
	// Separate-segment values, globally cached
	so := sop.StoreOptions{Name: "iat_commit_vals", SlotLength: 2, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Two tracked items with values
	pk1, pv1 := newPerson("c1", "d1", "m", "a@b", "p")
	it1 := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk1, Value: &pv1, Version: 1}
	_ = trk.Add(ctx, it1)
	pk2, pv2 := newPerson("c2", "d2", "m", "a2@b", "p")
	it2 := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk2, Value: &pv2, Version: 3}
	_ = trk.Update(ctx, it2)

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}

	// Validate blobs and cache exist for all items now marked ValueNeedsFetch.
	validated := 0
	for _, ci := range trk.items {
		if ci.item == nil {
			continue
		}
		if !ci.item.ValueNeedsFetch {
			continue
		}
		id := ci.item.ID
		if ba, _ := bs.GetOne(ctx, si.BlobTable, id); ba == nil {
			t.Fatalf("expected blob for %s", id.String())
		}
		var p Person
		if ok, _ := l2.GetStruct(ctx, formatItemKey(id.String()), &p); !ok {
			t.Fatalf("expected cache for %s", id.String())
		}
		validated++
	}
	if validated < 2 {
		t.Fatalf("expected to validate at least 2 items, got %d", validated)
	}
}

// Ensures manage path appends forDeletionItems for removeAction when ValueNeedsFetch is true.
func Test_ItemActionTracker_Manage_Remove_AppendsForDeletion(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_manage_remove", SlotLength: 2, IsValueDataInNodeSegment: false}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	// Insert a cached entry with removeAction and ValueNeedsFetch=true
	id := sop.NewUUID()
	pk, _ := newPerson("r1", "d1", "m", "r@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, Version: 1}
	it.ValueNeedsFetch = true
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        it,
		versionInDB: 1,
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}

	// Now getObsoleteTrackedItemsValues should include id
	obsolete := trk.getObsoleteTrackedItemsValues()
	if obsolete == nil {
		t.Fatalf("expected obsolete not nil")
	}
	found := false
	for _, bid := range obsolete.Blobs {
		if bid == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected id in obsolete list")
	}

	_ = ctx // silence unused if necessary
	_ = time.Minute
}

// errGetExCache wraps the mock cache to force Lock(false, owner=tid) and GetEx error during takeover.
type errGetExCache struct{ sop.L2Cache }

func (e errGetExCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	// Signal takeover path by returning owner tid equal to the transaction tid passed later.
	// We don't know tid here; acquireLocks compares with provided tid and ownerTID!=0 triggers else branch.
	// Return ok=false with a non-nil owner to simulate locked by the same tid; caller passes that tid.
	return false, sop.NewUUID(), nil
}
func (e errGetExCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return false, "", fmt.Errorf("getex err")
}

func Test_TransactionLogger_AcquireLocks_Takeover_GetEx_Error_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	ec := errGetExCache{L2Cache: base}
	tx := &Transaction{l2Cache: ec}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	// Prepare input handles
	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	// Pre-seed lock keys so takeover path is intended; use a deterministic tid and set keys to that tid.
	takeoverTID := sop.NewUUID()
	for _, h := range ids {
		_ = base.Set(ctx, base.FormatLockKey(h.LogicalID.String()), takeoverTID.String(), time.Minute)
	}

	// Call with the same tid to exercise takeover path; errGetExCache will raise error on GetEx.
	_, err := tl.acquireLocks(ctx, tx, takeoverTID, stores)
	if err == nil {
		t.Fatalf("expected error from GetEx during takeover")
	}
}

func Test_TransactionLogger_Rollback_NoLogs_RemovesTid_NoError(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Non-nil tid with no logs; Remove should be invoked and return nil.
	if err := tl.rollback(ctx, &Transaction{l2Cache: mocks.NewMockClient(), blobStore: mocks.NewMockBlobStore(), registry: mocks.NewMockRegistry(false), StoreRepository: mocks.NewMockStoreRepository()}, sop.NewUUID(), nil); err != nil {
		t.Fatalf("rollback with no logs should not error, got: %v", err)
	}
}

// isLockedErrCache forces Lock to succeed and IsLocked to return an error.
type isLockedErrCache struct{ sop.L2Cache }

func (c isLockedErrCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return true, sop.NilUUID, nil
}
func (c isLockedErrCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, fmt.Errorf("islocked err")
}

func (c isLockedErrCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := c.Lock(ctx, duration, lockKeys)
	if err != nil || !ok {
		return ok, tid, err
	}
	if _, err := c.IsLocked(ctx, lockKeys); err != nil {
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func Test_TransactionLogger_AcquireLocks_IsLocked_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	cache := isLockedErrCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: cache}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || !strings.Contains(err.Error(), "islocked err") {
		t.Fatalf("expected islocked err, got: %v", err)
	}
}

// failingBlobStoreGet returns an error from GetOne to force itemActionTracker.Get error path.
type failingBlobStoreGet struct{ sop.BlobStore }

func (f failingBlobStoreGet) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, fmt.Errorf("blob get err")
}

func Test_ItemActionTracker_Get_BlobStore_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_get_err", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), failingBlobStoreGet{}, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, _ := newPerson("ge", "vv", "m", "a@b", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil, Version: 1}
	it.ValueNeedsFetch = true

	if err := trk.Get(ctx, it); err == nil || err.Error() != "blob get err" {
		t.Fatalf("expected blob get err, got: %v", err)
	}
}

// lockErrCache makes Lock return an explicit error to exercise the error path in acquireLocks.
type lockErrCache2 struct{ sop.L2Cache }

func (c lockErrCache2) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("lock failed")
}

func (c lockErrCache2) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return c.Lock(ctx, duration, lockKeys)
}

// ownerMismatchCache makes Lock report a different non-nil ownerTID to trigger the owner mismatch branch.
type ownerMismatchCache struct{ sop.L2Cache }

var _ownerMismatchTID = sop.NewUUID()

func (c ownerMismatchCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, _ownerMismatchTID, nil
}

func (c ownerMismatchCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return c.Lock(ctx, duration, lockKeys)
}

func Test_TransactionLogger_AcquireLocks_Lock_Error_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	// Embed mock to reuse CreateLockKeys/Unlock behavior.
	l2 := lockErrCache2{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || !strings.Contains(err.Error(), "lock failed") {
		t.Fatalf("expected lock failed error, got: %v", err)
	}
}

func Test_TransactionLogger_AcquireLocks_OwnerMismatch_ReturnsFailover(t *testing.T) {
	ctx := context.Background()
	l2 := ownerMismatchCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tid := sop.NewUUID()
	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	_, err := tl.acquireLocks(ctx, tx, tid, stores)
	if err == nil {
		t.Fatalf("expected error due to owner mismatch, got nil")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got: %v", err)
	}
}

func Test_TransactionLogger_Rollback_CommitStoreInfo_UpdatesRepo(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tnx := &Transaction{StoreRepository: mocks.NewMockStoreRepository()}

	// Create a commitStoreInfo log entry with valid payload and ensure it's processed when a later stage exists.
	sis := []sop.StoreInfo{*sop.NewStoreInfo(sop.StoreOptions{Name: "csinfo", SlotLength: 2})}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitStoreInfo, Value: toByteArray(sis)},
		{Key: commitAddedNodes, Value: nil}, // lastCommittedFunctionLog > commitStoreInfo
	}

	if err := tl.rollback(ctx, tnx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("unexpected error in rollback commitStoreInfo: %v", err)
	}
}
