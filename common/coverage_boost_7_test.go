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

// Covers refetchAndMergeClosure remove path when values are in a separate segment.
func Test_RefetchAndMerge_Remove_SeparateSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_rm_sep", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false}
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

	// Seed an item; persist StoreInfo; seed MRU and Handle for root
	pk, pv := newPerson("rm", "sep", "m", "x@y", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	// Sync StoreRepository and MRU/Handle so refetch can find root after cache reset
	upd := *ns
	upd.RootNodeID = b3.StoreInfo.RootNodeID
	upd.Count = b3.StoreInfo.Count
	_ = sr.Add(ctx, upd)
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		cache.GetGlobalL1Cache(l2).SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		cache.GetGlobalL1Cache(l2).Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}

	// Track removal action
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        &cur,
		versionInDB: cur.Version,
	}

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err != nil {
		t.Fatalf("refetch remove separate-segment err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); ok {
		t.Fatal("expected item removed after refetch+remove (separate segment)")
	}
}

// blobStoreSpy tracks Add calls; used to assert no-op when actively persisted values are enabled.
type blobStoreSpy struct{ adds int }

func (s *blobStoreSpy) GetOne(ctx context.Context, blobTable string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (s *blobStoreSpy) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	s.adds++
	return nil
}
func (s *blobStoreSpy) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (s *blobStoreSpy) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return nil
}

// Covers commitTrackedItemsValues early-return when IsValueDataActivelyPersisted=true.
func Test_CommitTrackedItemsValues_ActivelyPersisted_NoOp(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_active_noop", SlotLength: 2, IsValueDataInNodeSegment: false, IsValueDataActivelyPersisted: true}
	ns := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	spy := &blobStoreSpy{}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	trk := newItemActionTracker[PersonKey, Person](ns, l2, spy, tl)
	// Seed a tracked item directly (avoid Add which actively persists in this mode)
	id := sop.NewUUID()
	pk, v := newPerson("a1", "b1", "m", "a1@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &v, Version: 1}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        it,
		versionInDB: it.Version,
	}
	// Commit values should be a no-op due to actively persisted flag
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	if spy.adds != 0 {
		t.Fatalf("expected no blob Add when actively persisted; got %d", spy.adds)
	}
	// Ensure tracked item still has same ID and non-nil value (no mutation occurred)
	ci, ok := trk.items[id]
	if !ok || ci.item == nil || ci.item.ID != id || ci.item.Value == nil {
		t.Fatalf("expected tracked item unchanged; ok=%v idOk=%v valNil=%v", ok, ci.item != nil && ci.item.ID == id, ci.item == nil || ci.item.Value == nil)
	}
}

// flipLockRefetchCache fails first lock attempt to force needsRefetchAndMerge, then succeeds.
type flipLockRefetchCache struct {
	sop.L2Cache
	first bool
}

func (f *flipLockRefetchCache) Lock(ctx context.Context, d time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if !f.first {
		f.first = true
		return false, sop.NilUUID, nil
	}
	return f.L2Cache.Lock(ctx, d, keys)
}

func (f *flipLockRefetchCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := f.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := f.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

// Ensures that if refetchAndMergeModifications returns error, phase1Commit propagates it.
func Test_Phase1Commit_RefetchError_Propagates(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &flipLockRefetchCache{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_refetch_err", SlotLength: 4, IsValueDataInNodeSegment: true})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}}
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return fmt.Errorf("refetch fail") },
	}}

	if err := tx.phase1Commit(ctx); err == nil || err.Error() != "refetch fail" {
		t.Fatalf("expected refetch error to propagate, got: %v", err)
	}
}

// addAlwaysErrReg makes Registry.Add return a plain error (non-sop) to assert propagation in phase1Commit.
type addAlwaysErrReg struct{ *mocks.Mock_vid_registry }

func (r addAlwaysErrReg) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return fmt.Errorf("add failed")
}

