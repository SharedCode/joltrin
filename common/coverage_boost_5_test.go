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

// Covers refetchAndMergeClosure update path for IsValueDataInNodeSegment=true.
func Test_RefetchAndMerge_Update_InNodeSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	// Build store with value data in node segment
	so := sop.StoreOptions{Name: "rfm_upd_node", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

	// Fresh global cache for determinism
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

	// Seed one item
	pk, pv := newPerson("iu1", "iv1", "m", "u@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)

	// Prepare tracker to simulate update with same version
	newV := pv
	newV.Phone = "changed"
	cur2 := cur // copy
	cur2.Value = &newV
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: updateAction}, item: &cur2, versionInDB: cur.Version}

	// Seed MRU and Handle for root so repo.get can fetch after reset
	rootID := b3.StoreInfo.RootNodeID
	if cn, ok := nrw.nodeRepositoryBackend.localCache[rootID]; ok && cn.node != nil {
		cache.GetGlobalL1Cache(l2).SetNodeToMRU(ctx, rootID, cn.node, ns.CacheConfig.NodeCacheDuration)
		cache.GetGlobalL1Cache(l2).Handles.Set([]sop.KeyValuePair[sop.UUID, sop.Handle]{{Key: rootID, Value: sop.NewHandle(rootID)}})
	} else {
		t.Fatalf("expected root node in local cache for MRU seed")
	}
	// Keep StoreRepository in sync so refetch picks current store info
	upd := *ns
	upd.RootNodeID = b3.StoreInfo.RootNodeID
	upd.Count = b3.StoreInfo.Count
	_ = sr.Add(ctx, upd)

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetchAndMerge update(in-node) err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("item missing after update")
	}
	got, _ := b3.GetCurrentItem(ctx)
	if got.Value == nil || got.Value.Phone != "changed" {
		t.Fatalf("expected updated value, got %+v", got.Value)
	}
}

// Covers refetchAndMergeClosure remove path for IsValueDataInNodeSegment=true when backend can refetch root.
func Test_RefetchAndMerge_Remove_InNodeSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_rm_node", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

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

	// Seed an item that resides in root
	pk, pv := newPerson("ru1", "rv1", "m", "r@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)

	// Track removal
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: removeAction}, item: &cur, versionInDB: cur.Version}

	// Seed MRU for root and handle
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
	if err := closure(ctx); err != nil {
		t.Fatalf("refetchAndMerge remove(in-node) err: %v", err)
	}
	// Expect item removed
	if ok, _ := b3.Find(ctx, pk, false); ok {
		t.Fatal("expected item removed after refetch+remove")
	}
}

// Covers commitUpdatedNodes branch when handle is deleted but expired -> cleared and new inactive allocated.
func Test_CommitUpdatedNodes_DeletedExpired_Allocates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	reg := mocks.NewMockRegistry(false)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: reg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: tl, StoreRepository: sr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "upd_expired", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Prepare a handle marked deleted with expired inactive timestamp and existing active ID A only.
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.IsDeleted = true
	h.WorkInProgressTimestamp = sop.Now().Add(-2 * time.Hour).UnixMilli()
	h.Version = 1
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})

	// Node to update with matching version
	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{
		{First: si, Second: []interface{}{n}},
	}

	ok, handles, err := nr.commitUpdatedNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitUpdatedNodes err: %v", err)
	}
	if !ok || len(handles) == 0 || len(handles[0].IDs) != 1 {
		t.Fatalf("expected success and 1 handle, ok=%v handles=%v", ok, handles)
	}
	if handles[0].IDs[0].GetInActiveID().IsNil() {
		t.Fatalf("expected inactive ID allocated after clearing expired deleted state")
	}
}

