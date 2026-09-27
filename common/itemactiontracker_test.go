package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_ExtractRequestPayloadIDs(t *testing.T) {
	// Prepare a payload with two KV pairs and verify only IDs are serialized.
	id1 := sop.NewUUID()
	id2 := sop.NewUUID()
	payload := sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{
		BlobTable: "tbl",
		Blobs: []sop.KeyValuePair[sop.UUID, []byte]{
			{Key: id1, Value: []byte("v1")},
			{Key: id2, Value: []byte("v2")},
		},
	}

	ba := extractRequestPayloadIDs(&payload)
	// Decode back and assert only IDs remained.
	ids := toStruct[sop.BlobsPayload[sop.UUID]](ba)
	if ids.BlobTable != payload.BlobTable {
		t.Fatalf("blob table mismatch: got %s want %s", ids.BlobTable, payload.BlobTable)
	}
	if len(ids.Blobs) != 2 || ids.Blobs[0] != id1 || ids.Blobs[1] != id2 {
		t.Fatalf("ids mismatch: got %v want [%s %s]", ids.Blobs, id1.String(), id2.String())
	}
}

func Test_ItemActionTracker_CommitTrackedItemsValues_Add_PersistsAndCaches(t *testing.T) {
	ctx := context.Background()
	// Store where value data is in separate segment and globally cached.
	so := sop.StoreOptions{
		Name:                      "iat_add",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	// Use project-wide mocks
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	// Build tracker
	iat := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	// Create a btree.Item directly (constructor is unexported)
	pk, p := newPerson("mark", "twain", "male", "m@t", "123")
	item := &btree.Item[PersonKey, Person]{
		ID:    sop.NewUUID(),
		Key:   pk,
		Value: &p,
	}
	if err := iat.Add(ctx, item); err != nil {
		t.Fatalf("Add to tracker failed: %v", err)
	}
	if err := iat.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues failed: %v", err)
	}
	// Item value should be externalized and flagged for fetch
	if item.Value != nil || !item.ValueNeedsFetch {
		t.Fatalf("item externalization flags unexpected; Value=%v NeedsFetch=%v", item.Value, item.ValueNeedsFetch)
	}
	// Blob exists and can be decoded
	ba, err := mockNodeBlobStore.GetOne(ctx, si.BlobTable, item.ID)
	if err != nil || len(ba) == 0 {
		t.Fatalf("blob not found for %s: %v", item.ID.String(), err)
	}
	// Cached in Redis
	var pv Person
	if ok, err := mockRedisCache.GetStruct(ctx, formatItemKey(item.ID.String()), &pv); err != nil || !ok {
		t.Fatalf("cached value not found in redis for %s: %v", item.ID.String(), err)
	}
}

func Test_ItemActionTracker_Update_ActivelyPersisted_LogsAndCaches(t *testing.T) {
	ctx := context.Background()
	// Actively persisted configuration exercises pre-commit logging path
	so := sop.StoreOptions{
		Name:                         "iat_update_active",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    true,
	}
	si := sop.NewStoreInfo(so)
	// Use logging to ensure log path is taken
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	iat := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	pk, p := newPerson("ada", "lovelace", "female", "a@l", "555")
	id := sop.NewUUID()
	item := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &p}
	// Update on an untracked item should still actively persist
	if err := iat.Update(ctx, item); err != nil {
		t.Fatalf("Update to tracker failed: %v", err)
	}
	// Blob exists for item's ID (or temp ID if value duplicated); we check via cache first
	var pv Person
	if ok, err := mockRedisCache.GetStruct(ctx, formatItemKey(item.ID.String()), &pv); err != nil || !ok {
		// If not in cache, ensure blob exists
		ba, berr := mockNodeBlobStore.GetOne(ctx, si.BlobTable, item.ID)
		if berr != nil || len(ba) == 0 {
			t.Fatalf("neither cache nor blob exist for updated item %s: cacheErr=%v blobErr=%v", item.ID.String(), err, berr)
		}
	}
}