// Updated-nodes conflict (version mismatch) should cause retry via refetch and then succeed once versions align.
func Test_Phase1Commit_CommitUpdatedNodes_Conflict_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_updated_conflict_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	uid := sop.NewUUID()
	nr.localCache[uid] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: uid, Version: 1}}
	h := sop.NewHandle(uid)
	h.Version = 2 // mismatch -> conflict
	reg.Lookup[uid] = h

	refetched := false
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge: func(context.Context) error {
			refetched = true
			// Align version for success
			hh := reg.Lookup[uid]
			hh.Version = 1
			reg.Lookup[uid] = hh
			return nil
		},
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
	if !refetched {
		t.Fatalf("expected refetch to be invoked on updated conflict")
	}
}

// Added-nodes non-sop error from registry.Add should propagate immediately.
func Test_Phase1Commit_CommitAddedNodes_NonSopError_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := addAlwaysErrReg{Mock_vid_registry: baseReg}
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_add_non_sop_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	aid := sop.NewUUID()
	nr.localCache[aid] = cachedNode{action: addAction, node: &btree.Node[PersonKey, Person]{ID: aid, Version: 1}}

	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err == nil {
		t.Fatalf("expected non-sop error from registry.Add to propagate")
	}
}

// errOnceBlobStore errors on first Add, then delegates to inner.
type errOnceBlobStore struct {
	inner sop.BlobStore
	fired bool
}

func (e *errOnceBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return e.inner.GetOne(ctx, blobName, blobID)
}
func (e *errOnceBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	if !e.fired {
		e.fired = true
		return fmt.Errorf("blob add failed")
	}
	return e.inner.Add(ctx, storesblobs)
}
func (e *errOnceBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return e.inner.Update(ctx, storesblobs)
}
func (e *errOnceBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return e.inner.Remove(ctx, storesBlobsIDs)
}

// commitUpdatedNodes path: blob store Add error should propagate out of phase1Commit.
func Test_Phase1Commit_CommitUpdatedNodes_BlobAddError_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	inner := mocks.NewMockBlobStore()
	bs := &errOnceBlobStore{inner: inner}
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_updated_bloberr", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	uid := sop.NewUUID()
	nr.localCache[uid] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: uid, Version: 1}}
	h := sop.NewHandle(uid)
	h.Version = 1
	reg.Lookup[uid] = h

	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err == nil {
		t.Fatalf("expected blob add error to propagate out of phase1Commit")
	}
}

// Ensure rollback removes inactive blobs via commitUpdatedNodes payload.
func Test_TransactionLogger_Rollback_CommitUpdatedNodes_RemoveNodes(t *testing.T) {
	ctx := context.Background()
	// Prepare tx with nodeRepository wired for removeNodes
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	blobs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: blobs, registry: reg, StoreRepository: sr}

	// One store and btree backend to satisfy t.btreesBackend[0]
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_rm_nodes", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	// Seed blob and cache entry corresponding to an inactive blob ID
	inactive := sop.NewUUID()
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: inactive, Value: []byte("x")}}}})
	// Seed cache under the node key used by removeNodes (N<uuid>)
	type stub struct{ A int }
	_ = l2.SetStruct(ctx, nr.formatKey(inactive.String()), &stub{A: 1}, time.Minute)

	// Build commitUpdatedNodes payload of inactive blob IDs
	payload := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{inactive}}}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(commitUpdatedNodes), Value: toByteArray(payload)},
	}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	// Blob should be removed and cache entry deleted
	if ba, _ := blobs.GetOne(ctx, "bt", inactive); len(ba) != 0 {
		t.Fatalf("inactive blob not removed")
	}
	var x stub
	if ok, _ := l2.GetStruct(ctx, nr.formatKey(inactive.String()), &x); ok {
		t.Fatalf("cache entry not removed for inactive id")
	}
}

// recTLRemove counts Remove calls to assert log cleanup via rollback paths.
type recTLRemove struct {
	inner   sop.TransactionLog
	removes int
}

