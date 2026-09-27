package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// recRemoveTL records Remove calls to validate pre-commit log cleanup in phase1Commit.
type recRemoveTL struct {
	inner   sop.TransactionLog
	removed []sop.UUID
}

func (r *recRemoveTL) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return r.inner.GetOne(ctx)
}
func (r *recRemoveTL) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return r.inner.GetOneOfHour(ctx, hour)
}
func (r *recRemoveTL) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return r.inner.Add(ctx, tid, commitFunction, payload)
}
func (r *recRemoveTL) Remove(ctx context.Context, tid sop.UUID) error {
	r.removed = append(r.removed, tid)
	return r.inner.Remove(ctx, tid)
}
func (r *recRemoveTL) NewUUID() sop.UUID                       { return r.inner.NewUUID() }
func (r *recRemoveTL) PriorityLog() sop.TransactionPriorityLog { return r.inner.PriorityLog() }

func Test_Phase1Commit_Removes_PreCommit_Logs(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	base := mocks.NewMockTransactionLog()
	rec := &recRemoveTL{inner: base}
	tl := newTransactionLogger(rec, true)

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0}
	// Simulate pre-commit state exists by setting committedState and giving a pre-commit TID
	tl.committedState = addActivelyPersistedItem
	preTID := sop.NewUUID()
	tl.transactionID = preTID

	// Minimal backend that has tracked items but no nodes to mutate
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
	// Expect a Remove call for pre-commit TID
	found := false
	for _, id := range rec.removed {
		if id.Compare(preTID) == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pre-commit TID %s to be removed, got %v", preTID.String(), rec.removed)
	}
}

func Test_HandleRegistrySectorLockTimeout_LockNotAcquired_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	// Pre-acquire the DTrollbk lock with a different owner so transaction cannot lock it.
	k := l2.FormatLockKey("DTrollbk")
	_ = l2.Set(ctx, k, sop.NewUUID().String(), time.Minute)

	tx := &Transaction{l2Cache: l2, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	se := sop.Error{Err: context.DeadlineExceeded}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err == nil {
		t.Fatalf("expected original error returned when DTrollbk lock not acquired")
	}
}

func Test_HandleRegistrySectorLockTimeout_UserDataTypeMismatch_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	tx := &Transaction{l2Cache: l2, logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	// Provide non-*sop.LockKey user data to exercise early return branch after acquiring lock.
	se := sop.Error{Err: context.DeadlineExceeded, UserData: "not_a_lock_key"}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err == nil {
		t.Fatalf("expected original error returned when user data is not *LockKey")
	}
}
