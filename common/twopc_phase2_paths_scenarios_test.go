package common

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func seedUpdateAndRemove(t *testing.T, ctx context.Context, b3 btree.BtreeInterface[PersonKey, Person], pk PersonKey, p Person) {
	t.Helper()
	if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
		t.Fatalf("Add failed: ok=%v err=%v", ok, err)
	}
	p2 := p
	p2.Email = "u@x"
	if _, err := b3.Update(ctx, pk, p2); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	pkR, pR := newPerson("r", "m", "x", "r@x", "1")
	if ok, err := b3.Add(ctx, pkR, pR); !ok || err != nil {
		t.Fatalf("Add(second) failed: ok=%v err=%v", ok, err)
	}
	if _, err := b3.Remove(ctx, pkR); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
}

func Test_Phase2Commit_Success_CleansUp(t *testing.T) {
	ctx := context.Background()

	tx, err := newMockTransactionWithLogging(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatalf("newMockTransactionWithLogging error: %v", err)
	}
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin error: %v", err)
	}

	so := sop.StoreOptions{Name: "c2_success_store", SlotLength: 8, IsValueDataInNodeSegment: false}
	b3, err := NewBtree[PersonKey, Person](ctx, so, tx, Compare)
	if err != nil {
		t.Fatalf("NewBtree error: %v", err)
	}

	pk, p := newPerson("a", "b", "m", "e@x", "p")
	seedUpdateAndRemove(t, ctx, b3, pk, p)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	tx2, _ := newMockTransaction(t, sop.ForReading, -1)
	_ = tx2.Begin(ctx)
	b3r, _ := OpenBtree[PersonKey, Person](ctx, so.Name, tx2, Compare)
	if ok, err := b3r.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find after commit failed: ok=%v err=%v", ok, err)
	}
	if _, err := b3r.GetCurrentValue(ctx); err != nil {
		t.Fatalf("GetCurrentValue after commit: %v", err)
	}
	_ = tx2.Commit(ctx)
}

func Test_Phase2Commit_Error_UpdateNoLocks_RollsBack(t *testing.T) {
	ctx := context.Background()

	so := sop.StoreOptions{Name: "c2_error_store", SlotLength: 8, IsValueDataInNodeSegment: true}
	pkUpd, pUpd := newPerson("e1", "e2", "m", "e@x", "p")
	pkDel, pDel := newPerson("d1", "d2", "m", "d@x", "p")
	seedStoreWithOne(t, so.Name, so.IsValueDataInNodeSegment, pkUpd, pUpd)
	seedStoreWithOne(t, so.Name, so.IsValueDataInNodeSegment, pkDel, pDel)

	tx, err := newMockTransactionWithLogging(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatalf("newMockTransactionWithLogging error: %v", err)
	}
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin error: %v", err)
	}
	b3, err := NewBtree[PersonKey, Person](ctx, so, tx, Compare)
	if err != nil {
		t.Fatalf("NewBtree error: %v", err)
	}

	pUpd2 := pUpd
	pUpd2.Email = "changed@x"
	if _, err := b3.Update(ctx, pkUpd, pUpd2); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if _, err := b3.Remove(ctx, pkDel); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	t2 := tx.GetPhasedTransaction().(*Transaction)
	if err := t2.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit error: %v", err)
	}
	t2.registry = mocks.NewMockRegistry(true)
	if err := t2.Phase2Commit(ctx); err == nil {
		t.Fatalf("expected Phase2Commit error due to UpdateNoLocks failure")
	}
}

