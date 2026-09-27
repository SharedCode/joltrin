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

// Covers addAction path in refetchAndMergeClosure when values are stored in a separate segment (AddItem).
func Test_RefetchAndMerge_AddItem_SeparateSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_add_sep", SlotLength: 4, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	l2 := mocks.NewMockClient()
	c := cache.GetGlobalL1Cache(l2)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: c, blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed a new item in tracker as addAction for AddItem path
	pk, pv := newPerson("ka", "va", "m", "a@b", "p")
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
	// Verify that the item is marked persisted and remains tracked
	ci, ok := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[it.ID]
	if !ok || !ci.persisted {
		t.Fatalf("expected item to be persisted in tracker, ok=%v persisted=%v", ok, ci.persisted)
	}
}

// registry mock that makes Remove return an error
type failingRemoveRegistry struct{ *mocks.Mock_vid_registry }

func (f *failingRemoveRegistry) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	return errors.New("induced remove error")
}

// blob store mock that fails Remove to exercise rollbackNewRootNodes error path
type failingBlobStoreRemove struct{ sop.BlobStore }

func (f failingBlobStoreRemove) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return errors.New("induced blob remove error")
}

// Covers commitNewRootNodes early-return false when a non-empty root exists in registry.
func Test_CommitNewRootNodes_ReturnsFalse_OnExistingRoot(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: gc, blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "root_exists", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc}

	// Prepare node and pre-populate registry with existing handle for same logical ID
	lid := sop.NewUUID()
	h := sop.NewHandle(lid) // LogicalID is set -> non-empty root
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("reg add err: %v", err)
	}
	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 0}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}

	ok, handles, err := nr.commitNewRootNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok || handles != nil {
		t.Fatalf("expected ok=false and nil handles when root exists, got ok=%v handles=%v", ok, handles)
	}
}

// Covers commitRemovedNodes returning false on conflicts (IsDeleted or version mismatch).
func Test_CommitRemovedNodes_Conflict_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: gc, blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rm_conflict", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc}

	// Case 1: IsDeleted=true triggers false
	{
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		h.IsDeleted = true
		if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
			t.Fatalf("reg add err: %v", err)
		}
		n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
		ok, _, err := nr.commitRemovedNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false on IsDeleted conflict")
		}
	}
	// Case 2: Version mismatch triggers false
	{
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		h.Version = 2 // backend newer than node's
		if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
			t.Fatalf("reg add err: %v", err)
		}
		n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
		ok, _, err := nr.commitRemovedNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false on version mismatch")
		}
	}
}

// Covers rollbackNewRootNodes error flows from blobStore.Remove and registry.Remove
func Test_RollbackNewRootNodes_Error_Paths(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	baseReg := &mocks.Mock_vid_registry{Lookup: make(map[sop.UUID]sop.Handle)}
	reg := &failingRemoveRegistry{Mock_vid_registry: baseReg}
	bs := mocks.NewMockBlobStore()
	txLog := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: gc, blobStore: failingBlobStoreRemove{BlobStore: bs}, logger: txLog}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_root_errs", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc}

	// Build rollback payloads
	lid := sop.NewUUID()
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}

	// When committedState > commitNewRootNodes, registry.Remove is invoked; here it will error
	txLog.committedState = deleteObsoleteEntries
	if err := nr.rollbackNewRootNodes(ctx, sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}); err == nil {
		t.Fatalf("expected rollbackNewRootNodes to return error from blob remove or registry remove")
	}
}

