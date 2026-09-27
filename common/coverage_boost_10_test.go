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

func Test_Phase1Commit_NoCheck_ReturnsImmediately(t *testing.T) {
	ctx := context.Background()
	twoPhase, err := newMockTwoPhaseCommitTransaction(t, sop.NoCheck, time.Minute, false)
	if err != nil {
		t.Fatalf("newMockTwoPhaseCommitTransaction failed: %v", err)
	}
	tr := twoPhase.(*Transaction)
	if err := tr.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if err := tr.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit(NoCheck) unexpected error: %v", err)
	}
}

func Test_Phase1Commit_ForReading_NoTrackedItems(t *testing.T) {
	ctx := context.Background()
	twoPhase, err := newMockTwoPhaseCommitTransaction(t, sop.ForReading, time.Minute, false)
	if err != nil {
		t.Fatalf("newMockTwoPhaseCommitTransaction failed: %v", err)
	}
	tr := twoPhase.(*Transaction)
	if err := tr.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if err := tr.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit(ForReading) unexpected error: %v", err)
	}
}

func Test_TransactionLogger_PriorityRollback_NoLogs_NoOp(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// No logs exist for the TID in the mock priority log; priorityRollback should be a no-op and return nil.
	if err := tl.priorityRollback(ctx, nil, sop.NewUUID()); err != nil {
		t.Fatalf("priorityRollback no-logs unexpected error: %v", err)
	}
}

func Test_CommitTrackedItemsValues_NoItems_NoOp(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_noitems", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	// No items added; commit should be a no-op and return nil.
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("expected nil error for no items, got: %v", err)
	}
}

// secondGetMissCache stubs GetStruct to miss both before and after SetStruct to force the race/error branch in lock.
type secondGetMissCache struct{ sop.L2Cache }

func (c secondGetMissCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return false, nil
}

func (c secondGetMissCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, duration time.Duration) ([]bool, error) {
	results := make([]bool, len(keys))
	// Return false for all to simulate miss
	return results, nil
}

func Test_ItemActionTracker_Lock_SecondGet_NotFound_ReturnsError(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	c := secondGetMissCache{L2Cache: base}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_race", SlotLength: 2})
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, c, bs, tl)

	// Prepare an updatable item so lock path executes the Set + second GetStruct branch
	pk, _ := newPerson("z", "y", "x", "w@v", "u")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil, Version: 1}
	trk.items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: it.Version,
	}

	err := trk.lock(ctx, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "can't attain a lock in Redis") {
		t.Fatalf("expected can't attain a lock in Redis error, got: %v", err)
	}
}

// lockBackendErrCache simulates a backend error during Lock (false, Nil owner, non-nil err).
type lockBackendErrCache struct{ sop.L2Cache }

func (c lockBackendErrCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("lock backend error")
}

func (m lockBackendErrCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, duration, keys)
}

func Test_TransactionLogger_AcquireLocks_Lock_Backend_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	l2 := lockBackendErrCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || !strings.Contains(err.Error(), "lock backend error") {
		t.Fatalf("expected lock backend error, got: %v", err)
	}
}

// getStructErrCache returns an error from GetStruct to exercise checkTrackedItems error path.
type getStructErrCache2 struct{ sop.L2Cache }

func (c getStructErrCache2) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return false, fmt.Errorf("cache get error")
}

func (c getStructErrCache2) GetStructs(ctx context.Context, keys []string, targets []interface{}, duration time.Duration) ([]bool, error) {
	return nil, fmt.Errorf("cache get error")
}

func Test_ItemActionTracker_CheckTrackedItems_Propagates_Error(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_chk_err", SlotLength: 2})
	c := getStructErrCache2{L2Cache: mocks.NewMockClient()}
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	trk := newItemActionTracker[PersonKey, Person](si, c, bs, tl)

	pk, _ := newPerson("aa", "bb", "cc", "dd@ee", "ff")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil, Version: 1}
	trk.items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: it.Version,
	}

	if err := trk.checkTrackedItems(ctx); err == nil || !strings.Contains(err.Error(), "cache get error") {
		t.Fatalf("expected cache get error, got: %v", err)
	}
}