func Test_ItemActionTracker_CommitTrackedItemsValues_Update_MarksObsolete(t *testing.T) {
	ctx := context.Background()
	// Value data in separate segment and globally cached
	so := sop.StoreOptions{
		Name:                      "iat_update_mark_obsolete",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	iat := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	// Manually seed a tracked item in "update" state with existing externalized value (ValueNeedsFetch)
	pk, p := newPerson("seed", "s", "z", "s@z", "9")
	originalID := sop.NewUUID()
	item := &btree.Item[PersonKey, Person]{ID: originalID, Key: pk, Value: &p, ValueNeedsFetch: true}
	iat.items[originalID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        item,
		versionInDB: item.Version,
	}

	// Commit tracked values; this should mark originalID obsolete and externalize a new blob under a new ID
	if err := iat.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues update failed: %v", err)
	}

	// Obsolete list should include originalID
	obs := iat.getObsoleteTrackedItemsValues()
	if obs == nil || len(obs.Blobs) != 1 || obs.Blobs[0] != originalID {
		t.Fatalf("expected obsolete to include originalID, got: %+v", obs)
	}
	// Item should be re-externalized with a new ID and cached
	if item.Value != nil || !item.ValueNeedsFetch {
		t.Fatalf("expected externalized value after update")
	}
	if item.ID == originalID {
		t.Fatalf("expected new ID to be assigned on update")
	}
	// Blob for new ID should exist
	if ba, _ := mockNodeBlobStore.GetOne(ctx, si.BlobTable, item.ID); len(ba) == 0 {
		t.Fatalf("expected blob for new ID after update")
	}
}

func Test_ItemActionTracker_GetForRollback_And_Obsolete_NilWhenInNodeSegment(t *testing.T) {
	// When value data is co-located in node segment, both getters should return nil.
	so := sop.StoreOptions{
		Name:                     "iat_nil_segment",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	iat := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	if got := iat.getForRollbackTrackedItemsValues(); got != nil {
		t.Fatalf("expected nil getForRollbackTrackedItemsValues when in node segment")
	}
	if got := iat.getObsoleteTrackedItemsValues(); got != nil {
		t.Fatalf("expected nil getObsoleteTrackedItemsValues when in node segment")
	}
}

func Test_ItemActionTracker_Remove_ActivelyPersisted_QueuesForDeletion(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                         "iat_remove_active",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
	}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tr := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	pk, p := newPerson("rm", "a", "m", "e", "ph")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tr.Remove(ctx, it); err != nil {
		t.Fatalf("Remove err: %v", err)
	}
	if len(tr.forDeletionItems) != 1 || tr.forDeletionItems[0] != it.ID {
		t.Fatalf("expected item queued for deletion, got %#v", tr.forDeletionItems)
	}
	if it.ValueNeedsFetch {
		t.Fatalf("expected ValueNeedsFetch=false")
	}
}

func Test_ItemActionTracker_Remove_AfterAdd_DropsFromTracked(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_remove_after_add", SlotLength: 8})
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tr := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)

	pk, p := newPerson("rm2", "b", "m", "e", "ph")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tr.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if _, ok := tr.items[it.ID]; !ok {
		t.Fatalf("expected item tracked after Add")
	}
	if err := tr.Remove(ctx, it); err != nil {
		t.Fatalf("Remove err: %v", err)
	}
	if _, ok := tr.items[it.ID]; ok {
		t.Fatalf("expected item removed from tracking after remove of new item")
	}
}

// Ensure unlock is a no-op for items where we are not the lock owner.
func Test_ItemActionTracker_Unlock_NonOwner_NoOp(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_unlock_noop", SlotLength: 8})
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tracker := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	// Track one item
	pk, p := newPerson("u", "n", "k", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tracker.Update(ctx, it); err != nil {
		t.Fatal(err)
	}

	// Manually set a different lock owner in cache, and do not set isLockOwner
	other := lockRecord{LockID: sop.NewUUID(), Action: updateAction}
	if err := redis.SetStruct(ctx, redis.FormatLockKey(it.ID.String()), &other, time.Minute); err != nil {
		t.Fatal(err)
	}

	// unlock should not delete the key since we are not owners; no error expected
	if err := tracker.unlock(ctx); err != nil {
		t.Fatalf("unexpected error on unlock: %v", err)
	}
	var lr lockRecord
	found, err := redis.GetStruct(ctx, redis.FormatLockKey(it.ID.String()), &lr)
	if err != nil {
		t.Fatalf("redis get err: %v", err)
	}
	if !found {
		t.Fatalf("lock key should remain when not owner")
	}
}

func Test_ItemActionTracker_LockUnlock_SetsOwnershipAndDeletes(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock", SlotLength: 8})
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tracker := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	// Two tracked items marked for update
	pk1, p1 := newPerson("a", "a", "m", "e", "ph")
	it1 := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk1, Value: &p1}
	pk2, p2 := newPerson("b", "b", "m", "e", "ph")
	it2 := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk2, Value: &p2}
	if err := tracker.Update(ctx, it1); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Update(ctx, it2); err != nil {
		t.Fatal(err)
	}

	if err := tracker.lock(ctx, time.Minute); err != nil {
		t.Fatalf("lock returned error: %v", err)
	}
	// Verify ownership flags set and then unlock removes keys
	for id := range tracker.items {
		var lr lockRecord
		// Found in cache before unlock
		found, err := redis.GetStruct(ctx, redis.FormatLockKey(id.String()), &lr)
		if err != nil || !found {
			t.Fatalf("expected lock in cache for %s", id.String())
		}
	}
	if err := tracker.unlock(ctx); err != nil {
		t.Fatalf("unlock returned error: %v", err)
	}
	for id := range tracker.items {
		var lr lockRecord
		found, err := redis.GetStruct(ctx, redis.FormatLockKey(id.String()), &lr)
		if err != nil {
			t.Fatalf("redis get err: %v", err)
		}
		if found {
			t.Fatalf("expected lock key deleted for %s", id.String())
		}
	}
}

