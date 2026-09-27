package cache

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

type item struct {
	data       []byte
	expiration time.Time
}

type lockItem struct {
	lockID     sop.UUID
	expiration time.Time
}

type L2InMemoryCache struct {
	data       *shardedMap
	locks      *shardedMap
	standalone bool
}

// DefaultInMemoryCacheShardCapacity is the default per-shard entry ceiling for the in-memory L2 cache.
// In standalone mode, L1 is intentionally smaller while L2 carries the broader hot-node working set.
// With 256 shards, this provides a total capacity of 256,000 entries (1000 × 256).
var DefaultInMemoryCacheShardCapacity = 1000

func NewL2InMemoryCache() sop.L2Cache {
	return newL2InMemoryCache(false)
}

// NewStandaloneL2InMemoryCache creates an in-memory L2 cache suitable for standalone processes.
func NewStandaloneL2InMemoryCache() sop.L2Cache {
	return newL2InMemoryCache(true)
}

func newL2InMemoryCache(standalone bool) sop.L2Cache {
	capacity := DefaultInMemoryCacheShardCapacity
	return &L2InMemoryCache{
		data:       newShardedMap(capacity),
		locks:      newShardedMap(capacity),
		standalone: standalone,
	}
}

// Returns InMemoryCache as L2Cache type.
func (c *L2InMemoryCache) GetType() sop.L2CacheType {
	return sop.InMemory
}

func (c *L2InMemoryCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	// log.Debug("entered Set")
	var exp time.Time
	if expiration > 0 {
		exp = time.Now().Add(expiration)
	}

	c.data.store(key, item{
		data:       []byte(value),
		expiration: exp,
	})
	return nil
}

func (c *L2InMemoryCache) Get(ctx context.Context, key string) (bool, string, error) {
	// log.Debug("entered Get")
	val, ok := c.data.load(key)
	if !ok {
		return false, "", nil
	}
	it := val.(item)

	if !it.expiration.IsZero() && time.Now().After(it.expiration) {
		c.data.delete(key)
		return false, "", nil
	}

	return true, string(it.data), nil
}

func (c *L2InMemoryCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	// log.Debug("entered GetEx")
	val, ok := c.data.load(key)
	if !ok {
		return false, "", nil
	}
	it := val.(item)

	if !it.expiration.IsZero() && time.Now().After(it.expiration) {
		c.data.delete(key)
		return false, "", nil
	}

	if expiration > 0 {
		it.expiration = time.Now().Add(expiration)
		c.data.store(key, it)
	}

	return true, string(it.data), nil
}

func (c *L2InMemoryCache) IsRestarted(ctx context.Context) bool {
	return false
}

func (c *L2InMemoryCache) SetStruct(ctx context.Context, key string, value any, expiration time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	var exp time.Time
	if expiration > 0 {
		exp = time.Now().Add(expiration)
	}

	c.data.store(key, item{
		data:       data,
		expiration: exp,
	})
	return nil
}

func (c *L2InMemoryCache) SetStructs(ctx context.Context, keys []string, values []any, expiration time.Duration) error {
	for i, key := range keys {
		if err := c.SetStruct(ctx, key, values[i], expiration); err != nil {
			return err
		}
	}
	return nil
}

func (c *L2InMemoryCache) GetStruct(ctx context.Context, key string, target any) (bool, error) {
	// log.Debug("entered GetStruct")
	val, ok := c.data.load(key)
	if !ok {
		return false, nil
	}
	it := val.(item)

	if !it.expiration.IsZero() && time.Now().After(it.expiration) {
		c.data.delete(key)
		return false, nil
	}

	if err := json.Unmarshal(it.data, target); err != nil {
		return false, err
	}

	return true, nil
}

func (c *L2InMemoryCache) GetStructEx(ctx context.Context, key string, target any, expiration time.Duration) (bool, error) {
	// log.Debug("entered GetStructEx")
	val, ok := c.data.load(key)
	if !ok {
		return false, nil
	}
	it := val.(item)

	if !it.expiration.IsZero() && time.Now().After(it.expiration) {
		c.data.delete(key)
		return false, nil
	}

	if expiration > 0 {
		it.expiration = time.Now().Add(expiration)
		c.data.store(key, it)
	}

	if err := json.Unmarshal(it.data, target); err != nil {
		return false, err
	}

	return true, nil
}

func (c *L2InMemoryCache) GetStructs(ctx context.Context, keys []string, targets []any, expiration time.Duration) ([]bool, error) {
	found := make([]bool, len(keys))
	for i, key := range keys {
		var err error
		var ok bool
		if expiration > 0 {
			ok, err = c.GetStructEx(ctx, key, targets[i], expiration)
		} else {
			ok, err = c.GetStruct(ctx, key, targets[i])
		}
		if err != nil {
			return nil, err
		}
		found[i] = ok
	}
	return found, nil
}

func (c *L2InMemoryCache) Delete(ctx context.Context, keys []string) (bool, error) {
	for _, k := range keys {
		c.data.delete(k)
	}
	return true, nil
}

func (c *L2InMemoryCache) Ping(ctx context.Context) error {
	return nil
}

func (c *L2InMemoryCache) Clear(ctx context.Context) error {
	c.data.Range(func(key, value any) bool {
		c.data.delete(key.(string))
		return true
	})
	return nil
}

func (c *L2InMemoryCache) Info(ctx context.Context, section string) (string, error) {
	return "InMemoryCache", nil
}

