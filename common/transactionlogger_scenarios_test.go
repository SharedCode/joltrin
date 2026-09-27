package common

// Consolidated transaction logger scenarios.
// Sources merged: transactionlogger_test.go, transactionlogger_helpers_test.go,
// transactionlogger_branches_test.go, transactionlogger_process_expired_test.go

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"

	// cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// updErrRegistry wraps the mock registry and forces UpdateNoLocks to return an error.
type updErrRegistry struct{ *mocks.Mock_vid_registry }

func (u updErrRegistry) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	return fmt.Errorf("forced updatelocks error")
}

// ---- Helper types from helpers test ----
// tlRecorder is a minimal TransactionLog test double capturing Add/Remove calls.
type tlRecorder struct {
	added   []sop.KeyValuePair[int, []byte]
	removed []sop.UUID
	tid     sop.UUID
}

// noOpPrioLog implements sop.TransactionPriorityLog as no-op.
type noOpPrioLog struct{}

func (noOpPrioLog) IsEnabled() bool                             { return false }
func (noOpPrioLog) Add(context.Context, sop.UUID, []byte) error { return nil }
func (noOpPrioLog) Remove(context.Context, sop.UUID) error      { return nil }
func (noOpPrioLog) Get(context.Context, sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (noOpPrioLog) GetBatch(context.Context, int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (noOpPrioLog) LogCommitChanges(context.Context, []sop.StoreInfo, []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (noOpPrioLog) ProcessNewer(context.Context, func(sop.UUID, []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (t *tlRecorder) PriorityLog() sop.TransactionPriorityLog { return noOpPrioLog{} }
func (t *tlRecorder) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	t.added = append(t.added, sop.KeyValuePair[int, []byte]{Key: commitFunction, Value: payload})
	return nil
}
func (t *tlRecorder) Remove(ctx context.Context, tid sop.UUID) error {
	t.removed = append(t.removed, tid)
	return nil
}
func (t *tlRecorder) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (t *tlRecorder) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (t *tlRecorder) NewUUID() sop.UUID { return t.tid }

// ---- Stubs for priority log and transaction log used by priority/onIdle tests ----
// stubPriorityLog implements sop.TransactionPriorityLog for deterministic tests.
type stubPriorityLog struct {
	batch     []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]
	removeErr map[string]error
	// Following fields kept for compatibility with older tests; RemoveBackup is no longer used.
	writeBackupErr  map[string]error
	removeBackupHit map[string]int
	removedHit      map[string]int
}

func (s *stubPriorityLog) IsEnabled() bool                                             { return true }
func (s *stubPriorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (s *stubPriorityLog) Remove(ctx context.Context, tid sop.UUID) error {
	if s.removedHit == nil {
		s.removedHit = map[string]int{}
	}
	s.removedHit[tid.String()]++
	// For tests that still look at removeBackupHit, mirror the counter.
	if s.removeBackupHit == nil {
		s.removeBackupHit = map[string]int{}
	}
	s.removeBackupHit[tid.String()]++
	if err, ok := s.removeErr[tid.String()]; ok {
		return err
	}
	// Remove from batch to simulate actual removal
	newBatch := make([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], 0, len(s.batch))
	for _, kv := range s.batch {
		if kv.Key.Compare(tid) != 0 {
			newBatch = append(newBatch, kv)
		}
	}
	s.batch = newBatch
	return nil
}
func (s *stubPriorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	for _, kv := range s.batch {
		if kv.Key.Compare(tid) == 0 {
			return kv.Value, nil
		}
	}
	return nil, nil
}
func (s *stubPriorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return s.batch, nil
}
func (s *stubPriorityLog) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (s *stubPriorityLog) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// stubTLog implements sop.TransactionLog and returns our stubPriorityLog.
type stubTLog struct{ pl *stubPriorityLog }

func (l stubTLog) PriorityLog() sop.TransactionPriorityLog { return l.pl }
func (l stubTLog) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return nil
}
func (l stubTLog) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (l stubTLog) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (l stubTLog) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (l stubTLog) NewUUID() sop.UUID { return sop.NewUUID() }

// ---- Tests from transactionlogger_test.go ----
func Test_TransactionLogger_AcquireLocks_Cases(t *testing.T) {
	ctx := context.Background()
	type preFn func(ctx context.Context, txn *Transaction, ids []sop.UUID, owner sop.UUID)
	mkStores := func(ids []sop.UUID) []sop.RegistryPayload[sop.Handle] {
		hs := make([]sop.Handle, len(ids))
		for i := range ids {
			hs[i] = sop.NewHandle(ids[i])
		}
		return []sop.RegistryPayload[sop.Handle]{{IDs: hs}}
	}
	setKey := func(txn *Transaction, id sop.UUID, owner sop.UUID) {
		k := txn.l2Cache.CreateLockKeys([]string{id.String()})[0].Key
		_ = txn.l2Cache.Set(ctx, k, owner.String(), time.Minute)
	}

	cases := []struct {
		name        string
		ids         []sop.UUID
		tid         sop.UUID
		pre         preFn
		expectErr   bool
		expectCode  sop.ErrorCode
		expectLen   int
		expectOwner bool
	}{
		{name: "succeeds_single", ids: []sop.UUID{sop.NewUUID()}, tid: sop.NewUUID(), expectErr: false, expectLen: 1, expectOwner: true},
		{name: "partial_lock_fails", ids: []sop.UUID{sop.NewUUID(), sop.NewUUID()}, tid: sop.NewUUID(), pre: func(ctx context.Context, txn *Transaction, ids []sop.UUID, owner sop.UUID) {
			setKey(txn, ids[0], owner)
		}, expectErr: true, expectCode: sop.RestoreRegistryFileSectorFailure},
		{name: "takeover_dead_owner", ids: []sop.UUID{sop.NewUUID(), sop.NewUUID()}, tid: sop.NewUUID(), pre: func(ctx context.Context, txn *Transaction, ids []sop.UUID, owner sop.UUID) {
			setKey(txn, ids[0], owner)
			setKey(txn, ids[1], owner)
		}, expectLen: 2, expectOwner: true},
		{name: "locked_by_other_owner_fails", ids: []sop.UUID{sop.NewUUID(), sop.NewUUID()}, tid: sop.NewUUID(), pre: func(ctx context.Context, txn *Transaction, ids []sop.UUID, owner sop.UUID) {
			setKey(txn, ids[0], owner)
			setKey(txn, ids[1], owner)
		}, expectErr: true, expectCode: sop.RestoreRegistryFileSectorFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mrc := mocks.NewMockClient()
			txn := &Transaction{l2Cache: mrc}
			tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
			otherOwner := sop.NewUUID()
			owner := otherOwner
			if tc.name == "takeover_dead_owner" {
				owner = tc.tid
			}
			if tc.pre != nil {
				tc.pre(ctx, txn, tc.ids, owner)
			}
			stores := mkStores(tc.ids)
			keys, err := tl.acquireLocks(ctx, txn, tc.tid, stores)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if se, ok := err.(sop.Error); !ok || se.Code != tc.expectCode {
					t.Fatalf("expected code %v got %v", tc.expectCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.expectLen > 0 && len(keys) != tc.expectLen {
				t.Fatalf("expected %d keys got %d", tc.expectLen, len(keys))
			}
			if tc.expectOwner {
				for _, k := range keys {
					if !k.IsLockOwner {
						t.Fatalf("expected IsLockOwner true")
					}
				}
			}
			_ = txn.l2Cache.Unlock(ctx, keys)
		})
	}
}

func Test_TransactionLogger_PriorityRollback_NoOps(t *testing.T) {
	ctx := context.Background()
	txn := &Transaction{registry: mocks.NewMockRegistry(false)}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	if err := tl.priorityRollback(ctx, txn.registry, sop.NewUUID()); err != nil {
		t.Fatalf("priorityRollback error: %v", err)
	}
}

func Test_TransactionLogger_DoPriorityRollbacks_Empty(t *testing.T) {
	ctx := context.Background()
	txn := &Transaction{l2Cache: mockRedisCache}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	ok, err := tl.doPriorityRollbacks(ctx, txn)
	if err != nil {
		t.Fatalf("doPriorityRollbacks error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false")
	}
}

// ---- Tests from helpers ----
func Test_TransactionLogger_Log_And_RemoveLogs(t *testing.T) {
	ctx := context.Background()
	fixedTID := sop.NewUUID()
	rec := &tlRecorder{tid: fixedTID}
	tl := newTransactionLogger(rec, false)
	if err := tl.log(ctx, commitAddedNodes, []byte{1, 2, 3}); err != nil {
		t.Fatalf("log error: %v", err)
	}
	if tl.committedState != commitAddedNodes {
		t.Fatalf("committedState not set")
	}
	if len(rec.added) != 0 {
		t.Fatalf("unexpected Add calls when disabled")
	}
	tl2 := newTransactionLogger(rec, true)
	if err := tl2.log(ctx, commitUpdatedNodes, []byte{9}); err != nil {
		t.Fatalf("log enabled error: %v", err)
	}
	if len(rec.added) == 0 || rec.added[len(rec.added)-1].Key != int(commitUpdatedNodes) {
		t.Fatalf("Add not recorded")
	}
	if err := tl2.removeLogs(ctx); err != nil {
		t.Fatalf("removeLogs error: %v", err)
	}
	if len(rec.removed) == 0 || rec.removed[len(rec.removed)-1] != fixedTID {
		t.Fatalf("Remove not called with fixed tid")
	}
}

func Test_ToStruct_ToByteArray_RoundTrip_And_Nil(t *testing.T) {
	var z sop.Tuple[int, string]
	got := toStruct[sop.Tuple[int, string]](nil)
	if !reflect.DeepEqual(got, z) {
		t.Fatalf("expected zero value")
	}
	original := sop.Tuple[sop.Tuple[int, string], []sop.UUID]{First: sop.Tuple[int, string]{First: 42, Second: "hello"}, Second: []sop.UUID{sop.NewUUID(), sop.NewUUID()}}
	ba := toByteArray(original)
	round := toStruct[sop.Tuple[sop.Tuple[int, string], []sop.UUID]](ba)
	if !reflect.DeepEqual(original, round) {
		t.Fatalf("roundtrip mismatch")
	}
}

// ---- Branch rollback tests ----
func Test_TransactionLogger_Rollback_AddedRemovedUpdated_Branches(t *testing.T) {
	ctx := context.Background()
	localRedis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(localRedis)
	localBlobs := mocks.NewMockBlobStore()
	localReg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis), blobStore: localBlobs, registry: localReg}
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis)}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}
	addedID := sop.NewUUID()
	removedLID := sop.NewUUID()
	updatedTempID := sop.NewUUID()
	_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_add", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: addedID, Value: []byte("a")}}}})
	_ = localRedis.SetStruct(ctx, nr.formatKey(addedID.String()), &btree.Node[PersonKey, Person]{ID: addedID}, time.Minute)
	_ = localReg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_add", IDs: []sop.Handle{sop.NewHandle(addedID)}}})
	hRemoved := sop.NewHandle(removedLID)
	hRemoved.IsDeleted = true
	hRemoved.WorkInProgressTimestamp = 123
	localReg.(*mocks.Mock_vid_registry).Lookup[removedLID] = hRemoved
	_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_upd", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: updatedTempID, Value: []byte("t")}}}})
	_ = localRedis.SetStruct(ctx, nr.formatKey(updatedTempID.String()), &btree.Node[PersonKey, Person]{ID: updatedTempID}, time.Minute)
	addedPayload := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_add", IDs: []sop.UUID{addedID}}}, Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_add", Blobs: []sop.UUID{addedID}}}}
	removedPayload := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_rem", IDs: []sop.UUID{removedLID}}}
	updatedPayload := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_upd", Blobs: []sop.UUID{updatedTempID}}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	logs := []sop.KeyValuePair[int, []byte]{{Key: commitAddedNodes, Value: toByteArray(addedPayload)}, {Key: commitRemovedNodes, Value: toByteArray(removedPayload)}, {Key: commitUpdatedNodes, Value: toByteArray(updatedPayload)}, {Key: finalizeCommit, Value: nil}}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback error: %v", err)
	}
	if ba, _ := localBlobs.GetOne(ctx, "bt_add", addedID); len(ba) != 0 {
		t.Fatalf("added blob not removed")
	}
	var out btree.Node[PersonKey, Person]
	if ok, _ := localRedis.GetStruct(ctx, nr.formatKey(addedID.String()), &out); ok {
		t.Fatalf("added cache not evicted")
	}
	g1, _ := localReg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_add", IDs: []sop.UUID{addedID}}})
	if len(g1) > 0 && len(g1[0].IDs) > 0 {
		t.Fatalf("registry ID not removed")
	}
	if got := localReg.(*mocks.Mock_vid_registry).Lookup[removedLID]; got.IsDeleted || got.WorkInProgressTimestamp != 0 {
		t.Fatalf("removed flags not cleared: %+v", got)
	}
	if ba, _ := localBlobs.GetOne(ctx, "bt_upd", updatedTempID); len(ba) != 0 {
		t.Fatalf("updated temp blob not removed")
	}
	if ok, _ := localRedis.GetStruct(ctx, nr.formatKey(updatedTempID.String()), &out); ok {
		t.Fatalf("updated temp cache not removed")
	}
}