// Covers rollback handling of addActivelyPersistedItem (pre-commit log).
func Test_TransactionLogger_Rollback_PreCommit_AddActivelyPersistedItem(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	trLog := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tnx := &Transaction{l2Cache: l2, l1Cache: gc, registry: reg, blobStore: bs, logger: trLog, StoreRepository: sr}

	// Build a pre-commit log for addActivelyPersistedItem
	bibs := sop.BlobsPayload[sop.UUID]{BlobTable: "bt_pre", Blobs: []sop.UUID{sop.NewUUID()}}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: addActivelyPersistedItem, Value: toByteArray(bibs)},
		{Key: commitStoreInfo, Value: nil},
	}
	if err := trLog.rollback(ctx, tnx, trLog.transactionID, logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

// errOnAddStoreRepo errors on Add and tracks Remove calls.
type errOnAddStoreRepo struct{ removed []string }

func (e *errOnAddStoreRepo) Add(ctx context.Context, stores ...sop.StoreInfo) error {
	return errors.New("add failed")
}
func (e *errOnAddStoreRepo) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, nil
}
func (e *errOnAddStoreRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	// Return empty to force NewBtree to try Add
	return []sop.StoreInfo{}, nil
}
func (e *errOnAddStoreRepo) GetAll(ctx context.Context) ([]string, error) { return nil, nil }
func (e *errOnAddStoreRepo) GetWithTTL(ctx context.Context, isTTL bool, d time.Duration, names ...string) ([]sop.StoreInfo, error) {
	return e.Get(ctx, names...)
}
func (e *errOnAddStoreRepo) Remove(ctx context.Context, names ...string) error {
	e.removed = append(e.removed, names...)
	return nil
}
func (e *errOnAddStoreRepo) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error {
	return nil
}

// errOnGetStoreRepo errors on Get for OpenBtree path.
type errOnGetStoreRepo struct{ err error }

func (e *errOnGetStoreRepo) Add(ctx context.Context, stores ...sop.StoreInfo) error { return nil }
func (e *errOnGetStoreRepo) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, nil
}
func (e *errOnGetStoreRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return nil, e.err
}
func (e *errOnGetStoreRepo) GetAll(ctx context.Context) ([]string, error) { return nil, nil }
func (e *errOnGetStoreRepo) GetWithTTL(ctx context.Context, isTTL bool, d time.Duration, names ...string) ([]sop.StoreInfo, error) {
	return nil, e.err
}
func (e *errOnGetStoreRepo) Remove(ctx context.Context, names ...string) error { return nil }
func (e *errOnGetStoreRepo) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error {
	return nil
}

// blob store that errors on GetOne to drive itemactiontracker.Get error branch.
type errGetBlobStore struct{}

func (e errGetBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, errors.New("blob get error")
}
func (e errGetBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (e errGetBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (e errGetBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return nil
}

func Test_NewBtree_AddFails_CleansUpAndRollsBack(t *testing.T) {
	ctx := context.Background()
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("begin err: %v", err)
	}
	// Swap repository with erroring one
	t2 := trans.GetPhasedTransaction().(*Transaction)
	ers := &errOnAddStoreRepo{}
	t2.StoreRepository = ers
	// Attempt to create new store; expect error and rollback (transaction ended)
	cmp := func(a, b int) int {
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	}
	_, err := NewBtree[int, int](ctx, sop.StoreOptions{Name: "add_fail", SlotLength: 2}, trans, cmp)
	if err == nil {
		t.Fatalf("expected error from NewBtree with Add failure")
	}
	if trans.HasBegun() {
		t.Fatalf("expected transaction ended after rollback")
	}
	// Remove should be called for attempted add
	if len(ers.removed) == 0 || ers.removed[0] != "add_fail" {
		t.Fatalf("expected Remove called for add_fail, got %v", ers.removed)
	}
}

func Test_OpenBtree_StoreRepositoryError_RollsBack(t *testing.T) {
	ctx := context.Background()
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	_ = trans.Begin(ctx)
	t2 := trans.GetPhasedTransaction().(*Transaction)
	t2.StoreRepository = &errOnGetStoreRepo{err: errors.New("get error")}
	cmp := func(a, b int) int {
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	}
	_, err := OpenBtree[int, int](ctx, "any", trans, cmp)
	if err == nil {
		t.Fatalf("expected error from OpenBtree when Get errors")
	}
	if trans.HasBegun() {
		t.Fatalf("expected transaction ended after OpenBtree failure")
	}
}

func Test_ItemActionTracker_Get_TTL_And_BlobError(t *testing.T) {
	ctx := context.Background()
	// TTL=true path with cache miss should fall back to blob store; here blob store errors.
	so := sop.StoreOptions{
		Name:                      "iat_get_ttl",
		SlotLength:                4,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
		CacheConfig: &sop.StoreCacheConfig{
			IsValueDataCacheTTL:    true,
			ValueDataCacheDuration: time.Minute,
		},
	}
	si := sop.NewStoreInfo(so)
	// Use mock transaction log
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	// Construct tracker with blob error store to force error path
	tr := newItemActionTracker[int, int](si, mockRedisCache, errGetBlobStore{}, tl)

	item := &btree.Item[int, int]{ID: sop.NewUUID(), Key: 1, Value: nil, ValueNeedsFetch: true}
	if err := tr.Get(ctx, item); err == nil {
		t.Fatalf("expected error when blob store GetOne fails")
	}
}

// flapLockCache fails the first Lock call, then delegates to the inner cache.
type flapLockCache struct {
	sop.L2Cache
	calls int
}

func (f *flapLockCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	f.calls++
	if f.calls == 1 {
		return false, sop.NilUUID, nil
	}
	return f.L2Cache.Lock(ctx, duration, lockKeys)
}

// Ensures phase1Commit takes the needsRefetchAndMerge path when lock first fails, then succeeds.
func Test_Phase1Commit_LockFailsOnce_TriggersRefetchAndMerge(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &flapLockCache{L2Cache: base}
	gc := cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: gc, blobStore: blobs, logger: tl, phaseDone: 0}

	// One updated node in local cache to produce a lock key.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_lock_refetch", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	uid := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: uid, Version: 1}
	nr.localCache[uid] = cachedNode{action: updateAction, node: n}
	// Seed registry with same version to allow commitUpdatedNodes to succeed after refetch.
	h := sop.NewHandle(uid)
	h.Version = 1
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
		refetchAndMerge:                  func(context.Context) error { refetched = true; return nil },
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
	if !refetched {
		t.Fatalf("expected refetchAndMerge path to be taken after initial lock failure")
	}
}

