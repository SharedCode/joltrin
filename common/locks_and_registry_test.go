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

// stubPriorityLog2 is a minimal priority log for priorityRollback tests.
type stubPriorityLog2 struct {
	payload map[string][]sop.RegistryPayload[sop.Handle]
	removed map[string]int
}

func (s *stubPriorityLog2) IsEnabled() bool                                             { return true }
func (s *stubPriorityLog2) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (s *stubPriorityLog2) Remove(ctx context.Context, tid sop.UUID) error {
	if s.removed == nil {
		s.removed = map[string]int{}
	}
	s.removed[tid.String()]++
	return nil
}
func (s *stubPriorityLog2) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	if s.payload == nil {
		return nil, nil
	}
	if v, ok := s.payload[tid.String()]; ok {
		return v, nil
	}
	return nil, nil
}
func (s *stubPriorityLog2) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (s *stubPriorityLog2) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
func (s *stubPriorityLog2) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// Backup methods removed from interface; no-op methods deleted.
// Removed backup methods

// stubTLog2 returns our stubPriorityLog2
type stubTLog2 struct{ pl *stubPriorityLog2 }

func (l stubTLog2) PriorityLog() sop.TransactionPriorityLog { return l.pl }
func (l stubTLog2) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return nil
}
func (l stubTLog2) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (l stubTLog2) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, nil
}
func (l stubTLog2) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, nil
}
func (l stubTLog2) NewUUID() sop.UUID { return sop.NewUUID() }

// errorRegistry lets us force UpdateNoLocks errors.
type errorRegistry struct{}

func (e errorRegistry) Add(context.Context, []sop.RegistryPayload[sop.Handle]) error    { return nil }
func (e errorRegistry) Update(context.Context, []sop.RegistryPayload[sop.Handle]) error { return nil }
func (e errorRegistry) UpdateNoLocks(context.Context, bool, []sop.RegistryPayload[sop.Handle]) error {
	return errors.New("forced")
}
func (e errorRegistry) Get(context.Context, []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (e errorRegistry) Remove(context.Context, []sop.RegistryPayload[sop.UUID]) error { return nil }
func (e errorRegistry) Replicate(context.Context, []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle], []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// Note: acquireLocks cases are covered in transactionlogger_unit_test.go

func Test_TransactionLogger_PriorityRollback_Cases(t *testing.T) {
	ctx := context.Background()
	tid := sop.NewUUID()

	// no payload -> Remove called; no registry required
	{
		pl := &stubPriorityLog2{}
		tl := &transactionLog{TransactionLog: stubTLog2{pl: pl}, logging: true}
		if err := tl.priorityRollback(ctx, nil, tid); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if pl.removed[tid.String()] == 0 {
			t.Fatalf("expected Remove called when no payload")
		}
	}

	// nil registry no-op
	{
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		pl := &stubPriorityLog2{payload: map[string][]sop.RegistryPayload[sop.Handle]{tid.String(): {{RegistryTable: "rt", IDs: []sop.Handle{h}}}}}
		tl := &transactionLog{TransactionLog: stubTLog2{pl: pl}, logging: true}
		if err := tl.priorityRollback(ctx, nil, tid); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}

	// UpdateNoLocks error -> failover sop.Error
	{
		lid := sop.NewUUID()
		h := sop.NewHandle(lid)
		pl := &stubPriorityLog2{payload: map[string][]sop.RegistryPayload[sop.Handle]{tid.String(): {{RegistryTable: "rt", IDs: []sop.Handle{h}}}}}
		tl := &transactionLog{TransactionLog: stubTLog2{pl: pl}, logging: true}
		tx := &Transaction{registry: errorRegistry{}}
		if err := tl.priorityRollback(ctx, tx.registry, tid); err == nil {
			t.Fatalf("expected failover error")
		} else if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
			t.Fatalf("expected RestoreRegistryFileSectorFailure, got %v", err)
		}
	}
}

func Test_HandleRegistrySectorLockTimeout_SuccessPath(t *testing.T) {
	ctx := context.Background()
	// Prepare tx with working registry and custom priority log carrying payload under ud.LockID
	reg := mocks.NewMockRegistry(false)
	pl := &stubPriorityLog2{payload: make(map[string][]sop.RegistryPayload[sop.Handle])}
	tl := &transactionLog{TransactionLog: stubTLog2{pl: pl}, logging: true}
	tx := &Transaction{l2Cache: mocks.NewMockClient(), logger: tl, registry: reg}

	// Build a lock key to embed in error.UserData
	ud := tx.l2Cache.CreateLockKeys([]string{"x"})[0]
	ud.LockID = sop.NewUUID()
	// Seed priority payload for that TID so priorityRollback succeeds
	h := sop.NewHandle(ud.LockID)
	pl.payload[ud.LockID.String()] = []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}

	// Invoke
	err := tx.handleRegistrySectorLockTimeout(ctx, sop.Error{Code: sop.RestoreRegistryFileSectorFailure, UserData: ud})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !ud.IsLockOwner {
		t.Fatalf("expected ud.IsLockOwner=true after priority rollback")
	}
}