func Test_ItemActionTracker_Lock_ConflictDetected(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_conflict", SlotLength: 8})
	redis := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tracker := newItemActionTracker[PersonKey, Person](si, redis, blobs, tl)

	pk, p := newPerson("c", "c", "m", "e", "ph")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tracker.Update(ctx, it); err != nil {
		t.Fatal(err)
	}
	// Pre-populate a different lock owner in cache to force conflict
	var other lockRecord
	other.LockID = sop.NewUUID()
	other.Action = updateAction
	if err := redis.SetStruct(ctx, redis.FormatLockKey(it.ID.String()), &other, time.Minute); err != nil {
		t.Fatalf("pre-set lock err: %v", err)
	}
	if err := tracker.lock(ctx, time.Minute); err == nil {
		t.Fatalf("expected lock conflict error, got nil")
	}
	if err := tracker.checkTrackedItems(ctx); err == nil {
		t.Fatalf("expected checkTrackedItems to report conflict")
	}
}

// Basic sanity covering Add, Get, Update, Remove paths using public tracker API.
func Test_ItemActionTracker_BasicPaths(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_basic", SlotLength: 8, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	tracker := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, newTransactionLogger(mocks.NewMockTransactionLog(), false))
	pk, p := newPerson("iat", "basic", "1", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tracker.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if it.Value == nil {
		t.Fatalf("expected value retained before commitTrackedItemsValues")
	}
	if err := tracker.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	if it.Value != nil || !it.ValueNeedsFetch {
		t.Fatalf("expected externalized value after commit, got value=%v needsFetch=%v", it.Value, it.ValueNeedsFetch)
	}
	// Simulate update
	updated := Person{Gender: p.Gender, Email: "new@x", Phone: p.Phone, SSN: p.SSN}
	it.Value = &updated
	it.ValueNeedsFetch = false
	if err := tracker.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if err := tracker.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("2nd commitTrackedItemsValues err: %v", err)
	}
	// Remove path
	if err := tracker.Remove(ctx, it); err != nil {
		t.Fatalf("Remove err: %v", err)
	}
}

type errBlob struct{ e error }

func (e *errBlob) GetOne(ctx context.Context, blobTable string, blobID sop.UUID) ([]byte, error) {
	return nil, e.e
}
func (e *errBlob) Add(ctx context.Context, blobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return e.e
}
func (e *errBlob) Update(ctx context.Context, blobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return e.e
}
func (e *errBlob) Remove(ctx context.Context, blobsIDs []sop.BlobsPayload[sop.UUID]) error {
	return e.e
}

func Test_ItemActionTracker_Add_Paths(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		inNode bool
	}{{"add_in_node", true}, {"add_out_of_node", false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			so := sop.StoreOptions{Name: "iat_add_" + c.name, SlotLength: 8, IsValueDataInNodeSegment: c.inNode, IsValueDataGloballyCached: !c.inNode}
			si := sop.NewStoreInfo(so)
			tracker := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, newTransactionLogger(mocks.NewMockTransactionLog(), false))
			pk, p := newPerson("iat", c.name, "1", "e@x", "p")
			it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
			if err := tracker.Add(ctx, it); err != nil {
				t.Fatalf("Add err: %v", err)
			}
			if err := tracker.commitTrackedItemsValues(ctx); err != nil {
				t.Fatalf("commitTrackedItemsValues err: %v", err)
			}
			if c.inNode { // value data stored inline so commitTrackedItemsValues is a no-op
				if it.Value == nil || it.ValueNeedsFetch {
					t.Fatalf("expected inline value retained; got value=%v needsFetch=%v", it.Value, it.ValueNeedsFetch)
				}
			} else { // value data externalized
				if it.Value != nil || !it.ValueNeedsFetch {
					t.Fatalf("expected externalized value; got value=%v needsFetch=%v", it.Value, it.ValueNeedsFetch)
				}
			}
		})
	}
}

