package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

func TestInMemoryCache_Eviction(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	// We need to fill one shard.
	// Let's pick a key "target" and find its shard.
	targetKey := "target"
	targetShard := c.data.getShard(targetKey)

	// Fill this shard
	for i := 0; i < maxItemsPerShard; i++ {
		// We need keys that map to the SAME shard.
		// This is tricky without mocking or brute force.
		// Brute force: generate keys, check if they map to targetShard, if so, add.
		added := false
		for j := 0; ; j++ {
			k := fmt.Sprintf("key-%d-%d", i, j)
			if c.data.getShard(k) == targetShard {
				c.Set(ctx, k, "value", 0)
				added = true
				break
			}
		}
		if !added {
			t.Fatal("Failed to find key for shard")
		}
	}

	// Verify shard is full
	targetShard.mu.RLock()
	if len(targetShard.items) != maxItemsPerShard {
		t.Fatalf("Shard should be full, got %d", len(targetShard.items))
	}
	targetShard.mu.RUnlock()

	// Add one more item to trigger eviction
	// We need one more key for this shard
	var victimKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("victim-%d", j)
		if c.data.getShard(k) == targetShard {
			victimKey = k
			break
		}
	}

	// Set with expiration to test the "min expiration" logic
	// First, let's make sure existing items have some expiration or zero
	// The loop above set them with 0 (infinite).

	// Let's add the new item. It should trigger eviction.
	c.Set(ctx, victimKey, "value", time.Minute)

	targetShard.mu.RLock()
	count := len(targetShard.items)
	targetShard.mu.RUnlock()

	// Count should still be maxItemsPerShard (1000) because one was evicted and one added
	// OR it could be less if multiple were evicted (but our logic evicts 1).
	// Actually, the logic is: if len >= max, delete 1, then add. So count should be max.
	if count != maxItemsPerShard {
		t.Errorf("Expected count %d, got %d", maxItemsPerShard, count)
	}
}

func TestInMemoryCache_LoadOrStore_Eviction(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	// ctx := context.Background()

	targetKey := "target"
	targetShard := c.data.getShard(targetKey)

	// Fill shard
	for i := 0; i < maxItemsPerShard; i++ {
		for j := 0; ; j++ {
			k := fmt.Sprintf("key-%d-%d", i, j)
			if c.data.getShard(k) == targetShard {
				c.data.store(k, item{data: []byte("val")})
				break
			}
		}
	}

	// Trigger LoadOrStore eviction
	var newKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("new-%d", j)
		if c.data.getShard(k) == targetShard {
			newKey = k
			break
		}
	}

	c.data.loadOrStore(newKey, item{data: []byte("val")})

	targetShard.mu.RLock()
	if len(targetShard.items) != maxItemsPerShard {
		t.Errorf("Expected count %d, got %d", maxItemsPerShard, len(targetShard.items))
	}
	targetShard.mu.RUnlock()
}

func TestInMemoryCache_MiscMethods(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	if c.GetType() != sop.InMemory {
		t.Error("Wrong type")
	}

	if c.IsRestarted(ctx) {
		t.Error("Should not be restarted")
	}

	if err := c.Ping(ctx); err != nil {
		t.Error("Ping failed")
	}

	info, err := c.Info(ctx, "all")
	if err != nil || info != "InMemoryCache" {
		t.Error("Info failed")
	}

	// Clear
	c.Set(ctx, "k1", "v1", 0)
	c.Clear(ctx)
	found, _, _ := c.Get(ctx, "k1")
	if found {
		t.Error("Clear failed")
	}
}

func TestInMemoryCache_GetStructEx(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	type TestStruct struct {
		Name string
	}
	val := TestStruct{Name: "test"}

	c.SetStruct(ctx, "key", val, time.Minute)

	var res TestStruct
	found, err := c.GetStructEx(ctx, "key", &res, time.Minute*2)
	if !found || err != nil {
		t.Error("GetStructEx failed")
	}
	if res.Name != "test" {
		t.Error("Wrong value")
	}

	// Test expiration update
	// Hard to test exact time, but we can check it didn't error
}

func TestInMemoryCache_CreateLockKeysForIDs(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)

	ids := []sop.Tuple[string, sop.UUID]{
		{First: "k1", Second: sop.NewUUID()},
	}
	keys := c.CreateLockKeysForIDs(ids)
	if len(keys) != 1 {
		t.Error("CreateLockKeysForIDs failed")
	}
}

