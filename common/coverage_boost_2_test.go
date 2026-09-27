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

// --- ItemActionTracker lock/unlock additional error paths ---

type setErrCache struct{ sop.L2Cache }

func (s setErrCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return fmt.Errorf("setstruct fail")
}
func (s setErrCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return fmt.Errorf("setstruct fail")
}

// missAlwaysCache forces GetStruct to miss regardless of prior SetStruct calls;
// used to simulate the second get after a set also missing, triggering the
// "can't attain a lock" branch in itemActionTracker.lock.
type missAlwaysCache struct{ sop.L2Cache }

func (m missAlwaysCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return false, nil
}

func (m missAlwaysCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	results := make([]bool, len(keys))
	return results, nil
}

type deleteErrCache struct{ sop.L2Cache }

func (d deleteErrCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return false, fmt.Errorf("delete fail")
}

// errIsLockedCache returns an error from IsLocked after a successful Lock to hit the unlock+error branch.
type errIsLockedCache struct{ sop.L2Cache }

func (e errIsLockedCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, fmt.Errorf("islocked err")
}

func (e errIsLockedCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := e.Lock(ctx, duration, lockKeys)
	if !ok || err != nil {
		return ok, tid, err
	}
	if _, err := e.IsLocked(ctx, lockKeys); err != nil {
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

// (removed) errUpdateRegistry: use mocks.NewMockRegistry(true) to induce Update errors instead.

// lockErrCache forces Lock to return an error to cover acquireLocks' error branch.
type lockErrCache struct{ sop.L2Cache }

func (l lockErrCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("lock err")
}

func (l lockErrCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return l.Lock(ctx, duration, lockKeys)
}

// prioLogRemoveErr implements TransactionPriorityLog with Remove returning error to exercise warn path.
type prioLogRemoveErr struct{}

func (p prioLogRemoveErr) IsEnabled() bool                                             { return true }
func (p prioLogRemoveErr) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (p prioLogRemoveErr) Remove(ctx context.Context, tid sop.UUID) error {
	return fmt.Errorf("prio remove err")
}
func (p prioLogRemoveErr) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (p prioLogRemoveErr) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (p prioLogRemoveErr) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (p prioLogRemoveErr) LogCommitChanges(ctx context.Context, _ []sop.StoreInfo, _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle], _ []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// wrapTLPrioRemoveErr delegates to inner TransactionLog but returns prioLogRemoveErr for PriorityLog.
type wrapTLPrioRemoveErr struct{ inner *mocks.MockTransactionLog }

func (w wrapTLPrioRemoveErr) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOne(ctx)
}
func (w wrapTLPrioRemoveErr) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOneOfHour(ctx, hour)
}
func (w wrapTLPrioRemoveErr) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return w.inner.Add(ctx, tid, commitFunction, payload)
}
func (w wrapTLPrioRemoveErr) Remove(ctx context.Context, tid sop.UUID) error {
	return w.inner.Remove(ctx, tid)
}
func (w wrapTLPrioRemoveErr) NewUUID() sop.UUID                       { return w.inner.NewUUID() }
func (w wrapTLPrioRemoveErr) PriorityLog() sop.TransactionPriorityLog { return prioLogRemoveErr{} }

