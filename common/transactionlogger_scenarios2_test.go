package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"

	// cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// stubTLRemoveErr allows observing Remove calls and returning a configured error.
type stubTLRemove struct {
	mocks.MockTransactionLog
	removed []sop.UUID
}

func (s *stubTLRemove) Remove(ctx context.Context, tid sop.UUID) error {
	s.removed = append(s.removed, tid)
	return nil
}

// Covers rollback branch when committedFunctionLogs is empty and tid is non-nil: Remove is called.
func Test_TransactionLogger_Rollback_EmptyLogs_WithTid_Removes(t *testing.T) {
	ctx := context.Background()
	tl := &stubTLRemove{}
	logger := newTransactionLogger(tl, true)
	tx := &Transaction{logger: logger}

	tid := sop.NewUUID()
	if err := logger.rollback(ctx, tx, tid, nil); err != nil {
		t.Fatalf("rollback empty logs returned error: %v", err)
	}
	if len(tl.removed) != 1 || tl.removed[0].Compare(tid) != 0 {
		t.Fatalf("expected Remove called with tid, got %v", tl.removed)
	}
}

// Covers rollback branch when committedFunctionLogs is empty and tid is nil: returns nil without Remove.
func Test_TransactionLogger_Rollback_EmptyLogs_NilTid_NoRemove(t *testing.T) {
	ctx := context.Background()
	tl := &stubTLRemove{}
	logger := newTransactionLogger(tl, true)
	tx := &Transaction{logger: logger}

	if err := logger.rollback(ctx, tx, sop.NilUUID, nil); err != nil {
		t.Fatalf("rollback empty logs (nil tid) returned error: %v", err)
	}
	if len(tl.removed) != 0 {
		t.Fatalf("expected no Remove calls, got %d", len(tl.removed))
	}
}

// Covers processExpiredTransactionLogs path where hourBeingProcessed is set and no entries found; it should reset.
func Test_TransactionLogger_ProcessExpired_NoEntries_ResetsHour(t *testing.T) {
	ctx := context.Background()
	// Ensure non-empty hourBeingProcessed.
	hourBeingProcessed = "2024010112"
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{logger: tl}
	if err := tl.processExpiredTransactionLogs(ctx, tx); err != nil {
		t.Fatalf("processExpiredTransactionLogs error: %v", err)
	}
	if hourBeingProcessed != "" {
		t.Fatalf("expected hourBeingProcessed reset to empty, got %q", hourBeingProcessed)
	}
}

// Covers deleteTrackedItemsValues for both cache-deletion and skip-cache branches.
func Test_Transaction_DeleteTrackedItemsValues_CacheAndNoCache(t *testing.T) {
	ctx := context.Background()
	// Set up isolated mocks
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	blobs := mocks.NewMockBlobStore()
	tx := &Transaction{l2Cache: redis, blobStore: blobs}

	// Two blob IDs, one globally cached, one not
	cachedID := sop.NewUUID()
	nonCachedID := sop.NewUUID()

	// Seed blobs
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{
		{BlobTable: "it", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: cachedID, Value: []byte("c")}}},
		{BlobTable: "it", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: nonCachedID, Value: []byte("n")}}},
	})
	// Seed cache for the first id
	type stub struct{ A int }
	_ = redis.SetStruct(ctx, formatItemKey(cachedID.String()), &stub{A: 1}, time.Minute)

	payload := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
		{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{cachedID}}},
		{First: false, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{nonCachedID}}},
	}

	if err := tx.deleteTrackedItemsValues(ctx, payload); err != nil {
		t.Fatalf("deleteTrackedItemsValues error: %v", err)
	}

	// Cache for cachedID should be removed
	var x stub
	if ok, _ := redis.GetStruct(ctx, formatItemKey(cachedID.String()), &x); ok {
		t.Fatalf("cache not deleted for cachedID")
	}
	// Blobs for both should be removed
	if ba, _ := blobs.GetOne(ctx, "it", cachedID); len(ba) != 0 {
		t.Fatalf("cachedID blob not removed")
	}
	if ba, _ := blobs.GetOne(ctx, "it", nonCachedID); len(ba) != 0 {
		t.Fatalf("nonCachedID blob not removed")
	}
}

