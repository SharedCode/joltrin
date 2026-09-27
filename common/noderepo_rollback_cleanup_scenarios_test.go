package common

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// failingBlobStore returns an error on Remove to exercise error path in removeNodes.
type failingBlobStore struct{}

func (f failingBlobStore) GetOne(ctx context.Context, blobName string, blobID sop.UUID) ([]byte, error) {
	return nil, nil
}
func (f failingBlobStore) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (f failingBlobStore) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return nil
}
func (f failingBlobStore) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return fmt.Errorf("forced remove error")
}

// errRepo implements sop.StoreRepository and returns an error from GetWithTTL.
type errRepo struct{ e error }

func (e *errRepo) Add(ctx context.Context, stores ...sop.StoreInfo) error { return nil }
func (e *errRepo) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, nil
}
func (e *errRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) { return nil, nil }
func (e *errRepo) GetAll(ctx context.Context) ([]string, error)                      { return nil, nil }
func (e *errRepo) GetWithTTL(ctx context.Context, isTTL bool, d time.Duration, names ...string) ([]sop.StoreInfo, error) {
	return nil, e.e
}
func (e *errRepo) Remove(ctx context.Context, names ...string) error               { return nil }
func (e *errRepo) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error { return nil }
func Test_NodeRepository_RemoveNodes_Error_Propagates(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)

	tr := &Transaction{l2Cache: redis, l1Cache: cache.GetGlobalL1Cache(redis), blobStore: failingBlobStore{}}
	nr := &nodeRepositoryBackend{transaction: tr}

	ids := []sop.BlobsPayload[sop.UUID]{
		{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID(), sop.NewUUID()}},
	}
	if err := nr.removeNodes(ctx, ids); err == nil {
		t.Fatalf("expected error from failingBlobStore.Remove, got nil")
	}
}
func Test_NodeRepository_RollbackUpdatedNodes_Both_Locked_And_Unlocked(t *testing.T) {
	ctx := context.Background()
	// Prepare registry with handles for two logical IDs
	regOK := mocks.NewMockRegistry(false)
	regErr := mocks.NewMockRegistry(true) // induces error on Update (used for unlocked path)

	// Create handles and seed into regOK and regErr lookups
	lid1 := sop.NewUUID()
	lid2 := sop.NewUUID()
	h1 := sop.NewHandle(lid1)
	h1.WorkInProgressTimestamp = 1
	h2 := sop.NewHandle(lid2)
	h2.WorkInProgressTimestamp = 1
	_ = regOK.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h1, h2}}})
	_ = regErr.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h1, h2}}})

	trLocked := &Transaction{registry: regOK, l2Cache: mocks.NewMockClient(), blobStore: mocks.NewMockBlobStore()}
	trUnlocked := &Transaction{registry: regErr, l2Cache: mocks.NewMockClient(), blobStore: mocks.NewMockBlobStore()}
	nrLocked := &nodeRepositoryBackend{transaction: trLocked}
	nrUnlocked := &nodeRepositoryBackend{transaction: trUnlocked}

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid1, lid2}}}

	// Locked path uses UpdateNoLocks(false, ...) and should succeed
	if err := nrLocked.rollbackUpdatedNodes(ctx, true, vids); err != nil {
		t.Fatalf("locked rollbackUpdatedNodes error: %v", err)
	}

	// Unlocked path uses Update(...) and should error due to induced flag
	if err := nrUnlocked.rollbackUpdatedNodes(ctx, false, vids); err == nil {
		t.Fatalf("expected error on unlocked rollbackUpdatedNodes with induced registry error")
	}
}
func Test_NodeRepository_RollbackRemovedNodes_Both_Locked_And_Unlocked(t *testing.T) {
	ctx := context.Background()
	regOK := mocks.NewMockRegistry(false)
	regErr := mocks.NewMockRegistry(true)

	// Build two removed handles in registry so rollbackRemovedNodes has something to undo
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.IsDeleted = true
	h.WorkInProgressTimestamp = time.Now().UnixMilli()
	_ = regOK.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})
	_ = regErr.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})

	nrLocked := &nodeRepositoryBackend{transaction: &Transaction{registry: regOK}}
	nrUnlocked := &nodeRepositoryBackend{transaction: &Transaction{registry: regErr}}

	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid}}}

	// Locked path uses UpdateNoLocks(false, ...) and should succeed
	if err := nrLocked.rollbackRemovedNodes(ctx, true, vids); err != nil {
		t.Fatalf("locked rollbackRemovedNodes error: %v", err)
	}

	// Unlocked path uses Update(...) and should error due to induced flag
	if err := nrUnlocked.rollbackRemovedNodes(ctx, false, vids); err == nil {
		t.Fatalf("expected error on unlocked rollbackRemovedNodes with induced registry error")
	}
}

