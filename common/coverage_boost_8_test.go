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
)

// flipOnceNodesLock makes the first Lock call on nodes keys fail to force needsRefetchAndMerge=true,
// then allows subsequent locks to succeed.
type flipOnceNodesLock struct {
	sop.L2Cache
	tripped bool
}

func (f *flipOnceNodesLock) Lock(ctx context.Context, d time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if !f.tripped {
		f.tripped = true
		return false, sop.NilUUID, nil
	}
	return f.L2Cache.Lock(ctx, d, keys)
}

// Covers phase1Commit branch where initial nodes Lock fails (needsRefetchAndMerge set),
// refetchAndMerge runs, lockTrackedItems re-executes successfully, then commit proceeds and succeeds.
func Test_Phase1Commit_RefetchThenContinue_Succeeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &flipOnceNodesLock{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: sr, registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Seed a single updated node so nodesKeys are non-empty and commitUpdatedNodes path executes.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_refetch_then_continue", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed registry with matching handle/version so commitUpdatedNodes can succeed.
	h := sop.NewHandle(id)
	h.Version = 1
	rg.Lookup[id] = h

	var refetched bool
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
		// Mark that refetch occurred so we know Lock was retried after needsRefetchAndMerge branch.
		refetchAndMerge: func(context.Context) error { refetched = true; return nil },
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("expected success after refetch-and-continue, got: %v", err)
	}
	if !refetched {
		t.Fatalf("expected refetchAndMerge to be invoked after initial Lock failure")
	}
}

// Covers early timeout path in phase1Commit via context cancellation before the loop body progresses.
func Test_Phase1Commit_TimesOut_ViaContextCancel(t *testing.T) {
	// Use a canceled context so t.timedOut returns ctx.Err() immediately within the loop.
	baseCtx := context.Background()
	ctx, cancel := context.WithCancel(baseCtx)
	cancel()

	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{mode: sop.ForWriting, maxTime: 10 * time.Millisecond, StoreRepository: sr, registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Minimal backend: report tracked items so phase1Commit enters the loop, no mutations required.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_timeout_ctx", SlotLength: 4})
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](4, 8), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count},
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
		t.Fatalf("expected timeout error (context canceled), got nil")
	}
}

// errOnceOnAddReg induces a sector-timeout-like sop.Error on the first Add call, then delegates to inner.
type errOnceOnAddReg struct {
	inner       *mocks.Mock_vid_registry
	lk          sop.LockKey
	tripped     bool
	removeCount int
}

func (r *errOnceOnAddReg) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if !r.tripped {
		r.tripped = true
		return sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("sector timeout"), UserData: &r.lk}
	}
	return r.inner.Add(ctx, storesHandles)
}
func (r *errOnceOnAddReg) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Update(ctx, storesHandles)
}
func (r *errOnceOnAddReg) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.UpdateNoLocks(ctx, allOrNothing, storesHandles)
}
func (r *errOnceOnAddReg) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return r.inner.Get(ctx, storesLids)
}
func (r *errOnceOnAddReg) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	r.removeCount++
	return r.inner.Remove(ctx, storesLids)
}
func (r *errOnceOnAddReg) Replicate(ctx context.Context, newRootNodeHandles, addedNodeHandles, updatedNodeHandles, removedNodeHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Replicate(ctx, newRootNodeHandles, addedNodeHandles, updatedNodeHandles, removedNodeHandles)
}

// Covers phase1Commit branch in commitAddedNodes where sop.Error triggers handleRegistrySectorLockTimeout;
// after rollback and refetch, retry succeeds and phase1Commit completes.
func Test_Phase1Commit_CommitAddedNodes_SectorTimeout_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	induced := &errOnceOnAddReg{inner: baseReg, lk: sop.LockKey{Key: l2.FormatLockKey("X"), LockID: sop.NewUUID()}}

	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{mode: sop.ForWriting, maxTime: 250 * time.Millisecond, StoreRepository: sr, registry: induced, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Only an added node is required to exercise commitAddedNodes branch.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_added_sector_timeout_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: addAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 0}}

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
		t.Fatalf("expected success after sector-timeout retry on commitAddedNodes, got: %v", err)
	}
}