// Covers Phase2Commit success path where unlockTrackedItems errors are tolerated (warn only).
func Test_Phase2Commit_Success_UnlockTrackedItems_Error_Ignored(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{mode: sop.ForWriting, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_unlock_warn", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return false },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return errors.New("unlock err") },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}
	// No updated/removed handles -> UpdateNoLocks block skipped; just ensure commit completes
	if err := tx.Phase2Commit(ctx); err != nil {
		t.Fatalf("Phase2Commit should ignore unlockTrackedItems error, got: %v", err)
	}
}

// Minimal priority log that records Remove calls.
type recPrioLog2 struct{ removed []string }

func (r *recPrioLog2) IsEnabled() bool                             { return true }
func (r *recPrioLog2) Add(context.Context, sop.UUID, []byte) error { return nil }
func (r *recPrioLog2) Remove(ctx context.Context, tid sop.UUID) error {
	r.removed = append(r.removed, tid.String())
	return nil
}
func (r *recPrioLog2) Get(context.Context, sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	// Return a non-empty payload so priorityRollback attempts UpdateNoLocks (which we induce to fail)
	// and does not remove the priority log.
	return []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}, nil
}
func (r *recPrioLog2) GetBatch(context.Context, int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (r *recPrioLog2) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (r *recPrioLog2) LogCommitChanges(context.Context, []sop.StoreInfo, []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// TransactionLog wrapper that returns our recPrioLog2.
type tlWithPL2 struct{ pl sop.TransactionPriorityLog }

func (l tlWithPL2) PriorityLog() sop.TransactionPriorityLog          { return l.pl }
func (l tlWithPL2) Add(context.Context, sop.UUID, int, []byte) error { return nil }
func (l tlWithPL2) Remove(context.Context, sop.UUID) error           { return nil }
func (l tlWithPL2) GetOne(context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (l tlWithPL2) GetOneOfHour(context.Context, string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (l tlWithPL2) NewUUID() sop.UUID { return sop.NewUUID() }

// Registry stub that fails UpdateNoLocks to force phase2 error and priority rollback path.
type failingRegistry struct{}

func (f failingRegistry) Add(context.Context, []sop.RegistryPayload[sop.Handle]) error    { return nil }
func (f failingRegistry) Update(context.Context, []sop.RegistryPayload[sop.Handle]) error { return nil }
func (f failingRegistry) UpdateNoLocks(context.Context, bool, []sop.RegistryPayload[sop.Handle]) error {
	return fmt.Errorf("induced UpdateNoLocks error")
}
func (f failingRegistry) Get(context.Context, []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (f failingRegistry) Remove(context.Context, []sop.RegistryPayload[sop.UUID]) error { return nil }
func (f failingRegistry) Replicate(context.Context, []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// Covers Phase2Commit error branch when nodes are locked: priorityRollback is invoked and nodesKeys get cleared.
func Test_Phase2Commit_Error_WithLockedNodes_PriorityRollbackPath(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	pr := &recPrioLog2{}
	tx := &Transaction{
		id:              sop.NewUUID(),
		mode:            sop.ForWriting,
		phaseDone:       1,
		l2Cache:         l2,
		l1Cache:         cache.GetGlobalL1Cache(l2),
		blobStore:       mocks.NewMockBlobStore(),
		registry:        failingRegistry{},
		StoreRepository: mocks.NewMockStoreRepository(),
		logger:          newTransactionLogger(tlWithPL2{pl: pr}, true),
	}
	// Minimal backend to avoid rollback panics on btreesBackend[0]
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_err_locked", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
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
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}
	// Mark nodes as "locked"
	tx.nodesKeys = tx.l2Cache.CreateLockKeys([]string{"n1"})
	// Set updated handles so phase2Commit reaches UpdateNoLocks and errors
	tx.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}

	if err := tx.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error")
	}
	if tx.nodesKeys != nil {
		t.Fatalf("expected nodesKeys cleared by unlockNodesKeys")
	}
	// Rollback removes priority log even on locked path; expect at least one removal.
	if len(pr.removed) == 0 {
		t.Fatalf("expected priority log removed during rollback on locked path")
	}
}

// Covers Phase2Commit error branch when nodes are not locked: priority log Remove is called.
func Test_Phase2Commit_Error_WithoutLockedNodes_RemovesPriorityLog(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	pr := &recPrioLog2{}
	tx := &Transaction{
		id:              sop.NewUUID(),
		mode:            sop.ForWriting,
		phaseDone:       1,
		l2Cache:         l2,
		l1Cache:         cache.GetGlobalL1Cache(l2),
		blobStore:       mocks.NewMockBlobStore(),
		registry:        failingRegistry{},
		StoreRepository: mocks.NewMockStoreRepository(),
		logger:          newTransactionLogger(tlWithPL2{pl: pr}, true),
	}
	// Minimal backend to avoid rollback panics on btreesBackend[0]
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2_err_unlocked", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
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
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}
	// nodesKeys is nil to simulate not locked
	tx.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}

	if err := tx.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error")
	}
	if len(pr.removed) < 1 {
		t.Fatalf("expected priority log removed at least once, got %d", len(pr.removed))
	}
}