func (r *recTLRemove) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return r.inner.GetOne(ctx)
}
func (r *recTLRemove) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return r.inner.GetOneOfHour(ctx, hour)
}
func (r *recTLRemove) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return r.inner.Add(ctx, tid, commitFunction, payload)
}
func (r *recTLRemove) Remove(ctx context.Context, tid sop.UUID) error {
	r.removes++
	return r.inner.Remove(ctx, tid)
}
func (r *recTLRemove) NewUUID() sop.UUID                       { return r.inner.NewUUID() }
func (r *recTLRemove) PriorityLog() sop.TransactionPriorityLog { return r.inner.PriorityLog() }

// commitNewRootNodes rollback via tl.rollback: ensures cache deletion and registry unregister.
func Test_TransactionLogger_Rollback_CommitNewRootNodes_Path_Alt(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	sr := mocks.NewMockStoreRepository()

	// Wire transaction and backend
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: reg, StoreRepository: sr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_cnrn", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl
	// Mark committedState so rollbackNewRootNodes will unregister in registry.
	tl.committedState = commitRemovedNodes

	lid := sop.NewUUID()
	// Seed registry and L2 cache node key to observe effects
	reg.Lookup[lid] = sop.NewHandle(lid)
	_ = l2.Set(ctx, nr.formatKey(lid.String()), sop.NewUUID().String(), time.Minute)

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	// Arrange logs: last > commitNewRootNodes so branch executes
	logs := []sop.KeyValuePair[int, []byte]{
		// commitNewRootNodes happened, then later commitRemovedNodes making lastCommittedFunctionLog > commitNewRootNodes
		{Key: commitNewRootNodes, Value: toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs})},
		{Key: commitRemovedNodes, Value: nil},
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	// Registry entry should be removed
	if _, ok := reg.Lookup[lid]; ok {
		t.Fatalf("expected registry entry removed for %s", lid.String())
	}
	// Cache key should be deleted
	if ok, _, _ := l2.Get(ctx, nr.formatKey(lid.String())); ok {
		t.Fatalf("expected node cache key deleted for %s", lid.String())
	}
}

// commitRemovedNodes rollback via tl.rollback: clears deleted flag in registry.
func Test_TransactionLogger_Rollback_CommitRemovedNodes_Path(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: reg, blobStore: mocks.NewMockBlobStore(), StoreRepository: mocks.NewMockStoreRepository()}

	// Backend needed by tl.rollback
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_crn", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.IsDeleted = true
	reg.Lookup[lid] = h

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	logs := []sop.KeyValuePair[int, []byte]{
		// commitRemovedNodes occurred, then a later step commitAddedNodes finished
		{Key: commitRemovedNodes, Value: toByteArray(vids)},
		{Key: commitAddedNodes, Value: nil}, // lastCommittedFunctionLog > commitRemovedNodes
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	if reg.Lookup[lid].IsDeleted {
		t.Fatalf("expected IsDeleted=false after rollbackRemovedNodes")
	}
}

// commitAddedNodes rollback via tl.rollback: removes blobs, unregisters IDs, and clears cache.
func Test_TransactionLogger_Rollback_CommitAddedNodes_Path(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: reg, StoreRepository: mocks.NewMockStoreRepository()}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_can", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	lid := sop.NewUUID()
	reg.Lookup[lid] = sop.NewHandle(lid)
	// Seed blob and cache for the added node
	blobID := sop.NewUUID()
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: si.BlobTable, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: blobID, Value: []byte("x")}}}})
	_ = l2.Set(ctx, nr.formatKey(lid.String()), sop.NewUUID().String(), time.Minute)

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{blobID}}}
	logs := []sop.KeyValuePair[int, []byte]{
		// commitAddedNodes then later commitStoreInfo
		{Key: commitAddedNodes, Value: toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs})},
		{Key: commitStoreInfo, Value: nil}, // last > commitAddedNodes
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	// Blob removed
	if ba, _ := bs.GetOne(ctx, si.BlobTable, blobID); len(ba) != 0 {
		t.Fatalf("expected blob removed for added node")
	}
	// Registry unregistered
	if _, ok := reg.Lookup[lid]; ok {
		t.Fatalf("expected registry entry removed for added node lid")
	}
	// Cache key removed
	if ok, _, _ := l2.Get(ctx, nr.formatKey(lid.String())); ok {
		t.Fatalf("expected node key deleted from cache")
	}
}

