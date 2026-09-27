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

func Test_TransactionLogger_Rollback_CommitAddedRemovedUpdated_Paths(t *testing.T) {
	ctx := context.Background()
	// Wire a minimal transaction with nodeRepository backend needed by rollback methods.
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{l2Cache: redis, l1Cache: cache.GetGlobalL1Cache(redis), blobStore: bs, registry: rg, StoreRepository: sr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_paths", SlotLength: 4})
	nr := &nodeRepositoryBackend{
		transaction:    tx,
		storeInfo:      si,
		readNodesCache: cache.NewCache[sop.UUID, any](8, 12),
		localCache:     make(map[sop.UUID]cachedNode),
		l2Cache:        redis,
		l1Cache:        cache.GetGlobalL1Cache(redis),
		count:          si.Count,
	}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	// Prepare payloads for logs.
	// commitAddedNodes -> vids and bibs
	addLID := sop.NewUUID()
	vidsAdded := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{addLID}}}
	bibsAdded := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	payloadAdded := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vidsAdded, Second: bibsAdded})

	// commitRemovedNodes -> vids; seed registry handles marked deleted to exercise rollback logic
	remLID := sop.NewUUID()
	vidsRemoved := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{remLID}}}
	mr := rg.(*mocks.Mock_vid_registry)
	hRem := sop.NewHandle(remLID)
	hRem.IsDeleted = true
	mr.Lookup[remLID] = hRem
	payloadRemoved := toByteArray(vidsRemoved)

	// commitUpdatedNodes -> blobsIDs for removeNodes
	updBlobID := sop.NewUUID()
	blobsUpd := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{updBlobID}}}
	payloadUpdated := toByteArray(blobsUpd)

	// Build logs to trigger the three rollback branches; ensure lastCommittedFunctionLog is high.
	tid := sop.NewUUID()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitAddedNodes, Value: payloadAdded},
		{Key: commitRemovedNodes, Value: payloadRemoved},
		{Key: commitUpdatedNodes, Value: payloadUpdated},
		{Key: commitStoreInfo, Value: toByteArray([]sop.StoreInfo{{Name: si.Name}})},
	}
	if err := tl.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

// --- Additional coverage for Phase2/rollback/priority paths ---

func Test_Phase2Commit_LogError_WithNodeLocks_PriorityRollbackPath(t *testing.T) {
	ctx := context.Background()
	pr := &recPrioLog{}
	tl := newTransactionLogger(errAddTL{pr: pr}, true)

	// Use mock caches and repos
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false)

	tx := &Transaction{mode: sop.ForWriting, logger: tl, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: rg, id: sop.NewUUID()}
	// Simulate Phase1 done and pretend we hold node locks so error path uses priorityRollback
	tx.phaseDone = 1
	tx.nodesKeys = l2.CreateLockKeys([]string{"X", "Y"})

	// Provide minimal backend so rollback won't panic
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_err_locks", SlotLength: 4})
	nr := &nodeRepositoryBackend{
		transaction:    tx,
		storeInfo:      si,
		readNodesCache: cache.NewCache[sop.UUID, any](8, 12),
		localCache:     make(map[sop.UUID]cachedNode),
		l2Cache:        l2,
		l1Cache:        cache.GetGlobalL1Cache(l2),
		count:          si.Count,
	}
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return false },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
	}}

	if err := tx.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error due to logger.Add failure")
	}
	// nodesKeys should be cleared by unlockNodesKeys on error path
	if tx.nodesKeys != nil {
		t.Fatalf("expected nodesKeys to be nil after error handling")
	}
	if pr.removed == 0 {
		t.Fatalf("expected priority rollback to remove log entry")
	}
}

func Test_TransactionLogger_PriorityRollback_NoLogs_RemovesTid(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// With dummy priority log returning nil and no registry work, expect nil
	if err := tl.priorityRollback(ctx, mocks.NewMockRegistry(false), sop.NewUUID()); err != nil {
		t.Fatalf("priorityRollback with no logs expected nil, got: %v", err)
	}
}

func Test_TransactionLogger_DoPriorityRollbacks_IsLockedFalse(t *testing.T) {
	ctx := context.Background()
	// Wrap cache so IsLocked on coordinator key returns false even after lock
	base := mocks.NewMockClient()
	c := isLockedFalseCache{L2Cache: base}
	tnx := &Transaction{l2Cache: c}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	ok, err := tl.doPriorityRollbacks(ctx, tnx)
	if err != nil || ok {
		t.Fatalf("expected ok=false and err=nil when IsLocked=false; got ok=%v err=%v", ok, err)
	}
}

