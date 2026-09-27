package common

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers updateAction path when values are in node segment (IsValueDataInNodeSegment=true): uses UpdateCurrentItem.
func Test_RefetchAndMerge_UpdateAction_InNodeSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	// Use value-in-node segment to exercise UpdateCurrentItem branch.
	so := sop.StoreOptions{Name: "rfm_update_innode", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed an item into the tree.
	pk, pv := newPerson("u1", "v1", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: ok=%v err=%v", ok, err)
	}

	// Persist the root node so refetch (which clears caches) can retrieve it from blob store/registry.
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found in local cache")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}

	// Refresh StoreInfo in repository for refetch path.
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Locate the item and prepare an update tracked in the item action tracker.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find seed err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem err: %v", err)
	}
	newVal := pv
	newVal.Phone = "updated"
	upd := &btree.Item[PersonKey, Person]{ID: cur.ID, Key: pk, Value: &newVal, Version: cur.Version}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        upd,
		versionInDB: cur.Version,
	}

	// Run refetch and merge; it should update the current item in-node.
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch update in-node err: %v", err)
	}
	// Verify updated value is present.
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatalf("expected item still present")
	}
	cur2, _ := b3.GetCurrentItem(ctx)
	if cur2.Value == nil || cur2.Value.Phone != "updated" {
		t.Fatalf("expected updated value applied, got %+v", cur2.Value)
	}
}

// Covers updateAction path when values are NOT in node segment (separate segment):
// closure should mark current ID for deletion, replace tracker entry with inflight item ID,
// and update current node item metadata.
func Test_RefetchAndMerge_UpdateAction_SeparateSegment_MergesIDs(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	// Use value in separate segment to exercise UpdateCurrentNodeItem branch.
	so := sop.StoreOptions{Name: "rfm_update_sep", SlotLength: 4, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Seed an item and persist root for refetch.
	pk, pv := newPerson("u2", "v2", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	// Find current item and prepare tracked update with a new inflight ID.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newID := sop.NewUUID()
	inflight := &btree.Item[PersonKey, Person]{ID: newID, Key: pk, Value: nil, Version: cur.Version}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        inflight,
		versionInDB: cur.Version,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch update separate err: %v", err)
	}

	// Tracker should have queued old ID for deletion and replaced entry with newID marked persisted.
	iat := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person])
	if len(iat.forDeletionItems) != 1 || iat.forDeletionItems[0] != cur.ID {
		t.Fatalf("expected forDeletionItems to contain old ID; got %#v", iat.forDeletionItems)
	}
	ci, ok := iat.items[newID]
	if !ok || !ci.persisted {
		t.Fatalf("expected tracker to store persisted inflight item by new ID")
	}

	// B-tree current item for key should reflect the new ID.
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find post-merge err: %v", err)
	}
	cur2, _ := b3.GetCurrentItem(ctx)
	if cur2.ID != newID {
		t.Fatalf("expected current item ID to be newID; got %s", cur2.ID.String())
	}
}

// Covers removeAction path: item should be removed after refetch and merge.
func Test_RefetchAndMerge_RemoveAction_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_remove", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)

	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	pk, pv := newPerson("rm", "x", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, err := b3.GetCurrentItem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        &btree.Item[PersonKey, Person]{ID: cur.ID, Key: pk, Value: nil, Version: cur.Version},
		versionInDB: cur.Version,
	}

	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch remove err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); ok {
		t.Fatalf("expected item to be removed")
	}
}

// Covers addAction when values are NOT in node segment: AddItem path should be used,
// and tracker marks the inflight item as persisted without touching value segment.
func Test_RefetchAndMerge_AddAction_SeparateSegment_Succeeds_Alt(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_add_sep", SlotLength: 4, IsValueDataInNodeSegment: false}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)
	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure root node is staged in local cache by performing a seed add.
	if ok, err := b3.Add(ctx, PersonKey{Firstname: "seed", Lastname: "sep"}, Person{Gender: "x", Email: "seed@sep", Phone: "p"}); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}

	// Persist root
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	_ = sr.Add(ctx, *ns)

	// Track addAction with value kept in separate segment (nil here).
	pk, _ := newPerson("add", "sep", "m", "e", "p")
	newID := sop.NewUUID()
	inflight := &btree.Item[PersonKey, Person]{ID: newID, Key: pk, Value: nil, Version: 0}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[newID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        inflight,
		versionInDB: 0,
	}
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch add separate err: %v", err)
	}

	// Ensure item exists by ID lookup and tracker marked persisted.
	if ok, err := b3.FindWithID(ctx, pk, newID); !ok || err != nil {
		t.Fatalf("expected new item present: %v", err)
	}
	ci, ok := si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[newID]
	if !ok || !ci.persisted {
		t.Fatalf("expected tracker to store persisted item after add")
	}
}