// finalize with payload and lastCommittedFunctionLog=deleteObsoleteEntries: should call deleteObsoleteEntries and Remove logs.
func Test_TransactionLogger_Rollback_FinalizeWithPayload_DeleteObsoleteEntries_RemovesLogs(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	rec := &recTLRemove{inner: mocks.NewMockTransactionLog()}
	tl := newTransactionLogger(rec, true)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: reg, StoreRepository: sr}

	// Seed an obsolete node ID for deletion
	delID := sop.NewUUID()
	// Seed unused inactive ID for cache and blob removal
	unusedID := sop.NewUUID()
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: unusedID, Value: []byte("v")}}}})
	_ = l2.Set(ctx, cache.FormatNodeKey(unusedID.String()), sop.NewUUID().String(), time.Minute)

	dels := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{delID}}}
	unused := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{unusedID}}}
	pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: dels, Second: unused},
		Second: nil,
	}
	logs := []sop.KeyValuePair[int, []byte]{
		// finalizeCommit logged first with payload, lastCommittedFunctionLog is deleteObsoleteEntries
		{Key: finalizeCommit, Value: toByteArray(pl)},
		{Key: deleteObsoleteEntries, Value: nil}, // lastCommittedFunctionLog
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
	if rec.removes == 0 {
		t.Fatalf("expected logs to be removed after finalize-with-payload + deleteObsoleteEntries")
	}
	// Blob removed
	if ba, _ := bs.GetOne(ctx, "bt", unusedID); len(ba) != 0 {
		t.Fatalf("expected unused blob removed")
	}
}

// prioLogAddCounter records Add calls to verify PriorityLog.Add is invoked in phase1Commit
type prioLogAddCounter struct{ adds, removes int }

func (p *prioLogAddCounter) IsEnabled() bool { return true }
func (p *prioLogAddCounter) Add(ctx context.Context, tid sop.UUID, payload []byte) error {
	p.adds++
	return nil
}
func (p *prioLogAddCounter) Remove(ctx context.Context, tid sop.UUID) error { p.removes++; return nil }
func (p *prioLogAddCounter) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (p *prioLogAddCounter) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (p *prioLogAddCounter) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (p *prioLogAddCounter) LogCommitChanges(ctx context.Context, _ []sop.StoreInfo, _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// tlWithCustomPL injects a custom PriorityLog while delegating to the mock transaction log.
type tlWithCustomPL struct {
	inner *mocks.MockTransactionLog
	pl    sop.TransactionPriorityLog
}

func (w tlWithCustomPL) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOne(ctx)
}
func (w tlWithCustomPL) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOneOfHour(ctx, hour)
}
func (w tlWithCustomPL) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return w.inner.Add(ctx, tid, commitFunction, payload)
}
func (w tlWithCustomPL) Remove(ctx context.Context, tid sop.UUID) error {
	return w.inner.Remove(ctx, tid)
}
func (w tlWithCustomPL) NewUUID() sop.UUID                       { return w.inner.NewUUID() }
func (w tlWithCustomPL) PriorityLog() sop.TransactionPriorityLog { return w.pl }

func Test_Phase1Commit_PriorityLog_Add_Called_OnRemovedNodes(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)

	// Create a store + backend with a removed node so commitRemovedNodes path is taken.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_pl_add", SlotLength: 4})
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: sr, registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: nil, phaseDone: 0}
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Seed a node marked for removal with matching registry version.
	rid := sop.NewUUID()
	rn := &btree.Node[PersonKey, Person]{ID: rid, Version: 1}
	nr.localCache[rid] = cachedNode{action: removeAction, node: rn}
	h := sop.NewHandle(rid)
	h.Version = 1
	rg.Lookup[rid] = h

	// Inject a PriorityLog that records Add calls.
	baseTL := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	pl := &prioLogAddCounter{}
	tl := newTransactionLogger(tlWithCustomPL{inner: baseTL, pl: pl}, true)
	tx.logger = tl

	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
	if pl.adds == 0 {
		t.Fatalf("expected PriorityLog.Add to be called when removed nodes exist")
	}
}