func Test_ReaderTransaction_Commit_Succeeds_NoConflict(t *testing.T) {
	ctx := context.Background()

	so := sop.StoreOptions{Name: "reader_ok_store", SlotLength: 8, IsValueDataInNodeSegment: true}
	pk, p := newPerson("r1", "r2", "m", "r@x", "p")
	seedStoreWithOne(t, so.Name, so.IsValueDataInNodeSegment, pk, p)

	tx, err := newMockTransaction(t, sop.ForReading, -1)
	if err != nil {
		t.Fatalf("newMockTransaction err: %v", err)
	}
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin err: %v", err)
	}
	b3, err := OpenBtree[PersonKey, Person](ctx, so.Name, tx, Compare)
	if err != nil {
		t.Fatalf("OpenBtree err: %v", err)
	}
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find reader failed: ok=%v err=%v", ok, err)
	}
	if _, err := b3.GetCurrentValue(ctx); err != nil {
		t.Fatalf("GetCurrentValue err: %v", err)
	}

	t2 := tx.GetPhasedTransaction().(*Transaction)
	if err := t2.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit(reader) err: %v", err)
	}
	if err := t2.Phase2Commit(ctx); err != nil {
		t.Fatalf("Phase2Commit(reader) err: %v", err)
	}
}
func Test_Rollback_Error_From_PreCommitTrackedValues_Delete(t *testing.T) {
	ctx := context.Background()
	fb := failingBlobStore{}
	tx := &Transaction{blobStore: fb, l2Cache: mocks.NewMockClient()}
	tx.phaseDone = -1
	tx.logger = newTransactionLogger(mocks.NewMockTransactionLog(), true)
	if err := tx.Begin(ctx); err != nil {
		t.Fatalf("Begin err: %v", err)
	}

	blobID := sop.NewUUID()
	tx.btreesBackend = []btreeBackend{{
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
			return &sop.BlobsPayload[sop.UUID]{BlobTable: "it", Blobs: []sop.UUID{blobID}}
		},
		getStoreInfo: func() *sop.StoreInfo {
			si := sop.StoreInfo{IsValueDataGloballyCached: true}
			return &si
		},
	}}
	tx.logger.committedState = addActivelyPersistedItem

	if err := tx.Rollback(ctx, fmt.Errorf("trigger")); err == nil {
		t.Fatalf("expected rollback to return error due to failing blob delete")
	}
}
func Test_OnIdle_Runs_Priority_And_Expired_Paths(t *testing.T) {
	ctx := context.Background()
	pl := &stubPriorityLog{
		batch:     []sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]]{{Key: sop.NewUUID(), Value: []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(sop.NewUUID())}}}}},
		removeErr: map[string]error{},
	}
	tl := newTransactionLogger(stubTLog{pl: pl}, true)
	reg := mocks.NewMockRegistry(false)
	_ = reg.Add(ctx, pl.batch[0].Value)
	tx := &Transaction{logger: tl, l2Cache: mocks.NewMockClient(), registry: reg}
	tx.btreesBackend = []btreeBackend{{}}

	prevPrio := lastPriorityOnIdleTime
	prevFound := priorityLogFound
	prevHour := hourBeingProcessed
	prevIdle := lastOnIdleRunTime
	lastPriorityOnIdleTime = 0
	priorityLogFound = false
	hourBeingProcessed = "some-hour"
	lastOnIdleRunTime = 0
	defer func() {
		lastPriorityOnIdleTime = prevPrio
		priorityLogFound = prevFound
		hourBeingProcessed = prevHour
		lastOnIdleRunTime = prevIdle
	}()

	tx.onIdle(ctx)

	if !priorityLogFound {
		t.Fatalf("expected priorityLogFound to be true after onIdle")
	}
	if hourBeingProcessed != "" {
		t.Fatalf("expected hourBeingProcessed reset, got %q", hourBeingProcessed)
	}
	// Ensure some progress occurred by checking priority batch was consumed or Remove attempted.
	if len(pl.removedHit) == 0 {
		t.Fatalf("expected priority Remove to be attempted at least once")
	}
}

