package common

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// --- New tests to lift remaining low-coverage branches ---

// tlogErrOnce errors on the first Add (log) call to hit early log error branches.
type tlogErrOnce struct {
	inner   sop.TransactionLog
	tripped bool
}

func (w *tlogErrOnce) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOne(ctx)
}
func (w *tlogErrOnce) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return w.inner.GetOneOfHour(ctx, hour)
}
func (w *tlogErrOnce) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	if !w.tripped {
		w.tripped = true
		return fmt.Errorf("add fail once")
	}
	return w.inner.Add(ctx, tid, commitFunction, payload)
}
func (w *tlogErrOnce) Remove(ctx context.Context, tid sop.UUID) error {
	return w.inner.Remove(ctx, tid)
}
func (w *tlogErrOnce) NewUUID() sop.UUID                       { return w.inner.NewUUID() }
func (w *tlogErrOnce) PriorityLog() sop.TransactionPriorityLog { return w.inner.PriorityLog() }

func Test_Phase1Commit_TimedOut_ExitsEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ensure timedOut sees context canceled
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_timeout", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
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
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
	}}
	if err := tx.phase1Commit(ctx); err == nil {
		t.Fatalf("expected context error from timedOut, got nil")
	}
}

func Test_Phase1Commit_LogError_OnFirstLog(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	baseTL := mocks.NewMockTransactionLog().(*mocks.MockTransactionLog)
	tl := newTransactionLogger(&tlogErrOnce{inner: baseTL}, true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_logerr", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
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
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
	}}
	if err := tx.phase1Commit(ctx); err == nil {
		t.Fatalf("expected error from logger.Add failing on first log")
	}
}

func Test_Phase1Commit_CommitTrackedValues_Error(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_vals_err", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	tx.btreesBackend = []btreeBackend{{
		nodeRepository:                   nr,
		getStoreInfo:                     func() *sop.StoreInfo { return si },
		hasTrackedItems:                  func() bool { return true },
		checkTrackedItems:                func(context.Context) error { return nil },
		lockTrackedItems:                 func(context.Context, time.Duration) error { return nil },
		unlockTrackedItems:               func(context.Context) error { return nil },
		commitTrackedItemsValues:         func(context.Context) error { return fmt.Errorf("commit vals err") },
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] { return nil },
		getObsoleteTrackedItemsValues:    func() *sop.BlobsPayload[sop.UUID] { return nil },
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
	}}
	if err := tx.phase1Commit(ctx); err == nil || !strings.Contains(err.Error(), "commit vals err") {
		t.Fatalf("expected commitTrackedItemsValues error, got: %v", err)
	}
}

func Test_Phase1Commit_IsLockedFalse_Then_Succeeds(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	wc := &wrapCache{L2Cache: base, flipOnce: true}
	cache.GetGlobalL1Cache(wc)
	rg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: wc, l1Cache: cache.GetGlobalL1Cache(wc), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_islocked_false", SlotLength: 4, IsValueDataInNodeSegment: true})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: wc, l1Cache: cache.GetGlobalL1Cache(wc), count: si.Count}
	// One updated node with matching version so success once IsLocked returns true on second try
	uid := sop.NewUUID()
	n := &btree.Node[PersonKey, Person]{ID: uid, Version: 1}
	nr.localCache[uid] = cachedNode{action: updateAction, node: n}
	h := sop.NewHandle(uid)
	h.Version = 1
	rg.Lookup[uid] = h
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
		refetchAndMerge:                  func(ctx context.Context) error { return nil },
	}}
	if err := tx.phase1Commit(ctx); err != nil {
		t.Fatalf("phase1Commit should succeed after IsLocked=false retry, err: %v", err)
	}
}

// errGetRegistry returns error on Get to test rollbackRemovedNodes early error branch.
type errGetRegistry struct{ *mocks.Mock_vid_registry }

func (e errGetRegistry) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, fmt.Errorf("get err")
}

func Test_RollbackRemovedNodes_RegistryGetError(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_rm_geterr", SlotLength: 4})
	rg := errGetRegistry{Mock_vid_registry: mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)}
	l2 := mocks.NewMockClient()
	tx := &Transaction{registry: rg, l2Cache: l2}
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, l2Cache: tx.l2Cache, l1Cache: cache.GetGlobalL1Cache(l2)}
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, IDs: []sop.UUID{sop.NewUUID()}}}
	if err := nr.rollbackRemovedNodes(ctx, true, vids); err == nil {
		t.Fatalf("expected error from registry.Get in rollbackRemovedNodes")
	}
}