func Test_ItemActionTracker_Add_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_add_err", SlotLength: 8, IsValueDataInNodeSegment: false, IsValueDataActivelyPersisted: true}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	tracker := newItemActionTracker[PersonKey, Person](si, mockRedisCache, &errBlob{e: errors.New("boom")}, tl)
	pk, p := newPerson("err", "add", "1", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	err := tracker.Add(ctx, it)
	if err == nil {
		t.Fatalf("expected add error due to blob store failure")
	}
}

// No direct reset method; rely on creating a new tracker (stateless between instances).
func Test_ItemActionTracker_NewInstance_ResetsState(t *testing.T) {
	so := sop.StoreOptions{Name: "iat_reset", SlotLength: 8}
	si := sop.NewStoreInfo(so)
	tracker := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	pk, p := newPerson("r", "s", "g", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tracker.Add(context.Background(), it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if len(tracker.items) == 0 {
		t.Fatalf("expected tracked items")
	}
	tracker2 := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	if len(tracker2.items) != 0 {
		t.Fatalf("expected fresh tracker state")
	}
}

func Test_ItemActionTracker_Get_CacheHit_TTL(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "iat_cache_ttl", SlotLength: 8, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	si.CacheConfig.IsValueDataCacheTTL = true
	tracker := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, newTransactionLogger(mocks.NewMockTransactionLog(), false))
	pk, p := newPerson("ttl", "add", "1", "e@x", "p")
	id := sop.NewUUID()
	_ = mockRedisCache.SetStruct(ctx, formatItemKey(id.String()), &p, si.CacheConfig.ValueDataCacheDuration)
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, ValueNeedsFetch: true}
	if err := tracker.Get(ctx, it); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected hydrated value from cache")
	}
}

// Consolidated extra scenarios for itemactiontracker:
// - lock compatibility vs conflict
// - checkTrackedItems outcomes
// - manage branches (persisted flag, update with fetch, add with/without value)