func Test_ItemActionTracker_Lock_Error_Paths(t *testing.T) {
	ctx := context.Background()
	blobs := mocks.NewMockBlobStore()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_errors", SlotLength: 4})
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	id := sop.NewUUID()
	pk, p := newPerson("lkE", "c", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1}

	tests := []struct {
		name            string
		cache           sop.L2Cache
		expectErrSubstr string
		unlock          bool
	}{
		{name: "setstruct_error", cache: setErrCache{L2Cache: mocks.NewMockClient()}, expectErrSubstr: "setstruct fail"},
		{name: "post_set_get_miss", cache: missAlwaysCache{L2Cache: mocks.NewMockClient()}, expectErrSubstr: "can't attain a lock"},
		{name: "unlock_delete_error", cache: deleteErrCache{L2Cache: mocks.NewMockClient()}, expectErrSubstr: "delete fail", unlock: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trk := newItemActionTracker[PersonKey, Person](si, tc.cache, blobs, tl)
			if err := trk.Get(ctx, it); err != nil {
				t.Fatalf("Get err: %v", err)
			}
			// Move to update to exercise stricter lock behavior
			ci := trk.items[id]
			ci.Action = updateAction
			trk.items[id] = ci

			err := trk.lock(ctx, time.Minute)
			if tc.unlock {
				// If lock passed, force isLockOwner then unlock to trigger delete error
				ci := trk.items[id]
				ci.isLockOwner = true
				trk.items[id] = ci
				uerr := trk.unlock(ctx)
				if uerr == nil || (tc.expectErrSubstr != "" && !strings.Contains(uerr.Error(), tc.expectErrSubstr)) {
					t.Fatalf("expected unlock error containing %q, got: %v", tc.expectErrSubstr, uerr)
				}
				return
			}
			if err == nil || (tc.expectErrSubstr != "" && !strings.Contains(err.Error(), tc.expectErrSubstr)) {
				t.Fatalf("expected lock error containing %q, got: %v", tc.expectErrSubstr, err)
			}
		})
	}
}

// Cover rollbackUpdatedNodes branches with errors and nodesAreLocked true/false paths.
func Test_NodeRepository_RollbackUpdatedNodes_ErrorBranches(t *testing.T) {
	ctx := context.Background()
	// Case 1: nodesAreLocked=true uses UpdateNoLocks; blob remove and redis delete error captured.
	l2 := deleteErrCache{L2Cache: mocks.NewMockClient()}
	bs := errBlobStore{err: fmt.Errorf("blob rm err")}
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{l2Cache: l2, blobStore: bs, registry: rg}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_errs", SlotLength: 4})
	gc := cache.GetGlobalL1Cache(l2)
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, l2Cache: l2, l1Cache: gc}

	// Prepare vids and registry handles: one with inactive ID set, one without to hit WorkInProgressTimestamp reset path.
	lid1, lid2 := sop.NewUUID(), sop.NewUUID()
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{lid1, lid2}}}
	h1, h2 := sop.NewHandle(lid1), sop.NewHandle(lid2)
	// Set an inactive ID for h1 to trigger blob deletion, h2 remains without inactive ID.
	_ = h1.AllocateID()
	rg.Lookup[lid1] = h1
	rg.Lookup[lid2] = h2
	if err := nr.rollbackUpdatedNodes(ctx, true, vids); err == nil {
		t.Fatalf("expected error from rollbackUpdatedNodes with blob/remove redis errors")
	}

	// Case 2: nodesAreLocked=false uses Update; induce Update error via mock flag.
	rg2 := mocks.NewMockRegistry(true).(*mocks.Mock_vid_registry)
	tx2 := &Transaction{l2Cache: mocks.NewMockClient(), blobStore: mocks.NewMockBlobStore(), registry: rg2}
	nr2 := &nodeRepositoryBackend{transaction: tx2, storeInfo: si, l2Cache: tx2.l2Cache, l1Cache: gc}
	// Seed registry with inactive IDs so code attempts Update.
	lid3 := sop.NewUUID()
	vids2 := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{lid3}}}
	h3 := sop.NewHandle(lid3)
	_ = h3.AllocateID()
	rg2.Lookup[lid3] = h3
	if err := nr2.rollbackUpdatedNodes(ctx, false, vids2); err == nil || !strings.Contains(err.Error(), "induced error on Update") {
		t.Fatalf("expected registry update error, got: %v", err)
	}
}

func Test_NodeRepository_RollbackAddedNodes_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	// Error on blob remove + registry remove + redis delete -> lastErr must be non-nil
	l2 := deleteErrCache{L2Cache: mocks.NewMockClient()}
	rg := errRemoveRegistry{Mock_vid_registry: mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry), err: fmt.Errorf("reg rm err")}
	bs := errBlobStore{err: fmt.Errorf("blob rm err")}
	tx := &Transaction{l2Cache: l2, registry: rg, blobStore: bs}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_added_errs", SlotLength: 4})
	gc := cache.GetGlobalL1Cache(l2)
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}

	lid := sop.NewUUID()
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	if err := nr.rollbackAddedNodes(ctx, sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}); err == nil {
		t.Fatalf("expected aggregated error from rollbackAddedNodes")
	}
}