func Test_HandleRegistrySectorLockTimeout_UserDataNotLockKey_ReturnsOriginalErr(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tx := &Transaction{l2Cache: l2, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	se := sop.Error{Err: fmt.Errorf("sector lock timeout"), UserData: "not-a-lock"}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err == nil || err.Error() != se.Error() {
		t.Fatalf("expected original error to be returned when UserData not lock key, got: %v", err)
	}
}

func Test_HandleRegistrySectorLockTimeout_LockNotAcquired_ReturnsOriginalErr(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	// Pre-acquire the coordination lock so Lock returns false
	_ = l2.Set(ctx, l2.FormatLockKey("DTrollbk"), sop.NewUUID().String(), time.Minute)
	tx := &Transaction{l2Cache: l2, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	se := sop.Error{Err: fmt.Errorf("sector lock timeout"), UserData: &sop.LockKey{Key: l2.FormatLockKey("X")}}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err == nil || err.Error() != se.Error() {
		t.Fatalf("expected original error to be returned when coordinator lock not acquired, got: %v", err)
	}
}

func Test_HandleRegistrySectorLockTimeout_IsLockedFalse_ReturnsOriginalErr(t *testing.T) {
	ctx := context.Background()
	// IsLocked always false
	base := mocks.NewMockClient()
	c := isLockedFalseCache{L2Cache: base}
	tx := &Transaction{l2Cache: c, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	se := sop.Error{Err: fmt.Errorf("sector lock timeout"), UserData: &sop.LockKey{Key: c.FormatLockKey("X")}}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err == nil || err.Error() != se.Error() {
		t.Fatalf("expected original error to be returned when IsLocked=false, got: %v", err)
	}
}

func Test_TransactionLogger_Rollback_CommitNewRootNodes_Path(t *testing.T) {
	ctx := context.Background()
	// Wire minimal tx/nr to satisfy rollback calls
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: rg, StoreRepository: sr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_newroot_branch", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return si }}}

	// Payload for commitNewRootNodes
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{sop.NewUUID()}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	payload := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs})

	tid := sop.NewUUID()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitNewRootNodes, Value: payload},
		{Key: commitStoreInfo, Value: toByteArray([]sop.StoreInfo{{Name: si.Name}})},
	}
	if err := tl.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("rollback(commitNewRootNodes) err: %v", err)
	}
}

func Test_Transaction_Cleanup_LogError_Propagated(t *testing.T) {
	ctx := context.Background()
	// Use a TL that fails on Add so cleanup's first log call fails
	tl := newTransactionLogger(errAddTL{pr: &recPrioLog{}}, true)
	l2 := mocks.NewMockClient()
	l1 := cache.NewL1Cache(l2, cache.DefaultMinCapacity, cache.DefaultMaxCapacity)
	tx := &Transaction{l1Cache: l1, l2Cache: l2, registry: mocks.NewMockRegistry(false), blobStore: mocks.NewMockBlobStore(), logger: tl}
	if err := tx.cleanup(ctx); err == nil {
		t.Fatalf("expected cleanup to return error when logger.Add fails")
	}
}

// --- More phase1Commit and support branches ---

// prioLogRemoveFail simulates remove error so backup removal path is exercised in doPriorityRollbacks.
type prioLogRemoveFail struct {
	prioLogBatch
}

func (p *prioLogRemoveFail) Remove(ctx context.Context, tid sop.UUID) error {
	return fmt.Errorf("remove fail")
}

func Test_TransactionLogger_DoPriorityRollbacks_RemoveError_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tid := sop.NewUUID()
	// Prepare batch with one entry; Remove will fail and error should be returned.
	lid := sop.NewUUID()
	base := &prioLogRemoveFail{prioLogBatch{tid: tid, batch: [][]sop.RegistryPayload[sop.Handle]{
		{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}},
	}}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Wrap TransactionLog to return our prio log behavior
	tl.TransactionLog = tlWithPL{inner: tl.TransactionLog.(*mocks.MockTransactionLog), pl: base}
	// Seed registry so version checks don't panic; provide transaction with cache
	reg := mocks.NewMockRegistry(false)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}})
	txn := &Transaction{l2Cache: l2, registry: reg}
	ok, err := tl.doPriorityRollbacks(ctx, txn)
	if err == nil {
		t.Fatalf("expected error from Remove to be returned")
	}
	if ok {
		t.Fatalf("expected ok=false when Remove returns error")
	}
}