// tlAddErr makes TransactionLog.Add return an error to force Phase2Commit error path before any locks are held.
type tlAddErr struct{}

func (tlAddErr) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return fmt.Errorf("add err")
}
func (tlAddErr) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (tlAddErr) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (tlAddErr) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (tlAddErr) NewUUID() sop.UUID { return sop.NewUUID() }

// Return a priority log whose Remove errors to exercise warn path in Phase2Commit else branch.
type prioRemoveWarn struct{}

func (prioRemoveWarn) IsEnabled() bool                                             { return true }
func (prioRemoveWarn) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (prioRemoveWarn) Remove(ctx context.Context, tid sop.UUID) error              { return fmt.Errorf("rm warn") }
func (prioRemoveWarn) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (prioRemoveWarn) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (prioRemoveWarn) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (prioRemoveWarn) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// wrapTLAddErr implements sop.TransactionLog with Add error and PriorityLog warn-on-remove.
type wrapTLAddErr struct{ tlAddErr }

func (wrapTLAddErr) PriorityLog() sop.TransactionPriorityLog { return prioRemoveWarn{} }

// Ensure Phase2Commit handles log(Add) error by removing priority log when no locks are held, then rolling back and surfacing error.
func Test_Phase2Commit_LogAddError_RemovesPriorityWithoutLocks(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForWriting, phaseDone: 1, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(wrapTLAddErr{}, true)}

	// Minimal backend; no tracked items, so rollback is cheap.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_log_err", SlotLength: 2})
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   &nodeRepositoryBackend{transaction: tx, storeInfo: si, localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc},
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return false },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit to return error due to log add failure")
	}
}

// isLockedFlapCache returns false on first IsLocked call then delegates to inner cache.
type isLockedFlapCache struct {
	sop.L2Cache
	calls int
}

func (f *isLockedFlapCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	f.calls++
	if f.calls == 1 {
		return false, nil
	}
	return f.L2Cache.IsLocked(ctx, lockKeys)
}

// Exercises the branch where Lock succeeds but IsLocked reports false once; the loop should continue and eventually succeed.
func Test_Phase1Commit_IsLockedFlaps_ContinuesThenSucceeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &isLockedFlapCache{L2Cache: base}
	gc := cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: gc, blobStore: blobs, logger: tl, phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_flap", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	uid := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: uid, Version: 1}
	nr.localCache[uid] = cachedNode{action: updateAction, node: n}
	// Seed registry with same version to allow commitUpdatedNodes.
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

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
}