func Test_NodeRepository_RemoveNodes_DeleteError_PropagatesLastErr(t *testing.T) {
	ctx := context.Background()
	l2 := deleteErrCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2, blobStore: mocks.NewMockBlobStore()}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rm_nodes_del_err", SlotLength: 4})
	gc := cache.GetGlobalL1Cache(l2)
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, l2Cache: l2, l1Cache: gc}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	if err := nr.removeNodes(ctx, bibs); err == nil {
		t.Fatalf("expected delete error to propagate as lastErr")
	}
}

func Test_TransactionLogger_Rollback_Precommit_AddActivelyPersistedItem(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{blobStore: mocks.NewMockBlobStore()}
	blobID := sop.NewUUID()
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: addActivelyPersistedItem, Value: toByteArray(sop.BlobsPayload[sop.UUID]{BlobTable: "bt", Blobs: []sop.UUID{blobID}})},
	}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("precommit rollback err: %v", err)
	}
}

func Test_TransactionLogger_Rollback_EmptyLogs_RemovesTid(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.rollback(ctx, &Transaction{}, sop.NewUUID(), nil); err != nil {
		t.Fatalf("expected nil on empty logs with tid, got: %v", err)
	}
}

func Test_Phase2Commit_WithUpdatedHandles_Warns_And_Cleans(t *testing.T) {
	ctx := context.Background()
	// Seed caches
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	// Registry and store repo
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	// Priority log that errors on Remove (warn path)
	baseTL := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	tl := newTransactionLogger(wrapTLPrioRemoveErr{inner: baseTL}, true)

	tx := &Transaction{mode: sop.ForWriting, phaseDone: 1, StoreRepository: sr, registry: rg, logger: tl, l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore()}

	// Provide updated handles so UpdateNoLocks path triggers and Remove is attempted
	h := sop.NewHandle(sop.NewUUID())
	tx.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}

	// Backend stubs: unlockTrackedItems returns error to exercise warn path after commit
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_warns2", SlotLength: 4})
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   &nodeRepositoryBackend{transaction: tx, localCache: make(map[sop.UUID]cachedNode)},
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return false },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return fmt.Errorf("unlock fail") },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
	}}

	if err := tx.Phase2Commit(ctx); err != nil {
		t.Fatalf("Phase2Commit unexpected err: %v", err)
	}
}

func Test_TransactionLogger_AcquireLocks_LockError_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	l2 := lockErrCache{L2Cache: mocks.NewMockClient()}
	tnx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tid := sop.NewUUID()
	ids := []sop.Handle{{LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}
	if _, err := tl.acquireLocks(ctx, tnx, tid, stores); err == nil {
		t.Fatalf("expected error when Lock returns error")
	}
}

func Test_TransactionLogger_AcquireLocks_IsLocked_Error_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	ec := errIsLockedCache{L2Cache: base}
	txn := &Transaction{l2Cache: ec}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tid := sop.NewUUID()
	ids := []sop.Handle{{LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}
	if _, err := tl.acquireLocks(ctx, txn, tid, stores); err == nil || !strings.Contains(err.Error(), "islocked err") {
		t.Fatalf("expected islocked err, got: %v", err)
	}
}

// Exercise phase1Commit branch where commitUpdatedNodes returns false, triggering rollback and retry.
func Test_Phase1Commit_Conflict_Then_RefetchAndRetry_Succeeds(t *testing.T) {
	ctx := context.Background()
	// Redis/L1
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)
	// Transaction wiring
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0, id: sop.NewUUID()}

	// Store/node repo with one updated node
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_retry", SlotLength: 4, IsValueDataInNodeSegment: true})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: gc, count: si.Count}
	uid := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: uid, Version: 2}
	nr.localCache[uid] = cachedNode{action: updateAction, node: n}
	// Registry has stale version (1) initially to force commitUpdatedNodes false.
	h := sop.NewHandle(uid)
	h.Version = 1
	rg.Lookup[uid] = h

	// Backend with refetch that updates registry to match, so second attempt succeeds.
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
			// Align registry version to match node after first failure.
			if !retried {
				hh := rg.Lookup[uid]
				hh.Version = 2
				rg.Lookup[uid] = hh
				retried = true
			}
			return nil
		},
	}}

	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit expected to succeed after retry, err: %v", err)
	}
	if !retried {
		t.Fatalf("expected refetch/merge to be invoked on retry")
	}
}