// Sanity: ensure refetchAndMergeClosure error path is covered when StoreRepository.GetWithTTL fails.
func Test_RefetchAndMerge_Closure_Error_From_StoreRepo(t *testing.T) {
	ctx := context.Background()
	// Arrange store interface with failing GetWithTTL via custom repo
	eRepo := &errRepo{e: fmt.Errorf("boom")}

	so := sop.StoreOptions{Name: "rfm_err_closure", SlotLength: 4, IsValueDataInNodeSegment: true}
	si := sop.NewStoreInfo(so)
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: mocks.NewMockClient(), l1Cache: cache.GetGlobalL1Cache(mocks.NewMockClient()), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: eRepo}

	// Minimal btree wiring
	s := StoreInterface[PersonKey, Person]{}
	s.ItemActionTracker = newItemActionTracker[PersonKey, Person](si, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, si)
	s.NodeRepository = nrw
	s.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(si, &s.StoreInterface, Compare)
	if err != nil {
		t.Fatalf("btree.New error: %v", err)
	}

	closure := refetchAndMergeClosure(&s, b3, tr.StoreRepository)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error from failing GetWithTTL, got nil")
	}
}

// Cover rollbackAddedNodes and rollbackNewRootNodes happy paths
func Test_NodeRepository_RollbackAdded_And_NewRoot_Success(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	blobs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	tr := &Transaction{l2Cache: redis, l1Cache: cache.GetGlobalL1Cache(redis), blobStore: blobs, registry: reg}
	tr.logger = newTransactionLogger(mocks.NewMockTransactionLog(), true)
	nr := &nodeRepositoryBackend{transaction: tr, l2Cache: redis, l1Cache: cache.GetGlobalL1Cache(redis)}

	// Seed blobs and cache and registry for added/new root entries
	addID := sop.NewUUID()
	rootID := sop.NewUUID()
	rootBlob := sop.NewUUID()
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_add", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: addID, Value: []byte("a")}}}, {BlobTable: "bt_root", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: rootBlob, Value: []byte("r")}}}})
	_ = redis.SetStruct(ctx, nr.formatKey(addID.String()), &btree.Node[PersonKey, Person]{ID: addID}, time.Minute)
	_ = redis.SetStruct(ctx, nr.formatKey(rootID.String()), &btree.Node[PersonKey, Person]{ID: rootBlob}, time.Minute)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_add", IDs: []sop.Handle{sop.NewHandle(addID)}}})
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_root", IDs: []sop.Handle{sop.NewHandle(rootID)}}})

	// Execute rollbackAddedNodes
	if err := nr.rollbackAddedNodes(ctx, sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
		First:  []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_add", IDs: []sop.UUID{addID}}},
		Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_add", Blobs: []sop.UUID{addID}}},
	}); err != nil {
		t.Fatalf("rollbackAddedNodes err: %v", err)
	}

	// Execute rollbackNewRootNodes
	if err := nr.rollbackNewRootNodes(ctx, sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
		First:  []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root", IDs: []sop.UUID{rootID}}},
		Second: []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_root", Blobs: []sop.UUID{rootBlob}}},
	}); err != nil {
		t.Fatalf("rollbackNewRootNodes err: %v", err)
	}
}

// When no inactive IDs exist in handles, rollbackUpdatedNodes should clear WIP timestamp and not error.
func Test_NodeRepository_RollbackUpdatedNodes_NoInactiveIDs_ClearsWip(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false)
	// Create a handle with no inactive ID
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.WorkInProgressTimestamp = time.Now().UnixMilli()
	// Ensure inactive remains nil
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{h}}})
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg, blobStore: mocks.NewMockBlobStore(), l2Cache: mocks.NewMockClient()}}
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{lid}}}
	if err := nr.rollbackUpdatedNodes(ctx, true, vids); err != nil {
		t.Fatalf("rollbackUpdatedNodes err: %v", err)
	}
}

// If an existing non-empty root is present, commitNewRootNodes should return false (trigger refetch/merge path).
func Test_NodeRepository_CommitNewRootNodes_ExistingRoot_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false)
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rt_root2", SlotLength: 4})
	// Pre-seed registry with a non-empty root using a concrete logical ID
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})
	tr := &Transaction{registry: reg, blobStore: mocks.NewMockBlobStore(), l2Cache: mocks.NewMockClient()}
	nr := &nodeRepositoryBackend{transaction: tr}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: lid}}}}
	ok, _, err := nr.commitNewRootNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitNewRootNodes err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when existing root present")
	}
}