func Test_HandleRegistrySectorLockTimeout_Succeeds(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(stubTLog{pl: &stubPriorityLog{}}, true)
	tx := &Transaction{logger: tl, l2Cache: mocks.NewMockClient(), registry: mocks.NewMockRegistry(false)}
	se := sop.Error{Code: sop.RestoreRegistryFileSectorFailure, UserData: &sop.LockKey{LockID: sop.NewUUID()}}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err != nil {
		t.Fatalf("handleRegistrySectorLockTimeout err: %v", err)
	}
}
func Test_StoreInfo_Delta_Computations(t *testing.T) {
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "delta", SlotLength: 4})
	si.Count = 10
	nr := &nodeRepositoryBackend{count: 7, storeInfo: si}
	t2 := &Transaction{btreesBackend: []btreeBackend{{getStoreInfo: func() *sop.StoreInfo { return si }, nodeRepository: nr}}}

	commits, modified := t2.getCommitStoresInfo()
	if !modified || len(commits) != 1 || commits[0].CountDelta != (si.Count-nr.count) {
		t.Fatalf("commit CountDelta mismatch: %+v", commits)
	}
	rollbacks := t2.getRollbackStoresInfo()
	if len(rollbacks) != 1 || rollbacks[0].CountDelta != (nr.count-si.Count) {
		t.Fatalf("rollback CountDelta mismatch: %+v", rollbacks)
	}
}

func Test_PopulateMru_Updates_Versions(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "mru", SlotLength: 4})
	id := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: id, Version: 0}
	handles := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{{LogicalID: id, Version: 5}}}}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}

	tx := &Transaction{l1Cache: cache.GetGlobalL1Cache(redis)}
	tx.updateVersionThenPopulateMru(ctx, handles, nodes)

	got := tx.l1Cache.GetNodeFromMRU(handles[0].IDs[0], &btree.Node[PersonKey, Person]{})
	if got == nil {
		t.Fatalf("expected node present in MRU")
	}
	if got.(*btree.Node[PersonKey, Person]).Version != 5 {
		t.Fatalf("expected version 5 in MRU, got %d", got.(*btree.Node[PersonKey, Person]).Version)
	}
}

func Test_ReaderTxn_Commit_Retry_Succeed(t *testing.T) {
	ctx := context.Background()
	id := sop.NewUUID()
	node := &btree.Node[PersonKey, Person]{ID: id, Version: 1}
	reg := mocks.NewMockRegistry(false)
	h := sop.NewHandle(id)
	h.Version = 2
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	tx := &Transaction{mode: sop.ForReading, registry: reg, l2Cache: mocks.NewMockClient(), maxTime: time.Second}
	nr := &nodeRepositoryBackend{transaction: tx, localCache: map[sop.UUID]cachedNode{id: {node: node, action: getAction}}}
	b := btreeBackend{
		nodeRepository:  nr,
		hasTrackedItems: func() bool { return true },
		refetchAndMerge: func(context.Context) error {
			if n, ok := nr.localCache[id]; ok {
				n.node.(*btree.Node[PersonKey, Person]).Version = 2
				nr.localCache[id] = n
			}
			return nil
		},
		getStoreInfo: func() *sop.StoreInfo { si := sop.NewStoreInfo(sop.StoreOptions{Name: "rt", SlotLength: 2}); return si },
	}
	tx.btreesBackend = []btreeBackend{b}

	if err := tx.commitForReaderTransaction(ctx); err != nil {
		t.Fatalf("commitForReaderTransaction err: %v", err)
	}
}

func Test_ReaderTransaction_Commit_RefetchError(t *testing.T) {
	ctx := context.Background()
	id := sop.NewUUID()
	reg := mocks.NewMockRegistry(false)
	h := sop.NewHandle(id)
	h.Version = 2
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})
	tx := &Transaction{mode: sop.ForReading, registry: reg, l2Cache: mocks.NewMockClient(), maxTime: time.Second}
	nr := &nodeRepositoryBackend{transaction: tx, localCache: map[sop.UUID]cachedNode{id: {node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}, action: getAction}}}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rt", SlotLength: 2})
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr, hasTrackedItems: func() bool { return true }, getStoreInfo: func() *sop.StoreInfo { return si }, refetchAndMerge: func(context.Context) error { return fmt.Errorf("x") }}}
	if err := tx.commitForReaderTransaction(ctx); err == nil {
		t.Fatalf("expected error")
	}
}