// Cover NodeRepository.get branches: local removeAction, read MRU hit, and blob fetch path.
func Test_NodeRepository_Get_Paths(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	l1 := cache.NewL1Cache(l2, cache.DefaultMinCapacity, cache.DefaultMaxCapacity)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{l2Cache: l2, l1Cache: l1, blobStore: bs, registry: rg}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "nr_get", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: l1}

	// 1) local removeAction returns nil
	lidRem := sop.NewUUID()
	nr.localCache[lidRem] = cachedNode{action: removeAction, node: &btree.Node[PersonKey, Person]{ID: lidRem}}
	if v, err := nr.get(ctx, lidRem, &btree.Node[PersonKey, Person]{}); err != nil || v != nil {
		t.Fatalf("expected nil for removed local cache, got v=%v err=%v", v, err)
	}

	// 2) readNodesCache hit returns value
	lidRead := sop.NewUUID()
	tgt := &btree.Node[PersonKey, Person]{ID: lidRead}
	nr.readNodesCache.Set([]sop.KeyValuePair[sop.UUID, any]{{Key: lidRead, Value: tgt}})
	if v, err := nr.get(ctx, lidRead, &btree.Node[PersonKey, Person]{}); err != nil || v == nil {
		t.Fatalf("expected readNodesCache hit, got v=%v err=%v", v, err)
	}

	// 3) MRU path when phaseDone==0
	tx.phaseDone = 0
	lidMru := sop.NewUUID()
	h := sop.NewHandle(lidMru)
	h.Version = 7
	// Store handle in L1 Handles and node in MRU with matching version.
	l1.Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: lidMru, Value: h}})
	nodeMru := &btree.Node[PersonKey, Person]{ID: h.GetActiveID(), Version: 7}
	l1.SetNodeToMRU(ctx, nodeMru.ID, nodeMru, time.Minute)
	if v, err := nr.get(ctx, lidMru, &btree.Node[PersonKey, Person]{}); err != nil || v == nil {
		t.Fatalf("expected MRU hit, got v=%v err=%v", v, err)
	}

	// 4) BlobStore path when not in MRU; seed registry and blob store.
	tx.phaseDone = 1 // force skip of L1 Handles check so it goes through registry+blob path
	lid := sop.NewUUID()
	h2 := sop.NewHandle(lid)
	h2.Version = 3
	rg.Lookup[lid] = h2
	// Add blob
	node := &btree.Node[PersonKey, Person]{ID: h2.GetActiveID(), Version: 0}
	// Marshal via encoding inside Add by passing KeyValuePair with Key=node ID and marshaled value in Value
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: si.BlobTable, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: node.ID, Value: toByteArray(node)}}}}); err != nil {
		t.Fatalf("blob add err: %v", err)
	}
	if v, err := nr.get(ctx, lid, &btree.Node[PersonKey, Person]{}); err != nil || v == nil {
		t.Fatalf("expected blob fetch path, got v=%v err=%v", v, err)
	}
}

func Test_NodeRepository_RollbackNewRootNodes_ErrorPaths_NoUnregister(t *testing.T) {
	ctx := context.Background()
	l2 := deleteErrCache{L2Cache: mocks.NewMockClient()}
	bs := errBlobStore{err: fmt.Errorf("blob rm err")}
	rg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, blobStore: bs, registry: rg, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_newroot_err", SlotLength: 4})
	gc := cache.GetGlobalL1Cache((l2))
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, l2Cache: l2, l1Cache: gc}
	lid := sop.NewUUID()
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: si.BlobTable, Blobs: []sop.UUID{sop.NewUUID()}}}
	// Ensure committedState <= commitNewRootNodes to skip unregister branch.
	tx.logger.committedState = commitNewRootNodes
	if err := nr.rollbackNewRootNodes(ctx, sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}); err == nil {
		t.Fatalf("expected error from rollbackNewRootNodes due to blob/redis errors")
	}
}