// Covers refetchAndMergeClosure getAction path.
func Test_RefetchAndMerge_GetAction_PassThrough(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_get_node", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)

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

	// Seed an item and then mark as getAction
	pk, pv := newPerson("gg1", "gv1", "m", "g@x", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: getAction}, item: &cur, versionInDB: cur.Version}

	// Seed MRU and handle for root so get/find can refetch after reset
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
	if err := closure(ctx); err != nil {
		t.Fatalf("refetchAndMerge getAction err: %v", err)
	}
}

// Ensures acquireLocks returns failover when taking over a dead owner's locks but GetEx
// reveals a mismatched owner on one of the keys.
func Test_AcquireLocks_Takeover_GetExMismatch_Fails(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tid := sop.NewUUID()
	id1 := sop.NewUUID()
	id2 := sop.NewUUID()

	// Pre-seed lock keys: simulate dead owner = tid on first key, different owner on second.
	k1 := tx.l2Cache.CreateLockKeys([]string{id1.String()})[0].Key
	k2 := tx.l2Cache.CreateLockKeys([]string{id2.String()})[0].Key
	_ = tx.l2Cache.Set(ctx, k1, tid.String(), time.Minute)
	_ = tx.l2Cache.Set(ctx, k2, sop.NewUUID().String(), time.Minute)

	stores := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(id1), sop.NewHandle(id2)}}}
	_, err := tl.acquireLocks(ctx, tx, tid, stores)
	if err == nil {
		t.Fatalf("expected failover error")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
}

// Ensures handleRegistrySectorLockTimeout returns the original error when priorityRollback fails.
func Test_HandleRegistrySectorLockTimeout_PriorityRollbackError_ReturnsOriginal(t *testing.T) {
	ctx := context.Background()
	// Registry that forces UpdateNoLocks error to make priorityRollback fail.
	reg := errRegistry{}
	tx := &Transaction{l2Cache: mocks.NewMockClient(), registry: reg}
	// Priority log returns any non-nil payload for the tid embedded in LockKey.
	tid := sop.NewUUID()
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}}}}
	tx.logger = newTransactionLogger(stubTLog{pl: pl}, true)

	// Build original error carrying *sop.LockKey in UserData as required by handler.
	ud := &sop.LockKey{Key: tx.l2Cache.FormatLockKey("X"), LockID: tid}
	orig := sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("orig"), UserData: ud}

	if out := tx.handleRegistrySectorLockTimeout(ctx, orig); out == nil || out.Error() != orig.Error() {
		t.Fatalf("expected original error to be returned, got %v", out)
	}
}