// Build a pair of dummy handles for acquireLocks tests.
func buildHandles(ids ...sop.UUID) []sop.RegistryPayload[sop.Handle] {
	hs := make([]sop.Handle, len(ids))
	for i := range ids {
		hs[i] = sop.NewHandle(ids[i])
	}
	return []sop.RegistryPayload[sop.Handle]{{IDs: hs}}
}

// When keys are owned by a dead transaction with same tid, acquireLocks should take over successfully.
func Test_AcquireLocks_TakeoverFromSameTransaction_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{l2Cache: l2, registry: mocks.NewMockRegistry(false), blobStore: mocks.NewMockBlobStore(), StoreRepository: mocks.NewMockStoreRepository()}
	tid := tl.TransactionLog.NewUUID()
	id1, id2 := sop.NewUUID(), sop.NewUUID()
	// Pre-own the keys with the same tid to simulate dead owner we can take over from.
	_ = l2.Set(ctx, l2.FormatLockKey(id1.String()), tid.String(), 0)
	_ = l2.Set(ctx, l2.FormatLockKey(id2.String()), tid.String(), 0)

	keys, err := tl.acquireLocks(ctx, tx, tid, buildHandles(id1, id2))
	if err != nil {
		t.Fatalf("acquireLocks err: %v", err)
	}
	if len(keys) != 2 || !keys[0].IsLockOwner || !keys[1].IsLockOwner {
		t.Fatalf("expected takeover with ownership on both keys")
	}
}

// When keys are owned by a different owner, acquireLocks should return a failover error.
func Test_AcquireLocks_LockedByDifferentTransaction_ReturnsFailoverError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{l2Cache: l2}
	owner := sop.NewUUID()
	tid := sop.NewUUID()
	id := sop.NewUUID()
	_ = l2.Set(ctx, l2.FormatLockKey(id.String()), owner.String(), 0)

	_, err := tl.acquireLocks(ctx, tx, tid, buildHandles(id))
	if err == nil {
		t.Fatalf("expected error when key is locked by different transaction")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
}

// Rollback should remove logs when finalizeCommit has nil payload and the last committed step is deleteObsoleteEntries.
func Test_Rollback_FinalizeWithoutPayload_RemovesLogs(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{blobStore: mocks.NewMockBlobStore(), StoreRepository: mocks.NewMockStoreRepository(), l2Cache: mocks.NewMockClient(), registry: mocks.NewMockRegistry(false)}
	tid := sop.NewUUID()
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(finalizeCommit), Value: nil},
		{Key: int(deleteObsoleteEntries), Value: nil},
	}
	if err := tl.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

// priorityRollback should be a no-op when transaction or registry are nil.
func Test_PriorityRollback_NoTransactionOrRegistry_NoOp(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.priorityRollback(ctx, nil, sop.NewUUID()); err != nil {
		t.Fatalf("priorityRollback unexpected err: %v", err)
	}
}

// lockThenIsLockedFalseCache wraps a cache and forces IsLocked to return false even after Lock succeeds.
type lockThenIsLockedFalseCache struct{ inner sop.L2Cache }