// Covers addAction when values are in node segment: Update via Add(key, value) path.
func Test_RefetchAndMerge_AddAction_InNodeSegment_Succeeds(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_add_innode", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)
	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure root node is staged in local cache by performing a seed add.
	if ok, err := b3.Add(ctx, PersonKey{Firstname: "seed", Lastname: "innode"}, Person{Gender: "x", Email: "seed@innode", Phone: "p"}); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}

	// Persist root
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	_ = sr.Add(ctx, *ns)

	pk, pv := newPerson("add", "innode", "m", "e", "p")
	inflight := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 0}
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[inflight.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        inflight,
		versionInDB: 0,
	}
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch add innode err: %v", err)
	}
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("expected item present: %v", err)
	}
}

// Covers getAction path: ensure no modification happens and no errors returned.
func Test_RefetchAndMerge_GetAction_NoOp(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_get", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)
	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	pk, pv := newPerson("g", "x", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, _ := b3.GetCurrentItem(ctx)
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: getAction},
		item:        &btree.Item[PersonKey, Person]{ID: cur.ID, Key: pk, Value: cur.Value, Version: cur.Version},
		versionInDB: cur.Version,
	}
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err != nil {
		t.Fatalf("refetch get err: %v", err)
	}
	if ok, _ := b3.Find(ctx, pk, false); !ok {
		t.Fatalf("expected item to remain present")
	}
}

// Covers version conflict branch: if the backend item version differs from versionInDB, return error.
func Test_RefetchAndMerge_VersionConflict_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	rg := mocks.NewMockRegistry(false)
	sr := mocks.NewMockStoreRepository()
	tx := &Transaction{registry: rg, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: sr}

	so := sop.StoreOptions{Name: "rfm_conflict", SlotLength: 4, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	_ = sr.Add(ctx, *ns)
	si := StoreInterface[PersonKey, Person]{}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tx.l2Cache, tx.blobStore, tx.logger)
	nrw := newNodeRepository[PersonKey, Person](tx, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}

	pk, pv := newPerson("c", "v", "m", "a@b", "p")
	if ok, err := b3.Add(ctx, pk, pv); !ok || err != nil {
		t.Fatalf("seed Add error: %v", err)
	}
	rootID := b3.StoreInfo.RootNodeID
	cn, ok := si.backendNodeRepository.localCache[rootID]
	if !ok {
		t.Fatalf("root node not found")
	}
	rootNode := cn.node.(*btree.Node[PersonKey, Person])
	if ok2, _, err := si.backendNodeRepository.commitNewRootNodes(ctx, []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: ns, Second: []interface{}{rootNode}}}); err != nil || !ok2 {
		t.Fatalf("commitNewRootNodes err=%v ok=%v", err, ok2)
	}
	ns.RootNodeID = b3.StoreInfo.RootNodeID
	ns.Count = 1
	_ = sr.Add(ctx, *ns)

	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("Find err: %v", err)
	}
	cur, _ := b3.GetCurrentItem(ctx)
	// Prepare tracker with mismatched versionInDB (cur.Version - 1) to force conflict.
	si.ItemActionTracker.(*itemActionTracker[PersonKey, Person]).items[cur.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        &btree.Item[PersonKey, Person]{ID: cur.ID, Key: pk, Value: cur.Value, Version: cur.Version},
		versionInDB: cur.Version - 1,
	}
	closure := refetchAndMergeClosure(&si, b3, sr)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected version conflict error, got nil")
	}
}