func Test_MergeNodesKeys_Unlocks_On_Empty(t *testing.T) {
	ctx := context.Background()
	lc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: lc}
	tx.nodesKeys = lc.CreateLockKeys([]string{sop.NewUUID().String()})
	tx.mergeNodesKeys(ctx, nil, nil)
	if tx.nodesKeys != nil {
		t.Fatalf("expected nodesKeys to be nil after mergeNodesKeys with empty inputs")
	}
}

func Test_RefetchAndMergeModifications_Error(t *testing.T) {
	ctx := context.Background()
	tx := &Transaction{}
	tx.btreesBackend = []btreeBackend{{refetchAndMerge: func(context.Context) error { return fmt.Errorf("boom") }}}
	if err := tx.refetchAndMergeModifications(ctx); err == nil {
		t.Fatalf("expected error from refetchAndMergeModifications")
	}
}
func Test_MergeNodesKeys_Retain_And_Release(t *testing.T) {
	ctx := context.Background()
	lc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: lc}
	existing := lc.CreateLockKeys([]string{sop.NewUUID().String(), sop.NewUUID().String(), sop.NewUUID().String()})
	for _, k := range existing {
		_ = lc.Set(ctx, k.Key, k.LockID.String(), time.Minute)
		k.IsLockOwner = true
	}
	tx.nodesKeys = existing

	updated := []sop.Tuple[*sop.StoreInfo, []interface{}]{}
	removed := []sop.Tuple[*sop.StoreInfo, []interface{}]{}
	keepUUID, _ := sop.ParseUUID(existing[1].Key[1:])
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 2})
	updated = append(updated, sop.Tuple[*sop.StoreInfo, []interface{}]{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: keepUUID}}})
	remUUID, _ := sop.ParseUUID(existing[0].Key[1:])
	removed = append(removed, sop.Tuple[*sop.StoreInfo, []interface{}]{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: remUUID}}})

	tx.mergeNodesKeys(ctx, updated, removed)
	if len(tx.nodesKeys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(tx.nodesKeys))
	}
	if !(strings.HasSuffix(tx.nodesKeys[0].Key, keepUUID.String()) || strings.HasSuffix(tx.nodesKeys[1].Key, keepUUID.String())) || !(strings.HasSuffix(tx.nodesKeys[0].Key, remUUID.String()) || strings.HasSuffix(tx.nodesKeys[1].Key, remUUID.String())) {
		t.Fatalf("keys mismatch: %+v", tx.nodesKeys)
	}
	if ok, _, _ := lc.Get(ctx, existing[2].Key); ok {
		t.Fatalf("expected third existing key to be unlocked (deleted)")
	}
}
func Test_GetObsoleteTrackedItemsValues_Aggregates(t *testing.T) {
	tx := &Transaction{}
	blobID1 := sop.NewUUID()
	blobID2 := sop.NewUUID()
	tx.btreesBackend = []btreeBackend{
		{getStoreInfo: func() *sop.StoreInfo {
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 2})
			si.IsValueDataGloballyCached = true
			return si
		},
			getObsoleteTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
				return &sop.BlobsPayload[sop.UUID]{BlobTable: "t1", Blobs: []sop.UUID{blobID1}}
			}},
		{getStoreInfo: func() *sop.StoreInfo {
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "s2", SlotLength: 2})
			si.IsValueDataGloballyCached = false
			return si
		},
			getObsoleteTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
				return &sop.BlobsPayload[sop.UUID]{BlobTable: "t2", Blobs: []sop.UUID{blobID2}}
			}},
	}
	out := tx.getObsoleteTrackedItemsValues()
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if !out[0].First || out[1].First {
		t.Fatalf("cache flags mismatch: %+v", out)
	}
}