// Asserts that a sector-timeout during commitAddedNodes triggers rollback (registry.Remove invoked)
// before retrying and succeeding.
func Test_Phase1Commit_CommitAddedNodes_SectorTimeout_NoRollbackAndRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	induced := &errOnceOnAddReg{inner: baseReg, lk: sop.LockKey{Key: l2.FormatLockKey("Y"), LockID: sop.NewUUID()}}

	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{mode: sop.ForWriting, maxTime: 250 * time.Millisecond, StoreRepository: sr, registry: induced, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_added_sector_timeout_rollback", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: addAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 0}}

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
		t.Fatalf("phase1Commit error: %v", err)
	}
	// No rollback (registry.Remove) is expected at the exact commitAddedNodes failure point; rollbackAddedNodes only
	// occurs when committedState > commitAddedNodes, which is not the case here.
	if induced.removeCount != 0 {
		t.Fatalf("did not expect registry.Remove during sector-timeout handling at commitAddedNodes; got %d", induced.removeCount)
	}
	// After retry, the handle for the added node should be registered.
	if _, ok := baseReg.Lookup[id]; !ok {
		t.Fatalf("expected handle for added node to be registered after retry")
	}
}

// onceMismatchGetReg returns IsDeleted=true on first Get for target IDs to force commitRemovedNodes to return false,
// then delegates to inner for subsequent calls to allow success on retry.
type onceMismatchGetReg struct {
	inner   *mocks.Mock_vid_registry
	tripped bool
}

func (r *onceMismatchGetReg) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Add(ctx, storesHandles)
}
func (r *onceMismatchGetReg) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Update(ctx, storesHandles)
}
func (r *onceMismatchGetReg) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.UpdateNoLocks(ctx, allOrNothing, storesHandles)
}
func (r *onceMismatchGetReg) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	if !r.tripped {
		r.tripped = true
		out := make([]sop.RegistryPayload[sop.Handle], len(storesLids))
		for i := range storesLids {
			out[i].RegistryTable = storesLids[i].RegistryTable
			out[i].IDs = make([]sop.Handle, len(storesLids[i].IDs))
			for ii := range storesLids[i].IDs {
				h := sop.NewHandle(storesLids[i].IDs[ii])
				h.IsDeleted = true // force mismatch path in commitRemovedNodes
				out[i].IDs[ii] = h
			}
		}
		return out, nil
	}
	return r.inner.Get(ctx, storesLids)
}
func (r *onceMismatchGetReg) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	return r.inner.Remove(ctx, storesLids)
}
func (r *onceMismatchGetReg) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Replicate(ctx, a, b, c, d)
}

// Covers commitRemovedNodes returning false (mismatch) -> rollback -> needsRefetchAndMerge -> retry success.
func Test_Phase1Commit_CommitRemovedNodes_Mismatch_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := &onceMismatchGetReg{inner: baseReg}

	tx := &Transaction{mode: sop.ForWriting, maxTime: 500 * time.Millisecond, StoreRepository: sr, registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_removed_mismatch_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	// Use a removed node; commitRemovedNodes will check registry handle (first call: IsDeleted=true -> false result; second: normal -> success)
	nr.localCache[id] = cachedNode{action: removeAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed a good handle for retry path
	h := sop.NewHandle(id)
	h.Version = 1
	baseReg.Lookup[id] = h

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
		t.Fatalf("expected success after mismatch->rollback->retry for removed nodes, got: %v", err)
	}
}

// errOnceIsLocked wraps cache to return an error on the first IsLocked call, then behaves normally.
type errOnceIsLocked struct {
	sop.L2Cache
	tripped bool
}

func (m *errOnceIsLocked) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !m.tripped {
		m.tripped = true
		return false, fmt.Errorf("islocked err once")
	}
	return m.L2Cache.IsLocked(ctx, lockKeys)
}

// Covers phase1Commit branch where IsLocked returns an error: it should sleep and continue, then succeed.
func Test_Phase1Commit_IsLockedErrorThenSucceed(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &errOnceIsLocked{L2Cache: base}
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: sr, registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed registry with matching handle so commitUpdatedNodes can proceed after the IsLocked error retry.
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

	if err := tx.phase1Commit(ctx); err == nil || err.Error() != "islocked err once" {
		t.Fatalf("expected error from IsLocked, got: %v", err)
	}
}

// onceVersionMismatchReg returns handles with mismatched Version on first Get to force commitUpdatedNodes=false.
type onceVersionMismatchReg struct {
	inner   *mocks.Mock_vid_registry
	tripped bool
}