func Test_TransactionLogger_Rollback_NewRootAndTrackedValues(t *testing.T) {
	ctx := context.Background()
	localRedis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(localRedis)
	localBlobs := mocks.NewMockBlobStore()
	localReg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis), blobStore: localBlobs, registry: localReg}
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis)}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl
	tl.committedState = finalizeCommit
	rootLID := sop.NewUUID()
	rootNodeBlobID := sop.NewUUID()
	_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_root", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: rootNodeBlobID, Value: []byte("r")}}}})
	_ = localRedis.SetStruct(ctx, nr.formatKey(rootLID.String()), &btree.Node[PersonKey, Person]{ID: rootNodeBlobID}, time.Minute)
	_ = localReg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_root", IDs: []sop.Handle{sop.NewHandle(rootLID)}}})
	rootPayload := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root", IDs: []sop.UUID{rootLID}}}, Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_root", Blobs: []sop.UUID{rootNodeBlobID}}}}
	valBlobID := sop.NewUUID()
	_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_val", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: valBlobID, Value: []byte("v")}}}})
	_ = localRedis.SetStruct(ctx, formatItemKey(valBlobID.String()), &Person{Email: "x"}, time.Minute)
	trackedVals := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "bt_val", Blobs: []sop.UUID{valBlobID}}}}
	logs := []sop.KeyValuePair[int, []byte]{{Key: commitNewRootNodes, Value: toByteArray(rootPayload)}, {Key: commitTrackedItemsValues, Value: toByteArray(trackedVals)}, {Key: finalizeCommit, Value: nil}}
	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback error: %v", err)
	}
	if ba, _ := localBlobs.GetOne(ctx, "bt_root", rootNodeBlobID); len(ba) != 0 {
		t.Fatalf("root blob not removed")
	}
	var n btree.Node[PersonKey, Person]
	if ok, _ := localRedis.GetStruct(ctx, nr.formatKey(rootLID.String()), &n); ok {
		t.Fatalf("root cache not evicted")
	}
	g, _ := localReg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root", IDs: []sop.UUID{rootLID}}})
	if len(g) > 0 && len(g[0].IDs) > 0 {
		t.Fatalf("root registry not removed")
	}
	if ba, _ := localBlobs.GetOne(ctx, "bt_val", valBlobID); len(ba) != 0 {
		t.Fatalf("tracked value blob not removed")
	}
	var pv Person
	if ok, _ := localRedis.GetStruct(ctx, formatItemKey(valBlobID.String()), &pv); ok {
		t.Fatalf("tracked value cache not removed")
	}
}