func Test_ItemActionTracker_Add_ActivelyPersisted_WritesBlob_And_Caches(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{
		Name:                         "iat_add_active",
		SlotLength:                   2,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    true,
	})
	c := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	trk := newItemActionTracker[PersonKey, Person](si, c, bs, tl)

	pk, v := newPerson("ep", "er", "es", "et@eu", "ev")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &v, Version: 1}

	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add actively persisted failed: %v", err)
	}

	// Verify blob exists under item's ID
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); ba == nil {
		t.Fatalf("expected blob persisted for item ID: %s", it.ID)
	}
	// Verify cache contains the value
	var cached Person
	found, _ := c.GetStruct(ctx, formatItemKey(it.ID.String()), &cached)
	if !found {
		t.Fatalf("expected value cached for item ID: %s", it.ID)
	}
}

// firstFailThenSucceedCache: first Lock returns false, subsequent Locks return true; IsLocked always true.
type firstFailThenSucceedCache struct {
	sop.L2Cache
	calls int
}

func (c *firstFailThenSucceedCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	c.calls++
	if c.calls == 1 {
		return false, sop.NilUUID, nil
	}
	return true, sop.NilUUID, nil
}

func (m *firstFailThenSucceedCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, duration, keys)
}

func (m *firstFailThenSucceedCache) IsLocked(ctx context.Context, keys []*sop.LockKey) (bool, error) {
	return true, nil
}

func Test_Phase1Commit_RefetchAndMerge_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	// Start with a mock two-phase transaction and override only what we need.
	twoPhase, err := newMockTwoPhaseCommitTransaction(t, sop.ForWriting, time.Minute, false)
	if err != nil {
		t.Fatalf("newMockTwoPhaseCommitTransaction failed: %v", err)
	}
	tr := twoPhase.(*Transaction)
	if err := tr.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	// Swap in our flipping lock cache
	flipper := &firstFailThenSucceedCache{L2Cache: mocks.NewMockClient()}
	tr.l2Cache = flipper

	// Provide a minimal backend with no nodes so commit steps are no-ops but paths execute.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_loop", SlotLength: 2})
	// Ensure global cache is initialized to avoid nil L1
	gc := cache.GetGlobalL1Cache(mocks.NewMockClient())

	tr.btreesBackend = []btreeBackend{{
		nodeRepository:                   &nodeRepositoryBackend{transaction: tr, l1Cache: gc},
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(ctx context.Context) error { return nil },
		lockTrackedItems:                 func(ctx context.Context, d time.Duration) error { return nil },
		unlockTrackedItems:               func(ctx context.Context) error { return nil },
		commitTrackedItemsValues:         func(ctx context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
	}}

	if err := tr.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit with retry expected success, got: %v", err)
	}
}

// takeoverCacheOK simulates a dead-owner takeover success.
type takeoverCacheOK struct{ sop.L2Cache }

var takeoverTID = sop.NewUUID()

func (c takeoverCacheOK) Lock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, takeoverTID, nil
}

func (m takeoverCacheOK) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, duration, keys)
}

func (m takeoverCacheOK) GetEx(ctx context.Context, key string, duration time.Duration) (bool, string, error) {
	return true, takeoverTID.String(), nil
}

func Test_TransactionLogger_AcquireLocks_Takeover_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := takeoverCacheOK{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: l2}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	ids := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: ids}}

	keys, err := tl.acquireLocks(ctx, tx, takeoverTID, stores)
	if err != nil {
		t.Fatalf("unexpected error in takeover success: %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("expected some keys returned")
	}
}

type recordingBlobStore struct {
	calls int
	last  [][]sop.BlobsPayload[sop.UUID]
}