// deleteObsoleteEntries should prefer the last error (registry) after prior L1 delete warnings and blob remove errors.
func Test_DeleteObsoleteEntries_ErrorPrecedence(t *testing.T) {
	ctx := context.Background()
	// L1 with L2 delete error, blob remove error, and registry remove error
	l2 := deleteErrCache{L2Cache: mocks.NewMockClient()}
	l1 := cache.NewL1Cache(l2, cache.DefaultMinCapacity, cache.DefaultMaxCapacity)
	bs := errBlobStore{err: fmt.Errorf("blob rm err2")}
	rg := errRemoveRegistry{Mock_vid_registry: mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry), err: fmt.Errorf("reg rm err2")}

	tx := &Transaction{l1Cache: l1, blobStore: bs, registry: rg}
	// Prepare inputs
	del := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{sop.NewUUID()}}}
	unused := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}}}
	if err := tx.deleteObsoleteEntries(ctx, del, unused); err == nil || !strings.Contains(err.Error(), "reg rm err2") {
		t.Fatalf("expected registry error precedence, got: %v", err)
	}
}

// AcquireLocks should fail with RestoreRegistryFileSectorFailure when IsLocked returns false after a successful Lock.
type isLockedFalseCache struct{ sop.L2Cache }

func (c isLockedFalseCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return false, nil
}

func (c isLockedFalseCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
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

func Test_TransactionLogger_AcquireLocks_IsLockedFalse_RaisesFailover(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	c := isLockedFalseCache{L2Cache: base}
	txn := &Transaction{l2Cache: c}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tid := sop.NewUUID()
	ids := []sop.Handle{{LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}
	if _, err := tl.acquireLocks(ctx, txn, tid, stores); err == nil {
		t.Fatalf("expected failover error when IsLocked=false")
	} else if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected RestoreRegistryFileSectorFailure, got: %v", err)
	}
}

// Cover rollbackRemovedNodes for both nodesAreLocked branches with successful updates.
func Test_NodeRepository_RollbackRemovedNodes_BothPaths(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_removed", SlotLength: 4})

	// Case 1: nodesAreLocked=true -> UpdateNoLocks
	rg1 := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	l2 := mocks.NewMockClient()
	tx1 := &Transaction{registry: rg1, l2Cache: l2}
	gc := cache.GetGlobalL1Cache(l2)
	nr1 := &nodeRepositoryBackend{transaction: tx1, storeInfo: si, l2Cache: l2, l1Cache: gc}
	lid1 := sop.NewUUID()
	h1 := sop.NewHandle(lid1)
	h1.IsDeleted = true
	rg1.Lookup[lid1] = h1
	vids1 := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid1}}}
	if err := nr1.rollbackRemovedNodes(ctx, true, vids1); err != nil {
		t.Fatalf("rollbackRemovedNodes(true) err: %v", err)
	}

	// Case 2: nodesAreLocked=false -> Update
	rg2 := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx2 := &Transaction{registry: rg2, l2Cache: l2}
	nr2 := &nodeRepositoryBackend{transaction: tx2, storeInfo: si, l2Cache: l2, l1Cache: gc}
	lid2 := sop.NewUUID()
	h2 := sop.NewHandle(lid2)
	h2.WorkInProgressTimestamp = 123
	rg2.Lookup[lid2] = h2
	vids2 := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{lid2}}}
	if err := nr2.rollbackRemovedNodes(ctx, false, vids2); err != nil {
		t.Fatalf("rollbackRemovedNodes(false) err: %v", err)
	}
}

// (removed) Duplicate of Test_HandleRegistrySectorLockTimeout_Succeeds in twopc_phase2_paths_scenarios_test.go