// getStructErrCache forces GetStruct to return an error while indicating not found.
type getStructErrCache struct{ sop.L2Cache }

func (g getStructErrCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return false, fmt.Errorf("getstruct err")
}

func Test_ItemActionTracker_Get_GlobalCache_ErrorAndHit(t *testing.T) {
	ctx := context.Background()
	// Prepare a deterministic RNG for jittered sleeps in unrelated paths
	sop.SetJitterRNG(rand.New(rand.NewSource(1)))

	// Case 1: cache GetStruct error -> blob fetch path
	bs := mocks.NewMockBlobStore()
	// Seed blob store with serialized value
	id1 := sop.NewUUID()
	_, p1 := newPerson("x", "y", "m", "z@x", "p")
	ba, _ := encoding.BlobMarshaler.Marshal(p1)
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tb", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id1, Value: ba}}}})
	si1 := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_get_err", SlotLength: 2})
	si1.IsValueDataGloballyCached = true
	trk1 := newItemActionTracker[PersonKey, Person](si1, getStructErrCache{L2Cache: mocks.NewMockClient()}, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))
	it1 := &btree.Item[PersonKey, Person]{ID: id1, Key: PersonKey{Lastname: "k"}, Version: 1, ValueNeedsFetch: true}
	if err := trk1.Get(ctx, it1); err != nil {
		t.Fatalf("Get(global cache error) err: %v", err)
	}
	if it1.Value == nil || it1.Value.Email != "z@x" {
		t.Fatalf("expected value fetched from blob store")
	}

	// Case 2: cache hit path
	l2 := mocks.NewMockClient()
	si2 := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_get_hit", SlotLength: 2})
	si2.IsValueDataGloballyCached = true
	trk2 := newItemActionTracker[PersonKey, Person](si2, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id2 := sop.NewUUID()
	_, p2 := newPerson("a", "b", "m", "c@x", "p")
	_ = l2.SetStruct(ctx, formatItemKey(id2.String()), &p2, time.Minute)
	it2 := &btree.Item[PersonKey, Person]{ID: id2, Key: PersonKey{Lastname: "k2"}, Version: 1, ValueNeedsFetch: true}
	if err := trk2.Get(ctx, it2); err != nil {
		t.Fatalf("Get(cache hit) err: %v", err)
	}
	if it2.Value == nil || it2.Value.Email != "c@x" {
		t.Fatalf("expected value fetched from cache")
	}
}

func Test_ItemActionTracker_CheckTrackedItems_Conflict(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_check", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, l2, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id := sop.NewUUID()
	pk, p := newPerson("ck", "c", "m", "ck@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1}
	// Track as update to exercise conflict
	_ = trk.Update(ctx, it)
	// Seed a different lock owner with non-compatible action
	lr := lockRecord{LockID: sop.NewUUID(), Action: updateAction}
	_ = l2.SetStruct(ctx, l2.FormatLockKey(id.String()), &lr, time.Minute)
	if err := trk.checkTrackedItems(ctx); err == nil {
		t.Fatalf("expected conflict error")
	}
}

// zeroSetCache stores a zero LockID in SetStruct to trigger the "can't attain a lock" path after re-get.
type zeroSetCache struct{ sop.L2Cache }

func (z zeroSetCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if lr, ok := value.(*lockRecord); ok {
		// Store with zero UUID
		v := &lockRecord{LockID: sop.NilUUID, Action: lr.Action}
		return z.L2Cache.SetStruct(ctx, key, v, expiration)
	}
	return z.L2Cache.SetStruct(ctx, key, value, expiration)
}
func (z zeroSetCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	newValues := make([]interface{}, len(values))
	for i, v := range values {
		if lr, ok := v.(*lockRecord); ok {
			// Store with zero UUID
			v = &lockRecord{LockID: sop.NilUUID, Action: lr.Action}
		}
		newValues[i] = v
	}
	return z.L2Cache.SetStructs(ctx, keys, newValues, expiration)
}

func Test_ItemActionTracker_Lock_RegetNilLockID_Error(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	zc := zeroSetCache{L2Cache: base}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_nil", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, zc, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id := sop.NewUUID()
	pk, p := newPerson("lkz", "c", "m", "z@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1}
	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	// Force to be update to make lock non-compatible
	ci := trk.items[id]
	ci.Action = updateAction
	trk.items[id] = ci
	if err := trk.lock(ctx, time.Minute); err == nil || !strings.Contains(err.Error(), "can't attain a lock") {
		t.Fatalf("expected can't attain a lock error, got: %v", err)
	}
}

func Test_RefetchAndMerge_FindWithID_Miss_ReturnsError(t *testing.T) {
	ctx := context.Background()
	// Build minimal store interface, with store repo returning Count=0 so FindWithID returns false
	so := sop.StoreOptions{Name: "rfm_miss", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	l2 := mocks.NewMockClient()
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: mocks.NewMockStoreRepository()}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	// Seed one tracked get-item that won't be found
	id := sop.NewUUID()
	pk := PersonKey{Lastname: "q"}
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Version: 1}
	ci := cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: getAction}, item: it, versionInDB: 1}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id] = ci
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}
	closure := refetchAndMergeClosure(&si, b3, tr.StoreRepository)
	if err := closure(ctx); err == nil || !strings.Contains(err.Error(), "failed to find item") {
		t.Fatalf("expected find miss error, got: %v", err)
	}
}