func (r *recordingBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (r *recordingBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (r *recordingBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (r *recordingBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	r.calls++
	// save a shallow copy for assertion safety
	cp := make([]sop.BlobsPayload[sop.UUID], len(storesBlobsIDs))
	copy(cp, storesBlobsIDs)
	r.last = append(r.last, cp)
	return nil
}

func Test_TransactionLogger_Rollback_CommitAddedNodes_TriggersRollbackAddedNodes(t *testing.T) {
	ctx := context.Background()

	// Arrange tx with real nodeRepositoryBackend but recording blob store
	rec := &recordingBlobStore{}
	tx := &Transaction{
		l2Cache:   mocks.NewMockClient(),
		registry:  mocks.NewMockRegistry(false),
		blobStore: rec,
		btreesBackend: []btreeBackend{{
			nodeRepository: &nodeRepositoryBackend{},
		}},
	}
	// Wire back-pointer so rollbackAddedNodes sees tx
	tx.btreesBackend[0].nodeRepository.transaction = tx

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl

	// Build a commitAddedNodes log with dummy payload and make lastCommittedFunctionLog > commitAddedNodes
	vids := []sop.RegistryPayload[sop.UUID]{{}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: "tb", Blobs: []sop.UUID{sop.NewUUID()}}}
	payload := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs})
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitAddedNodes, Value: payload},
		{Key: commitStoreInfo, Value: nil}, // ensures lastCommittedFunctionLog > commitAddedNodes
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("unexpected error in rollback: %v", err)
	}
	if rec.calls == 0 {
		t.Fatalf("expected blob remove to be called during rollbackAddedNodes")
	}
}

func Test_TransactionLogger_Rollback_CommitNewRootNodes_TriggersRollbackNewRootNodes(t *testing.T) {
	ctx := context.Background()

	rec := &recordingBlobStore{}
	tx := &Transaction{
		l2Cache:   mocks.NewMockClient(),
		registry:  mocks.NewMockRegistry(false),
		blobStore: rec,
		btreesBackend: []btreeBackend{{
			nodeRepository: &nodeRepositoryBackend{},
		}},
	}
	tx.btreesBackend[0].nodeRepository.transaction = tx

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl

	// Build a commitNewRootNodes log with payload and set lastCommittedFunctionLog > commitNewRootNodes
	vids := []sop.RegistryPayload[sop.UUID]{{}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: "tb", Blobs: []sop.UUID{sop.NewUUID()}}}
	payload := toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs})
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitNewRootNodes, Value: payload},
		{Key: commitAddedNodes, Value: nil},
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("unexpected error in rollback: %v", err)
	}
	if rec.calls == 0 {
		t.Fatalf("expected blob remove to be called during rollbackNewRootNodes")
	}
}

// recordingBlobStore is reused to count Remove calls in different rollback paths.
type recordingBlobStore2 struct {
	calls int
	last  [][]sop.BlobsPayload[sop.UUID]
}

func (r *recordingBlobStore2) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (r *recordingBlobStore2) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (r *recordingBlobStore2) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (r *recordingBlobStore2) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	r.calls++
	cp := make([]sop.BlobsPayload[sop.UUID], len(storesBlobsIDs))
	copy(cp, storesBlobsIDs)
	r.last = append(r.last, cp)
	return nil
}