// cleanup should log both delete steps and remove transaction logs.
func Test_Transaction_Cleanup_RemovesLogs(t *testing.T) {
	ctx := context.Background()
	rtl := &recTL{}
	tl := newTransactionLogger(rtl, true)
	// L1/L2 and stores
	l2 := mocks.NewMockClient()
	l1 := cache.NewL1Cache(l2, cache.DefaultMinCapacity, cache.DefaultMaxCapacity)
	rg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	tx := &Transaction{l1Cache: l1, l2Cache: l2, registry: rg, blobStore: bs, logger: tl}
	// Seed some handles to produce non-empty obsolete entries
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "cleanup", SlotLength: 4})
	h := sop.NewHandle(sop.NewUUID())
	tx.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.Handle{h}}}
	tx.removedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.Handle{h}}}
	if err := tx.cleanup(ctx); err != nil {
		t.Fatalf("cleanup err: %v", err)
	}
	if rtl.removes == 0 {
		t.Fatalf("expected removeLogs to be invoked")
	}
}

// alterSetCache overrides SetStruct to store a different LockID to simulate a concurrent writer winning the lock.
type alterSetCache struct{ sop.L2Cache }

func (a alterSetCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if lr, ok := value.(*lockRecord); ok {
		// Change the LockID to a different UUID before delegating.
		fake := &lockRecord{LockID: sop.NewUUID(), Action: lr.Action}
		return a.L2Cache.SetStruct(ctx, key, fake, expiration)
	}
	return a.L2Cache.SetStruct(ctx, key, value, expiration)
}
func (a alterSetCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	newValues := make([]interface{}, len(values))
	for i, v := range values {
		if lr, ok := v.(*lockRecord); ok {
			// Change the LockID to a different UUID.
			newValues[i] = &lockRecord{LockID: sop.NewUUID(), Action: lr.Action}
		} else {
			newValues[i] = v
		}
	}
	return a.L2Cache.SetStructs(ctx, keys, newValues, expiration)
}

func Test_ItemActionTracker_Lock_SetThenGet_MismatchConflict(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	ac := alterSetCache{L2Cache: base}
	blobs := mocks.NewMockBlobStore()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_conflict", SlotLength: 4})
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, ac, blobs, tl)

	id := sop.NewUUID()
	pk, p := newPerson("lk2", "c", "m", "e2@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1}
	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	// Force action to update to make mismatch non-compatible so it errors.
	ci := trk.items[id]
	ci.Action = updateAction
	trk.items[id] = ci
	if err := trk.lock(ctx, time.Minute); err == nil {
		t.Fatalf("expected conflict error due to SetStruct storing a different LockID")
	}
}

// Finalize payload covers deleteTrackedItemsValues branch without deleteObsoleteEntries and ensures no Remove call there.
func Test_TransactionLogger_Rollback_FinalizeWithTrackedValuesOnly(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{blobStore: mocks.NewMockBlobStore(), l2Cache: mocks.NewMockClient()}

	// Build payload: no obsolete entries (First empty), only tracked values in Second.
	valID := sop.NewUUID()
	tracked := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{valID}}}}

	// Seed a value blob so deleteTrackedItemsValues has work to do.
	_ = tx.blobStore.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "it", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: valID, Value: []byte("v")}}}})

	pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{},
		Second: tracked,
	}
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: toByteArray(pl)},
		{Key: commitUpdatedNodes, Value: nil},
	}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}
}

// Covers doPriorityRollbacks path when Remove returns error: error is returned and consumed=false.
func Test_TransactionLogger_DoPriorityRollbacks_RemoveError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, registry: reg}

	// Seed lock ownership for the batch processing loop to enter.
	_ = l2.Set(ctx, l2.FormatLockKey(coordinatorLockName), sop.NewUUID().String(), time.Minute)
	_, _ = l2.Delete(ctx, []string{l2.FormatLockKey(coordinatorLockName)})

	tid := sop.NewUUID()
	lid := sop.NewUUID()
	// Seed registry handle so version checks pass.
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}})

	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{
		{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{sop.NewHandle(lid)}}}},
	}, removeErr: map[string]error{tid.String(): context.DeadlineExceeded}}

	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err == nil {
		t.Fatalf("expected Remove error to be returned")
	}
	if consumed {
		t.Fatalf("expected consumed=false on Remove error")
	}
}