// Locking implementation

func (c *L2InMemoryCache) FormatLockKey(k string) string {
	return fmt.Sprintf("lock:%s", k)
}

func (c *L2InMemoryCache) CreateLockKeys(keys []string) []*sop.LockKey {
	locks := make([]*sop.LockKey, len(keys))
	for i, k := range keys {
		locks[i] = &sop.LockKey{
			Key:    c.FormatLockKey(k),
			LockID: sop.NewUUID(),
		}
	}
	return locks
}

func (c *L2InMemoryCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	locks := make([]*sop.LockKey, len(keys))
	for i, k := range keys {
		locks[i] = &sop.LockKey{
			Key:    c.FormatLockKey(k.First),
			LockID: k.Second,
		}
	}
	return locks
}

func (c *L2InMemoryCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	// log.Debug("entered IsLockedTTL")
	// 1. Check if all keys are locked by us and valid
	for _, lk := range lockKeys {
		val, ok := c.locks.load(lk.Key)
		if !ok {
			return false, nil
		}
		item := val.(lockItem)
		if item.lockID != lk.LockID {
			return false, nil
		}
		if time.Now().After(item.expiration) {
			c.locks.compareAndDelete(lk.Key, val)
			return false, nil
		}
	}

	// 2. Refresh TTL
	newExp := time.Now().Add(duration)
	for _, lk := range lockKeys {
		for {
			val, ok := c.locks.load(lk.Key)
			if !ok {
				return false, nil
			}
			item := val.(lockItem)
			if item.lockID != lk.LockID {
				return false, nil
			}
			newItem := lockItem{
				lockID:     item.lockID,
				expiration: newExp,
			}
			if c.locks.compareAndSwap(lk.Key, item, newItem) {
				break
			}
		}
	}

	return true, nil
}

func (c *L2InMemoryCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	// log.Debug("entered Lock")
	if duration <= 0 {
		duration = 15 * time.Minute
	}

	// Sort keys to avoid deadlocks/livelocks (A->B vs B->A)
	slices.SortFunc(lockKeys, func(a, b *sop.LockKey) int {
		return cmp.Compare(a.Key, b.Key)
	})

	acquired := make([]*sop.LockKey, 0, len(lockKeys))

	for _, lk := range lockKeys {
		newItem := lockItem{
			lockID:     lk.LockID,
			expiration: time.Now().Add(duration),
		}

		// Try to load or store
		val, loaded := c.locks.loadOrStore(lk.Key, newItem)
		if loaded {
			// Item exists
			existing := val.(lockItem)

			// Check if expired
			if time.Now().After(existing.expiration) {
				// Expired. Try to CAS.
				if c.locks.compareAndSwap(lk.Key, existing, newItem) {
					// Success
					acquired = append(acquired, lk)
					lk.IsLockOwner = true
					continue
				}
				// CAS failed, someone else took it.
				// Fall through to failure.
			} else {
				// Not expired. Check re-entry.
				if existing.lockID == lk.LockID {
					// Already owned by us.
					lk.IsLockOwner = true
					continue
				}
			}

			// Failed to acquire. Rollback newly acquired locks.
			for _, acquiredLk := range acquired {
				if v, ok := c.locks.load(acquiredLk.Key); ok {
					if v.(lockItem).lockID == acquiredLk.LockID {
						c.locks.compareAndDelete(acquiredLk.Key, v)
					}
				}
				acquiredLk.IsLockOwner = false
			}
			return false, existing.lockID, nil
		}

		// Success (Stored)
		acquired = append(acquired, lk)
		lk.IsLockOwner = true
	}

	return true, sop.NilUUID, nil
}

func (c *L2InMemoryCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, owner, err := c.Lock(ctx, duration, lockKeys)
	if err != nil || !ok {
		return ok, owner, err
	}
	// Verify lock acquisition
	isLocked, err := c.IsLocked(ctx, lockKeys)
	if err != nil {
		return false, sop.NilUUID, err
	}
	if !isLocked {
		return false, sop.NilUUID, nil
	}
	return true, sop.NilUUID, nil
}

func (c *L2InMemoryCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	// log.Debug("entered IsLocked")
	for _, lk := range lockKeys {
		val, ok := c.locks.load(lk.Key)
		if !ok {
			return false, nil
		}
		item := val.(lockItem)
		if item.lockID != lk.LockID {
			return false, nil
		}
		if time.Now().After(item.expiration) {
			return false, nil
		}
	}
	return true, nil
}

func (c *L2InMemoryCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	// log.Debug("entered IsLockedByOthers")
	for _, key := range lockKeyNames {
		val, ok := c.locks.load(key)
		if ok {
			item := val.(lockItem)
			if time.Now().After(item.expiration) {
				continue
			}
			return true, nil
		}
	}
	return false, nil
}

func (c *L2InMemoryCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	// log.Debug("entered IsLockedByOthersTTL")
	return true, nil
}

func (c *L2InMemoryCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	for _, lk := range lockKeys {
		val, ok := c.locks.load(lk.Key)
		if ok {
			item := val.(lockItem)
			if item.lockID == lk.LockID {
				c.locks.compareAndDelete(lk.Key, val)
			}
		}
	}
	return nil
}

func init() {
	sop.RegisterL2CacheFactory(sop.InMemory, func(sop.TransactionOptions) sop.L2Cache { return NewL2InMemoryCache() })
}