func Test_TransactionLogger_Rollback_CommitStoreInfo_Path(t *testing.T) {
	ctx := context.Background()
	localReg := mocks.NewMockRegistry(false)
	localBlobs := mocks.NewMockBlobStore()
	localRedis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(localRedis)
	tx := &Transaction{registry: localReg, blobStore: localBlobs, l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis), StoreRepository: mocks.NewMockStoreRepository()}
	ll := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	name := "st1"
	_ = tx.StoreRepository.Add(ctx, sop.StoreInfo{Name: name})
	stores := []sop.StoreInfo{{Name: name, CountDelta: 10}}
	logs := []sop.KeyValuePair[int, []byte]{{Key: commitStoreInfo, Value: toByteArray(stores)}, {Key: finalizeCommit, Value: nil}}
	if err := ll.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback error: %v", err)
	}
}

// ---- Expired transaction logs processing (process_expired) ----
func Test_ProcessExpiredTransactionLogs_ConsumesHourAndClears(t *testing.T) {
	ctx := context.Background()
	// origNow := cas.Now
	// base := time.Date(2025, 1, 2, 15, 0, 0, 0, time.UTC)
	// cas.Now = func() time.Time { return base }
	// defer func() { cas.Now = origNow }()

	backend := mocks.NewMockTransactionLog()
	tl := newTransactionLogger(backend, true)
	if err := tl.log(ctx, finalizeCommit, nil); err != nil {
		t.Fatalf("seed log error: %v", err)
	}
	// cas.Now = func() time.Time { return base.Add(2 * time.Hour) }

	// Use a different logger to simulate another transaction processing expired logs.
	processorTL := newTransactionLogger(backend, true)
	tx := &Transaction{}
	hourBeingProcessed = ""
	if err := processorTL.processExpiredTransactionLogs(ctx, tx); err != nil {
		t.Fatalf("processExpiredTransactionLogs error: %v", err)
	}
	if err := processorTL.processExpiredTransactionLogs(ctx, tx); err != nil {
		t.Fatalf("second process error: %v", err)
	}
	if hourBeingProcessed != "" {
		t.Fatalf("hourBeingProcessed not cleared")
	}
}

func Test_PriorityRollback_NilTransaction_NoPanic(t *testing.T) {
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tl.priorityRollback(context.Background(), nil, sop.NewUUID()); err != nil {
		t.Fatalf("priorityRollback nil txn error: %v", err)
	}
}

// ---- OnIdle and Priority specific scenarios moved from separate files ----
func Test_Transaction_OnIdle_ProcessesExpired_WhenHourSet(t *testing.T) {
	ctx := context.Background()
	tx := &Transaction{btreesBackend: []btreeBackend{{}}}
	tx.logger = newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.l2Cache = mocks.NewMockClient()

	prevHour := hourBeingProcessed
	prevLast := lastOnIdleRunTime
	hourBeingProcessed = "2022010112"
	lastOnIdleRunTime = 0
	defer func() { hourBeingProcessed = prevHour; lastOnIdleRunTime = prevLast }()

	tx.onIdle(ctx)
	if hourBeingProcessed != "" {
		t.Fatalf("expected hourBeingProcessed reset to empty, got %q", hourBeingProcessed)
	}
}