func Test_TrackedItems_Check_Lock_Unlock_Errors_Table(t *testing.T) {
	ctx := context.Background()
	mk := func(has bool, ch, lk, ul error) *Transaction {
		tx := &Transaction{l2Cache: mocks.NewMockClient()}
		si := sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 2})
		tx.btreesBackend = []btreeBackend{{
			hasTrackedItems: func() bool { return has }, getStoreInfo: func() *sop.StoreInfo { return si },
			checkTrackedItems:  func(context.Context) error { return ch },
			lockTrackedItems:   func(context.Context, time.Duration) error { return lk },
			unlockTrackedItems: func(context.Context) error { return ul },
		}}
		return tx
	}
	cases := []struct {
		name          string
		hs            bool
		ch, lk, ul    error
		eCh, eLk, eUl bool
	}{
		{name: "ok", hs: true},
		{name: "check", hs: true, ch: fmt.Errorf("x"), eCh: true},
		{name: "lock", hs: true, lk: fmt.Errorf("x"), eLk: true},
		{name: "unlock", hs: true, ul: fmt.Errorf("x"), eUl: true},
		{name: "skip_hasTracked_false", hs: false, ch: fmt.Errorf("x"), lk: fmt.Errorf("x"), ul: fmt.Errorf("x"), eCh: false, eLk: false, eUl: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := mk(tc.hs, tc.ch, tc.lk, tc.ul)
			if err := tx.checkTrackedItems(ctx); (err != nil) != tc.eCh {
				t.Fatalf("check err? %v got %v", tc.eCh, err)
			}
			if err := tx.lockTrackedItems(ctx); (err != nil) != tc.eLk {
				t.Fatalf("lock err? %v got %v", tc.eLk, err)
			}
			if err := tx.unlockTrackedItems(ctx); (err != nil) != tc.eUl {
				t.Fatalf("unlock err? %v got %v", tc.eUl, err)
			}
		})
	}
}

// processExpiredTransactionLogs should no-op when logger returns no tid and clear hour state.
func Test_ProcessExpiredTransactionLogs_NoTid_NoOp(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(&tlRecorder{}, true)
	prev := hourBeingProcessed
	hourBeingProcessed = "test-hour"
	defer func() { hourBeingProcessed = prev }()
	if err := tl.processExpiredTransactionLogs(ctx, &Transaction{logger: tl}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hourBeingProcessed != "" {
		t.Fatalf("expected cleared hour")
	}
}

// commitForReaderTransaction edge cases using a table-driven test to cover: no tracked items,
// immediate timeout, and intact-on-first-check paths.
func Test_ReaderTransaction_Commit_EdgeCases_Table(t *testing.T) {
	ctx := context.Background()

	id := sop.NewUUID()
	mkTxn := func(hasTracked bool, timeout time.Duration, intact bool) *Transaction {
		tx := &Transaction{mode: sop.ForReading, l2Cache: mocks.NewMockClient(), maxTime: timeout}
		reg := mocks.NewMockRegistry(false)
		// Seed registry only when we want intact=true scenario
		if intact {
			h := sop.NewHandle(id)
			h.Version = 1
			_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})
		}
		tx.registry = reg

		// Node repo with one fetched node so classifyModifiedNodes picks it up when hasTracked=true
		nr := &nodeRepositoryBackend{transaction: tx, localCache: map[sop.UUID]cachedNode{}}
		if hasTracked {
			nr.localCache[id] = cachedNode{node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}, action: getAction}
		}
		si := sop.NewStoreInfo(sop.StoreOptions{Name: "rt", SlotLength: 2})
		tx.btreesBackend = []btreeBackend{{
			nodeRepository:  nr,
			hasTrackedItems: func() bool { return hasTracked },
			getStoreInfo:    func() *sop.StoreInfo { return si },
			refetchAndMerge: func(context.Context) error { return nil },
		}}
		return tx
	}

	cases := []struct {
		name      string
		tracked   bool
		timeout   time.Duration
		intactNow bool
		wantErr   bool
	}{
		{name: "no_tracked_items_noop", tracked: false, timeout: time.Second, intactNow: false, wantErr: false},
		{name: "timeout_immediate", tracked: true, timeout: 0, intactNow: false, wantErr: true},
		{name: "intact_on_first_check", tracked: true, timeout: time.Second, intactNow: true, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := mkTxn(tc.tracked, tc.timeout, tc.intactNow)
			err := tx.commitForReaderTransaction(ctx)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