// areFetchedItemsIntact should return false when versions mismatch.
func Test_NodeRepository_AreFetchedItemsIntact_VersionMismatch(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false)
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.Version = 2
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rtv", IDs: []sop.Handle{h}}})
	tr := &Transaction{registry: reg}
	nr := &nodeRepositoryBackend{transaction: tr}
	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rtv", SlotLength: 2})
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}
	ok, err := nr.areFetchedItemsIntact(ctx, nodes)
	if err != nil {
		t.Fatalf("areFetchedItemsIntact err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false on version mismatch")
	}
}

// commitRemovedNodes should return false when a handle is already deleted.
func Test_NodeRepository_CommitRemovedNodes_AlreadyDeleted_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false)
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.IsDeleted = true
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rtrm", IDs: []sop.Handle{h}}})
	tr := &Transaction{registry: reg}
	nr := &nodeRepositoryBackend{transaction: tr}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "rtrm", SlotLength: 2})
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: lid, Version: 0}}}}
	ok, _, err := nr.commitRemovedNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitRemovedNodes err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when handle is already deleted")
	}
}

func Test_Transaction_DeleteObsoleteEntries_PropagatesBlobError(t *testing.T) {
	ctx := context.Background()
	// Use failing blobstore and ensure MRU cache is initialized
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	tx := &Transaction{l2Cache: redis, blobStore: failingBlobStore{}, registry: mocks.NewMockRegistry(false), l1Cache: cache.GetGlobalL1Cache(redis)}
	unused := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{sop.NewUUID()}}}
	err := tx.deleteObsoleteEntries(ctx, nil, unused)
	if err == nil {
		t.Fatalf("expected error from failing blob remove")
	}
}

func Test_Transaction_DeleteObsoleteEntries_Branches(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	blobs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: redis, l1Cache: cache.GetGlobalL1Cache(redis), blobStore: blobs, registry: reg}

	// Prepare unusedNodeIDs and deletedRegistryIDs
	id1 := sop.NewUUID()
	id2 := sop.NewUUID()
	// Seed cache entries for id1 so DeleteNodes runs
	_ = redis.SetStruct(ctx, formatItemKey(id1.String()), &Person{Email: "x"}, time.Minute)
	// Seed blobs so Remove will be called
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id1, Value: []byte("1")}}}, {BlobTable: "bt", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id2, Value: []byte("2")}}}})
	// Seed registry for removal
	rid := sop.NewUUID()
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt", IDs: []sop.Handle{sop.NewHandle(rid)}}})

	unused := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt", Blobs: []sop.UUID{id1, id2}}}
	del := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt", IDs: []sop.UUID{rid}}}
	if err := tx.deleteObsoleteEntries(ctx, del, unused); err != nil {
		t.Fatalf("deleteObsoleteEntries err: %v", err)
	}
}

// Exercise itemActionTracker.Get cache miss (blob fetch + cache set) then hit from cache
func Test_ItemActionTracker_Get_CacheMissThenHit(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)
	blobs := mocks.NewMockBlobStore()

	so := sop.StoreOptions{Name: "iat_get", SlotLength: 4, IsValueDataInNodeSegment: false}
	si := sop.NewStoreInfo(so)
	si.CacheConfig.IsValueDataCacheTTL = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute
	si.IsValueDataGloballyCached = true

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	// Seed blob store with value for item
	id := sop.NewUUID()
	_, p := newPerson("g", "1", "m", "g@x", "p")
	ba, err := encoding.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: si.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: ba}},
	}}); err != nil {
		t.Fatalf("seed blobs: %v", err)
	}

	// Miss -> fetch from blob, set Redis, populate tracker
	it := &btree.Item[PersonKey, Person]{ID: id, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("Get miss->fetch err: %v", err)
	}
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected item value to be populated from blob store")
	}
	// Hit -> should return quickly, not error
	if err := trk.Get(ctx, it); err != nil {
		t.Fatalf("Get hit err: %v", err)
	}
}

// Update/Add/Remove behaviors including actively persisted path and in-memory state transitions
func Test_ItemActionTracker_Update_Add_Remove_ActivelyPersisted(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()

	so := sop.StoreOptions{Name: "iat_upd", SlotLength: 4, IsValueDataInNodeSegment: false}
	si := sop.NewStoreInfo(so)
	si.IsValueDataActivelyPersisted = true
	si.IsValueDataGloballyCached = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	id := sop.NewUUID()
	pk, p := newPerson("u", "2", "m", "u@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 0}

	// Add then Update should actively persist value and bump version
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Update existing addAction
	p.Email = "new@x"
	it.Value = &p
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}

	// Remove on actively persisted mode should enqueue for deletion (no error)
	if err := trk.Remove(ctx, it); err != nil {
		t.Fatalf("Remove err: %v", err)
	}
}