// Covers doPriorityRollbacks version-check failover where registry version advanced beyond repairable (not equal or +1).
func Test_TransactionLogger_DoPriorityRollbacks_VersionAdvance_TriggersFailover(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{l2Cache: l2, registry: reg}

	// Unlockable processing lock.
	_ = l2.Set(ctx, l2.FormatLockKey(coordinatorLockName), sop.NewUUID().String(), time.Minute)
	_, _ = l2.Delete(ctx, []string{l2.FormatLockKey(coordinatorLockName)})

	tid := sop.NewUUID()
	lid := sop.NewUUID()
	// Prepare priority log with version 1, but seed registry with version 3 to force failover.
	h := sop.NewHandle(lid)
	h.Version = 3
	reg.Lookup[lid] = h

	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{
		{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{{LogicalID: lid, Version: 1}}}}},
	}}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err == nil {
		t.Fatalf("expected failover error due to version advance")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
	if consumed {
		t.Fatalf("expected consumed=false on early failover")
	}
}

// lockFailOnceCache fails Lock once to force the phase1Commit loop to set needsRefetchAndMerge.
type lockFailOnceCache struct {
	sop.L2Cache
	flipped bool
}

func (c *lockFailOnceCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	if !c.flipped {
		// Only fail if it's not the "notrestarted" key used by onIdle
		if len(lockKeys) > 0 && strings.Contains(lockKeys[0].Key, "notrestarted") {
			return c.L2Cache.Lock(ctx, duration, lockKeys)
		}
		c.flipped = true
		return false, sop.NilUUID, nil
	}
	return c.L2Cache.Lock(ctx, duration, lockKeys)
}

func (c *lockFailOnceCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := c.Lock(ctx, duration, lockKeys)
	if !ok || err != nil {
		return ok, tid, err
	}
	if locked, err := c.L2Cache.IsLocked(ctx, lockKeys); err != nil || !locked {
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func Test_Phase1Commit_LockFailOnce_TriggersRefetchAndMerge(t *testing.T) {
	ctx := context.Background()

	// L2 cache fails first Lock, then succeeds.
	base := mocks.NewMockClient()
	l2 := &lockFailOnceCache{L2Cache: base}
	gc := cache.GetGlobalL1Cache(l2)

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_lock_fail_once", SlotLength: 2})

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: -1}

	// Prepare updated node so nodesKeys are formed and lock path executes.
	lid := sop.NewUUID()
	n := &btree.Node[sop.UUID, int]{ID: lid, Version: 0}
	// Seed registry with handle
	reg.(*mocks.Mock_vid_registry).Lookup[lid] = sop.Handle{LogicalID: lid, PhysicalIDA: lid, Version: 0}

	// Count refetch-and-merge invocations.
	var refetchCount int

	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, localCache: map[sop.UUID]cachedNode{lid: {node: n, action: updateAction}}, l2Cache: l2, l1Cache: gc, count: si.Count}
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
		refetchAndMerge:                  func(context.Context) error { refetchCount++; return nil },
	}}

	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("begin err: %v", err)
	}
	// Reset flipped because Begin -> onIdle might have consumed the failure.
	l2.flipped = false

	if err := tx.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit err: %v", err)
	}
	if refetchCount == 0 {
		t.Fatalf("expected refetchAndMerge to be invoked at least once")
	}
}

// errUpdateRegistry wraps a registry and fails on Update to cover nodesAreLocked=false path in rollbackRemovedNodes.
type errUpdateRegistry struct{ inner sop.Registry }

func (e errUpdateRegistry) Add(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Add(ctx, s)
}
func (e errUpdateRegistry) Update(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return errors.New("update err")
}
func (e errUpdateRegistry) UpdateNoLocks(ctx context.Context, a bool, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.UpdateNoLocks(ctx, a, s)
}
func (e errUpdateRegistry) Get(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return e.inner.Get(ctx, lids)
}
func (e errUpdateRegistry) Remove(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) error {
	return e.inner.Remove(ctx, lids)
}
func (e errUpdateRegistry) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Replicate(ctx, a, b, c, d)
}