func (r *onceVersionMismatchReg) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Add(ctx, storesHandles)
}
func (r *onceVersionMismatchReg) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Update(ctx, storesHandles)
}
func (r *onceVersionMismatchReg) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.UpdateNoLocks(ctx, allOrNothing, storesHandles)
}
func (r *onceVersionMismatchReg) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	if !r.tripped {
		r.tripped = true
		out := make([]sop.RegistryPayload[sop.Handle], len(storesLids))
		for i := range storesLids {
			out[i].RegistryTable = storesLids[i].RegistryTable
			out[i].IDs = make([]sop.Handle, len(storesLids[i].IDs))
			for ii := range storesLids[i].IDs {
				h := sop.NewHandle(storesLids[i].IDs[ii])
				// Purposely mismatch version: commitUpdatedNodes compares to node version 1; use 2 here.
				h.Version = 2
				out[i].IDs[ii] = h
			}
		}
		return out, nil
	}
	return r.inner.Get(ctx, storesLids)
}
func (r *onceVersionMismatchReg) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	return r.inner.Remove(ctx, storesLids)
}
func (r *onceVersionMismatchReg) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Replicate(ctx, a, b, c, d)
}

// Covers commitUpdatedNodes returning false (version mismatch) on first attempt then success after refetch & retry.
func Test_Phase1Commit_CommitUpdatedNodes_VersionMismatch_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	sr := mocks.NewMockStoreRepository()

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := &onceVersionMismatchReg{inner: baseReg}
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: sr, registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_updated_mismatch_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	// Node version is 1; first Get will return version 2 to force mismatch.
	nr.localCache[id] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed base registry with matching handle for retry path.
	h := sop.NewHandle(id)
	h.Version = 1
	baseReg.Lookup[id] = h

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
		t.Fatalf("expected success after updated-nodes version mismatch retry, got: %v", err)
	}
}

// failingGetWithTTLStoreRepo wraps a StoreRepository to force GetWithTTL to return an error once.
type failingGetWithTTLStoreRepo struct {
	inner   sop.StoreRepository
	tripped bool
}

func (f *failingGetWithTTLStoreRepo) Add(ctx context.Context, s ...sop.StoreInfo) error {
	return f.inner.Add(ctx, s...)
}
func (f *failingGetWithTTLStoreRepo) Update(ctx context.Context, s []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return f.inner.Update(ctx, s)
}
func (f *failingGetWithTTLStoreRepo) Remove(ctx context.Context, name ...string) error {
	return f.inner.Remove(ctx, name...)
}
func (f *failingGetWithTTLStoreRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return f.inner.Get(ctx, names...)
}
func (f *failingGetWithTTLStoreRepo) Replicate(ctx context.Context, stores []sop.StoreInfo) error {
	return f.inner.Replicate(ctx, stores)
}
func (f *failingGetWithTTLStoreRepo) GetAll(ctx context.Context) ([]string, error) {
	return f.inner.GetAll(ctx)
}
func (f *failingGetWithTTLStoreRepo) GetWithTTL(ctx context.Context, isCacheTTL bool, cacheDuration time.Duration, names ...string) ([]sop.StoreInfo, error) {
	if !f.tripped {
		f.tripped = true
		return nil, fmt.Errorf("induced GetWithTTL error")
	}
	return f.inner.GetWithTTL(ctx, isCacheTTL, cacheDuration, names...)
}