// flipOnceLock makes the first Lock call fail to set needsRefetchAndMerge flag.
type flipOnceLock struct {
	sop.L2Cache
	tripped bool
}

func (f *flipOnceLock) Lock(ctx context.Context, d time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if !f.tripped {
		f.tripped = true
		return false, sop.NilUUID, nil
	}
	return f.L2Cache.Lock(ctx, d, keys)
}

func (f *flipOnceLock) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := f.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := f.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func Test_Phase1Commit_LockTrackedItems_Error_AfterRefetch_Propagates(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &flipOnceLock{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: sr, registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_refetch_lock_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	// Stage an updated node so nodesKeys are non-empty
	uid := sop.NewUUID()
	nr.localCache[uid] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: uid, Version: 1}}

	// Make lockTrackedItems return error ONLY after refetch path is taken.
	var afterRefetch bool
	var lockErr = errors.New("lock_tracked_items_err")

	tx.btreesBackend = []btreeBackend{{
		nodeRepository:    nr,
		getStoreInfo:      func() *sop.StoreInfo { return si },
		hasTrackedItems:   func() bool { return true },
		checkTrackedItems: func(context.Context) error { return nil },
		lockTrackedItems: func(context.Context, time.Duration) error {
			if afterRefetch {
				return lockErr
			}
			return nil
		},
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { afterRefetch = true; return nil },
	}}

	err := tx.phase1Commit(ctx)
	if err == nil || err.Error() != lockErr.Error() {
		t.Fatalf("expected lockTrackedItems error after refetch to propagate, got: %v", err)
	}
}

// errUpdateStoreRepo returns an error from Update to exercise commitStores error propagation at the tail of phase1Commit.
type errUpdateStoreRepo struct{ inner sop.StoreRepository }

func (e errUpdateStoreRepo) Add(ctx context.Context, stores ...sop.StoreInfo) error {
	return e.inner.Add(ctx, stores...)
}
func (e errUpdateStoreRepo) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, fmt.Errorf("update fail")
}
func (e errUpdateStoreRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return e.inner.Get(ctx, names...)
}
func (e errUpdateStoreRepo) GetAll(ctx context.Context) ([]string, error) { return e.inner.GetAll(ctx) }
func (e errUpdateStoreRepo) GetWithTTL(ctx context.Context, isCacheTTL bool, cacheDuration time.Duration, names ...string) ([]sop.StoreInfo, error) {
	return e.inner.GetWithTTL(ctx, isCacheTTL, cacheDuration, names...)
}
func (e errUpdateStoreRepo) Remove(ctx context.Context, names ...string) error {
	return e.inner.Remove(ctx, names...)
}
func (e errUpdateStoreRepo) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error {
	return e.inner.Replicate(ctx, storesInfo)
}

func Test_Phase1Commit_CommitStores_Update_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	baseSR := mocks.NewMockStoreRepository()
	sr := errUpdateStoreRepo{inner: baseSR}
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: sr, registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Force a non-zero count delta so commitStores actually executes the repository update path.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_commitstores_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count + 1}

	// Minimal backend with tracked items but no mutations; ensures we reach commitStores
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err == nil || err.Error() != "update fail" {
		t.Fatalf("expected commitStores Update error to propagate, got: %v", err)
	}
}

// isLockedFalseOnce causes the first IsLocked to return false to cover the continue branch in phase1Commit.
type isLockedFalseOnce struct {
	sop.L2Cache
	tripped bool
}

func (m *isLockedFalseOnce) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !m.tripped {
		m.tripped = true
		return false, nil
	}
	return m.L2Cache.IsLocked(ctx, lockKeys)
}

func (m *isLockedFalseOnce) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := m.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := m.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func Test_Phase1Commit_IsLockedFalseThenSucceed(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &isLockedFalseOnce{L2Cache: base}
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: sr, registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_false", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed registry with matching handle so commitUpdatedNodes proceeds normally after the IsLocked false retry.
	h := sop.NewHandle(id)
	h.Version = 1
	rg.Lookup[id] = h

	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("expected success after IsLocked false then succeed, got: %v", err)
	}
}
