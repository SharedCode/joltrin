package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers Get path where the item is already tracked (ok==true) and ValueNeedsFetch=true,
// so after fetching value it returns early without overwriting the existing tracked record.
func Test_ItemActionTracker_Get_AlreadyTracked_EarlyReturn(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_tracked_early",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	// Seed blob with value under the item ID.
	pk, pv := newPerson("x", "y", "m", "e", "p")
	id := sop.NewUUID()
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: si.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: toByteArray(pv)}},
	}}); err != nil {
		t.Fatalf("blob add err: %v", err)
	}

	// Track an existing record with a non-get action to detect overwrite vs early return.
	trackedItem := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        trackedItem,
		versionInDB: 0,
	}

	// Call Get with a distinct pointer; should fetch value and return early without replacing map entry.
	req := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, req); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if req.Value == nil || req.ValueNeedsFetch {
		t.Fatalf("expected value fetched and flag cleared")
	}
	// Ensure existing tracked lock/action not replaced by a new getAction entry.
	got := trk.items[id]
	if got.lockRecord.Action != updateAction {
		t.Fatalf("expected existing tracked action preserved, got %v", got.lockRecord.Action)
	}
}

// Covers Add path when IsValueDataActivelyPersisted=true and IsValueDataGloballyCached=true.
func Test_ItemActionTracker_Add_ActivelyPersisted_PersistsAndCaches(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                         "iat_add_active",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	pk, pv := newPerson("aa", "bb", "f", "e@x", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Blob should exist
	if ba, err := bs.GetOne(ctx, si.BlobTable, it.ID); err != nil || len(ba) == 0 {
		t.Fatalf("expected blob saved for %s, err=%v", it.ID.String(), err)
	}
	// And cache should have the struct
	var out Person
	if ok, err := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); err != nil || !ok {
		t.Fatalf("expected cached value for %s", it.ID.String())
	}
}

// Covers lock compatibility branch: both existing and requested locks are getAction -> no error.
func Test_ItemActionTracker_Lock_GetAction_Compat(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_get", SlotLength: 8})
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	// Track one item via Get to mark action=getAction.
	pk, pv := newPerson("gg", "hh", "m", "e", "p")
	id := sop.NewUUID()
	// Pre-seed blob so Get fetches value and records tracking.
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: si.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: toByteArray(pv)}},
	}}); err != nil {
		t.Fatal(err)
	}
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, ValueNeedsFetch: true}
	if err := trk.Get(ctx, it); err != nil {
		t.Fatal(err)
	}
	// Pre-populate a different lock owner with getAction in cache to hit compatibility continue.
	other := lockRecord{LockID: sop.NewUUID(), Action: getAction}
	if err := l2.SetStruct(ctx, l2.FormatLockKey(id.String()), &other, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := trk.lock(ctx, time.Minute); err != nil {
		t.Fatalf("lock should tolerate concurrent getAction: %v", err)
	}
}

// Covers lock path where the key is found in cache and has the same LockID as ours;
// code should continue without error and without changing ownership.
func Test_ItemActionTracker_Lock_Found_SameOwner_Continues(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_same_owner", SlotLength: 8})
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	// Track an item for update
	pk, pv := newPerson("so", "so", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatal(err)
	}

	// Pre-populate cache with the same LockID as tracker for this key
	ci := trk.items[it.ID]
	lr := lockRecord{LockID: ci.LockID, Action: updateAction}
	if err := l2.SetStruct(ctx, l2.FormatLockKey(it.ID.String()), &lr, time.Minute); err != nil {
		t.Fatalf("pre-set lock err: %v", err)
	}

	// Lock should succeed without error; ownership flag should remain default(false) for found path
	if err := trk.lock(ctx, time.Minute); err != nil {
		t.Fatalf("lock returned error: %v", err)
	}
	ci = trk.items[it.ID]
	if ci.isLockOwner {
		t.Fatalf("expected isLockOwner=false when lock is already ours in cache")
	}
}

// Covers commitTrackedItemsValues early-return when value data stays in node segment.
func Test_CommitTrackedItemsValues_EarlyReturn_InNodeSegment(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                     "iat_commit_early",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues early return err: %v", err)
	}
}

// Covers Get path where value is resolved from Redis cache (found=true) so blob store is not read.
func Test_ItemActionTracker_Get_FromRedisCache_SetsValue(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_cache",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	pk, pv := newPerson("cache", "hit", "m", "e@x", "p")
	id := sop.NewUUID()
	// Seed only Redis, not blob, to ensure Get uses cache path.
	if err := l2.SetStruct(ctx, formatItemKey(id.String()), &pv, time.Minute); err != nil {
		t.Fatalf("seed redis err: %v", err)
	}

	req := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, req); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if req.Value == nil || req.ValueNeedsFetch {
		t.Fatalf("expected value from cache and NeedsFetch cleared")
	}
	// Item should be tracked with getAction
	if got, ok := trk.items[id]; !ok || got.lockRecord.Action != getAction {
		t.Fatalf("expected item tracked with getAction")
	}
}