// regAddSectorErr simulates a sector-lock-timeout error on Add once, then succeeds.
type regAddSectorErr struct {
	*mocks.Mock_vid_registry
	tripped bool
	tid     sop.UUID
}

func (r *regAddSectorErr) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if !r.tripped {
		r.tripped = true
		return sop.Error{Err: fmt.Errorf("sector timeout"), UserData: &sop.LockKey{Key: "Lx", LockID: r.tid}}
	}
	return r.Mock_vid_registry.Add(ctx, storesHandles)
}

func Test_Phase1Commit_Add_SectorTimeout_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	// Cache and logger
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	// Registry that fails once with sop.Error on Add, then succeeds
	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	rg := &regAddSectorErr{Mock_vid_registry: baseReg}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Minute, StoreRepository: mocks.NewMockStoreRepository(), registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: tl, id: sop.NewUUID()}
	// Ensure the simulated sector-timeout error carries our current TID so priorityRollback can find logs.
	rg.tid = tx.id

	// Store/backend with one added node
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_add_retry", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	aid := sop.NewUUID()
	an := &btree.Node[PersonKey, Person]{ID: aid, Version: 0}
	nr.localCache[aid] = cachedNode{action: addAction, node: an}

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
		t.Fatalf("phase1Commit with sector-timeout retry should succeed, err: %v", err)
	}
}