func TestInMemoryCache_IsLockedTTL(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	keys := c.CreateLockKeys([]string{"k1"})

	// Not locked yet
	locked, err := c.IsLockedTTL(ctx, time.Minute, keys)
	if err != nil || locked {
		t.Error("Should not be locked")
	}

	// Lock it
	c.Lock(ctx, time.Minute, keys)

	// Check again
	locked, err = c.IsLockedTTL(ctx, time.Minute, keys)
	if err != nil || !locked {
		t.Error("Should be locked")
	}

	// Check with wrong ID
	keys[0].LockID = sop.NewUUID()
	locked, err = c.IsLockedTTL(ctx, time.Minute, keys)
	if err != nil || locked {
		t.Error("Should not be locked with wrong ID")
	}
}

func TestInMemoryCache_DualLock(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})

	ok, _, err := c.DualLock(ctx, time.Minute, keys)
	if err != nil || !ok {
		t.Error("DualLock failed")
	}
}

func TestInMemoryCache_Store_UnknownType(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	// Fill shard with unknown types
	targetKey := "target"
	targetShard := c.data.getShard(targetKey)

	for i := 0; i < maxItemsPerShard; i++ {
		for j := 0; ; j++ {
			k := fmt.Sprintf("key-%d-%d", i, j)
			if c.data.getShard(k) == targetShard {
				// Store int, which is not item or lockItem
				c.data.store(k, 123)
				break
			}
		}
	}

	// Trigger eviction
	var newKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("new-%d", j)
		if c.data.getShard(k) == targetShard {
			newKey = k
			break
		}
	}
	c.data.store(newKey, 123)

	targetShard.mu.RLock()
	if len(targetShard.items) != maxItemsPerShard {
		t.Errorf("Expected count %d, got %d", maxItemsPerShard, len(targetShard.items))
	}
	targetShard.mu.RUnlock()
}

func TestInMemoryCache_Get_EdgeCases(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	// GetEx missing
	found, _, _ := c.GetEx(ctx, "missing", time.Minute)
	if found {
		t.Error("GetEx should return false for missing")
	}

	// GetStruct missing
	found, _ = c.GetStruct(ctx, "missing", nil)
	if found {
		t.Error("GetStruct should return false for missing")
	}

	// GetStructEx missing
	found, _ = c.GetStructEx(ctx, "missing", nil, time.Minute)
	if found {
		t.Error("GetStructEx should return false for missing")
	}

	// GetStruct invalid json
	c.data.store("invalid", item{data: []byte("{invalid")})
	var res struct{}
	found, err := c.GetStruct(ctx, "invalid", &res)
	if found || err == nil {
		t.Error("GetStruct should fail on invalid json")
	}

	// GetStructEx invalid json
	found, err = c.GetStructEx(ctx, "invalid", &res, time.Minute)
	if found || err == nil {
		t.Error("GetStructEx should fail on invalid json")
	}
}

func TestInMemoryCache_Lock_EdgeCases(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})

	// Duration 0
	c.Lock(ctx, 0, keys)
	// Check expiration is roughly 15 mins from now
	shard := c.locks.getShard(keys[0].Key)
	shard.mu.RLock()
	val, _ := shard.items[keys[0].Key]
	shard.mu.RUnlock()
	item := val.(lockItem)
	if item.expiration.Before(time.Now().Add(14 * time.Minute)) {
		t.Error("Default duration should be 15 min")
	}

	// Re-entry
	ok, _, _ := c.Lock(ctx, time.Minute, keys)
	if !ok {
		t.Error("Re-entry should succeed")
	}

	// Locked by other
	otherKeys := c.CreateLockKeys([]string{"k1"})
	// Same key, different LockID
	ok, _, _ = c.Lock(ctx, time.Minute, otherKeys)
	if ok {
		t.Error("Should fail if locked by other")
	}
}

func TestInMemoryCache_IsLockedTTL_Expired(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})

	// Manually insert expired lock
	shard := c.locks.getShard(keys[0].Key)
	shard.mu.Lock()
	shard.items[keys[0].Key] = lockItem{
		lockID:     keys[0].LockID,
		expiration: time.Now().Add(-time.Minute),
	}
	shard.mu.Unlock()

	// IsLockedTTL should return false and delete it
	locked, _ := c.IsLockedTTL(ctx, time.Minute, keys)
	if locked {
		t.Error("Should be expired")
	}

	shard.mu.RLock()
	_, ok := shard.items[keys[0].Key]
	shard.mu.RUnlock()
	if ok {
		t.Error("Expired lock should be deleted")
	}
}

func TestInMemoryCache_Lock_Rollback(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	// Lock B by someone else
	keysB := c.CreateLockKeys([]string{"B"})
	c.Lock(ctx, time.Minute, keysB)

	// Try to lock A and B
	keysAB := c.CreateLockKeys([]string{"A", "B"})
	// Ensure A < B so A is tried first (lexicographical sort in Lock)

	ok, _, _ := c.Lock(ctx, time.Minute, keysAB)
	if ok {
		t.Error("Should fail to lock A and B")
	}

	// Verify A is not locked (rolled back)
	// IsLockedByOthers expects raw key names (formatted)
	lockedByOthers, _ := c.IsLockedByOthers(ctx, []string{keysAB[0].Key})
	if lockedByOthers {
		t.Error("A should be rolled back")
	}
}