// Covers Add when IsValueDataActivelyPersisted is false: only tracking and version bump happen.
func Test_ItemActionTracker_Add_NoActivePersist_TracksAndBumpsVersion(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_add_simple",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("add", "nopersist", "f", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	// Version starts at zero; Add should increment it while versionInDB stored remains 0.
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if it.Version != 1 {
		t.Fatalf("expected version increment to 1, got %d", it.Version)
	}
	ci, ok := trk.items[it.ID]
	if !ok {
		t.Fatalf("expected item tracked after Add")
	}
	if ci.lockRecord.Action != addAction || ci.versionInDB != 0 {
		t.Fatalf("unexpected cached record: action=%v versionInDB=%d", ci.lockRecord.Action, ci.versionInDB)
	}
}

// Ensures commitTrackedItemsValues persists to blob but does not cache when global cache is disabled.
func Test_CommitTrackedItemsValues_NoGlobalCache_NoRedisSet(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_commit_nocache",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("cg", "off", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	// Blob should exist
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob persisted for %s", it.ID.String())
	}
	// Cache should not contain value
	var out Person
	if ok, err := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); err != nil || ok {
		t.Fatalf("expected no cached value when global cache disabled; ok=%v err=%v", ok, err)
	}
}

// Covers Update path when the item was previously tracked via Add (v.Action==addAction),
// ensuring activelyPersist path runs and version is not bumped again.
func Test_ItemActionTracker_Update_AfterAdd_ActivelyPersisted(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                         "iat_update_after_add_active",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), true))

	pk, pv := newPerson("upd", "afteradd", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// Version bumped once by Add
	if it.Version != 1 {
		t.Fatalf("expected version 1 after Add, got %d", it.Version)
	}

	// Now call Update; since tracked action is addAction, code should activelyPersist without version bump.
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if it.Version != 1 {
		t.Fatalf("expected version to remain 1, got %d", it.Version)
	}
	// Verify blob exists and cache set
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob persisted for %s", it.ID.String())
	}
	var out Person
	if ok, err := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); err != nil || !ok {
		t.Fatalf("expected cached value for %s", it.ID.String())
	}
}

// Covers Get path when global cache is disabled; value should be fetched from blob store.
func Test_ItemActionTracker_Get_FromBlob_NoGlobalCache(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_blob_only",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("blob", "only", "m", "e", "p")
	id := sop.NewUUID()
	// Seed blob store only
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: si.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: toByteArray(pv)}},
	}}); err != nil {
		t.Fatalf("blob seed err: %v", err)
	}

	req := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, req); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if req.Value == nil || req.ValueNeedsFetch {
		t.Fatalf("expected value from blob and NeedsFetch cleared")
	}
}

// Covers Add when actively persisted but Value is nil: nothing should be persisted or cached.
func Test_ItemActionTracker_Add_ActivePersist_NilValue_NoOps(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                         "iat_add_active_nil",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), true))

	pk := PersonKey{Firstname: "nil", Lastname: "val"}
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}
	// No blob should have been written
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) != 0 {
		t.Fatalf("expected no blob written for nil value")
	}
	// No cache set either
	var out Person
	if ok, _ := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); ok {
		t.Fatalf("expected no cache set for nil value")
	}
}

// Covers commitTrackedItemsValues/manage path for removeAction: when ValueNeedsFetch=true,
// the item ID is queued for deletion and no blob is written.
func Test_CommitTrackedItemsValues_Remove_MarksForDeletion_NoBlob(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_commit_remove_mark",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, _ := newPerson("rm", "val", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: removeAction},
		item:        it,
		versionInDB: 0,
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	// No blob should exist since removeAction doesn't add.
	if ba, _ := bs.GetOne(ctx, si.BlobTable, id); len(ba) != 0 {
		t.Fatalf("expected no blob for removeAction")
	}
	// Item should be queued for deletion.
	if len(trk.forDeletionItems) != 1 || trk.forDeletionItems[0] != id {
		t.Fatalf("expected id queued for deletion, got %#v", trk.forDeletionItems)
	}
	// And ValueNeedsFetch gets cleared as part of manage()
	if it.ValueNeedsFetch {
		t.Fatalf("expected ValueNeedsFetch=false after manage for removeAction")
	}
}