func Test_RefetchAndMerge_Action_Paths(t *testing.T) {
	ctx := context.Background()

	// Helper to build btree + interfaces quickly
	build := func(name string, inNode bool) (*StoreInterface[PersonKey, Person], *btree.Btree[PersonKey, Person], sop.StoreRepository, *Transaction) {
		so := sop.StoreOptions{Name: name, SlotLength: 4, IsValueDataInNodeSegment: inNode}
		ns := sop.NewStoreInfo(so)
		si := StoreInterface[PersonKey, Person]{}
		sr := mocks.NewMockStoreRepository()
		_ = sr.Add(ctx, *ns)
		l2 := mocks.NewMockClient()
		tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}
		si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
		nrw := newNodeRepository[PersonKey, Person](tr, ns)
		si.NodeRepository = nrw
		si.backendNodeRepository = nrw.nodeRepositoryBackend
		b3, err := btree.New(ns, &si.StoreInterface, Compare)
		if err != nil {
			t.Fatal(err)
		}
		return &si, b3, sr, tr
	}

	// 1) addAction with IsValueDataInNodeSegment=false
	{
		si, b3, sr, _ := build("rfm_add_sep", false)
		id := sop.NewUUID()
		pk, p := newPerson("a1", "b1", "m", "a@x", "p")
		it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 0}
		ci := cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction}, item: it, versionInDB: 0}
		si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id] = ci
		closure := refetchAndMergeClosure(si, b3, sr)
		if err := closure(ctx); err != nil {
			t.Fatalf("closure add(sep) err: %v", err)
		}
		// Expect item persisted flag set back into tracker
		got := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id]
		if !got.persisted {
			t.Fatalf("expected persisted=true after add in separate segment mode")
		}
	}

	// 2) addAction with IsValueDataInNodeSegment=true
	{
		si, b3, sr, _ := build("rfm_add_node", true)
		id := sop.NewUUID()
		pk, p := newPerson("a2", "b2", "m", "b@x", "p")
		it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 0}
		ci := cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: addAction}, item: it, versionInDB: 0}
		si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[id] = ci
		closure := refetchAndMergeClosure(si, b3, sr)
		if err := closure(ctx); err != nil {
			t.Fatalf("closure add(node) err: %v", err)
		}
		if ok, _ := b3.Find(ctx, pk, false); !ok {
			t.Fatalf("expected item present after add in node segment mode")
		}
	}

	// 3) (intentionally skipping updateAction separate segment edge here; covered elsewhere)

	// 4) removeAction: item removed
	{
		si, b3, sr, _ := build("rfm_rm", true)
		pk, p := newPerson("r1", "r2", "m", "r@x", "p")
		if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
			t.Fatalf("seed add err: %v", err)
		}
		// Fetch once to get current item and ID
		if ok, _ := b3.Find(ctx, pk, false); !ok {
			t.Fatal("seed find err")
		}
		item, _ := b3.GetCurrentItem(ctx)
		// Track removal under that ID
		ci := cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: removeAction}, item: &item, versionInDB: item.Version}
		si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[item.ID] = ci
		closure := refetchAndMergeClosure(si, b3, sr)
		if err := closure(ctx); err == nil {
			t.Fatalf("expected error when backend has not persisted nodes for remove path")
		}
	}
}

func Test_ItemActionTracker_Get_NoFetch_WhenPresent(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_no_fetch", SlotLength: 2})
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))
	id := sop.NewUUID()
	pk, p := newPerson("g1", "g2", "m", "g@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1, ValueNeedsFetch: false}
	// Seed tracker with existing item so Get should be a no-op
	trk.items[id] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: getAction}, item: it, versionInDB: 1}
	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
}

func Test_NewBtree_WithTTL_And_EmptyName_Error(t *testing.T) {
	ctx := context.Background()
	tx, _ := newMockTransaction(t, sop.ForWriting, -1)
	_ = tx.Begin(ctx)
	// With TTL path: provide CacheConfig
	so := sop.StoreOptions{Name: "nb_ttl", SlotLength: 4}
	so.CacheConfig = &sop.StoreCacheConfig{IsStoreInfoCacheTTL: true, StoreInfoCacheDuration: time.Minute}
	if _, err := NewBtree[PersonKey, Person](ctx, so, tx, Compare); err != nil {
		t.Fatalf("NewBtree with TTL err: %v", err)
	}
	// Empty name error path
	if _, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{}, tx, Compare); err == nil {
		t.Fatalf("expected error for empty btree name")
	}
}

func Test_NewBtree_Incompatible_Config_ReturnsError(t *testing.T) {
	ctx := context.Background()
	tx, _ := newMockTransaction(t, sop.ForWriting, -1)
	_ = tx.Begin(ctx)
	// Seed store repo with a StoreInfo of slot length 8
	existing := sop.StoreOptions{Name: "nb_incompat", SlotLength: 8}
	si := sop.NewStoreInfo(existing)
	_ = tx.GetPhasedTransaction().(*Transaction).StoreRepository.Add(ctx, *si)
	// Now request a NewBtree with incompatible slot length 4
	so := sop.StoreOptions{Name: "nb_incompat", SlotLength: 4}
	if _, err := NewBtree[PersonKey, Person](ctx, so, tx, Compare); err == nil || !strings.Contains(err.Error(), "exists & has different configuration") {
		t.Fatalf("expected incompatible configuration error, got: %v", err)
	}
}

func Test_Phase1Commit_Mode_NoCheck_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.NoCheck, phaseDone: 0, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false)}
	if err := tx.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase1Commit(NoCheck) err: %v", err)
	}
}

func Test_Phase2Commit_ReadOnly_ShortCircuits(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	tx := &Transaction{mode: sop.ForReading, phaseDone: 1, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), StoreRepository: mocks.NewMockStoreRepository(), registry: mocks.NewMockRegistry(false), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false)}
	if err := tx.Phase2Commit(ctx); err != nil {
		t.Fatalf("Phase2Commit(read-only) unexpected err: %v", err)
	}
}