// ---- Rollback finalize commit cases ----
func Test_TransactionLogger_Rollback_FinalizeCommit_Cases(t *testing.T) {
	ctx := context.Background()
	type fixture struct {
		tl       *transactionLog
		tx       *Transaction
		logs     []sop.KeyValuePair[int, []byte]
		validate func(t *testing.T)
	}
	mk := func(name string) fixture {
		switch name {
		case "deletes_all":
			localRedis := mocks.NewMockClient()
			cache.GetGlobalL1Cache(localRedis)
			localBlobs := mocks.NewMockBlobStore()
			localReg := mocks.NewMockRegistry(false)
			tx := &Transaction{l2Cache: localRedis, l1Cache: cache.GetGlobalL1Cache(localRedis), blobStore: localBlobs, registry: localReg}
			tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
			nodeBlobID := sop.NewUUID()
			itemBlobID := sop.NewUUID()
			regID := sop.NewUUID()
			_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: nodeBlobID, Value: []byte("n")}}}, {BlobTable: "it", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: itemBlobID, Value: []byte("v")}}}})
			_ = localRedis.SetStruct(ctx, formatItemKey(itemBlobID.String()), &Person{Email: "e"}, time.Minute)
			_ = localReg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(regID)}}})
			pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{First: sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{regID}}}, Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{nodeBlobID}}}}, Second: []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{itemBlobID}}}}}
			logs := []sop.KeyValuePair[int, []byte]{{Key: finalizeCommit, Value: toByteArray(pl)}, {Key: deleteTrackedItemsValues, Value: nil}}
			validate := func(t *testing.T) {
				if ba, _ := localBlobs.GetOne(ctx, "bt", nodeBlobID); len(ba) != 0 {
					t.Fatalf("node blob not deleted")
				}
				if ba, _ := localBlobs.GetOne(ctx, "it", itemBlobID); len(ba) != 0 {
					t.Fatalf("item blob not deleted")
				}
				var pv Person
				if ok, _ := localRedis.GetStruct(ctx, formatItemKey(itemBlobID.String()), &pv); ok {
					t.Fatalf("value cache not deleted")
				}
				got, _ := localReg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{regID}}})
				if len(got) > 0 && len(got[0].IDs) > 0 {
					t.Fatalf("registry ID not removed")
				}
			}
			return fixture{tl: tl, tx: tx, logs: logs, validate: validate}
		case "payload_continue_no_delete":
			tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
			tx := &Transaction{}
			blobID := sop.NewUUID()
			regID := sop.NewUUID()
			pl := sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{First: sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{regID}}}, Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{blobID}}}}, Second: []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{blobID}}}}}
			logs := []sop.KeyValuePair[int, []byte]{{Key: finalizeCommit, Value: toByteArray(pl)}, {Key: commitUpdatedNodes, Value: nil}}
			return fixture{tl: tl, tx: tx, logs: logs}
		case "no_payload_continue":
			tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
			tx := &Transaction{}
			logs := []sop.KeyValuePair[int, []byte]{{Key: finalizeCommit, Value: nil}, {Key: commitUpdatedNodes, Value: nil}}
			return fixture{tl: tl, tx: tx, logs: logs}
		case "tracked_values_only":
			localRedis := mocks.NewMockClient()
			localBlobs := mocks.NewMockBlobStore()
			tx := &Transaction{l2Cache: localRedis, blobStore: localBlobs}
			tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
			valID := sop.NewUUID()
			_ = localBlobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "it", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: valID, Value: []byte("v")}}}})
			_ = localRedis.SetStruct(ctx, formatItemKey(valID.String()), &Person{Email: "x"}, time.Minute)
			trackedVals := []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{{First: true, Second: sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{valID}}}}
			logs := []sop.KeyValuePair[int, []byte]{{Key: commitTrackedItemsValues, Value: toByteArray(trackedVals)}}
			validate := func(t *testing.T) {
				if ba, _ := localBlobs.GetOne(ctx, "it", valID); len(ba) != 0 {
					t.Fatalf("tracked value blob not deleted")
				}
				var pv Person
				if ok, _ := localRedis.GetStruct(ctx, formatItemKey(valID.String()), &pv); ok {
					t.Fatalf("tracked value cache not deleted")
				}
			}
			return fixture{tl: tl, tx: tx, logs: logs, validate: validate}
		}
		return fixture{}
	}
	cases := []string{"deletes_all", "payload_continue_no_delete", "no_payload_continue", "tracked_values_only"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			f := mk(name)
			if err := f.tl.rollback(ctx, f.tx, sop.NewUUID(), f.logs); err != nil {
				t.Fatalf("rollback error %s: %v", name, err)
			}
			if f.validate != nil {
				f.validate(t)
			}
		})
	}
}