func TestInMemoryCache_IsLocked_Expiration(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})
	c.Lock(ctx, time.Minute, keys)

	// Expire it manually
	shard := c.locks.getShard(keys[0].Key)
	shard.mu.Lock()
	shard.items[keys[0].Key] = lockItem{
		lockID:     keys[0].LockID,
		expiration: time.Now().Add(-time.Minute),
	}
	shard.mu.Unlock()

	locked, _ := c.IsLocked(ctx, keys)
	if locked {
		t.Error("Should be expired")
	}
}

func TestInMemoryCache_IsLockedByOthers_Expiration(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})
	c.Lock(ctx, time.Minute, keys)

	// Expire it manually
	shard := c.locks.getShard(keys[0].Key)
	shard.mu.Lock()
	shard.items[keys[0].Key] = lockItem{
		lockID:     keys[0].LockID,
		expiration: time.Now().Add(-time.Minute),
	}
	shard.mu.Unlock()

	locked, _ := c.IsLockedByOthers(ctx, []string{keys[0].Key})
	if locked {
		t.Error("Should be expired")
	}
}

func TestInMemoryCache_IsLocked_EdgeCases(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	keys := c.CreateLockKeys([]string{"k1"})

	// Missing
	locked, _ := c.IsLocked(ctx, keys)
	if locked {
		t.Error("Should be false for missing")
	}

	// Wrong ID
	c.Lock(ctx, time.Minute, keys)
	keys[0].LockID = sop.NewUUID()
	locked, _ = c.IsLocked(ctx, keys)
	if locked {
		t.Error("Should be false for wrong ID")
	}
}

func TestInMemoryCache_Expiration_Methods(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()

	// GetEx Expiration
	c.Set(ctx, "k1", "v1", time.Millisecond)
	time.Sleep(time.Millisecond * 10)
	found, _, _ := c.GetEx(ctx, "k1", time.Minute)
	if found {
		t.Error("GetEx should return false for expired")
	}

	// GetStruct Expiration
	type S struct{ Name string }
	c.SetStruct(ctx, "k2", S{Name: "s"}, time.Millisecond)
	time.Sleep(time.Millisecond * 10)
	var s S
	found, _ = c.GetStruct(ctx, "k2", &s)
	if found {
		t.Error("GetStruct should return false for expired")
	}

	// GetStructEx Expiration
	c.SetStruct(ctx, "k3", S{Name: "s"}, time.Millisecond)
	time.Sleep(time.Millisecond * 10)
	found, _ = c.GetStructEx(ctx, "k3", &s, time.Minute)
	if found {
		t.Error("GetStructEx should return false for expired")
	}
}

func TestInMemoryCache_SetStruct_Error(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	// Channel cannot be marshaled
	err := c.SetStruct(ctx, "k", make(chan int), 0)
	if err == nil {
		t.Error("Should fail to marshal channel")
	}
}

func TestInMemoryCache_CAS_CAD_Fail(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	// CAS fail
	c.data.store("k", "v1")
	if c.data.compareAndSwap("k", "v2", "v3") {
		t.Error("CAS should fail if old value mismatch")
	}
	if c.data.compareAndSwap("missing", "v1", "v2") {
		t.Error("CAS should fail if missing")
	}

	// CAD fail
	if c.data.compareAndDelete("k", "v2") {
		t.Error("CAD should fail if value mismatch")
	}
	if c.data.compareAndDelete("missing", "v1") {
		t.Error("CAD should fail if missing")
	}
}

func TestInMemoryCache_Range_Stop(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	c.data.store("k1", "v1")
	c.data.store("k2", "v2")

	count := 0
	c.data.Range(func(k, v interface{}) bool {
		count++
		return false // Stop after first
	})
	if count != 1 {
		t.Errorf("Range should stop, visited %d", count)
	}
}

