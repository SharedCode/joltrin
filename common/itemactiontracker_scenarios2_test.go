package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// flakyCache wraps a base Cache and forces the second GetStruct after SetStruct to miss for a specific key.
// This simulates the lock() branch where the post-set read fails: "can't attain a lock in Redis".
type flakyCache struct {
	base        sop.L2Cache
	targetKey   string
	triggerOnce bool
}

func newFlakyCache(base sop.L2Cache, targetKey string) *flakyCache {
	return &flakyCache{base: base, targetKey: targetKey, triggerOnce: false}
}

func (f *flakyCache) GetType() sop.L2CacheType {
	return sop.Redis
}

// Cache interface forwarding with minimal behavior changes.
func (f *flakyCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return f.base.Set(ctx, key, value, expiration)
}
func (f *flakyCache) Get(ctx context.Context, key string) (bool, string, error) {
	return f.base.Get(ctx, key)
}
func (f *flakyCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return f.base.GetEx(ctx, key, expiration)
}
func (f *flakyCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	// After a SetStruct to the target key, arm the next GetStruct to miss once.
	if key == f.targetKey {
		f.triggerOnce = true
	}
	return f.base.SetStruct(ctx, key, value, expiration)
}
func (f *flakyCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	for _, key := range keys {
		if key == f.targetKey {
			f.triggerOnce = true
		}
	}
	return f.base.SetStructs(ctx, keys, values, expiration)
}
func (f *flakyCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	if key == f.targetKey && f.triggerOnce {
		// Consume the one-shot trigger and report miss.
		f.triggerOnce = false
		return false, nil
	}
	return f.base.GetStruct(ctx, key, target)
}
func (f *flakyCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	// Delegate; not used in this test scenario.
	return f.base.GetStructEx(ctx, key, target, expiration)
}
func (f *flakyCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	results, err := f.base.GetStructs(ctx, keys, targets, expiration)
	if err != nil {
		return results, err
	}
	if f.triggerOnce {
		// If triggered, verify if any key matches targetKey
		for i, k := range keys {
			if k == f.targetKey {
				// Found the key to flake on
				f.triggerOnce = false
				results[i] = false
			}
		}
	}
	return results, nil
}
func (f *flakyCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return f.base.Delete(ctx, keys)
}
func (f *flakyCache) Ping(ctx context.Context) error              { return f.base.Ping(ctx) }
func (f *flakyCache) FormatLockKey(k string) string               { return f.base.FormatLockKey(k) }
func (f *flakyCache) CreateLockKeys(keys []string) []*sop.LockKey { return f.base.CreateLockKeys(keys) }
func (f *flakyCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return f.base.CreateLockKeysForIDs(keys)
}
func (f *flakyCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return f.base.IsLockedTTL(ctx, duration, lockKeys)
}
func (f *flakyCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return f.base.Lock(ctx, duration, lockKeys)
}
func (f *flakyCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := f.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := f.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (f *flakyCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return f.base.IsLocked(ctx, lockKeys)
}
func (f *flakyCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return f.base.IsLockedByOthers(ctx, lockKeyNames)
}
func (f *flakyCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return f.base.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (f *flakyCache) IsRestarted(ctx context.Context) bool {
	return f.base.IsRestarted(ctx)
}
func (f *flakyCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return f.base.Unlock(ctx, lockKeys)
}
func (f *flakyCache) Clear(ctx context.Context) error { return f.base.Clear(ctx) }

func Test_ItemActionTracker_Lock_PostSetReadMiss_ReturnsError(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_postset_miss", SlotLength: 8})
	base := mocks.NewMockClient()
	blobs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	// We'll determine targetKey after we know the item ID and cache's FormatLockKey.
	// For now, pass a placeholder and update once item is set.
	placeholderKey := ""
	fc := newFlakyCache(base, placeholderKey)
	tracker := newItemActionTracker[PersonKey, Person](si, fc, blobs, tl)

	// Track an item slated for update.
	pk, p := newPerson("lk", "miss", "m", "e", "p")
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: &p}
	if err := tracker.Update(ctx, it); err != nil {
		t.Fatal(err)
	}

	// Arm flaky cache for the specific lock key used by lock().
	fc.targetKey = fc.FormatLockKey(it.ID.String())

	// Attempt to lock; expect error due to post-set read miss.
	if err := tracker.lock(ctx, time.Minute); err == nil {
		t.Fatalf("expected error from lock when post-set read misses")
	}
}

// errLockCache forces GetStruct to return an error for a specific lock key,
// simulating the early GetStruct failure path in lock().
type errLockCache struct {
	base      sop.L2Cache
	targetKey string
}

func (e *errLockCache) GetType() sop.L2CacheType {
	return sop.Redis
}

func (e *errLockCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return e.base.Set(ctx, key, value, expiration)
}
func (e *errLockCache) Get(ctx context.Context, key string) (bool, string, error) {
	return e.base.Get(ctx, key)
}
func (e *errLockCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return e.base.GetEx(ctx, key, expiration)
}
func (e *errLockCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return e.base.SetStruct(ctx, key, value, expiration)
}
func (e *errLockCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return e.base.SetStructs(ctx, keys, values, expiration)
}
func (e *errLockCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	if key == e.targetKey {
		return false, fmt.Errorf("early getstruct failure")
	}
	return e.base.GetStruct(ctx, key, target)
}
func (e *errLockCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return e.base.GetStructEx(ctx, key, target, expiration)
}
func (e *errLockCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	for _, k := range keys {
		if k == e.targetKey {
			return nil, fmt.Errorf("early getstruct failure")
		}
	}
	return e.base.GetStructs(ctx, keys, targets, expiration)
}
func (e *errLockCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return e.base.Delete(ctx, keys)
}
func (e *errLockCache) Ping(ctx context.Context) error { return e.base.Ping(ctx) }
func (e *errLockCache) FormatLockKey(k string) string  { return e.base.FormatLockKey(k) }
func (e *errLockCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return e.base.CreateLockKeys(keys)
}
func (e *errLockCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return e.base.CreateLockKeysForIDs(keys)
}
func (e *errLockCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return e.base.IsLockedTTL(ctx, duration, lockKeys)
}
func (e *errLockCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return e.base.Lock(ctx, duration, lockKeys)
}
func (e *errLockCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := e.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := e.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (e *errLockCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return e.base.IsLocked(ctx, lockKeys)
}
func (e *errLockCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return e.base.IsLockedByOthers(ctx, lockKeyNames)
}
func (e *errLockCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return e.base.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (e *errLockCache) IsRestarted(ctx context.Context) bool {
	return e.base.IsRestarted(ctx)
}
func (e *errLockCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return e.base.Unlock(ctx, lockKeys)
}
func (e *errLockCache) Clear(ctx context.Context) error { return e.base.Clear(ctx) }

func Test_ItemActionTracker_Lock_EarlyGetStructError(t *testing.T) {
	ctx := context.Background()
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "iat_lock_get_error", SlotLength: 8})
	base := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	// Use a temporary tracker to get the lock key format for an ID, then wrap with err cache.
	tmp := newItemActionTracker[PersonKey, Person](si, base, bs, tl)
	pk, pv := newPerson("ge", "rr", "m", "e", "p")
	id := sop.NewUUID()
	it := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: &pv}
	if err := tmp.Update(ctx, it); err != nil {
		t.Fatal(err)
	}

	// Now create errLockCache targeted at this lock key.
	target := base.FormatLockKey(id.String())
	ec := &errLockCache{base: base, targetKey: target}
	trk := newItemActionTracker[PersonKey, Person](si, ec, bs, tl)
	// Re-seed tracked item into the new tracker.
	trk.items[id] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: tmp.items[id].LockID, Action: updateAction},
		item:        it,
		versionInDB: 0,
	}

	if err := trk.lock(ctx, time.Minute); err == nil {
		t.Fatalf("expected error from early GetStruct on lock key")
	}
}

