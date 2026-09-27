//go:build integration
// +build integration

package database_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/database"
)

type commitToggleRedisCache struct {
	base       sop.L2Cache
	mu         sync.Mutex
	failCommit bool
}

func newCommitToggleRedisCache() *commitToggleRedisCache {
	return &commitToggleRedisCache{base: mocks.NewMockClient()}
}

func (m *commitToggleRedisCache) EnableCommitFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCommit = true
}

func (m *commitToggleRedisCache) shouldFailCommit() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failCommit
}

func (m *commitToggleRedisCache) GetType() sop.L2CacheType { return sop.Redis }
func (m *commitToggleRedisCache) Set(ctx context.Context, key string, value string, expiration time.Duration) error {
	return m.base.Set(ctx, key, value, expiration)
}
func (m *commitToggleRedisCache) Get(ctx context.Context, key string) (bool, string, error) {
	return m.base.Get(ctx, key)
}
func (m *commitToggleRedisCache) GetEx(ctx context.Context, key string, expiration time.Duration) (bool, string, error) {
	return m.base.GetEx(ctx, key, expiration)
}
func (m *commitToggleRedisCache) IsRestarted(ctx context.Context) bool {
	return m.base.IsRestarted(ctx)
}
func (m *commitToggleRedisCache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return m.base.SetStruct(ctx, key, value, expiration)
}
func (m *commitToggleRedisCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return m.base.SetStructs(ctx, keys, values, expiration)
}
func (m *commitToggleRedisCache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return m.base.GetStruct(ctx, key, target)
}
func (m *commitToggleRedisCache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	return m.base.GetStructEx(ctx, key, target, expiration)
}
func (m *commitToggleRedisCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return m.base.GetStructs(ctx, keys, targets, expiration)
}
func (m *commitToggleRedisCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return m.base.Delete(ctx, keys)
}
func (m *commitToggleRedisCache) Ping(ctx context.Context) error { return nil }
func (m *commitToggleRedisCache) FormatLockKey(k string) string  { return m.base.FormatLockKey(k) }
func (m *commitToggleRedisCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.base.CreateLockKeys(keys)
}
func (m *commitToggleRedisCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.base.CreateLockKeysForIDs(keys)
}
func (m *commitToggleRedisCache) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	return m.base.IsLockedTTL(ctx, duration, lockKeys)
}
func (m *commitToggleRedisCache) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	if m.shouldFailCommit() {
		return false, sop.UUID{}, fmt.Errorf("redis unavailable during commit")
	}
	return m.base.Lock(ctx, duration, lockKeys)
}
func (m *commitToggleRedisCache) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	if m.shouldFailCommit() {
		return false, sop.UUID{}, fmt.Errorf("redis unavailable during commit")
	}
	return m.base.DualLock(ctx, duration, lockKeys)
}
func (m *commitToggleRedisCache) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	return m.base.IsLocked(ctx, lockKeys)
}
func (m *commitToggleRedisCache) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	return m.base.IsLockedByOthers(ctx, lockKeyNames)
}
func (m *commitToggleRedisCache) IsLockedByOthersTTL(ctx context.Context, lockKeyNames []string, duration time.Duration) (bool, error) {
	return m.base.IsLockedByOthersTTL(ctx, lockKeyNames, duration)
}
func (m *commitToggleRedisCache) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	return m.base.Unlock(ctx, lockKeys)
}
func (m *commitToggleRedisCache) Clear(ctx context.Context) error { return m.base.Clear(ctx) }

func TestDatabase_Clustered_Construction(t *testing.T) {
	// This test verifies we can construct a clustered database and attempt to open
	// a real transaction when integration tests explicitly opt in.
	db, err := database.ValidateOptions(sop.DatabaseOptions{
		CacheType:     sop.Redis,
		StoresFolders: []string{t.TempDir()},
	})
	if err != nil {
		t.Fatal("ValidateOptions returned error for Clustered")
	}

	ctx := context.Background()
	_, err = database.BeginTransaction(ctx, db, sop.ForWriting)
	if err == nil {
		t.Log("BeginTransaction succeeded")
		return
	}

	t.Logf("BeginTransaction failed as expected when Redis is unavailable: %v", err)
}

func TestCommit_ReturnsErrorWhenRedisBecomesUnavailable(t *testing.T) {
	uniqueAddr := fmt.Sprintf("commit-unreachable-%d:6379", time.Now().UnixNano())
	cache := newCommitToggleRedisCache()
	sop.RegisterL2CacheFactory(sop.Redis, func(opts sop.TransactionOptions) sop.L2Cache {
		return cache
	})
	t.Cleanup(func() {
		sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
	})

	db, err := database.ValidateOptions(sop.DatabaseOptions{
		Type:          sop.Clustered,
		CacheType:     sop.Redis,
		StoresFolders: []string{t.TempDir()},
		RedisConfig: &sop.RedisCacheConfig{
			Address: uniqueAddr,
		},
	})
	if err != nil {
		t.Fatalf("ValidateOptions returned error: %v", err)
	}

	ctx := context.Background()
	tx, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed unexpectedly: %v", err)
	}

	store, err := database.NewBtree[string, string](ctx, db, "test_store", tx, nil)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if _, err := store.Add(ctx, "key1", "value1"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	cache.EnableCommitFailure()
	err = tx.Commit(ctx)
	if err == nil {
		t.Fatal("expected Commit to return error when Redis fails during commit")
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"redis unavailable during commit"}) {
		t.Fatalf("expected commit redis error, got %v", err)
	}
}

func containsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