func Test_NodeRepository_CommitUpdatedNodes_RegistryUpdateError(t *testing.T) {
	ctx := context.Background()
	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := errUpdateRegistry{inner: baseReg}
	l2 := mocks.NewMockClient()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2}, l2Cache: l2}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_removed_unlocked_upderr", SlotLength: 2})

	id := sop.NewUUID()
	// Handle without deleted/WIP flags; rollback should try Update with empty set and here error will come from Update anyway.
	baseReg.Lookup[id] = sop.NewHandle(id)

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackRemovedNodes(ctx, false, vids); err == nil {
		t.Fatalf("expected error from registry.Update in unlocked path")
	}
}

// tlogFailAdd fails Add to test cleanup log error propagation.
type tlogFailAdd struct{}

func (t tlogFailAdd) PriorityLog() sop.TransactionPriorityLog {
	return mocks.NewMockTransactionLog().PriorityLog()
}
func (t tlogFailAdd) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return errors.New("add fail")
}
func (t tlogFailAdd) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (t tlogFailAdd) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (t tlogFailAdd) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (t tlogFailAdd) NewUUID() sop.UUID { return sop.NewUUID() }

func Test_Transaction_Cleanup_LogError_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(tlogFailAdd{}, true)}

	if err := tx.cleanup(ctx); err == nil {
		t.Fatalf("expected cleanup to propagate log(Add) error")
	}
}

// isLockedErrOnceCache makes IsLocked return an error the first time to cover the error branch in phase1Commit.
type isLockedErrOnceCache struct {
	sop.L2Cache
	flipped bool
}

func (c *isLockedErrOnceCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if !c.flipped {
		c.flipped = true
		return false, errors.New("islocked err")
	}
	return c.L2Cache.IsLocked(ctx, lockKeys)
}

func (c *isLockedErrOnceCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := c.L2Cache.Lock(ctx, duration, lockKeys)
	if !ok || err != nil {
		return ok, tid, err
	}
	if locked, err := c.IsLocked(ctx, lockKeys); err != nil || !locked {
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func Test_Phase1Commit_IsLockedError_ThenSucceeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	l2 := &isLockedErrOnceCache{L2Cache: base}
	cache.GetGlobalL1Cache(l2)

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_err", SlotLength: 2})

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: -1}

	lid := sop.NewUUID()
	n := &btree.Node[sop.UUID, int]{ID: lid, Version: 0}
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

// errGetRegistry3 fails on Get to cover early error branch in rollbackRemovedNodes.
type errGetRegistry3 struct{ inner sop.Registry }

func (e errGetRegistry3) Add(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Add(ctx, s)
}
func (e errGetRegistry3) Update(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Update(ctx, s)
}
func (e errGetRegistry3) UpdateNoLocks(ctx context.Context, a bool, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.UpdateNoLocks(ctx, a, s)
}
func (e errGetRegistry3) Get(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, errors.New("get err")
}
func (e errGetRegistry3) Remove(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) error {
	return e.inner.Remove(ctx, lids)
}
func (e errGetRegistry3) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Replicate(ctx, a, b, c, d)
}

func Test_NodeRepository_CommitUpdatedNodes_RegistryGetError(t *testing.T) {
	ctx := context.Background()
	reg := errGetRegistry3{inner: mocks.NewMockRegistry(false)}
	l2 := mocks.NewMockClient()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2}, l2Cache: l2}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_removed_get_err", SlotLength: 2})
	id := sop.NewUUID()
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackRemovedNodes(ctx, true, vids); err == nil {
		t.Fatalf("expected error from registry.Get in rollbackRemovedNodes")
	}
}

func Test_Transaction_Cleanup_Succeeds_ExecutesAllSteps(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl}

	// Seed updated and removed handles so getToBeObsoleteEntries yields work.
	id := sop.NewUUID()
	h := sop.Handle{LogicalID: id, PhysicalIDA: id, Version: 1}
	tx.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{BlobTable: "bt", IDs: []sop.Handle{h}}}
	tx.removedNodeHandles = []sop.RegistryPayload[sop.Handle]{{BlobTable: "bt", IDs: []sop.Handle{h}}}

	if err := tx.cleanup(ctx); err != nil {
		t.Fatalf("cleanup err: %v", err)
	}
}

// Backup APIs removed: drop WriteBackup error scenario; keep doPriorityRollbacks exercised elsewhere.