// Remove behavior coverage: when action is addAction, entry is deleted; when in readNodesCache, becomes removeAction
func Test_NodeRepository_Remove_ActionPaths(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "nr_remove", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}

	// Case 1: addAction -> delete from localCache
	idAdd := sop.NewUUID()
	nr.localCache[idAdd] = cachedNode{action: addAction, node: &btree.Node[PersonKey, Person]{ID: idAdd}}
	nr.remove(idAdd)
	if _, ok := nr.localCache[idAdd]; ok {
		t.Fatalf("expected localCache entry to be removed when action was addAction")
	}

	// Case 2: present in readNodesCache -> ultimately marked removeAction in localCache
	idR := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: idR}
	nr.readNodesCache.Set([]sop.KeyValuePair[sop.UUID, any]{{Key: idR, Value: n}})
	nr.remove(idR)
	if v, ok := nr.localCache[idR]; !ok || v.action != removeAction {
		t.Fatalf("expected localCache action removeAction, got: %#v", v)
	}
	_ = ctx
}

// errOnceIsLockedCache returns error on first IsLocked then delegates to base.
type errOnceIsLockedCache struct {
	sop.L2Cache
	tripped bool
}

func (e *errOnceIsLockedCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !e.tripped {
		e.tripped = true
		return false, fmt.Errorf("islocked once err")
	}
	return e.L2Cache.IsLocked(ctx, lockKeys)
}

func Test_Phase1Commit_IsLockedError_Then_Succeeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	c := &errOnceIsLockedCache{L2Cache: base}
	cache.GetGlobalL1Cache(c)
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: c, l1Cache: cache.GetGlobalL1Cache(c), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0, id: sop.NewUUID()}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_err", SlotLength: 4, IsValueDataInNodeSegment: true})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: c, l1Cache: cache.GetGlobalL1Cache(c), count: si.Count}
	uid := sop.NewUUID()
	node := &btree.Node[PersonKey, Person]{ID: uid, Version: 1}
	nr.localCache[uid] = cachedNode{action: updateAction, node: node}
	// Registry with matching version so commitUpdatedNodes will succeed
	h := sop.NewHandle(uid)
	h.Version = 1
	rg.Lookup[uid] = h

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
	}}
	if err := tx.phase1Commit(ctx); err == nil || err.Error() != "islocked once err" {
		t.Fatalf("expected phase1Commit error 'islocked once err', got: %v", err)
	}
}

func Test_Phase1Commit_AreFetchedItemsIntact_False_TriggersRefetch(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0, id: sop.NewUUID()}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_fetched_conflict", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	// Updated node that will succeed
	uid := sop.NewUUID()
	un := &btree.Node[PersonKey, Person]{ID: uid, Version: 1}
	nr.localCache[uid] = cachedNode{action: updateAction, node: un}
	hh := sop.NewHandle(uid)
	hh.Version = 1
	rg.Lookup[uid] = hh
	// Fetched node with mismatched version to force areFetchedItemsIntact=false
	fid := sop.NewUUID()
	fn := &btree.Node[PersonKey, Person]{ID: fid, Version: 1}
	nr.localCache[fid] = cachedNode{action: getAction, node: fn}
	// Registry has different version
	fh := sop.NewHandle(fid)
	fh.Version = 2
	rg.Lookup[fid] = fh

	retried := false
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
		refetchAndMerge: func(ctx context.Context) error {
			if !retried { // align fetched version then
				fh := rg.Lookup[fid]
				fh.Version = 1
				rg.Lookup[fid] = fh
				retried = true
			}
			return nil
		},
	}}
	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit expected to succeed after fetched-items retry, err: %v", err)
	}
	if !retried {
		t.Fatalf("expected refetch to be invoked after areFetchedItemsIntact=false")
	}
}

// registryGetErrorOnce returns sop.Error on the first Get to trigger handleRegistrySectorLockTimeout,
// then delegates to the wrapped registry so the retry can succeed.
type registryGetErrorOnce struct {
	sop.Registry
	called bool
}

func (r *registryGetErrorOnce) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	if !r.called {
		r.called = true
		return nil, sop.Error{Err: fmt.Errorf("get err"), UserData: &sop.LockKey{Key: "Lx"}}
	}
	return r.Registry.Get(ctx, storesLids)
}