// Covers doPriorityRollbacks iterating over a multi-entry batch where both entries are processed.
func Test_TransactionLogger_DoPriorityRollbacks_MultiEntry_MixedOutcomes(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, registry: reg}

	// Two tids and lids
	tid1 := sop.NewUUID()
	lid1 := sop.NewUUID()
	tid2 := sop.NewUUID()
	lid2 := sop.NewUUID()

	// Seed registry so version checks pass for both
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid1)}}})
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid2)}}})

	// Batch: two entries
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{
		{Key: tid1, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{sop.NewHandle(lid1)}}}},
		{Key: tid2, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", BlobTable: "bt", IDs: []sop.Handle{sop.NewHandle(lid2)}}}},
	}, removeErr: map[string]error{tid2.String(): errors.New("rm fail")}}

	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	// Let locks be acquirable and stable
	_ = l2.Set(ctx, l2.FormatLockKey(coordinatorLockName), sop.NewUUID().String(), time.Minute)
	// Clear to allow our lock to be acquired by doPriorityRollbacks
	_, _ = l2.Delete(ctx, []string{l2.FormatLockKey(coordinatorLockName)})

	consumed, err := tl.doPriorityRollbacks(ctx, tx)
	if err == nil {
		t.Fatalf("expected error from Remove to be returned")
	}
	if consumed {
		t.Fatalf("expected consumed=false when Remove errors")
	}

	// Remove should be attempted for both tids
	if pl.removedHit[tid1.String()] == 0 || pl.removedHit[tid2.String()] == 0 {
		t.Fatalf("expected Remove attempted for both tids")
	}
}

// addOnceLockErrReg wraps the mock registry and forces Add to return a sop.Error with *LockKey once.
type addOnceLockErrReg struct {
	*mocks.Mock_vid_registry
	fired bool
	lk    sop.LockKey
}

func (r *addOnceLockErrReg) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if !r.fired {
		r.fired = true
		return sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("sector timeout"), UserData: &r.lk}
	}
	return r.Mock_vid_registry.Add(ctx, storesHandles)
}

// errReg wraps the mock registry to force UpdateNoLocks to return a non-sop error.
type errReg struct{ *mocks.Mock_vid_registry }

func (e errReg) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return fmt.Errorf("boom")
}

// commitAddedNodes error path exercising handleRegistrySectorLockTimeout within phase1Commit loop.
func Test_Phase1Commit_CommitAddedNodes_SectorTimeout_ThenRetry(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// Build a backend that has an added node and forces registry.Add to return a sop.Error carrying a *sop.LockKey
	// on first attempt, so handleRegistrySectorLockTimeout is exercised; then succeed on retry.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_added_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Add an added node so commitAddedNodes path is taken.
	aid := sop.NewUUID()
	nr.localCache[aid] = cachedNode{action: addAction, node: &btree.Node[PersonKey, Person]{ID: aid}}
	tx.registry = &addOnceLockErrReg{Mock_vid_registry: tx.registry.(*mocks.Mock_vid_registry), lk: sop.LockKey{Key: l2.FormatLockKey("X"), LockID: sop.NewUUID()}}

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
		t.Fatalf("phase1Commit err on retry path: %v", err)
	}
}

// Non-sop error from commitRemovedNodes should propagate directly.
func Test_Phase1Commit_CommitRemovedNodes_NonSopError_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_removed_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}

	// Stage a removed node so commitRemovedNodes is invoked.
	rid := sop.NewUUID()
	nr.localCache[rid] = cachedNode{action: removeAction, node: &btree.Node[PersonKey, Person]{ID: rid, Version: 1}}
	// Seed registry with same version so it reaches UpdateNoLocks call that we will force to error.
	hh := sop.NewHandle(rid)
	hh.Version = 1
	tx.registry.(*mocks.Mock_vid_registry).Lookup[rid] = hh

	tx.registry = errReg{Mock_vid_registry: tx.registry.(*mocks.Mock_vid_registry)}

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
		t.Fatalf("expected non-sop error to propagate from commitRemovedNodes")
	}
}

// deleteErrCache2 wraps a Cache and forces Delete to return an error.
type deleteErrCache2 struct{ sop.L2Cache }

func (d deleteErrCache2) Delete(ctx context.Context, keys []string) (bool, error) {
	return false, fmt.Errorf("delete failed")
}

func (d deleteErrCache2) IsRestarted(ctx context.Context) bool {
	return d.L2Cache.IsRestarted(ctx)
}