func (c lockThenIsLockedFalseCache) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c lockThenIsLockedFalseCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return c.inner.Set(ctx, key, value, expiration)
}
func (c lockThenIsLockedFalseCache) Get(ctx context.Context, key string) (bool, string, error) {
	return c.inner.Get(ctx, key)
}
func (c lockThenIsLockedFalseCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return c.inner.GetEx(ctx, key, expiration)
}
func (c lockThenIsLockedFalseCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.inner.SetStruct(ctx, key, value, expiration)
}
func (c lockThenIsLockedFalseCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.inner.SetStructs(ctx, keys, values, expiration)
}
func (c lockThenIsLockedFalseCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return c.inner.GetStruct(ctx, key, target)
}
func (c lockThenIsLockedFalseCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return c.inner.GetStructEx(ctx, key, target, expiration)
}
func (c lockThenIsLockedFalseCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.inner.GetStructs(ctx, keys, targets, expiration)
}
func (c lockThenIsLockedFalseCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return c.inner.Delete(ctx, keys)
}
func (c lockThenIsLockedFalseCache) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c lockThenIsLockedFalseCache) FormatLockKey(k string) string  { return c.inner.FormatLockKey(k) }
func (c lockThenIsLockedFalseCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.inner.CreateLockKeys(keys)
}
func (c lockThenIsLockedFalseCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.inner.CreateLockKeysForIDs(keys)
}
func (c lockThenIsLockedFalseCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return c.inner.IsLockedTTL(ctx, duration, lockKeys)
}
func (c lockThenIsLockedFalseCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return c.inner.Lock(ctx, duration, lockKeys)
}
func (c lockThenIsLockedFalseCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := c.Lock(ctx, duration, lockKeys)
	if !ok || err != nil {
		return ok, tid, err
	}
	if locked, err := c.IsLocked(ctx, lockKeys); err != nil || !locked {
		if err == nil {
			err = sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("failover")}
		}
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (c lockThenIsLockedFalseCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, nil
}
func (c lockThenIsLockedFalseCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return c.inner.IsLockedByOthers(ctx, lockKeyNames)
}
func (c lockThenIsLockedFalseCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return c.inner.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (c lockThenIsLockedFalseCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return c.inner.Unlock(ctx, lockKeys)
}
func (c lockThenIsLockedFalseCache) Clear(ctx context.Context) error { return c.inner.Clear(ctx) }
func (c lockThenIsLockedFalseCache) IsRestarted(ctx context.Context) bool {
	return c.inner.IsRestarted(ctx)
}

// lockErrorCache forces Lock to return an error to trigger error propagation path in acquireLocks.
type lockErrorCache struct{ inner sop.L2Cache }

func (c lockErrorCache) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c lockErrorCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return c.inner.Set(ctx, key, value, expiration)
}
func (c lockErrorCache) Get(ctx context.Context, key string) (bool, string, error) {
	return c.inner.Get(ctx, key)
}
func (c lockErrorCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return c.inner.GetEx(ctx, key, expiration)
}
func (c lockErrorCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return c.inner.SetStruct(ctx, key, value, expiration)
}
func (c lockErrorCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.inner.SetStructs(ctx, keys, values, expiration)
}
func (c lockErrorCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return c.inner.GetStruct(ctx, key, target)
}
func (c lockErrorCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return c.inner.GetStructEx(ctx, key, target, expiration)
}
func (c lockErrorCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.inner.GetStructs(ctx, keys, targets, expiration)
}
func (c lockErrorCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return c.inner.Delete(ctx, keys)
}
func (c lockErrorCache) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c lockErrorCache) FormatLockKey(k string) string  { return c.inner.FormatLockKey(k) }
func (c lockErrorCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.inner.CreateLockKeys(keys)
}
func (c lockErrorCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.inner.CreateLockKeysForIDs(keys)
}
func (c lockErrorCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return c.inner.IsLockedTTL(ctx, duration, lockKeys)
}
func (c lockErrorCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("forced lock error")
}
func (c lockErrorCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("forced lock error")
}
func (c lockErrorCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, nil
}
func (c lockErrorCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return c.inner.IsLockedByOthers(ctx, lockKeyNames)
}
func (c lockErrorCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return c.inner.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (c lockErrorCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return c.inner.Unlock(ctx, lockKeys)
}
func (c lockErrorCache) Clear(ctx context.Context) error { return c.inner.Clear(ctx) }
func (c lockErrorCache) IsRestarted(ctx context.Context) bool {
	return c.inner.IsRestarted(ctx)
}

func Test_AcquireLocks_PartialAfterOk_ReturnsFailover(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := lockThenIsLockedFalseCache{inner: base}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	lid := sop.NewUUID()
	stores := []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{{LogicalID: lid}}}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil {
		t.Fatalf("expected failover error, got nil")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
}

func Test_AcquireLocks_LockError_Propagates(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := lockErrorCache{inner: base}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	lid := sop.NewUUID()
	stores := []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{{LogicalID: lid}}}}
	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || err.Error() != "forced lock error" {
		t.Fatalf("expected forced lock error, got %v", err)
	}
}