// Covers refetchAndMergeClosure early error path when StoreRepository.GetWithTTL fails.
func Test_RefetchAndMerge_GetWithTTLError_ReturnsError(t *testing.T) {
	ctx := context.Background()

	// Base repository with an added store; wrap to fail GetWithTTL once.
	base := mocks.NewMockStoreRepository()
	so := sop.StoreOptions{Name: "rfm_ttl_err", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = base.Add(ctx, *ns)
	sr := &failingGetWithTTLStoreRepo{inner: base}

	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed a tracked getAction (won't be reached due to early error, but keeps setup realistic)
	pk, pv := newPerson("te", "st", "m", "e@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatalf("seed find failed")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: getAction},
		item:        &cur,
		versionInDB: cur.Version,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil || err.Error() != "induced GetWithTTL error" {
		t.Fatalf("expected GetWithTTL error to propagate, got: %v", err)
	}
}

// Covers addAction duplicate error path when values are in node segment (unique tree): Add returns !ok.
func Test_RefetchAndMerge_Add_InNode_DuplicateKey_ReturnsError_Alt(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_add_dup_in", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true}
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

	// Seed an item and persist root for refetch.
	pk, pv := newPerson("ad", "in", "m", "a@b", "p")
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

	// Track addAction with the same key to trigger duplicate on Add (in-node segment path).
	dup := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[dup.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        &dup,
		versionInDB: 0,
	}

	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err == nil {
		t.Fatalf("expected duplicate error for in-node add, got nil")
	}
}

// Ensures rollback returns lastErr when encountering addActivelyPersistedItem (blob remove error)
// and finalizeCommit has nil payload with lastCommittedFunctionLog >= deleteObsoleteEntries.
func Test_TransactionLogger_Rollback_AddActive_RemoveError_WithFinalizeNil_ReturnsLastErr(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	// Reuse existing errBlobStore helper from coverage_boost_test.go by constructing with specific error.
	ebs := errBlobStore{err: errors.New("remove induced error")}
	tx := &Transaction{blobStore: ebs, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}

	// Build logs: [finalizeCommit(nil), addActivelyPersistedItem(payload), deleteObsoleteEntries(last)]
	// lastCommittedFunctionLog will be deleteObsoleteEntries due to last entry.
	tid := sop.NewUUID()

	// Payload for addActivelyPersistedItem: one blobs payload
	payload := toByteArray(sop.BlobsPayload[sop.UUID]{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}})

	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: nil},
		{Key: addActivelyPersistedItem, Value: payload},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	if err := tl.rollback(ctx, tx, tid, logs); err == nil || err.Error() != "remove induced error" {
		t.Fatalf("expected lastErr from blob remove, got: %v", err)
	}
}

// Ensures rollback processes finalizeCommit non-nil payload with lastCommittedFunctionLog == deleteTrackedItemsValues
// and calls deleteTrackedItemsValues; we simulate a blob remove error to observe propagation.
func Test_TransactionLogger_Rollback_Finalize_WithDeleteTrackedItemsValues_PropagatesErr(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	// err blob store to force error in deleteTrackedItemsValues
	ebs := errBlobStore{err: errors.New("tracked remove induced error")}
	// Non-nil registry avoids panic when deleteObsoleteEntries is also invoked by rollback.
	rg := mocks.NewMockRegistry(false)
	tx := &Transaction{blobStore: ebs, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: rg, StoreRepository: mocks.NewMockStoreRepository()}

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tid := sop.NewUUID()

	// Build finalize payload using toByteArray with Second as items-for-delete (non-empty)
	toDelete := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{
		First:  false,
		Second: sop.BlobsPayload[sop.UUID]{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}},
	}}
	fin := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		Second: toDelete,
	}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: toByteArray(fin)},
		{Key: deleteTrackedItemsValues, Value: nil}, // mark last to trigger that branch
	}
	if err := tl.rollback(ctx, tx, tid, logs); err == nil || err.Error() != "tracked remove induced error" {
		t.Fatalf("expected error from deleteTrackedItemsValues, got: %v", err)
	}
}

// partialLockCache forces Lock to succeed but IsLocked to report false (no error),
// to exercise acquireLocks partial-lock failover branch.
type partialLockCache struct{ sop.L2Cache }

func (p *partialLockCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return true, sop.NilUUID, nil
}
func (p *partialLockCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, nil
}

func (p *partialLockCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := p.Lock(ctx, duration, lockKeys)
	if err != nil || !ok {
		return ok, tid, err
	}
	if locked, err := p.IsLocked(ctx, lockKeys); err != nil || !locked {
		if err == nil {
			err = sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("failover")}
		}
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

// Ensures refetchAndMergeClosure returns an error when AddItem returns false with no error
// for separate-segment values (IsValueDataInNodeSegment=false) on a unique tree.
func Test_RefetchAndMerge_AddItem_SeparateSegment_DuplicateFalse_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	// Separate segment and unique to trigger AddItem duplicate=false path during refetch.
	so := sop.StoreOptions{Name: "rfm_additem_dup_sep", SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: false}
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

	// Seed an item and persist root for refetch baseline.
	pk, pv := newPerson("ad", "sp", "m", "a@b", "p")
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

	// Reset tracker to only include our duplicate addAction.
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	dup := btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[dup.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        &dup,
		versionInDB: 0,
	}

	// Expect error due to AddItem returning false (duplicate) without inner error.
	if err := refetchAndMergeClosure(&si, b3, sr)(ctx); err == nil || !strings.Contains(err.Error(), "failed to merge add item") {
		t.Fatalf("expected duplicate error for separate-segment add, got: %v", err)
	}
}

// Ensures acquireLocks returns a failover error when Lock succeeds but IsLocked reports false (partial lock scenario).
func Test_TransactionLogger_AcquireLocks_PartialLock_FailoverError(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	plc := &partialLockCache{L2Cache: base}
	cache.GetGlobalL1Cache(plc)

	tx := &Transaction{l2Cache: plc, l1Cache: cache.GetGlobalL1Cache(plc), registry: mocks.NewMockRegistry(false), StoreRepository: mocks.NewMockStoreRepository(), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	id := sop.NewUUID()
	stores := []sop.RegistryPayload[sop.Handle]{
		{RegistryTable: "r", IDs: []sop.Handle{sop.NewHandle(id)}},
	}
	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected RestoreRegistryFileSectorFailure, got: %v", err)
	}
}