func Test_ItemActionTracker_Lock_GetVsGet_Compatible_NoError(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_get_get", SlotLength: 2})
	c := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	trk := newItemActionTracker[PersonKey, Person](si, c, bs, tl)

	// Prepare an item tracked as getAction
	pk, _ := newPerson("a", "b", "c", "d@e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil, Version: 1}
	it.ValueNeedsFetch = false
	// Insert into tracker with getAction and a specific lock ID
	lr := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	trk.items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lr,
		item:        it,
		versionInDB: it.Version,
	}

	// Pre-populate cache with a different lock record but also getAction (compatible)
	other := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	if err := c.SetStruct(ctx, c.FormatLockKey(it.ID.String()), &other, time.Minute); err != nil {
		t.Fatalf("setup cache SetStruct failed: %v", err)
	}

	// Should not error as get vs get is compatible
	if err := trk.lock(ctx, time.Minute); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func Test_ItemActionTracker_Unlock_Collects_Delete_Error(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_unlock_err", SlotLength: 2})
	base := mocks.NewMockClient()
	c := deleteErrCache2{L2Cache: base}
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	trk := newItemActionTracker[PersonKey, Person](si, c, bs, tl)

	// Prepare an item that we "own" the lock of and is not addAction
	pk, _ := newPerson("q", "w", "e", "f@g", "h")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil, Version: 1}
	trk.items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: it.Version,
		isLockOwner: true,
	}

	if err := trk.unlock(ctx); err == nil {
		t.Fatalf("expected delete error collected, got nil")
	}
}

// failingAddBlobStore errors on Add to exercise commitNewRootNodes error path.
type failingAddBlobStore struct{}

func (f failingAddBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (f failingAddBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return errors.New("boom add")
}
func (f failingAddBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (f failingAddBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return nil
}

// errGetRegistry returns an error on Get to exercise rollbackRemovedNodes early error branch.
type errGetRegistry2 struct{ inner sop.Registry }

func (e errGetRegistry2) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Add(ctx, storesHandles)
}
func (e errGetRegistry2) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Update(ctx, storesHandles)
}
func (e errGetRegistry2) UpdateNoLocks(ctx context.Context, a bool, s []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.UpdateNoLocks(ctx, a, s)
}
func (e errGetRegistry2) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, errors.New("boom get")
}
func (e errGetRegistry2) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	return e.inner.Remove(ctx, storesLids)
}
func (e errGetRegistry2) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return e.inner.Replicate(ctx, a, b, c, d)
}

// falseOnceIsLockedCache returns IsLocked=false once (no error), then delegates.
type falseOnceIsLockedCache struct {
	inner   sop.L2Cache
	tripped bool
}