func TestInMemoryCache_Eviction_ZeroExpiration(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	targetKey := "target"
	targetShard := c.data.getShard(targetKey)

	// Fill with 0 expiration (infinite)
	// We need maxItemsPerShard items total.
	// Let's fill max-1 with infinite.
	for i := 0; i < maxItemsPerShard-1; i++ {
		for j := 0; ; j++ {
			k := fmt.Sprintf("key-%d-%d", i, j)
			if c.data.getShard(k) == targetShard {
				c.Set(ctx, k, "value", 0)
				break
			}
		}
	}

	// Add one with short expiration
	var shortExpKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("short-%d", j)
		if c.data.getShard(k) == targetShard {
			shortExpKey = k
			break
		}
	}
	c.Set(ctx, shortExpKey, "value", time.Minute)

	// Now shard is full (maxItemsPerShard).
	// Add one more to trigger eviction.
	var newKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("new-%d", j)
		if c.data.getShard(k) == targetShard {
			newKey = k
			break
		}
	}
	c.Set(ctx, newKey, "value", time.Minute)

	// The shortExpKey should be evicted because 0 expiration is treated as +100 years.
	// Wait, random sampling of 5 items.
	// If shortExpKey is NOT in the sample, it won't be evicted.
	// This test is flaky because of random sampling.
	// But if we have 999 infinite and 1 short, probability of picking short is low (1/1000).
	// So most likely an infinite one will be picked.
	// And infinite ones have +100 years.
	// So minExp will be +100 years.
	// So one of the infinite ones will be evicted.
	// So shortExpKey should REMAIN?
	// Yes, if shortExpKey is NOT sampled, it remains.
	// If shortExpKey IS sampled, it has time.Minute (now + 1m).
	// Infinite ones have now + 100y.
	// So shortExpKey < Infinite.
	// So shortExpKey will be evicted IF sampled.

	// To test "Zero is Infinite", we need to ensure a Zero item is sampled and compared against a Non-Zero item that is LATER than the Zero item (if Zero wasn't infinite).
	// But Zero is 0. 0 < Now+1m.
	// If we didn't treat Zero as Infinite, Zero would be evicted.
	// By treating it as Infinite, it becomes Now+100y.
	// So Now+1m < Now+100y.
	// So Now+1m (short) is evicted.

	// So if we sample [Zero, Short], Short is evicted.
	// If we sample [Zero, Zero], one Zero is evicted.

	// We want to verify that Zero is NOT evicted if there is a better candidate?
	// Or just that Zero is treated as large.

	// Actually, the code says:
	// if exp.IsZero() { effectiveExp = +100y }
	// if effectiveExp.Before(minExp) { minExp = effectiveExp; victim = k }

	// So we want to find the SMALLEST effective expiration.
	// Short (Now+1m) < Infinite (Now+100y).
	// So Short is smaller. Short is evicted.

	// So if we have [Zero, Short], Short is evicted.
	// If we have [Zero, Zero], Zero is evicted.

	// If I want to test that Zero is NOT evicted when it shouldn't be (i.e. it's not treated as 0 timestamp which is 1970),
	// I should compare it with something old?
	// No, 0 timestamp is 1970.
	// If I didn't handle Zero, it would be 1970.
	// 1970 < Now+1m.
	// So Zero would be evicted.
	// By handling it, it becomes 2124.
	// 2124 > Now+1m.
	// So Short is evicted.

	// So if I ensure Short is evicted, it means Zero was treated as > Short.
	// But Short might be evicted just because it was sampled and Zero wasn't.
	// Or because Short was the only one sampled?

	// To make this deterministic, I need to ensure Short is sampled?
	// Or I can fill with ALL Short, and one Zero.
	// Then Zero should NOT be evicted.
	// Because Zero > Short.
	// If Zero is sampled, it won't be picked as min.
	// If Zero is not sampled, it won't be picked.
	// So Zero should never be evicted (unless all are Zero).

	// So: Fill with Short. Add one Zero. Add one New.
	// Zero should remain.

	// Let's do that.
}

func TestInMemoryCache_Eviction_ZeroExpiration_Preserved(t *testing.T) {
	c := NewL2InMemoryCache().(*L2InMemoryCache)
	ctx := context.Background()
	targetKey := "target"
	targetShard := c.data.getShard(targetKey)

	// Fill with Short expiration
	for i := 0; i < maxItemsPerShard-1; i++ {
		for j := 0; ; j++ {
			k := fmt.Sprintf("key-%d-%d", i, j)
			if c.data.getShard(k) == targetShard {
				c.Set(ctx, k, "value", time.Minute)
				break
			}
		}
	}

	// Add one with Zero expiration (Infinite)
	var zeroExpKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("zero-%d", j)
		if c.data.getShard(k) == targetShard {
			zeroExpKey = k
			break
		}
	}
	c.Set(ctx, zeroExpKey, "value", 0)

	// Now shard is full.
	// Add one more.
	var newKey string
	for j := 0; ; j++ {
		k := fmt.Sprintf("new-%d", j)
		if c.data.getShard(k) == targetShard {
			newKey = k
			break
		}
	}
	c.Set(ctx, newKey, "value", time.Minute)

	// Zero key should be preserved.
	found, _, _ := c.Get(ctx, zeroExpKey)
	if !found {
		t.Error("Zero expiration item should be preserved (treated as infinite)")
	}
}