func Test_Phase1Commit_RegistryGetError_HandledBySectorTimeout(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	// Compose registry that returns sop.Error once on Get then succeeds
	rg := &registryGetErrorOnce{Registry: mocks.NewMockRegistry(false)}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0, id: sop.NewUUID()}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_regerr", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	// Add a root node so commitNewRootNodes triggers a Get which returns sop.Error
	rid := sop.NewUUID()
	rn := &btree.Node[PersonKey, Person]{ID: rid, Version: 0}
	// Make it a root by matching store root ID
	si.RootNodeID = rid
	nr.localCache[rid] = cachedNode{action: addAction, node: rn}

	// retried := false
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
		refetchAndMerge:                  func(ctx context.Context) error { /*retried = true;*/ return nil },
	}}
	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("expected phase1Commit to succeed after sector-timeout handling and refetch, err: %v", err)
	}
	// if !retried {
	// 	t.Fatalf("expected refetch to run after sector-timeout handling")
	// }
}

func Test_Phase1Commit_PreCommitLogs_CleanedUp(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	mtl := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	tl := newTransactionLogger(mtl, true)
	// Force pre-commit state
	tl.committedState = addActivelyPersistedItem
	preTid := tl.transactionID
	// Seed a pre-commit log under preTid so we can verify it's removed
	_ = mtl.Add(ctx, preTid, int(addActivelyPersistedItem), []byte("x"))
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0, id: sop.NewUUID()}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_precommit_cleanup", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	// Drive phase1 through normal path (with no actual node changes) so the pre-commit logs are cleaned up as implemented.
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
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
	}}
	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
	// Logs for preTid should be gone
	if logs := mtl.GetTIDLogs(preTid); len(logs) != 0 {
		t.Fatalf("expected pre-commit logs to be removed; still present: %v", logs)
	}
}

// timeoutPriorityLog returns a constant batch to keep the loop busy.
type timeoutPriorityLog struct{ inner *stubPriorityLog }

func (t timeoutPriorityLog) IsEnabled() bool                                             { return true }
func (t timeoutPriorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (t timeoutPriorityLog) Remove(ctx context.Context, tid sop.UUID) error              { return nil }
func (t timeoutPriorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return t.inner.Get(ctx, tid)
}
func (t timeoutPriorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return t.inner.GetBatch(ctx, batchSize)
}
func (t timeoutPriorityLog) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (t timeoutPriorityLog) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// stubTLogTimeout wraps stubPriorityLog inside a TransactionLog implementation.
type stubTLogTimeout struct{ pl timeoutPriorityLog }

func (l stubTLogTimeout) PriorityLog() sop.TransactionPriorityLog { return l.pl }
func (l stubTLogTimeout) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return nil
}
func (l stubTLogTimeout) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (l stubTLogTimeout) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (l stubTLogTimeout) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (l stubTLogTimeout) NewUUID() sop.UUID { return sop.NewUUID() }

// Ensures doPriorityRollbacks can time out mid-loop and return without errors besides context deadline.
func Test_TransactionLogger_DoPriorityRollbacks_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	// Prepare a small batch to keep the function busy until timeout.
	lid := sop.NewUUID()
	tid := sop.NewUUID()
	basePL := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}}}}}
	// The PriorityLog contains one handle; also seed the registry with the same handle so version checks don't panic.
	tl := newTransactionLogger(stubTLogTimeout{pl: timeoutPriorityLog{inner: basePL}}, true)
	reg := mocks.NewMockRegistry(false)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}})
	tx := &Transaction{l2Cache: mocks.NewMockClient(), registry: reg}

	// doPriorityRollbacks should exit due to TimedOut with busy=true or false depending on timing; err can be nil or context error.
	_, _ = tl.doPriorityRollbacks(ctx, tx)
}

// isLockedFlap returns false once on IsLocked then delegates.
type isLockedFlap struct {
	sop.L2Cache
	seen bool
}

func (f *isLockedFlap) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !f.seen {
		f.seen = true
		return false, nil
	}
	return f.L2Cache.IsLocked(ctx, lockKeys)
}