func Test_TransactionLogger_Rollback_FinalizeCommit_DeletesObsoleteAndTracked(t *testing.T) {
	ctx := context.Background()

	// Build transaction with global L1, mock L2 and registry, and recording blob store.
	rec := &recordingBlobStore2{}
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{
		l1Cache:   cache.NewL1Cache(mocks.NewMockClient(), 16, 128),
		l2Cache:   mocks.NewMockClient(),
		registry:  reg,
		blobStore: rec,
		btreesBackend: []btreeBackend{{
			nodeRepository: &nodeRepositoryBackend{},
		}},
	}
	tx.btreesBackend[0].nodeRepository.transaction = tx

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl

	// Prepare finalizeCommit payload: some obsolete entries and some tracked items values.
	// Obsolete entries
	delIDs := []sop.RegistryPayload[sop.UUID]{
		{RegistryTable: "rt1", IDs: []sop.UUID{sop.NewUUID()}},
	}
	unused := []sop.BlobsPayload[sop.UUID]{
		{BlobTable: "bt1", Blobs: []sop.UUID{sop.NewUUID()}},
	}
	// Tracked items values: leave empty to avoid Redis branch entirely
	var tracked []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]
	payload := toByteArray(sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
		First:  sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: delIDs, Second: unused},
		Second: tracked,
	})

	// Arrange logs: finalizeCommit with payload followed by deleteObsoleteEntries to trigger deletion
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: finalizeCommit, Value: payload},
		{Key: deleteObsoleteEntries, Value: nil},
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("unexpected error in rollback: %v", err)
	}
	if rec.calls < 1 {
		t.Fatalf("expected at least one blob remove call (obsolete), got %d", rec.calls)
	}
}

// errDeleteCache wraps a Cache and forces Delete to return an error to cover error branch.
type errDeleteCache struct{ sop.L2Cache }

func (e errDeleteCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return false, fmt.Errorf("forced delete error")
}

// errBlobStore2 wraps a BlobStore and forces Remove to return an error to cover error branch.
type errBlobStore2 struct{ sop.BlobStore }

func (e errBlobStore2) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return fmt.Errorf("forced remove error")
}

func Test_DeleteTrackedItemsValues_PropagatesErrors(t *testing.T) {
	ctx := context.Background()

	id := sop.NewUUID()
	payload := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
		{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{id}}},
	}

	// Case 1: both cache delete and blob remove error; expect a non-nil error returned.
	tx1 := &Transaction{l2Cache: errDeleteCache{L2Cache: mocks.NewMockClient()}, blobStore: errBlobStore2{BlobStore: mocks.NewMockBlobStore()}}
	if err := tx1.deleteTrackedItemsValues(ctx, payload); err == nil {
		t.Fatalf("expected error when both cache delete and blob remove fail")
	}

	// Case 2: no-cache variant: First=false to skip cache deletion; still propagate blob remove error.
	payload2 := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
		{First: false, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{id}}},
	}
	tx2 := &Transaction{l2Cache: mocks.NewMockClient(), blobStore: errBlobStore2{BlobStore: mocks.NewMockBlobStore()}}
	if err := tx2.deleteTrackedItemsValues(ctx, payload2); err == nil {
		t.Fatalf("expected error when blob remove fails")
	}
}

// Exercises onIdle priority-rollback polling paths: first run finds work (batch present),
// second run (with priorityLogFound=true) uses shorter interval and finds none.
func Test_OnIdle_PriorityPaths(t *testing.T) {
	ctx := context.Background()

	// Save/restore globals affected by onIdle
	prevHour := hourBeingProcessed
	prevLast := lastOnIdleRunTime
	prevPrLast := lastPriorityOnIdleTime
	prevFound := priorityLogFound
	defer func() {
		hourBeingProcessed = prevHour
		lastOnIdleRunTime = prevLast
		lastPriorityOnIdleTime = prevPrLast
		priorityLogFound = prevFound
	}()

	// Build a transaction with l2 cache and registry seeded for doPriorityRollbacks to succeed.
	redis := mocks.NewMockClient()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: redis, registry: reg}
	// Add a dummy backend to avoid early return in onIdle
	tx.btreesBackend = []btreeBackend{{}}

	// One handle with matching version so version checks pass.
	id := sop.NewUUID()
	h := sop.NewHandle(id)
	h.Version = 1
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})

	// Priority log returns a batch on first call, then empty on next call.
	pl := &stubPriorityLog{}
	tid := sop.NewUUID()
	pl.batch = []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}}}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)
	tx.logger = tl

	// Force both onIdle blocks to run by resetting last run times.
	lastOnIdleRunTime = 0
	lastPriorityOnIdleTime = 0
	priorityLogFound = false

	// First run: should consume batch and set priorityLogFound accordingly.
	tx.onIdle(ctx)
	if !priorityLogFound {
		t.Fatalf("expected priorityLogFound=true after consuming non-empty batch")
	}

	// Second run: priorityLogFound=true leads to shorter interval; empty batch -> found=false.
	pl.batch = nil
	// Reset priority timer to allow another run immediately.
	lastPriorityOnIdleTime = 0
	tx.onIdle(ctx)
	if priorityLogFound {
		t.Fatalf("expected priorityLogFound=false after empty batch")
	}
}