// buildTracker constructs a tracker with a fresh store info and mock deps.
func buildTracker(name string) (*itemActionTracker[PersonKey, Person], *sop.StoreInfo) {
	so := sop.StoreOptions{Name: name, SlotLength: 8, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tr := newItemActionTracker[PersonKey, Person](si, mockRedisCache, mockNodeBlobStore, tl)
	return tr, si
}

func Test_ItemActionTracker_Lock_CompatibilityAndConflicts(t *testing.T) {
	ctx := context.Background()
	freshCache := mocks.NewMockClient()
	freshBlobs := mocks.NewMockBlobStore()
	so := sop.StoreOptions{Name: "iat_lock", SlotLength: 8, IsValueDataInNodeSegment: false, IsValueDataGloballyCached: true}
	si := sop.NewStoreInfo(so)
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tracker := newItemActionTracker[PersonKey, Person](si, freshCache, freshBlobs, tl)

	// Prepare one item tracked with getAction
	idGet := sop.NewUUID()
	pk, p := newPerson("l", "get", "g", "e", "ph")
	itGet := &btree.Item[PersonKey, Person]{ID: idGet, Key: pk, Value: &p}
	if err := tracker.Get(ctx, itGet); err != nil {
		t.Fatalf("Get setup err: %v", err)
	}

	// In Redis, pre-set a GET lock with different LockID (compatible)
	lrGet := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	if err := tracker.cache.SetStruct(ctx, tracker.cache.FormatLockKey(idGet.String()), &lrGet, time.Minute); err != nil {
		t.Fatalf("seed get lock err: %v", err)
	}

	// Another item tracked with updateAction
	idUpd := sop.NewUUID()
	itUpd := &btree.Item[PersonKey, Person]{ID: idUpd, Key: pk, Value: &p}
	if err := tracker.Update(ctx, itUpd); err != nil {
		t.Fatalf("Update setup err: %v", err)
	}
	// In Redis, pre-set a GET lock (incompatible with update)
	lrUpd := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	if err := tracker.cache.SetStruct(ctx, tracker.cache.FormatLockKey(idUpd.String()), &lrUpd, time.Minute); err != nil {
		t.Fatalf("seed upd lock err: %v", err)
	}

	// And one more item without any Redis record (new lock path)
	idNew := sop.NewUUID()
	itNew := &btree.Item[PersonKey, Person]{ID: idNew, Key: pk, Value: &p}
	if err := tracker.Update(ctx, itNew); err != nil {
		t.Fatalf("Update(new) setup err: %v", err)
	}

	// Attempt to lock all tracked items
	err := tracker.lock(ctx, time.Second)
	if err == nil {
		t.Fatalf("expected conflict error for update-vs-get lock, got nil")
	}

	// Remove the conflicting one and ensure the remaining can be locked
	delete(tracker.items, idUpd)
	if err := tracker.lock(ctx, time.Second); err != nil {
		t.Fatalf("unexpected lock error after removing conflict: %v", err)
	}
	// New item should have ownership after creating lock
	if !tracker.items[idNew].isLockOwner {
		t.Fatalf("expected new item lock ownership")
	}
}

func Test_ItemActionTracker_CheckTrackedItems_ReportsConflictAndOK(t *testing.T) {
	ctx := context.Background()
	tracker, _ := buildTracker("iat_check")

	// Track one GET and one UPDATE
	idA := sop.NewUUID()
	idB := sop.NewUUID()
	pk, p := newPerson("c", "k", "g", "e", "ph")
	itA := &btree.Item[PersonKey, Person]{ID: idA, Key: pk, Value: &p}
	_ = tracker.Get(ctx, itA)
	itB := &btree.Item[PersonKey, Person]{ID: idB, Key: pk, Value: &p}
	_ = tracker.Update(ctx, itB)

	// Redis states: for A, another GET (compatible); for B, another UPDATE lock with different LockID (conflict)
	_ = tracker.cache.SetStruct(ctx, tracker.cache.FormatLockKey(idA.String()), &lockRecord{LockID: sop.NewUUID(), Action: getAction}, time.Minute)
	_ = tracker.cache.SetStruct(ctx, tracker.cache.FormatLockKey(idB.String()), &lockRecord{LockID: sop.NewUUID(), Action: updateAction}, time.Minute)

	err := tracker.checkTrackedItems(ctx)
	if err == nil {
		t.Fatalf("expected conflict reported for idB")
	}
}

func Test_ItemActionTracker_Manage_Branches(t *testing.T) {
	tracker, _ := buildTracker("iat_manage")

	// Case 1: persisted=true => no-op
	id := sop.NewUUID()
	item := &btree.Item[PersonKey, Person]{ID: id, Value: &Person{Email: "x"}}
	persisted := cacheItem[PersonKey, Person]{persisted: true, lockRecord: lockRecord{Action: addAction}, item: item}
	if kv, err := tracker.manage(id, persisted); err != nil || kv != nil {
		t.Fatalf("persisted branch failed, kv=%v err=%v", kv, err)
	}

	// Case 2: updateAction with ValueNeedsFetch set and value present -> mark old for deletion,
	// externalize new value and set ValueNeedsFetch
	id2 := sop.NewUUID()
	item2 := &btree.Item[PersonKey, Person]{ID: id2, Value: &Person{Email: "y"}, ValueNeedsFetch: true}
	upd := cacheItem[PersonKey, Person]{lockRecord: lockRecord{Action: updateAction}, item: item2}
	if kv, err := tracker.manage(id2, upd); err != nil || kv == nil {
		t.Fatalf("update branch expected kv add and no err; got kv=%v err=%v", kv, err)
	}
	if !item2.ValueNeedsFetch {
		t.Fatalf("expected ValueNeedsFetch set during manage(update)")
	}

	// Case 3: addAction with nil value => kv returned nil
	id3 := sop.NewUUID()
	item3 := &btree.Item[PersonKey, Person]{ID: id3}
	addNil := cacheItem[PersonKey, Person]{lockRecord: lockRecord{Action: addAction}, item: item3}
	if kv, err := tracker.manage(id3, addNil); err != nil || kv != nil {
		t.Fatalf("add(nil) branch expected no kv and no err; got kv=%v err=%v", kv, err)
	}

	// Case 4: addAction with value => kv returned and value externalized
	id4 := sop.NewUUID()
	pv := &Person{Email: "z"}
	item4 := &btree.Item[PersonKey, Person]{ID: id4, Value: pv}
	addVal := cacheItem[PersonKey, Person]{lockRecord: lockRecord{Action: addAction}, item: item4}
	if kv, err := tracker.manage(id4, addVal); err != nil || kv == nil || kv.Key.Compare(id4) != 0 {
		t.Fatalf("add(value) expected kv for id4; got kv=%v err=%v", kv, err)
	}
	if item4.Value != nil || !item4.ValueNeedsFetch {
		t.Fatalf("expected value externalized and ValueNeedsFetch set")
	}
}
