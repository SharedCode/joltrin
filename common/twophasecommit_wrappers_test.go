package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// closerRegistry embeds the mock registry and adds Close for exercising Transaction.Close.
type closerRegistry struct {
	mocks.Mock_vid_registry
	closed bool
}

func (c *closerRegistry) Close() error { c.closed = true; return nil }

func Test_NewTwoPhase_TimeBounds_Begin_Close(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	cr := &closerRegistry{}

	// Negative maxTime -> defaults to 15m
	tx, err := NewTwoPhaseCommitTransaction(sop.ForReading, -1, bs, sr, cr, l2, mocks.NewMockTransactionLog())
	if err != nil {
		t.Fatalf("ctor err: %v", err)
	}
	if tx.maxTime <= 0 || tx.maxTime > time.Hour {
		t.Fatalf("unexpected maxTime clamp: %v", tx.maxTime)
	}
	if got := tx.GetMode(); got != sop.ForReading {
		t.Fatalf("GetMode=%v", got)
	}
	if tx.HasBegun() {
		t.Fatal("HasBegun true before Begin")
	}
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin err: %v", err)
	}
	if !tx.HasBegun() {
		t.Fatal("HasBegun false after Begin")
	}
	// Begin again -> error
	if err := tx.Begin(ctx); err == nil {
		t.Fatal("expected Begin again to error")
	}
	// Close calls underlying Closer
	if err := tx.Close(); err != nil {
		t.Fatalf("Close err: %v", err)
	}
	if !cr.closed {
		t.Fatal("expected registry.Close to be invoked")
	}
	_ = tx // keep tx referenced

	// Over-1h clamp
	tx2, err := NewTwoPhaseCommitTransaction(sop.ForWriting, 2*time.Hour, bs, sr, mocks.NewMockRegistry(false), l2, mocks.NewMockTransactionLog())
	if err != nil {
		t.Fatalf("ctor2 err: %v", err)
	}
	if tx2.maxTime > time.Hour {
		t.Fatalf("expected maxTime <= 1h, got %v", tx2.maxTime)
	}
	// GetStores delegates
	_ = sr.Add(ctx, sop.StoreInfo{Name: "s1"})
	if tx2.GetStoreRepository() == nil {
		t.Fatal("GetStoreRepository nil")
	}

	names, err := tx2.GetStoreRepository().GetAll(ctx)
	if err != nil || len(names) == 0 {
		t.Fatalf("GetStores err=%v names=%v", err, names)
	}

	if tx2.GetID().IsNil() {
		t.Fatal("GetID is nil")
	}
}

