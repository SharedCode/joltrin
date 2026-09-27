//go:build integration
// +build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
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

func TestRunIngestImportSpace_PropagatesCommitError(t *testing.T) {
	oldConfig := config
	defer func() { config = oldConfig }()

	uniqueAddr := fmt.Sprintf("ingest-import-commit-%d:6379", time.Now().UnixNano())
	config = Config{
		Databases: []DatabaseConfig{{Name: "testdb", Path: t.TempDir(), Mode: "clustered", RedisURL: uniqueAddr}},
	}

	cache := newCommitToggleRedisCache()
	sop.RegisterL2CacheFactory(sop.Redis, func(opts sop.TransactionOptions) sop.L2Cache {
		return cache
	})
	defer sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)

	request := IngestSpaceRequest{
		DatabaseName: "testdb",
		SpaceName:    "testspace",
		CustomData:   json.RawMessage(`{"categories":[{"id":"11111111-1111-1111-1111-111111111111","name":"Engineering"}],"items":[{"category":"11111111-1111-1111-1111-111111111111","data":{"text":"We use SOP","description":"commit failure repro"},"summaries":["SOP usage"]}]}`),
	}

	err := runIngestImportSpace(context.Background(), request, nil, &MockGenerator{}, nil, cache.EnableCommitFailure)
	if err == nil {
		t.Fatal("expected ingest/import helper to return commit error")
	}
	if !strings.Contains(err.Error(), "failed to commit ingest import") {
		t.Fatalf("expected commit wrapper error, got %v", err)
	}
	if !strings.Contains(err.Error(), "redis unavailable during commit") {
		t.Fatalf("expected redis commit error, got %v", err)
	}
}

func TestRunIngestSpace_PropagatesCommitError(t *testing.T) {
	oldConfig := config
	defer func() { config = oldConfig }()

	uniqueAddr := fmt.Sprintf("ingest-space-commit-%d:6379", time.Now().UnixNano())
	config = Config{
		Databases: []DatabaseConfig{{Name: "testdb", Path: t.TempDir(), Mode: "clustered", RedisURL: uniqueAddr}},
	}

	cache := newCommitToggleRedisCache()
	sop.RegisterL2CacheFactory(sop.Redis, func(opts sop.TransactionOptions) sop.L2Cache {
		return cache
	})
	defer sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)

	request := IngestSpaceRequest{
		DatabaseName: "testdb",
		SpaceName:    "medical",
		CustomData:   json.RawMessage(`{"items":[{"id":"11111111-1111-1111-1111-111111111111","category":"medical","data":{"text":"Redis failure repro","description":"commit should fail"},"summaries":["medical preload"]}]}`),
	}

	err := runIngestSpace(context.Background(), request, nil, &MockGenerator{}, nil, cache.EnableCommitFailure)
	if err == nil {
		t.Fatal("expected ingest helper to return commit error")
	}
	if !strings.Contains(err.Error(), "failed to commit vector store initialization") {
		t.Fatalf("expected commit wrapper error, got %v", err)
	}
	if !strings.Contains(err.Error(), "redis unavailable during commit") {
		t.Fatalf("expected redis commit error, got %v", err)
	}
}