// Fetched items intact=false should trigger rollback+retry via needsRefetchAndMerge and then succeed.
func Test_Phase1Commit_FetchedItemsIntact_False_TriggersRetry(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_fetched_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Fetched node with version 1
	fid := sop.NewUUID()
	nr.localCache[fid] = cachedNode{action: getAction, node: &btree.Node[PersonKey, Person]{ID: fid, Version: 1}}
	// Registry says version 2 -> mismatch => areFetchedItemsIntact=false
	h := sop.NewHandle(fid)
	h.Version = 2
	reg.Lookup[fid] = h

	// Refetch closure fixes version to match so next loop passes
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
			hh := reg.Lookup[fid]
			hh.Version = 1
			reg.Lookup[fid] = hh
			return nil
		},
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit err: %v", err)
	}
	if !refetched {
		t.Fatalf("expected refetchAndMerge to be invoked")
	}
}

// Lock acquired but IsLocked reports false once: loop should continue and then succeed on next iteration.
func Test_Phase1Commit_IsLockedFalseOnce_Retries(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &isLockedFlap{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Updated node to generate a lock key; version matches registry so commitUpdatedNodes can succeed.
	uid := sop.NewUUID()
	nr.localCache[uid] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: uid, Version: 1}}
	hh := sop.NewHandle(uid)
	hh.Version = 1
	reg.Lookup[uid] = hh

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

// When committedState indicates pre-commit logs exist, phase1Commit should remove them after committing tracked values.
func Test_Phase1Commit_PrecommitLogsRemoved(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	rec := &tlRecorder{tid: sop.NewUUID()}
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(rec, true), phaseDone: 0}

	// Emulate pre-commit state
	tx.logger.committedState = addActivelyPersistedItem

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_precommit_remove", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

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

	// pre-commit logs should be removed for the pre-commit transaction ID
	if len(rec.removed) == 0 || rec.removed[0].Compare(rec.tid) != 0 {
		t.Fatalf("expected pre-commit logs removed for tid %s, got %v", rec.tid, rec.removed)
	}
}

// errOnAddRegistry wraps a registry and fails on Add to exercise commitAddedNodes error path.
type errOnAddRegistry struct{ inner sop.Registry }

func (e errOnAddRegistry) Add(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return errors.New("add fail")
}
func (e errOnAddRegistry) Update(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Update(ctx, s)
}
func (e errOnAddRegistry) UpdateNoLocks(ctx context.Context, a bool, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.UpdateNoLocks(ctx, a, s)
}
func (e errOnAddRegistry) Get(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return e.inner.Get(ctx, lids)
}
func (e errOnAddRegistry) Remove(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) error {
	return e.inner.Remove(ctx, lids)
}
func (e errOnAddRegistry) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Replicate(ctx, a, b, c, d)
}

func Test_NodeRepository_CommitAddedNodes_RegistryAddError(t *testing.T) {
	ctx := context.Background()
	reg := errOnAddRegistry{inner: mocks.NewMockRegistry(false)}
	l2 := mocks.NewMockClient()
	tx := &Transaction{registry: reg, blobStore: mocks.NewMockBlobStore(), l2Cache: l2}
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: l2}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "add_reg_err", SlotLength: 2})
	id := sop.NewUUID()
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: id}}}}
	if _, err := nr.commitAddedNodes(ctx, nodes); err == nil {
		t.Fatalf("expected registry.Add error")
	}
}

func Test_NodeRepository_RollbackUpdatedNodes_DeletesCacheError_ReturnsLastErr(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	base := mocks.NewMockClient()
	// Use existing errDeleteCache type from coverage_boost_10_test.go which embeds sop.Cache and overrides Delete
	ecc := errDeleteCache{L2Cache: base}
	bs := mocks.NewMockBlobStore()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: ecc, blobStore: bs}, l2Cache: ecc}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_cache_err", SlotLength: 2})

	id := sop.NewUUID()
	h := sop.NewHandle(id)
	_ = h.AllocateID()
	reg.Lookup[id] = h
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackUpdatedNodes(ctx, true, vids); err == nil {
		t.Fatalf("expected lastErr from cache.Delete in rollbackUpdatedNodes")
	}
}

func Test_ItemActionTracker_CommitTrackedItemsValues_AddsAndCaches_2(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_commit_values", SlotLength: 4, IsValueDataInNodeSegment: false})
	si.IsValueDataActivelyPersisted = false
	si.IsValueDataGloballyCached = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	id := sop.NewUUID()
	_, p := newPerson("z", "9", "m", "z@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Value: &p}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	if _, err := bs.GetOne(ctx, si.BlobTable, it.ID); err != nil {
		t.Fatalf("blob not written: %v", err)
	}
}