// Ensures commitTrackedItemsValues no-ops when tracked item has no Value (no blob to write).
func Test_CommitTrackedItemsValues_NoValue_NoBlob(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_commit_novalue",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	l2 := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)
	trk := newItemActionTracker[PersonKey, Person](si, l2, bs, tl)

	// Track an item with nil Value so manage() returns nil and nothing is added to itemsForAdd.
	pk := PersonKey{Lastname: "ln", Firstname: "fn"}
	it := &btree.Item[PersonKey, Person]{ID: sop.NewUUID(), Key: pk, Value: nil}
	trk.items[it.ID] = cacheItem[PersonKey, Person]{
		lockRecord:  lockRecord{LockID: sop.NewUUID(), Action: addAction},
		item:        it,
		versionInDB: 0,
	}

	if err := trk.commitTrackedItemsValues(ctx); err != nil {
		t.Fatalf("commitTrackedItemsValues err: %v", err)
	}
	// No blob should be written for the item ID.
	if ba, _ := bs.GetOne(ctx, si.BlobTable, it.ID); len(ba) != 0 {
		t.Fatalf("expected no blob for item with nil value")
	}
}

// errCache simulates Redis errors for value caching paths in Get().
// - GetStruct returns (false, err) for the target value key to trigger blob fallback and warn log.
// - SetStruct returns err for the target value key to hit the warn log after successful blob fetch.
type errCache struct {
	base     sop.L2Cache
	valueKey string
}