func Test_TransactionLogger_DoPriorityRollbacks_Cases(t *testing.T) {
	ctx := context.Background()

	mkHandle := func(id sop.UUID, ver int32) sop.Handle {
		h := sop.NewHandle(id)
		h.Version = ver
		return h
	}

	// Common store payload factory
	makePayload := func(rt, bt string, ids []sop.Handle) []sop.RegistryPayload[sop.Handle] {
		return []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: rt, BlobTable: bt, IDs: ids},
		}
	}

	t.Run("consumes_one_success", func(t *testing.T) {
		// Mocks
		redis := mocks.NewMockClient()
		reg := mocks.NewMockRegistry(false)
		tx := &Transaction{l2Cache: redis, registry: reg}
		// Create one tid with one handle, version aligned
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		uh := mkHandle(lid, 3)
		// Seed current registry with same version to satisfy check
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 3)}},
		})
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{uh})}}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)

		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !consumed {
			t.Fatalf("expected consumed=true")
		}
		// Priority log should be removed
		if pl.removedHit[tid.String()] == 0 {
			t.Fatalf("expected Remove to be called")
		}
	})

	t.Run("write_backup_error_continue", func(t *testing.T) {
		redis := mocks.NewMockClient()
		reg := mocks.NewMockRegistry(false)
		tx := &Transaction{l2Cache: redis, registry: reg}
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		// Seed registry to avoid version error; lock path should succeed
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 1)}},
		})
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{mkHandle(lid, 1)})}}, writeBackupErr: map[string]error{tid.String(): fmt.Errorf("wb err")}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)
		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !consumed {
			t.Fatalf("expected consumed=true due to batch present")
		}
	})

	t.Run("remove_error_returns_error", func(t *testing.T) {
		redis := mocks.NewMockClient()
		reg := mocks.NewMockRegistry(false)
		tx := &Transaction{l2Cache: redis, registry: reg}
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 1)}},
		})
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{mkHandle(lid, 1)})}}, removeErr: map[string]error{tid.String(): fmt.Errorf("rm err")}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)
		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if err == nil {
			t.Fatalf("expected error from Remove to be returned")
		}
		if consumed {
			t.Fatalf("expected consumed=false when Remove returns error")
		}
	})

	t.Run("acquire_locks_conflict_returns_failover", func(t *testing.T) {
		redis := mocks.NewMockClient()
		reg := mocks.NewMockRegistry(false)
		tx := &Transaction{l2Cache: redis, registry: reg}
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		// Seed registry so version check would pass if reached
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 1)}},
		})
		// Pre-lock the key by someone else to force acquireLocks error with owner mismatch
		k := tx.l2Cache.CreateLockKeys([]string{lid.String()})[0].Key
		other := sop.NewUUID()
		_ = tx.l2Cache.Set(ctx, k, other.String(), time.Minute)
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{mkHandle(lid, 1)})}}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)
		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if consumed {
			t.Fatalf("expected consumed=false when conflict")
		}
		if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
			t.Fatalf("expected failover error, got %v", err)
		}
	})

	t.Run("version_too_far_returns_failover", func(t *testing.T) {
		redis := mocks.NewMockClient()
		reg := mocks.NewMockRegistry(false)
		tx := &Transaction{l2Cache: redis, registry: reg}
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		// Seed registry with version too far ahead (e.g., 5 vs uh 3) to trigger failover
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 5)}},
		})
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{mkHandle(lid, 3)})}}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)
		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if consumed {
			t.Fatalf("expected consumed=false on version failover")
		}
		if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
			t.Fatalf("expected failover error, got %v", err)
		}
	})

	// Custom registry that errors on UpdateNoLocks but supports Get with seeded values.
	t.Run("update_no_locks_error_returns_failover", func(t *testing.T) {
		redis := mocks.NewMockClient()
		base := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
		r := updErrRegistry{Mock_vid_registry: base}
		tx := &Transaction{l2Cache: redis, registry: r}
		tid := sop.NewUUID()
		lid := sop.NewUUID()
		// Seed registry with same version to satisfy version check
		_ = r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{mkHandle(lid, 1)}}})
		pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: makePayload("rt", "bt", []sop.Handle{mkHandle(lid, 1)})}}}
		tl := newTransactionLogger(stubTLog{pl: pl}, true)
		consumed, err := tl.doPriorityRollbacks(ctx, tx)
		if consumed {
			t.Fatalf("expected consumed=false on update error")
		}
		if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
			t.Fatalf("expected failover error, got %v", err)
		}
	})
}

// errRegistry is a stub sop.Registry that forces UpdateNoLocks to return an error.
type errRegistry struct{}

func (e errRegistry) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (e errRegistry) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (e errRegistry) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return fmt.Errorf("forced error")
}
func (e errRegistry) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (e errRegistry) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	return nil
}
func (e errRegistry) Replicate(ctx context.Context, newRootNodeHandles, addedNodeHandles, updatedNodeHandles, removedNodeHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// Ensures the error branch in priorityRollback returns a sop.Error when UpdateNoLocks fails.
func Test_TransactionLogger_PriorityRollback_ErrorBranch(t *testing.T) {
	ctx := context.Background()
	// Registry that always errors on UpdateNoLocks
	tx := &Transaction{registry: errRegistry{}}
	// Priority log returns a payload for the same tid
	tid := sop.NewUUID()
	pl := &stubPriorityLog{batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: tid, Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}}}}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)

	err := tl.priorityRollback(ctx, tx.registry, tid)
	if err == nil {
		t.Fatalf("expected error from priorityRollback")
	}
	// Expect a wrapped sop.Error carrying failover code
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
}