// Covers commitForReaderTransaction path where areFetchedItemsIntact returns an error.
func Test_ReaderTxn_AreFetchedItemsIntact_Error(t *testing.T) {
	ctx := context.Background()
	id := sop.NewUUID()

	// Use a registry whose Get returns error to force areFetchedItemsIntact error.
	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	// errGetRegistry is defined in coverage_boost_4_test.go and overrides Get to return error.
	rg := errGetRegistry{Mock_vid_registry: baseReg}

	tx := &Transaction{mode: sop.ForReading, registry: rg, l2Cache: mocks.NewMockClient(), maxTime: time.Second}
	// One fetched node in local cache so commitForReaderTransaction performs the check.
	nr := &nodeRepositoryBackend{transaction: tx, localCache: map[sop.UUID]cachedNode{id: {node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}, action: getAction}}}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rt", SlotLength: 2})
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, hasTrackedItems: func() bool { return true }, getStoreInfo: func() *sop.StoreInfo { return si }, refetchAndMerge: func(context.Context) error { return nil }}}

	if err := tx.commitForReaderTransaction(ctx); err == nil {
		t.Fatalf("expected error from areFetchedItemsIntact path")
	}
}

// cacheIsLockedErr wraps the mock cache to return an error from IsLocked after a successful Lock.
type cacheIsLockedErr struct{ sop.L2Cache }

func (c cacheIsLockedErr) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	// Delegate to base cache to mark keys as locked by this owner
	return c.L2Cache.Lock(ctx, duration, lockKeys)
}
func (m cacheIsLockedErr) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, tid, err := m.Lock(ctx, duration, keys)
	if !ok || err != nil {
		return ok, tid, err
	}
	if _, err := m.IsLocked(ctx, keys); err != nil {
		return false, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}

func (m cacheIsLockedErr) IsLocked(ctx context.Context, keys []*sop.LockKey) (bool, error) {
	return false, fmt.Errorf("islocked err")
}

// cacheLockFailNoOwner makes Lock fail with no owner and returns an error.
type cacheLockFailNoOwner struct{ sop.L2Cache }

func (c cacheLockFailNoOwner) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, fmt.Errorf("lock err")
}

func (m cacheLockFailNoOwner) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, duration, keys)
}

func Test_AcquireLocks_IsLocked_ReturnsError(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	wc := cacheIsLockedErr{L2Cache: base}
	tx := &Transaction{l2Cache: wc}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	ids := []sop.UUID{sop.NewUUID(), sop.NewUUID()}
	hs := []sop.Handle{sop.NewHandle(ids[0]), sop.NewHandle(ids[1])}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: hs}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || err.Error() != "islocked err" {
		t.Fatalf("expected islocked err, got %v", err)
	}
}

func Test_AcquireLocks_LockError_NoOwner(t *testing.T) {
	ctx := context.Background()
	wc := cacheLockFailNoOwner{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{l2Cache: wc}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	ids := []sop.UUID{sop.NewUUID()}
	hs := []sop.Handle{sop.NewHandle(ids[0])}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: hs}}

	_, err := tl.acquireLocks(ctx, tx, sop.NewUUID(), stores)
	if err == nil || err.Error() != "lock err" {
		t.Fatalf("expected lock err, got %v", err)
	}
}