// getFlipRegistry returns mismatching versions on first Get, then matching on next calls.
type getFlipRegistry struct {
	*mocks.Mock_vid_registry
	cnt int
}

func (g *getFlipRegistry) Get(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	g.cnt++
	out := make([]sop.RegistryPayload[sop.Handle], len(lids))
	for i := range lids {
		out[i].RegistryTable = lids[i].RegistryTable
		out[i].IDs = make([]sop.Handle, len(lids[i].IDs))
		for j := range lids[i].IDs {
			h := sop.NewHandle(lids[i].IDs[j])
			if g.cnt == 1 {
				// Force version mismatch for first pass to return false from areFetchedItemsIntact
				h.Version = 999
			} else {
				// Match fetched node version (=1) on subsequent passes
				h.Version = 1
			}
			out[i].IDs[j] = h
		}
	}
	return out, nil
}

// tlogFailOnFunc forces Add to fail for a specific commit function id.
type tlogFailOnFunc struct {
	inner  sop.TransactionLog
	target int
}

func (t tlogFailOnFunc) PriorityLog() sop.TransactionPriorityLog { return t.inner.PriorityLog() }
func (t tlogFailOnFunc) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	if commitFunction == t.target {
		return errors.New("add fail on target")
	}
	return t.inner.Add(ctx, tid, commitFunction, payload)
}
func (t tlogFailOnFunc) Remove(ctx context.Context, tid sop.UUID) error {
	return t.inner.Remove(ctx, tid)
}
func (t tlogFailOnFunc) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return t.inner.GetOne(ctx)
}
func (t tlogFailOnFunc) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return t.inner.GetOneOfHour(ctx, hour)
}
func (t tlogFailOnFunc) NewUUID() sop.UUID { return t.inner.NewUUID() }

func Test_Phase1Commit_AreFetchedItemsIntact_FalseThenRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()

	// Registry that flips from mismatch to match on successive Get calls.
	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := &getFlipRegistry{Mock_vid_registry: baseReg}

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: gc, blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Backend with one fetched node; no updates/removes/adds/root nodes.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_fetched_retry", SlotLength: 2})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: gc, count: si.Count}
	fid := sop.NewUUID()
	// Version must be 1 to match reg on retry
	nr.localCache[fid] = cachedNode{action: getAction, node: &btree.Node[PersonKey, Person]{ID: fid, Version: 1}}

	tx.btreesBackend = []btreeBackend{
		{
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
		},
	}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err on fetched retry path: %v", err)
	}
}

func Test_Transaction_Cleanup_LogError_DeleteObsoleteEntries(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(tlogFailOnFunc{inner: mocks.NewMockTransactionLog(), target: int(deleteObsoleteEntries)}, true)
	tx := &Transaction{l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), registry: mocks.NewMockRegistry(false), logger: tl}
	if err := tx.cleanup(ctx); err == nil {
		t.Fatalf("expected error from cleanup when deleteObsoleteEntries log fails")
	}
}

func Test_Transaction_Cleanup_LogError_DeleteTrackedItemsValues(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(tlogFailOnFunc{inner: mocks.NewMockTransactionLog(), target: int(deleteTrackedItemsValues)}, true)
	tx := &Transaction{l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), registry: mocks.NewMockRegistry(false), logger: tl}
	if err := tx.cleanup(ctx); err == nil {
		t.Fatalf("expected error from cleanup when deleteTrackedItemsValues log fails")
	}
}

func Test_TransactionLogger_DoPriorityRollbacks_RemoveError_Propagates_Alt(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, registry: reg}

	// Seed registry to avoid version failover during checks
	lid := sop.NewUUID()
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}})

	tid := sop.NewUUID()
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{sop.NewHandle(lid)}}}}}, removeErr: map[string]error{tid.String(): errors.New("rm fail")}}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err == nil {
		t.Fatalf("expected error when Remove fails")
	}
	if consumed {
		t.Fatalf("expected consumed=false when error occurs")
	}
	if pl.removedHit[tid.String()] == 0 {
		t.Fatalf("expected Remove attempted when Remove errors")
	}
}