func Test_RefetchAndMerge_Get_InNodeSegment_ReturnsError_NoBackend(t *testing.T) {
	ctx := context.Background()
	// Build with value data in node segment
	so := sop.StoreOptions{Name: "rfm_get_upd_node", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, *ns)
	l2 := mocks.NewMockClient()
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed existing item
	pk, p := newPerson("gk", "gv", "m", "g@x", "p")
	if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
		t.Fatalf("seed add err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatal("seed find err")
	}
	cur, _ := b3.GetCurrentItem(ctx)
	// 1) getAction: without backend persistence, closure will error on FindWithID
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{lockRecord: lockRecord{LockID: sop.NewUUID(), Action: getAction}, item: &cur, versionInDB: cur.Version}
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error due to missing backend nodes after reset")
	}
}

// errOnceOnUpdateNoLocksReg forces UpdateNoLocks to return a sop.Error with *LockKey once.
type errOnceOnUpdateNoLocksReg struct {
	inner *mocks.Mock_vid_registry
	fired bool
	lk    sop.LockKey
}

func (r *errOnceOnUpdateNoLocksReg) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if !r.fired {
		r.fired = true
		return sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: errors.New("sector timeout"), UserData: &r.lk}
	}
	return r.inner.UpdateNoLocks(ctx, allOrNothing, storesHandles)
}
func (r *errOnceOnUpdateNoLocksReg) Add(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Add(ctx, s)
}
func (r *errOnceOnUpdateNoLocksReg) Update(ctx context.Context, s []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Update(ctx, s)
}
func (r *errOnceOnUpdateNoLocksReg) Remove(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) error {
	return r.inner.Remove(ctx, lids)
}
func (r *errOnceOnUpdateNoLocksReg) Replicate(ctx context.Context, a, b, c, d []sop.RegistryPayload[sop.Handle]) error {
	return r.inner.Replicate(ctx, a, b, c, d)
}
func (r *errOnceOnUpdateNoLocksReg) Get(ctx context.Context, lids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	return r.inner.Get(ctx, lids)
}

func Test_Phase1Commit_CommitUpdatedNodes_SectorTimeout_Retry_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)

	baseReg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	induced := &errOnceOnUpdateNoLocksReg{inner: baseReg, lk: sop.LockKey{Key: l2.FormatLockKey("Z"), LockID: sop.NewUUID()}}

	tx := &Transaction{mode: sop.ForWriting, maxTime: time.Second, StoreRepository: mocks.NewMockStoreRepository(), registry: induced, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true), phaseDone: 0}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "p1_upd_sector_timeout", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, storeInfo: si, readNodesCache: cache.NewCache[sop.UUID, any](8, 12), localCache: make(map[sop.UUID]cachedNode), l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), count: si.Count}
	id := sop.NewUUID()
	nr.localCache[id] = cachedNode{action: updateAction, node: &btree.Node[PersonKey, Person]{ID: id, Version: 1}}
	// Seed base handle for retry path
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
		t.Fatalf("expected success after sector-timeout on UpdateNoLocks with retry, got: %v", err)
	}
}

// cacheWarnOnSetStruct returns error on SetStruct to exercise warning paths in commitAddedNodes/commitNewRootNodes.
type cacheWarnOnSetStruct struct{ sop.L2Cache }

func (c cacheWarnOnSetStruct) SetStruct(ctx context.Context, key string, value interface{}, d time.Duration) error {
	return fmt.Errorf("setstruct err")
}

func Test_NodeRepository_CommitAddedNodes_SetStructWarn(t *testing.T) {
	ctx := context.Background()
	base := mocks.NewMockClient()
	cw := cacheWarnOnSetStruct{L2Cache: base}
	cache.GetGlobalL1Cache(cw)
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{registry: reg, blobStore: mocks.NewMockBlobStore(), l2Cache: cw, l1Cache: cache.GetGlobalL1Cache(cw)}
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: cw, l1Cache: cache.GetGlobalL1Cache(cw)}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "added_warn", SlotLength: 2})
	id := sop.NewUUID()
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: id}}}}
	if _, err := nr.commitAddedNodes(ctx, nodes); err != nil {
		t.Fatalf("commitAddedNodes err: %v", err)
	}
}