func Test_Phase1Commit_Modes_And_Error(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1", SlotLength: 4})

	// NoCheck -> early return
	txNo := &Transaction{mode: sop.NoCheck, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := txNo.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := txNo.Phase1Commit(ctx); err != nil {
		t.Fatalf("NoCheck Phase1Commit err: %v", err)
	}

	// ForReading -> commitForReaderTransaction with no tracked items
	txR := &Transaction{mode: sop.ForReading, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	// minimal backend so classify/areFetchedItemsIntact run
	nr := &nodeRepositoryBackend{transaction: txR, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txR.btreesBackend = []btreeBackend{{
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
	if err := txR.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := txR.Phase1Commit(ctx); err != nil {
		t.Fatalf("ForReading Phase1Commit err: %v", err)
	}

	// ForWriting success with no-op tracked items (hasTrackedItems=false)
	txW := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	nrw := &nodeRepositoryBackend{transaction: txW, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txW.btreesBackend = []btreeBackend{{
		nodeRepository:                   nrw,
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
	if err := txW.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := txW.Phase1Commit(ctx); err != nil {
		t.Fatalf("ForWriting no-op Phase1Commit err: %v", err)
	}

	// Error path: lockTrackedItems fails -> rollback invoked and Phase1Commit returns error
	txE := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	// simulate pre-commit state to cover preCommitTID cleanup branch
	txE.logger.committedState = addActivelyPersistedItem
	txE.logger.transactionID = sop.NewUUID()
	nre := &nodeRepositoryBackend{transaction: txE, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txE.btreesBackend = []btreeBackend{{
		nodeRepository:                   nre,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return errors.New("induced lock error") },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return nil },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(context.Context) error { return nil },
	}}
	if err := txE.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := txE.Phase1Commit(ctx); err == nil {
		t.Fatalf("expected Phase1Commit error on lock failure")
	}
	if txE.HasBegun() {
		t.Fatalf("expected transaction ended after Phase1Commit error")
	}
}

func Test_Phase2Commit_Wrappers_Success_And_Errors(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p2", SlotLength: 4})

	// Not begun
	tx0 := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := tx0.Phase2Commit(ctx); err == nil {
		t.Fatal("expected error when not begun")
	}

	// Begun but phase1 not done
	tx1 := &Transaction{mode: sop.ForWriting, phaseDone: 0, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := tx1.Phase2Commit(ctx); err == nil {
		t.Fatal("expected error when phase1 not invoked")
	}

	// Already done
	tx2 := &Transaction{mode: sop.ForWriting, phaseDone: 2, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := tx2.Phase2Commit(ctx); err == nil {
		t.Fatal("expected error when already done")
	}

	// ForReading short-circuit
	txR := &Transaction{mode: sop.ForReading, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := txR.Phase2Commit(ctx); err != nil {
		t.Fatalf("ForReading Phase2 err: %v", err)
	}

	// ForWriting success minimal with registry UpdateNoLocks block executed
	txS := &Transaction{mode: sop.ForWriting, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	nr := &nodeRepositoryBackend{transaction: txS, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txS.btreesBackend = []btreeBackend{{
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
	// Trigger UpdateNoLocks branch by setting updated/removed handles
	txS.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}
	txS.removedNodeHandles = []sop.RegistryPayload[sop.Handle]{}
	if err := txS.Phase2Commit(ctx); err != nil {
		t.Fatalf("ForWriting Phase2 success err: %v", err)
	}

	// Error path: ForWriting with UpdateNoLocks failing, nodesKeys unlocked -> priority log remove path
	txE1 := &Transaction{mode: sop.ForWriting, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(true), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	nrE1 := &nodeRepositoryBackend{transaction: txE1, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txE1.btreesBackend = []btreeBackend{{
		nodeRepository:                   nrE1,
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
	txE1.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}
	if err := txE1.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error on UpdateNoLocks failure (unlocked path)")
	}

	// Error path: ForWriting with UpdateNoLocks failing, nodesKeys locked -> priorityRollback path
	txE2 := &Transaction{mode: sop.ForWriting, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(true), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	nrE2 := &nodeRepositoryBackend{transaction: txE2, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	txE2.btreesBackend = []btreeBackend{{
		nodeRepository:                   nrE2,
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
	// Mark nodesKeys non-nil to simulate locked path
	txE2.nodesKeys = []*sop.LockKey{{Key: l2.FormatLockKey("dummy"), LockID: sop.NewUUID(), IsLockOwner: true}}
	txE2.updatedNodeHandles = []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}
	if err := txE2.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error on UpdateNoLocks failure (locked path)")
	}
}

func Test_Rollback_Wrapper_Success_And_Fail(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	sr := mocks.NewMockStoreRepository()

	// Success path: nothing to rollback, committedState unknown
	txS := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: mocks.NewMockRegistry(false), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := txS.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := txS.Rollback(ctx, nil); err != nil {
		t.Fatalf("Rollback success path err: %v", err)
	}

	// Fail path: set state as already committed so internal rollback errors
	cr := &closerRegistry{}
	txF := &Transaction{mode: sop.ForWriting, phaseDone: -1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, StoreRepository: sr, registry: cr, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	if err := txF.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	txF.logger.committedState = finalizeCommit + 1
	called := false
	txF.HandleReplicationRelatedError = func(ctx context.Context, ioErr error, rbErr error, rbOK bool) { called = true }
	if err := txF.Rollback(ctx, errors.New("cause")); err == nil {
		t.Fatalf("expected rollback error")
	}
	if !called {
		t.Fatalf("expected HandleReplicationRelatedError invoked")
	}
	if !cr.closed {
		t.Fatalf("expected Close called on failure path")
	}
}