func newErrCache(base sop.L2Cache, valueKey string) *errCache {
	return &errCache{base: base, valueKey: valueKey}
}

func (e *errCache) GetType() sop.L2CacheType {
	return sop.Redis
}

func (e *errCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return e.base.Set(ctx, key, value, expiration)
}
func (e *errCache) Get(ctx context.Context, key string) (bool, string, error) {
	return e.base.Get(ctx, key)
}
func (e *errCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return e.base.GetEx(ctx, key, expiration)
}
func (e *errCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if key == e.valueKey {
		return errors.New("redis setstruct failure")
	}
	return e.base.SetStruct(ctx, key, value, expiration)
}
func (e *errCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	for _, key := range keys {
		if key == e.valueKey {
			return errors.New("redis setstructs failure")
		}
	}
	return e.base.SetStructs(ctx, keys, values, expiration)
}
func (e *errCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	if key == e.valueKey {
		return false, errors.New("redis getstruct failure")
	}
	return e.base.GetStruct(ctx, key, target)
}
func (e *errCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	// Simulate same failure behavior on Ex as well.
	if key == e.valueKey {
		return false, errors.New("redis getstructex failure")
	}
	return e.base.GetStructEx(ctx, key, target, expiration)
}
func (e *errCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return e.base.GetStructs(ctx, keys, targets, expiration)
}
func (e *errCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return e.base.Delete(ctx, keys)
}
func (e *errCache) Ping(ctx context.Context) error              { return e.base.Ping(ctx) }
func (e *errCache) FormatLockKey(k string) string               { return e.base.FormatLockKey(k) }
func (e *errCache) CreateLockKeys(keys []string) []*sop.LockKey { return e.base.CreateLockKeys(keys) }
func (e *errCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return e.base.CreateLockKeysForIDs(keys)
}
func (e *errCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return e.base.IsLockedTTL(ctx, duration, lockKeys)
}
func (e *errCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	return e.base.Lock(ctx, duration, lockKeys)
}
func (e *errCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	if s, l, err := e.Lock(ctx, duration, keys); !s || err != nil {
		return s, l, err
	}
	if s, err := e.IsLocked(ctx, keys); !s || err != nil {
		return s, sop.NilUUID, err
	}
	return true, sop.NilUUID, nil
}
func (e *errCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return e.base.IsLocked(ctx, lockKeys)
}
func (e *errCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return e.base.IsLockedByOthers(ctx, lockKeyNames)
}
func (e *errCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return e.base.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (e *errCache) IsRestarted(ctx context.Context) bool {
	return e.base.IsRestarted(ctx)
}
func (e *errCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return e.base.Unlock(ctx, lockKeys)
}
func (e *errCache) Clear(ctx context.Context) error { return e.base.Clear(ctx) }

func Test_ItemActionTracker_Get_RedisErrors_UsesBlob_StillSucceeds(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{
		Name:                      "iat_get_redis_err",
		SlotLength:                8,
		IsValueDataInNodeSegment:  false,
		IsValueDataGloballyCached: true,
	}
	si := sop.NewStoreInfo(so)
	base := mocks.NewMockClient()
	bs := mocks.NewMockBlobStore()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), false)

	pk, pv := newPerson("redis", "errs", "m", "e", "p")
	id := sop.NewUUID()
	// Seed only blob; cache will error to force blob path.
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: si.BlobTable,
		Blobs:     []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: toByteArray(pv)}},
	}}); err != nil {
		t.Fatalf("blob seed err: %v", err)
	}

	valueKey := formatItemKey(id.String())
	ec := newErrCache(base, valueKey)
	trk := newItemActionTracker[PersonKey, Person](si, ec, bs, tl)

	req := &btree.Item[PersonKey, Person]{ID: id, Key: pk, Value: nil, ValueNeedsFetch: true}
	if err := trk.Get(ctx, req); err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if req.Value == nil || req.ValueNeedsFetch {
		t.Fatalf("expected value from blob after redis errors")
	}
}