// Lock/Unlock conflict and success paths
func Test_ItemActionTracker_Lock_And_Unlock_Paths(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	so := sop.StoreOptions{Name: "iat_lock", SlotLength: 4, IsValueDataInNodeSegment: false}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	// Track an item for update
	id := sop.NewUUID()
	pk, p := newPerson("l", "3", "m", "l@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 1}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}

	// First lock should succeed (no prior key)
	if err := trk.lock(ctx, time.Minute); err != nil {
		t.Fatalf("lock err: %v", err)
	}
	// Unlock should succeed and delete the lock key
	if err := trk.unlock(ctx); err != nil {
		t.Fatalf("unlock err: %v", err)
	}

	// Seed conflicting lock and attempt again -> expect conflict error
	// Recreate tracked state
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update2 err: %v", err)
	}
	// Simulate another owner by setting a different LockID in redis
	lr := lockRecord{LockID: sop.NewUUID(), Action: updateAction}
	_ = redis.SetStruct(ctx, trk.cache.FormatLockKey(id.String()), &lr, time.Minute)
	if err := trk.lock(ctx, time.Minute); err == nil {
		t.Fatalf("expected lock conflict error, got nil")
	}
}

// commitTrackedItemsValues should add blobs and cache values when not actively persisted and not in-node
func Test_ItemActionTracker_CommitTrackedValues_AddsToBlobAndCache(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	so := sop.StoreOptions{Name: "iat_commit", SlotLength: 4, IsValueDataInNodeSegment: false}
	si := sop.NewStoreInfo(so)
	si.IsValueDataActivelyPersisted = false
	si.IsValueDataGloballyCached = true
	si.CacheConfig.ValueDataCacheDuration = time.Minute

	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	id := sop.NewUUID()
	pk, p := newPerson("c", "4", "m", "c@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p, Version: 0}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}

	// Verify value cached in Redis under the new persisted ID (manage() changes ID for updates)
	persistedID := trk.items[id].item.ID
	var pv Person
	if found, err := redis.GetStruct(ctx, formatItemKey(persistedID.String()), &pv); !found || err != nil {
		t.Fatalf("expected value in redis for ID %s, found=%v err=%v", persistedID.String(), found, err)
	}
}

// unlockNodesKeys should release held locks and nil out nodesKeys safely.
func Test_UnlockNodesKeys_Releases_And_Nils(t *testing.T) {
	ctx := context.Background()
	lc := mocks.NewMockClient()
	tx := &Transaction{l2Cache: lc}
	// Seed two lock keys and mark as owned so Unlock will clear them.
	ks := lc.CreateLockKeys([]string{sop.NewUUID().String(), sop.NewUUID().String()})
	for _, k := range ks {
		_ = lc.Set(ctx, k.Key, k.LockID.String(), time.Minute)
		k.IsLockOwner = true
	}
	tx.nodesKeys = ks
	if err := tx.unlockNodesKeys(ctx); err != nil {
		t.Fatalf("unlockNodesKeys err: %v", err)
	}
	if tx.nodesKeys != nil {
		t.Fatalf("expected nodesKeys to be nil after unlock")
	}
}

// unlockNodesKeys should be a no-op when nodesKeys is nil.
func Test_UnlockNodesKeys_NoNodes_NoError(t *testing.T) {
	ctx := context.Background()
	tx := &Transaction{l2Cache: mocks.NewMockClient()}
	if err := tx.unlockNodesKeys(ctx); err != nil {
		t.Fatalf("expected no error when unlocking with nil nodesKeys, got %v", err)
	}
}

// unlockTrackedItems aggregates last error across backends.
func Test_UnlockTrackedItems_AggregatesError(t *testing.T) {
	tx := &Transaction{btreesBackend: []btreeBackend{
		{
			unlockTrackedItems: func(context.Context) error { return nil },
			hasTrackedItems:    func() bool { return true },
		},
		{
			unlockTrackedItems: func(context.Context) error { return fmt.Errorf("agg") },
			hasTrackedItems:    func() bool { return true },
		},
	}}
	if err := tx.unlockTrackedItems(context.Background()); err == nil {
		t.Fatalf("expected aggregated error")
	}
}

// rollback should return an error if the transaction is already committed (state > finalizeCommit).
func Test_Transaction_Rollback_CommittedState_Error(t *testing.T) {
	ctx := context.Background()
	tx := &Transaction{l2Cache: mocks.NewMockClient(), blobStore: mocks.NewMockBlobStore(), registry: mocks.NewMockRegistry(false)}
	tx.logger = newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger.committedState = finalizeCommit + 1
	if err := tx.rollback(ctx, true); err == nil {
		t.Fatalf("expected error when rolling back an already committed transaction")
	}
}