func Test_Transaction_NodesKeys_Utilities(t *testing.T) {
	ctx := context.Background()
	rc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: rc}

	if tx.nodesKeysExist() {
		t.Fatalf("expected no nodes keys locked initially")
	}
	// Seed one key as owned
	k := rc.CreateLockKeys([]string{"n1"})[0]
	// Mark as owned in mock by setting the backing string
	_ = rc.Set(ctx, k.Key, k.LockID.String(), time.Minute)
	k.IsLockOwner = true
	tx.nodesKeys = []*sop.LockKey{k}

	if !tx.nodesKeysExist() {
		t.Fatalf("expected areNodesKeysLocked true")
	}
	// merge with empty slices should unlock and nil the field
	tx.mergeNodesKeys(ctx, nil, nil)
	if tx.nodesKeys != nil {
		t.Fatalf("expected nodesKeys cleared")
	}
	// unlockNodesKeys is a no-op when nil
	if err := tx.unlockNodesKeys(ctx); err != nil {
		t.Fatalf("unexpected error on unlockNodesKeys: %v", err)
	}
}

func Test_Transaction_GetCommitAndRollbackStoresInfo(t *testing.T) {
	// Build backends with storeInfo and repo counts
	s1 := sop.NewStoreInfo(sop.StoreOptions{Name: "a", SlotLength: 2})
	s2 := sop.NewStoreInfo(sop.StoreOptions{Name: "b", SlotLength: 2})
	s1.Count = 10
	s2.Count = 20
	be1 := btreeBackend{getStoreInfo: func() *sop.StoreInfo { return s1 }, nodeRepository: &nodeRepositoryBackend{count: 7}}
	be2 := btreeBackend{getStoreInfo: func() *sop.StoreInfo { return s2 }, nodeRepository: &nodeRepositoryBackend{count: 30}}
	tx := &Transaction{btreesBackend: []btreeBackend{be1, be2}}

	cs, modified := tx.getCommitStoresInfo()
	if !modified || len(cs) != 2 || cs[0].CountDelta != (10-7) || cs[1].CountDelta != (20-30) {
		t.Fatalf("unexpected commit deltas: %+v", cs)
	}
	rs := tx.getRollbackStoresInfo()
	if len(rs) != 2 || rs[0].CountDelta != (7-10) || rs[1].CountDelta != (30-20) {
		t.Fatalf("unexpected rollback deltas: %+v", rs)
	}
}

func Test_Transaction_DeleteObsoleteEntries_DeletesAll(t *testing.T) {
	ctx := context.Background()
	// Setup caches and mocks
	rc := mocks.NewMockClient()
	cache.GetGlobalL1Cache(rc)
	bs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l1Cache: cache.GetGlobalL1Cache(rc), l2Cache: rc, blobStore: bs, registry: reg}

	// Seed one unused node ID blob and MRU entry
	nid := sop.NewUUID()
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: nid, Value: []byte("x")}}}})
	// MRU uses N prefix via formatKey, but deleteObsoleteEntries deletes via l1Cache.DeleteNodes which accepts UUIDs
	// Seed registry delete ID
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})

	dels := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid}}}
	unused := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{nid}}}

	if err := tx.deleteObsoleteEntries(ctx, dels, unused); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	if ba, _ := bs.GetOne(ctx, "bt", nid); len(ba) != 0 {
		t.Fatalf("blob not deleted")
	}
	if got, _ := reg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid}}}); len(got) != 0 && len(got[0].IDs) != 0 {
		t.Fatalf("registry id not removed")
	}
}

func Test_NodeRepository_UpdateHandleHelpers(t *testing.T) {
	// activateInactiveNodes and touchNodes
	nr := &nodeRepositoryBackend{}
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.Version = 5
	h.FlipActiveID() // make inactive id populated so FlipActiveID will swap back
	in := []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}

	out, err := nr.activateInactiveNodes(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out[0].IDs[0].Version != 6 || out[0].IDs[0].WorkInProgressTimestamp == 0 {
		t.Fatalf("activateInactiveNodes did not bump version/WIP")
	}

	// touchNodes: should bump version and clear WIP
	out2, err := nr.touchNodes(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out2[0].IDs[0].Version != 7 || out2[0].IDs[0].WorkInProgressTimestamp != 0 {
		t.Fatalf("touchNodes did not finalize correctly")
	}
}

func Test_NodeRepository_RollbackRemovedNodes_Branches(t *testing.T) {
	ctx := context.Background()
	// Build a minimal transaction with a mock registry and seed state
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{registry: reg}
	nr := &nodeRepositoryBackend{transaction: tx}

	// Seed two handles with deleted/WIP flags
	lid1 := sop.NewUUID()
	lid2 := sop.NewUUID()
	h1 := sop.NewHandle(lid1)
	h1.IsDeleted = true
	h2 := sop.NewHandle(lid2)
	h2.WorkInProgressTimestamp = 123
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h1, h2}}})

	// nodesAreLocked=false path -> uses Update
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid1, lid2}}}
	if err := nr.rollbackRemovedNodes(ctx, false, vids); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Verify flags cleared in registry state
	got, _ := reg.Get(ctx, vids)
	if len(got) > 0 {
		for _, gh := range got[0].IDs {
			if gh.LogicalID.Compare(lid1) == 0 || gh.LogicalID.Compare(lid2) == 0 {
				if gh.IsDeleted || gh.WorkInProgressTimestamp != 0 {
					t.Fatalf("flags not cleared: %+v", gh)
				}
			}
		}
	}

	// Re-seed to test nodesAreLocked=true path -> uses UpdateNoLocks
	h1.IsDeleted = true
	h2.WorkInProgressTimestamp = 123
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h1, h2}}})
	if err := nr.rollbackRemovedNodes(ctx, true, vids); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