func (m *falseOnceIsLockedCache) Set(ctx context.Context, k, v string, d time.Duration) error {
	return m.inner.Set(ctx, k, v, d)
}
func (m *falseOnceIsLockedCache) Get(ctx context.Context, k string) (bool, string, error) {
	return m.inner.Get(ctx, k)
}
func (m *falseOnceIsLockedCache) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return m.inner.GetEx(ctx, k, d)
}
func (m *falseOnceIsLockedCache) Ping(ctx context.Context) error { return m.inner.Ping(ctx) }
func (m *falseOnceIsLockedCache) SetStruct(ctx context.Context, k string, v interface{}, d time.Duration) error {
	return m.inner.SetStruct(ctx, k, v, d)
}
func (m *falseOnceIsLockedCache) GetStruct(ctx context.Context, k string, t interface{}) (bool, error) {
	return m.inner.GetStruct(ctx, k, t)
}
func (m *falseOnceIsLockedCache) GetStructEx(ctx context.Context, k string, t interface{}, d time.Duration) (bool, error) {
	return m.inner.GetStructEx(ctx, k, t, d)
}
func (m *falseOnceIsLockedCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return m.inner.Delete(ctx, keys)
}
func (m *falseOnceIsLockedCache) FormatLockKey(k string) string { return m.inner.FormatLockKey(k) }
func (m *falseOnceIsLockedCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.inner.CreateLockKeys(keys)
}
func (m *falseOnceIsLockedCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.inner.CreateLockKeysForIDs(keys)
}
func (m *falseOnceIsLockedCache) IsLockedTTL(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, error) {
	return m.inner.IsLockedTTL(ctx, d, ks)
}
func (m *falseOnceIsLockedCache) Lock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return m.inner.Lock(ctx, d, ks)
}
func (m *falseOnceIsLockedCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := m.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := m.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (m *falseOnceIsLockedCache) IsLocked(ctx context.Context, ks []*sop.LockKey) (bool, error) {
	if !m.tripped {
		m.tripped = true
		return false, nil
	}
	return m.inner.IsLocked(ctx, ks)
}
func (m *falseOnceIsLockedCache) IsLockedByOthers(ctx context.Context, names []string) (bool, error) {
	return m.inner.IsLockedByOthers(ctx, names)
}
func (m *falseOnceIsLockedCache) IsRestarted(ctx context.Context) bool {
	return m.inner.IsRestarted(ctx)
}
func (m *falseOnceIsLockedCache) Unlock(ctx context.Context, ks []*sop.LockKey) error {
	return m.inner.Unlock(ctx, ks)
}
func (m *falseOnceIsLockedCache) Clear(ctx context.Context) error { return m.inner.Clear(ctx) }

// failSecondGetAfterSetCache simulates lock() path where SetStruct succeeds but second GetStruct does not find the key.
type failSecondGetAfterSetCache struct {
	inner         sop.L2Cache
	sawSet        bool
	failedOnceGet bool
}

func (m *failSecondGetAfterSetCache) Set(ctx context.Context, k, v string, d time.Duration) error {
	return m.inner.Set(ctx, k, v, d)
}
func (m *failSecondGetAfterSetCache) Get(ctx context.Context, k string) (bool, string, error) {
	return m.inner.Get(ctx, k)
}
func (m *failSecondGetAfterSetCache) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return m.inner.GetEx(ctx, k, d)
}
func (m *failSecondGetAfterSetCache) Ping(ctx context.Context) error { return m.inner.Ping(ctx) }
func (m *failSecondGetAfterSetCache) SetStruct(ctx context.Context, k string, v interface{}, d time.Duration) error {
	m.sawSet = true
	return m.inner.SetStruct(ctx, k, v, d)
}
func (m *failSecondGetAfterSetCache) GetStruct(ctx context.Context, k string, t interface{}) (bool, error) {
	if m.sawSet && !m.failedOnceGet {
		m.failedOnceGet = true
		return false, nil
	}
	return m.inner.GetStruct(ctx, k, t)
}
func (m *failSecondGetAfterSetCache) GetStructEx(ctx context.Context, k string, t interface{}, d time.Duration) (bool, error) {
	return m.GetStruct(ctx, k, t)
}
func (m *failSecondGetAfterSetCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return m.inner.Delete(ctx, keys)
}
func (m *failSecondGetAfterSetCache) FormatLockKey(k string) string { return m.inner.FormatLockKey(k) }
func (m *failSecondGetAfterSetCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.inner.CreateLockKeys(keys)
}
func (m *failSecondGetAfterSetCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.inner.CreateLockKeysForIDs(keys)
}
func (m *failSecondGetAfterSetCache) IsLockedTTL(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, error) {
	return m.inner.IsLockedTTL(ctx, d, ks)
}
func (m *failSecondGetAfterSetCache) Lock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return m.inner.Lock(ctx, d, ks)
}
func (m *failSecondGetAfterSetCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := m.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := m.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (m *failSecondGetAfterSetCache) IsLocked(ctx context.Context, ks []*sop.LockKey) (bool, error) {
	return m.inner.IsLocked(ctx, ks)
}
func (m *failSecondGetAfterSetCache) IsLockedByOthers(ctx context.Context, names []string) (bool, error) {
	return m.inner.IsLockedByOthers(ctx, names)
}
func (m *failSecondGetAfterSetCache) IsRestarted(ctx context.Context) bool {
	return m.inner.IsRestarted(ctx)
}
func (m *failSecondGetAfterSetCache) Clear(ctx context.Context) error {
	return m.inner.Clear(ctx)
}