// Covers acquireLocks branch where Lock fails with ownerTID equal to the requested tid,
// leading to successful takeover via GetEx and setting IsLockOwner.
func Test_AcquireLocks_Takeover_FromSameOwnerTID(t *testing.T) {
	ctx := context.Background()
	rc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: rc}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tid := sop.NewUUID()
	id := sop.NewUUID()
	// Pre-populate the lock key with the same owner TID so Lock returns (false, tid, nil)
	keyName := rc.FormatLockKey(id.String())
	if err := rc.Set(ctx, keyName, tid.String(), time.Minute); err != nil {
		t.Fatalf("seed lock owner err: %v", err)
	}

	hs := []sop.Handle{sop.NewHandle(id)}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: hs}}

	keys, err := tl.acquireLocks(ctx, tx, tid, stores)
	if err != nil {
		t.Fatalf("acquireLocks err: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if !keys[0].IsLockOwner || keys[0].LockID.Compare(tid) != 0 {
		t.Fatalf("expected takeover with IsLockOwner=true and LockID=tid; got %+v", keys[0])
	}
}

// Covers acquireLocks branch where takeover fails due to a different existing owner on one key.
func Test_AcquireLocks_Takeover_Mismatch_Failover(t *testing.T) {
	ctx := context.Background()
	rc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: rc}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)

	tid := sop.NewUUID()
	id1 := sop.NewUUID()
	id2 := sop.NewUUID()

	// Seed two lock keys in cache: one owned by tid, another by a different owner.
	k1 := rc.FormatLockKey(id1.String())
	k2 := rc.FormatLockKey(id2.String())
	_ = rc.Set(ctx, k1, tid.String(), time.Minute)
	other := sop.NewUUID()
	_ = rc.Set(ctx, k2, other.String(), time.Minute)

	hs := []sop.Handle{sop.NewHandle(id1), sop.NewHandle(id2)}
	stores := []sop.RegistryPayload[sop.Handle]{{IDs: hs}}

	_, err := tl.acquireLocks(ctx, tx, tid, stores)
	if err == nil {
		t.Fatalf("expected failover error when takeover finds mismatched owner on a key")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
}

// regUpdateFailover returns a sop.Error with RestoreRegistryFileSectorFailure and the provided lock key as UserData.
type regUpdateFailover struct {
	*mocks.Mock_vid_registry
	ud    *sop.LockKey
	calls int
}

// Override UpdateNoLocks to simulate a registry sector lock timeout during commitUpdatedNodes.
func (r *regUpdateFailover) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	// Fail once to trigger handleRegistrySectorLockTimeout, then succeed so phase1Commit can complete.
	if r.calls == 0 {
		r.calls++
		return sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: fmt.Errorf("upd nolocks fail"), UserData: r.ud}
	}
	return nil
}

func Test_Phase1Commit_HandleRegistrySectorLockTimeout_Path(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	gc := cache.GetGlobalL1Cache(l2)

	// Prepare a lock key that will be placed in sop.Error.UserData
	ud := l2.CreateLockKeys([]string{"rg"})[0]
	ud.LockID = sop.NewUUID()

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	reg := &regUpdateFailover{Mock_vid_registry: baseReg, ud: ud}

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: reg, l2Cache: l2, l1Cache: gc, blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	// One updated node with matching version so commitUpdatedNodes reaches Update and returns sop.Error
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_hlr", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: map[sop.UUID]cachedNode{}, l2Cache: l2, l1Cache: gc, count: si.Count}
	uid := sop.NewUUID()
	nr.localCache[uid] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: uid, Version: 1}}
	h := sop.NewHandle(uid)
	h.Version = 1
	baseReg.Lookup[uid] = h

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
		// If something failed unexpectedly, surface details including sop.Error
		var se sop.Error
		if errors.As(err, &se) {
			t.Fatalf("phase1Commit returned sop.Error: %+v", se)
		}
		t.Fatalf("phase1Commit err: %v", err)
	}
	if !ud.IsLockOwner {
		t.Fatalf("expected IsLockOwner=true after handleRegistrySectorLockTimeout, got false")
	}
}