// Ensures rollback processes pre-commit logs for actively persisted items by deleting value blobs.
func Test_TransactionLogger_Rollback_PreCommit_ActivelyPersisted_CleansValues(t *testing.T) {
	ctx := context.Background()
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{blobStore: blobs}

	// Prepare a pre-commit payload with one value blob ID.
	valID := sop.NewUUID()
	table := "it_values"
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: valID, Value: []byte("v")}}}})
	pre := sop.BlobsPayload[sop.UUID]{BlobTable: table, Blobs: []sop.UUID{valID}}

	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(addActivelyPersistedItem), Value: toByteArray(pre)},
	}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback pre-commit cleanup error: %v", err)
	}
	if ba, _ := blobs.GetOne(ctx, table, valID); len(ba) != 0 {
		t.Fatalf("pre-commit value blob not removed")
	}
}

// Ensures processExpiredTransactionLogs handles no logs for current hour and resets hourBeingProcessed.
func Test_TransactionLogger_ProcessExpired_NoLogs(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Force hourBeingProcessed to a stale hour and ensure it resets when no TID returned.
	hourBeingProcessed = time.Now().Add(-2 * time.Hour).Format(mocks.DateHourLayout)
	if err := tl.processExpiredTransactionLogs(ctx, &Transaction{blobStore: mocks.NewMockBlobStore(), l2Cache: mocks.NewMockClient(), registry: mocks.NewMockRegistry(false), logger: tl}); err != nil {
		t.Fatalf("processExpiredTransactionLogs err: %v", err)
	}
	if hourBeingProcessed != "" {
		t.Fatalf("expected hourBeingProcessed reset, got %q", hourBeingProcessed)
	}
}

// Covers rollback branch where finalizeCommit has nil payload and the last committed
// function is deleteObsoleteEntries, which should trigger immediate Remove and return.
func Test_TransactionLogger_Rollback_FinalizeNil_WithDeleteObsolete_Removes(t *testing.T) {
	ctx := context.Background()
	tl := &stubTLRemove{}
	logger := newTransactionLogger(tl, true)
	tx := &Transaction{logger: logger}

	tid := sop.NewUUID()
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: int(finalizeCommit), Value: nil},
		{Key: int(deleteObsoleteEntries), Value: nil},
	}
	if err := logger.rollback(ctx, tx, tid, logs); err != nil {
		t.Fatalf("rollback returned error: %v", err)
	}
	if len(tl.removed) != 1 || tl.removed[0].Compare(tid) != 0 {
		t.Fatalf("expected Remove called for tid, got %v", tl.removed)
	}
}

// Covers doPriorityRollbacks error-to-failover path when UpdateNoLocks fails in priority rollback.
func Test_TransactionLogger_DoPriorityRollbacks_Failover(t *testing.T) {
	ctx := context.Background()
	// Prepare a priority log batch with a single transaction and one handle.
	tid := sop.NewUUID()
	lid := sop.NewUUID()
	pl := &stubPriorityLog{
		batch: []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{
			Key:   tid,
			Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}},
		}},
	}
	tl := transactionLog{TransactionLog: stubTLog{pl: pl}}

	// Use a registry that returns matching Get results but errors on UpdateNoLocks to trigger failover path.
	base := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	// Seed registry with the same version so version check passes.
	_ = base.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(lid)}}})
	tx := &Transaction{registry: updErrRegistry{Mock_vid_registry: base}, l2Cache: mocks.NewMockClient()}

	busy, err := tl.doPriorityRollbacks(ctx, tx)
	if err == nil {
		t.Fatalf("expected failover error")
	}
	var se sop.Error
	if !errors.As(err, &se) || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected sop.RestoreRegistryFileSectorFailure, got %v", err)
	}
	_ = busy // busy may be true/false depending on timeouts
}