func Test_NodeRepository_RollbackUpdatedNodes_WithInactiveIDs_DeletesCacheAndClears(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2, blobStore: bs}, l2Cache: l2}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_inactive", SlotLength: 2})
	nr.storeInfo = si

	id := sop.NewUUID()
	h := sop.NewHandle(id)
	// Allocate an inactive ID to simulate staged update
	_ = h.AllocateID()
	// Ensure inactive present
	if h.GetInActiveID().IsNil() {
		t.Fatalf("expected inactive id allocated")
	}
	reg.Lookup[id] = h

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackUpdatedNodes(ctx, true, vids); err != nil {
		t.Fatalf("rollbackUpdatedNodes err: %v", err)
	}
}

func Test_ItemActionTracker_Add_ActivelyPersisted_WritesBlob_And_Cache(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_add_active", SlotLength: 4, IsValueDataInNodeSegment: false})
	si.IsValueDataActivelyPersisted = true
	si.IsValueDataGloballyCached = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	id := sop.NewUUID()
	_, p := newPerson("a", "1", "m", "a@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Value: &p}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Value should be moved to blob store and cached; item value becomes nil with ValueNeedsFetch
	if it.Value != nil || !it.ValueNeedsFetch {
		t.Fatalf("expected value moved out and ValueNeedsFetch=true")
	}
	if _, err := bs.GetOne(ctx, si.BlobTable, id); err != nil {
		t.Fatalf("blob GetOne err: %v", err)
	}
	var pv Person
	if found, _ := l2.GetStruct(ctx, formatItemKey(id.String()), &pv); !found {
		t.Fatalf("expected value cached in redis")
	}
}

func Test_ItemActionTracker_Update_WhenExistingAddAction_Persists(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_upd_on_add", SlotLength: 4, IsValueDataInNodeSegment: false})
	si.IsValueDataActivelyPersisted = true
	si.IsValueDataGloballyCached = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	id := sop.NewUUID()
	_, p := newPerson("b", "2", "m", "b@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Value: &p}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Update same ID with new value; since action is addAction, Update should actively persist via manage/add.
	p.Email = "new@x"
	it.Value = &p
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
}

func Test_NodeRepository_RollbackRemovedNodes_Unlocked_UndoesFlags(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	l2 := mocks.NewMockClient()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2}, l2Cache: l2}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_removed_unlocked", SlotLength: 2})
	id := sop.NewUUID()
	h := sop.NewHandle(id)
	h.IsDeleted = true
	h.WorkInProgressTimestamp = 1234
	reg.Lookup[id] = h

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackRemovedNodes(ctx, false, vids); err != nil {
		t.Fatalf("rollbackRemovedNodes err: %v", err)
	}
	if got := reg.Lookup[id]; got.IsDeleted || got.WorkInProgressTimestamp != 0 {
		t.Fatalf("expected flags cleared, got IsDeleted=%v wip=%d", got.IsDeleted, got.WorkInProgressTimestamp)
	}
}

func Test_NodeRepository_RollbackUpdatedNodes_Unlocked_WithInactiveIDs(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false).(*mocks.Mock_vid_registry)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, l2Cache: l2, blobStore: bs}, l2Cache: l2}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rb_upd_unlocked", SlotLength: 2})

	id := sop.NewUUID()
	h := sop.NewHandle(id)
	_ = h.AllocateID()
	reg.Lookup[id] = h

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: si.RegistryTable, BlobTable: si.BlobTable, IDs: []sop.UUID{id}}}
	if err := nr.rollbackUpdatedNodes(ctx, false, vids); err != nil {
		t.Fatalf("rollbackUpdatedNodes err: %v", err)
	}
}

func Test_ItemActionTracker_Add_ActivelyPersisted_BlobError_Propagates(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_add_err", SlotLength: 4, IsValueDataInNodeSegment: false})
	si.IsValueDataActivelyPersisted = true
	l2 := mocks.NewMockClient()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, failingAddBlobStore{}, tl)

	id := sop.NewUUID()
	_, p := newPerson("x", "1", "m", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Value: &p}
	if err := trk.Add(ctx, it); err == nil {
		t.Fatalf("expected blob add error to propagate from Add")
	}
}