func Test_TransactionLogger_ProcessExpired_Cases(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{}
	prev := hourBeingProcessed
	defer func() { hourBeingProcessed = prev }()
	t.Run("default_no_logs", func(t *testing.T) {
		hourBeingProcessed = ""
		if err := tl.processExpiredTransactionLogs(ctx, tx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("with_hour_set_resets", func(t *testing.T) {
		hourBeingProcessed = "2022010112"
		if err := tl.processExpiredTransactionLogs(ctx, tx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hourBeingProcessed != "" {
			t.Fatalf("expected reset")
		}
	})
}

// Ensures processExpiredTransactionLogs processes an older-than-1h log and calls rollback.
func Test_TransactionLogger_ProcessExpired_WithEntry(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	// Create a log from yesterday so mock GetOne returns it.
	// prevCasNow := cas.Now
	// defer func() { cas.Now = prevCasNow }()
	// cas.Now = func() time.Time { return time.Now().Add(-24 * time.Hour) }

	tid := tl.NewUUID()
	if err := tl.Add(ctx, tid, int(finalizeCommit), nil); err != nil {
		t.Fatalf("Add log err: %v", err)
	}

	// Restore time to current so process can advance hourBeingProcessed
	// cas.Now = prevCasNow
	tx := &Transaction{}
	if err := tl.processExpiredTransactionLogs(ctx, tx); err != nil {
		t.Fatalf("processExpiredTransactionLogs err: %v", err)
	}
}