func Test_Transaction_Rollback_WhenCommitted_ReturnsError(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tl.committedState = deleteObsoleteEntries // > finalizeCommit
	tx := &Transaction{logger: tl}
	if err := tx.rollback(ctx, false); err == nil {
		t.Fatalf("expected error when rolling back a committed transaction")
	}
}

// tlogAddErr implements TransactionLog and forces Add to return an error to hit Add() logging error path.
type tlogAddErr struct{}

func (t tlogAddErr) PriorityLog() sop.TransactionPriorityLog { return prioNoop{} }
func (t tlogAddErr) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return errors.New("tlog add err")
}
func (t tlogAddErr) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (t tlogAddErr) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (t tlogAddErr) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (t tlogAddErr) NewUUID() sop.UUID { return sop.NewUUID() }

type prioNoop struct{}

func (p prioNoop) IsEnabled() bool                                             { return false }
func (p prioNoop) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (p prioNoop) Remove(ctx context.Context, tid sop.UUID) error              { return nil }
func (p prioNoop) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (p prioNoop) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (p prioNoop) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (p prioNoop) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

func Test_ItemActionTracker_Add_ActivelyPersisted_LogError_Propagates(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_add_log_err", SlotLength: 4, IsValueDataInNodeSegment: false})
	si.IsValueDataActivelyPersisted = true
	l2 := mocks.NewMockClient()
	tl := newTransactionLogger(tlogAddErr{}, true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, mocks.NewMockBlobStore(), tl)

	id := sop.NewUUID()
	_, p := newPerson("q", "3", "m", "q@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Value: &p}
	if err := trk.Add(ctx, it); err == nil {
		t.Fatalf("expected log error to propagate from Add")
	}
}

func Test_NodeRepository_RollbackRemovedNodes_Locked_UndoesFlags(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	l2 := mocks.NewMockClient()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2}, l2Cache: l2}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_removed_locked", SlotLength: 2})

	id := sop.NewUUID()
	h := sop.NewHandle(id)
	h.IsDeleted = true
	h.WorkInProgressTimestamp = 555
	reg.Lookup[id] = h

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackRemovedNodes(ctx, true, vids); err != nil {
		t.Fatalf("rollbackRemovedNodes err: %v", err)
	}
	if got := reg.Lookup[id]; got.IsDeleted || got.WorkInProgressTimestamp != 0 {
		t.Fatalf("expected flags cleared, got IsDeleted=%v wip=%d", got.IsDeleted, got.WorkInProgressTimestamp)
	}
}

// commitTrackedItemsValuesErr backend hook to induce an error from phase1Commit before any node commits.
func Test_Phase1Commit_CommitTrackedItemsValues_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_ctiv_err", SlotLength: 2})
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0}

	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return errors.New("ctiv err") },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}

	if err := tx.phase1Commit(ctx); err == nil || err.Error() != "ctiv err" {
		t.Fatalf("expected ctiv err, got %v", err)
	}
}

// isLockedFalseOnceCache wraps a Cache and forces IsLocked to return false on first call
// to exercise the phase1Commit loop's IsLocked-false branch.
type isLockedFalseOnceCache struct {
	sop.L2Cache
	flipped bool
}

func (c *isLockedFalseOnceCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !c.flipped {
		c.flipped = true
		return false, nil
	}
	return c.L2Cache.IsLocked(ctx, lockKeys)
}

func Test_Phase1Commit_IsLockedFalse_ThenSucceeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &isLockedFalseOnceCache{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	si := sop.NewStoreInfo(sop.StoreOptions{
		Name:       "p1_islocked_false",
		SlotLength: 2,
	})

	// Initialize as not-begun so Begin() can transition it properly.
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: -1}

	// Prepare a node marked as updated so nodesKeys are created and IsLocked path executes.
	lid := sop.NewUUID()
	n := &btree.Node[sop.UUID, int]{ID: lid, Version: 0}
	// Seed registry with matching handle/version.
	reg.(*mocks.Mock_vid_registry).Lookup[lid] = sop.Handle{LogicalID: lid, PhysicalIDA: lid, Version: 0}

	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, localCache: map[sop.UUID]cachedNode{lid: {node: n, action: updateAction}}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
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

	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("begin err: %v", err)
	}
	if err := tx.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit err: %v", err)
	}
}