// Covers Update path when item is not yet tracked: tracker should create an entry,
// set action=update, bump version, and proceed without active persistence.
func Test_ItemActionTracker_Update_FirstTime_TracksAndBumps(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_update_first",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("uf", "t", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv, Version: 0}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	// Version should bump from 0 -> 1
	if it.Version != 1 {
		t.Fatalf("expected version 1, got %d", it.Version)
	}
	ci, ok := trk.items[it.ID]
	if !ok || ci.lockRecord.Action != updateAction {
		t.Fatalf("expected tracked item with updateAction")
	}
}

// Ensures commitTrackedItemsValues writes to blob and populates Redis when global cache is enabled.
func Test_CommitTrackedItemsValues_CachesOnSuccess(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_commit_cache_on",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
		CacheConfig:               sop.NewStoreCacheConfig(time.Minute, false),
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("cc", "on", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        it,
		versionInDB: 0,
	}
	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	// Blob should exist
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob written")
	}
	// And Redis should have the struct because global cache is enabled
	var out Person
	if ok, err := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); err != nil || !ok {
		t.Fatalf("expected cached value, ok=%v err=%v", ok, err)
	}
}

// Add with active persistence and no global cache: persists blob but does not set cache.
func Test_ItemActionTracker_Add_ActivePersist_NoGlobalCache(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                         "iat_add_active_nocache",
		SlotLength:                   8,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: true,
		IsValueDataGloballyCached:    false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), true))

	pk, pv := newPerson("ap", "nogc", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &pv}
	if err := trk.Add(ctx, it); err != nil {
		t.Fatalf("Add err: %v", err)
	}

	// Blob should exist
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) == 0 {
		t.Fatalf("expected blob persisted")
	}
	// Cache should not be set
	var out Person
	if ok, err := l2.GetStruct(ctx, formatItemKey(it.ID.String()), &out); err != nil || ok {
		t.Fatalf("expected no cache set; ok=%v err=%v", ok, err)
	}
}

// Update on already-tracked item (non-add) in non-active persist store: version bump occurs when equal to versionInDB.
func Test_ItemActionTracker_Update_Tracked_NonActivePersist_VersionBump(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_update_tracked_bump",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("up", "bump", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv, Version: 2}
	// Seed tracker with existing record (non-add), versionInDB=2
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: 2,
	}

	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if it.Version != 3 {
		t.Fatalf("expected version bumped to 3, got %d", it.Version)
	}
	// Ensure still tracked and action is update
	ci, ok := trk.items[id]
	if !ok || ci.lockRecord.Action != updateAction {
		t.Fatalf("expected item tracked with updateAction")
	}
}

// Manage() early-exit when cachedItem.persisted is true: no blob write and no value nullification.
func Test_ItemActionTracker_CommitTrackedItemsValues_Persisted_NoOp(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_commit_persisted_noop",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: false,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("ps", "noop", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: 0,
		persisted:   true,
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commit err: %v", err)
	}
	// No blob written
	if ba, _ := bs.GetOne(ctx, si.BlobTable, id); len(ba) != 0 {
		t.Fatalf("expected no blob when persisted=true")
	}
	// Value should remain non-nil and not toggled
	if it.Value == nil || it.ValueNeedsFetch {
		t.Fatalf("expected value untouched for persisted item")
	}
}

// Get() with IsValueDataCacheTTL=true should take GetStructEx path; we seed Redis so it returns early.
func Test_ItemActionTracker_Get_TTL_FromRedisCache(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_ttl",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
		CacheConfig:               sop.NewStoreCacheConfig(time.Minute, true),
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("ttl", "fromcache", "m", "e", "p")
	id := sop.NewUUID()
	if err := l2.SetStruct(ctx, formatItemKey(id.String()), &pv, time.Minute); err != nil {
		t.Fatal(err)
	}
	req := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, req); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if req.Value == nil || req.ValueNeedsFetch {
		t.Fatalf("expected value from TTL cache path")
	}
}

// Update of tracked item where item.Version != versionInDB should not bump version.
func Test_ItemActionTracker_Update_Tracked_NoVersionBump_WhenDifferent(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_update_nobump", SlotLength: 8})
	trk := newItemActionTracker[PersonKey, Person](si, mocks.NewMockClient(), mocks.NewMockBlobStore(), newTransactionLogger(mocks.NewMockTransactionLog(), false))

	pk, pv := newPerson("nb", "nb", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv, Version: 3}
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: updateAction},
		item:        it,
		versionInDB: 2,
	}
	if err := trk.Update(ctx, it); err != nil {
		t.Fatalf("Update err: %v", err)
	}
	if it.Version != 3 {
		t.Fatalf("expected version unchanged (3), got %d", it.Version)
	}
}
