package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// TestRegistry_AllScenarios consolidates registry & registryMap behaviors into one table-driven suite.
// It merges positive flows, error branches, replication edge cases, cache TTL paths, and helper coverage.
// Local helper types for lock behavior.
type testLockFail struct{ sop.L2Cache }

func (lf testLockFail) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.UUID{}, nil
}
func (lf testLockFail) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return lf.Lock(ctx, d, lk)
}
func (lf testLockFail) Unlock(ctx context.Context, lk []*sop.LockKey) error { return nil }
func (lf testLockFail) IsRestarted(ctx context.Context) bool                { return false }

type testAllLock struct{ sop.L2Cache }

func (al testAllLock) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return true, sop.UUID{}, nil
}
func (al testAllLock) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return al.Lock(ctx, d, lk)
}
func (al testAllLock) Unlock(ctx context.Context, lk []*sop.LockKey) error { return nil }
func (al testAllLock) IsRestarted(ctx context.Context) bool                { return false }

// Shared test fixtures (were previously in registry_test.go) still needed by other *_test files.
var uuid, _ = sop.ParseUUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
var hashMod = MinimumModValue

type setFail struct{ registryMap }

func (sf *setFail) set(ctx context.Context, p []sop.RegistryPayload[sop.Handle]) error {
	return errors.New("induced set error")
}

// cacheGetError induces an error on first GetStruct/GetStructEx to exercise registry.Get cache error path.
type cacheGetError struct {
	base    sop.L2Cache
	tripped bool
}

func newCacheGetError() *cacheGetError { return &cacheGetError{base: mocks.NewMockClient()} }

func (c *cacheGetError) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c *cacheGetError) Set(ctx context.Context, k, v string, d time.Duration) error {
	return c.base.Set(ctx, k, v, d)
}
func (c *cacheGetError) Get(ctx context.Context, k string) (bool, string, error) {
	return c.base.Get(ctx, k)
}
func (c *cacheGetError) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return c.base.GetEx(ctx, k, d)
}
func (c *cacheGetError) Ping(ctx context.Context) error { return nil }
func (c *cacheGetError) SetStruct(ctx context.Context, k string, v interface{}, d time.Duration) error {
	return c.base.SetStruct(ctx, k, v, d)
}
func (c *cacheGetError) SetStructs(ctx context.Context, keys []string, values []interface{}, duration time.Duration) error {
	return c.base.SetStructs(ctx, keys, values, duration)
}
func (c *cacheGetError) GetStruct(ctx context.Context, k string, v interface{}) (bool, error) {
	if !c.tripped {
		c.tripped = true
		return false, fmt.Errorf("induced getstruct error")
	}
	return c.base.GetStruct(ctx, k, v)
}
func (c *cacheGetError) GetStructEx(ctx context.Context, k string, v interface{}, d time.Duration) (bool, error) {
	if !c.tripped {
		c.tripped = true
		return false, fmt.Errorf("induced getstructex error")
	}
	return c.base.GetStructEx(ctx, k, v, d)
}
func (c *cacheGetError) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	if !c.tripped {
		c.tripped = true
		return nil, fmt.Errorf("induced getstructs error")
	}
	return c.base.GetStructs(ctx, keys, targets, expiration)
}
func (c *cacheGetError) Delete(ctx context.Context, ks []string) (bool, error) {
	return c.base.Delete(ctx, ks)
}
func (c *cacheGetError) FormatLockKey(k string) string { return c.base.FormatLockKey(k) }
func (c *cacheGetError) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.base.CreateLockKeys(keys)
}
func (c *cacheGetError) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.base.CreateLockKeysForIDs(keys)
}
func (c *cacheGetError) IsLockedTTL(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, error) {
	return c.base.IsLockedTTL(ctx, d, lks)
}
func (c *cacheGetError) Lock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return c.base.Lock(ctx, d, lks)
}
func (c *cacheGetError) DualLock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return c.base.DualLock(ctx, d, lks)
}
func (c *cacheGetError) IsLocked(ctx context.Context, lks []*sop.LockKey) (bool, error) {
	return c.base.IsLocked(ctx, lks)
}
func (c *cacheGetError) IsLockedByOthers(ctx context.Context, ks []string) (bool, error) {
	return c.base.IsLockedByOthers(ctx, ks)
}
func (c *cacheGetError) IsLockedByOthersTTL(ctx context.Context, ks []string, d time.Duration) (bool, error) {
	return c.base.IsLockedByOthersTTL(ctx, ks, d)
}
func (c *cacheGetError) Unlock(ctx context.Context, lks []*sop.LockKey) error {
	return c.base.Unlock(ctx, lks)
}
func (c *cacheGetError) Clear(ctx context.Context) error { return c.base.Clear(ctx) }
func (c *cacheGetError) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (c *cacheGetError) IsRestarted(ctx context.Context) bool { return false }

// mockCacheImmediateLockFail forces Lock to fail to trigger UpdateNoLocks set error path.
type mockCacheImmediateLockFail struct{ sop.L2Cache }

func (m mockCacheImmediateLockFail) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m mockCacheImmediateLockFail) IsRestarted(ctx context.Context) bool { return false }

func (m *mockCacheImmediateLockFail) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, errors.New("induced lock fail")
}

func (m *mockCacheImmediateLockFail) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, d, lk)
}

func TestRegistry_AllScenarios(t *testing.T) {
	// ensureTableDir creates the registry table directory under the active base folder.
	ensureTableDir := func(t *testing.T, rt *replicationTracker, table string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// ensurePassiveTableDir creates the registry table directory under the passive base folder.
	ensurePassiveTableDir := func(t *testing.T, rt *replicationTracker, table string) {
		t.Helper()
		if rt == nil || rt.getPassiveBaseFolder() == "" {
			return
		}
		if err := os.MkdirAll(filepath.Join(rt.getPassiveBaseFolder(), table), 0o755); err != nil {
			t.Fatalf("mkdir passive: %v", err)
		}
	}

	type scenario struct {
		name string
		run  func(t *testing.T)
	}
	ctx := context.Background()
	scenarios := []scenario{
		{name: "AddGetBasic", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "rg")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rg", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("add: %v", err)
			}
			res, err := r.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rg", IDs: []sop.UUID{h.LogicalID}}})
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if len(res) == 0 || len(res[0].IDs) == 0 || res[0].IDs[0].LogicalID != h.LogicalID {
				t.Fatalf("unexpected get result: %+v", res)
			}
		}},
		{name: "UpdateVariantsRemove", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "rgx")
			h1 := sop.NewHandle(sop.NewUUID())
			h2 := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgx", IDs: []sop.Handle{h1, h2}}}); err != nil {
				t.Fatalf("seed add: %v", err)
			}
			h1.Version = 1
			if err := r.Update(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgx", IDs: []sop.Handle{h1}}}); err != nil {
				t.Fatalf("update: %v", err)
			}
			h2.Version = 2
			if err := r.UpdateNoLocks(ctx, true, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgx", IDs: []sop.Handle{h2}}}); err != nil {
				t.Fatalf("update nolocks: %v", err)
			}
			if err := r.Remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rgx", IDs: []sop.UUID{h1.LogicalID}}}); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
		{name: "ReplicationSuccessPath", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			b := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "rgrepl")
			ensurePassiveTableDir(t, rt, "rgrepl")
			hRoot := sop.NewHandle(sop.NewUUID())
			hAdd := sop.NewHandle(sop.NewUUID())
			hUpd := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgrepl", IDs: []sop.Handle{hRoot, hAdd, hUpd}}}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := r.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgrepl", IDs: []sop.Handle{hRoot}}}, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgrepl", IDs: []sop.Handle{hAdd}}}, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgrepl", IDs: []sop.Handle{hUpd}}}, nil); err != nil {
				t.Fatalf("replicate: %v", err)
			}
		}},
		{name: "ReplicationEarlyReturnDisabledOrFailed", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			b := t.TempDir()
			rtNo, _ := NewReplicationTracker(ctx, []string{a, b}, false, l2)
			r1 := NewRegistry(true, MinimumModValue, rtNo, l2)
			defer r1.Close()
			h := sop.NewHandle(sop.NewUUID())
			if err := r1.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "t1", IDs: []sop.Handle{h}}}, nil, nil, nil); err != nil {
				t.Fatalf("disabled replicate: %v", err)
			}
			a2 := t.TempDir()
			b2 := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rtFail, _ := NewReplicationTracker(ctx, []string{a2, b2}, true, l2)
			rtFail.FailedToReplicate = true
			r2 := NewRegistry(true, MinimumModValue, rtFail, l2)
			defer r2.Close()
			if err := r2.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "t2", IDs: []sop.Handle{h}}}, nil, nil, nil); err != nil {
				t.Fatalf("failed replicate early: %v", err)
			}
		}},
		{name: "ReplicationErrorInvalidPassiveRoot", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			active := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			passiveFile := filepath.Join(active, "passive-file")
			os.WriteFile(passiveFile, []byte("x"), 0o600)
			rt, _ := NewReplicationTracker(ctx, []string{active, passiveFile}, true, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "rgep")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgep", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := r.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rgep", IDs: []sop.Handle{h}}}, nil, nil, nil); err == nil {
				t.Fatalf("expected error with file passive root")
			}
		}},
		{name: "ReplicationPartialErrorsSetAddRemove", run: func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("chmod semantics differ")
			}
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			b := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "pr")
			h1 := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "pr", IDs: []sop.Handle{h1}}}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			os.Chmod(b, 0o500)
			h2 := sop.NewHandle(sop.NewUUID())
			err := r.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "pr", IDs: []sop.Handle{h2}}}, nil, nil, nil)
			if err == nil {
				t.Fatalf("expected replication failure")
			}
			if !rt.FailedToReplicate {
				t.Fatalf("expected FailedToReplicate")
			}
			if err := r.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "pr", IDs: []sop.Handle{h2}}}, nil, nil, nil); err != nil {
				t.Fatalf("early return replicate: %v", err)
			}
		}},
		{name: "Get_AllFound_NoFetch", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "geta")
			h1 := sop.NewHandle(sop.NewUUID())
			h2 := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "geta", IDs: []sop.Handle{h1, h2}}}); err != nil {
				t.Fatalf("add: %v", err)
			}
			res, err := r.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "geta", IDs: []sop.UUID{h1.LogicalID, h2.LogicalID}}})
			if err != nil || len(res) != 1 || len(res[0].IDs) != 2 {
				t.Fatalf("unexpected get result: %v %+v", err, res)
			}
		}},
		{name: "Get_ErrorOnCacheGet", run: func(t *testing.T) {
			ctx := context.Background()
			cg := newCacheGetError()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, cg)
			r := NewRegistry(true, MinimumModValue, rt, cg)
			defer r.Close()
			ensureTableDir(t, rt, "geterr")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "geterr", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("add: %v", err)
			}
			res, err := r.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "geterr", IDs: []sop.UUID{h.LogicalID}}})
			if err != nil || len(res) != 1 || len(res[0].IDs) != 1 {
				t.Fatalf("unexpected get result after induced cache error: %v %+v", err, res)
			}
		}},
		{name: "Replicate_CloseOverrideErrors", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			b := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "replc")
			ensurePassiveTableDir(t, rt, "replc")
			h1 := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "replc", IDs: []sop.Handle{h1}}}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			r.rmCloseOverride = func() error { return fmt.Errorf("close override error") }
			if err := r.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "replc", IDs: []sop.Handle{h1}}}, nil, nil, nil); err == nil || err.Error() != "close override error" {
				t.Fatalf("expected close override error, got %v", err)
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			a2 := t.TempDir()
			passiveFile := filepath.Join(t.TempDir(), "pas-file")
			if err := os.WriteFile(passiveFile, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			rt2, _ := NewReplicationTracker(ctx, []string{a2, passiveFile}, true, l2)
			r2 := NewRegistry(true, MinimumModValue, rt2, l2)
			defer r2.Close()
			ensureTableDir(t, rt2, "replcerr")
			h2 := sop.NewHandle(sop.NewUUID())
			if err := r2.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "replcerr", IDs: []sop.Handle{h2}}}); err != nil {
				t.Fatalf("seed2: %v", err)
			}
			r2.rmCloseOverride = func() error { return fmt.Errorf("ignored close error") }
			if err := r2.Replicate(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "replcerr", IDs: []sop.Handle{h2}}}, nil, nil, nil); err == nil || err.Error() == "ignored close error" {
				t.Fatalf("expected earlier replication add error, got %v", err)
			}
		}},
		{name: "Replicate_LayeredErrors_FirstErrorWins", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			active := t.TempDir()
			passiveDir := t.TempDir()
			passiveFile := filepath.Join(passiveDir, "pasfile")
			if err := os.WriteFile(passiveFile, []byte("x"), 0o600); err != nil {
				t.Fatalf("seed passive file: %v", err)
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{active, passiveFile}, true, cache)
			r := NewRegistry(true, MinimumModValue, rt, cache)
			defer r.Close()
			ensureTableDir(t, rt, "rlay")
			// passive is a file; do not create passive table dir to keep the error path
			hSeed := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rlay", IDs: []sop.Handle{hSeed}}}); err != nil {
				t.Fatalf("seed add: %v", err)
			}
			newRoot := sop.NewHandle(sop.NewUUID())
			updated := hSeed
			updated.Version = 2
			remove := sop.NewHandle(sop.NewUUID())
			r.rmCloseOverride = func() error { return errors.New("close override error") }
			if err := r.Replicate(ctx,
				[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "rlay", IDs: []sop.Handle{newRoot}}},
				[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "rlay", IDs: []sop.Handle{hSeed}}},
				[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "rlay", IDs: []sop.Handle{updated}}},
				[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "rlay", IDs: []sop.Handle{remove}}},
			); err == nil {
				t.Fatalf("expected primary replication error")
			}
			if !rt.FailedToReplicate {
				t.Fatalf("expected FailedToReplicate set")
			}
		}},
		{name: "UpdateNoLocks_SetError", run: func(t *testing.T) {
			ctx := context.Background()
			base := t.TempDir()
			lockFailCache := &mockCacheImmediateLockFail{L2Cache: mocks.NewMockClient()}
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, lockFailCache)
			r := NewRegistry(true, MinimumModValue, rt, lockFailCache)
			defer r.Close()
			h := sop.NewHandle(sop.NewUUID())
			if err := r.UpdateNoLocks(ctx, false, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "tbl", IDs: []sop.Handle{h}}}); err == nil {
				t.Fatalf("expected UpdateNoLocks error due to lock failure")
			}
		}},
		{name: "UpdateLockFailureEvict", run: func(t *testing.T) {
			l2seed := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2seed)
			r := NewRegistry(true, MinimumModValue, rt, l2seed)
			defer r.Close()
			ensureTableDir(t, rt, "rg")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rg", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("add: %v", err)
			}
			// Force lock failure path (returns ok=false) and ensure Update returns error.
			r.l2Cache = testLockFail{L2Cache: l2seed}
			if err := r.Update(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rg", IDs: []sop.Handle{h}}}); err == nil {
				t.Fatalf("expected lock fail")
			}
		}},
		{name: "ErrorPathsDuplicateAddRemoveMissingLockFail", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "regerr")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regerr", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("seed add: %v", err)
			}
			ensureTableDir(t, rt, "reglock")
			missing := sop.NewUUID()
			if err := r.Remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "regerr", IDs: []sop.UUID{missing}}}); err == nil || !strings.Contains(err.Error(), "registryMap.remove failed") {
				t.Fatalf("expected remove missing error: %v", err)
			}
			h2 := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "reglock", IDs: []sop.Handle{h2}}}); err != nil {
				t.Fatalf("lock seed: %v", err)
			}
			lk := r.l2Cache.CreateLockKeys([]string{h2.LogicalID.String()})
			if ok, _, e := r.l2Cache.Lock(ctx, time.Minute, lk); !ok || e != nil {
				t.Fatalf("pre-lock: %v", e)
			}
			h2.Version = 1
			if err := r.Update(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "reglock", IDs: []sop.Handle{h2}}}); err == nil || !strings.Contains(err.Error(), "lock failed") {
				t.Fatalf("expected lock failed error: %v", err)
			}
			_ = r.l2Cache.Unlock(ctx, lk)
		}},
		{name: "CacheTTLFetch", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			r := NewRegistry(true, MinimumModValue, rt, l2)
			defer r.Close()
			ensureTableDir(t, rt, "regttl")
			h := sop.NewHandle(sop.NewUUID())
			if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regttl", CacheDuration: time.Minute, IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("seed add: %v", err)
			}
			res, err := r.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "regttl", CacheDuration: time.Minute, IsCacheTTL: true, IDs: []sop.UUID{h.LogicalID}}})
			if err != nil || len(res) == 0 || len(res[0].IDs) == 0 {
				t.Fatalf("ttl get unexpected: %v %+v", err, res)
			}
		}},
		{name: "RegistryMapBasicOps", run: func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			rm := newRegistryMap(true, MinimumModValue, rt, l2)
			defer rm.close()
			ensureTableDir(t, rt, "regmap")
			h := sop.NewHandle(sop.NewUUID())
			if err := rm.add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regmap", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("add: %v", err)
			}
			if err := rm.set(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regmap", IDs: []sop.Handle{h}}}); err != nil {
				t.Fatalf("set: %v", err)
			}
			if _, err := rm.fetch(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "regmap", IDs: []sop.UUID{h.LogicalID}}}); err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if err := rm.remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "regmap", IDs: []sop.UUID{h.LogicalID}}}); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
		{name: "HelperGetIDs", run: func(t *testing.T) {
			hs := []sop.Handle{{LogicalID: sop.NewUUID()}, {LogicalID: sop.NewUUID()}}
			ids := getIDs(hs)
			if len(ids) != len(hs) {
				t.Fatalf("len mismatch")
			}
		}},
		{name: "ConvertToKvp", run: func(t *testing.T) {
			h1 := sop.NewHandle(sop.NewUUID())
			h2 := sop.NewHandle(sop.NewUUID())
			kv := convertToKvp([]sop.Handle{h1, h2})
			if len(kv) != 2 || kv[0].Key != h1.LogicalID || kv[1].Key != h2.LogicalID {
				t.Fatalf("kv mismatch: %+v", kv)
			}
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			prev := GlobalReplicationDetails
			defer func() {
				globalReplicationDetailsLocker.Lock()
				GlobalReplicationDetails = prev
				globalReplicationDetailsLocker.Unlock()
			}()
			sc.run(t)
		})
	}
}

// Consolidated scenario restoring coverage for registry Replicate failure + short-circuit behavior
// previously in replicate_error_branches_cases_test.go.
func TestRegistry_ReplicateErrorBranches_Scenario(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	active := t.TempDir()
	passiveDir := t.TempDir()
	// Create a file inside passiveDir and then use that file path as passive root to induce errors.
	passiveFile := passiveDir + string(os.PathSeparator) + "passive_as_file"
	if err := os.WriteFile(passiveFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup file: %v", err)
	}

	rt, _ := NewReplicationTracker(ctx, []string{active, passiveFile}, true, cache)
	r := NewRegistry(true, MinimumModValue, rt, cache)
	defer r.Close()

	// Ensure table directory exists and seed active with a handle used in added/updated/removed slices.
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "regrep"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	hAdd := sop.NewHandle(sop.NewUUID())
	if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{hAdd}}}); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	del := sop.NewHandle(sop.NewUUID())
	if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{del}}}); err != nil {
		t.Fatalf("seed del add: %v", err)
	}
	upd := hAdd
	upd.Version = 2
	newRoot := sop.NewHandle(sop.NewUUID())

	firstErr := r.Replicate(ctx,
		[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{newRoot}}},
		[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{hAdd}}},
		[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{upd}}},
		[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{del}}},
	)
	if firstErr == nil && !rt.FailedToReplicate {
		t.Fatalf("expected replicate error or failure flag set on first call")
	}
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate flag set")
	}

	// Second replicate should short-circuit returning nil.
	if err := r.Replicate(ctx,
		[]sop.RegistryPayload[sop.Handle]{{RegistryTable: "regrep", IDs: []sop.Handle{newRoot}}},
		nil, nil, nil,
	); err != nil {
		t.Fatalf("expected nil on second replicate, got %v", err)
	}
}

// Exercise error branches in registryMap.remove and Replicate close override.
func Test_registryMap_remove_Errors_And_Replicate_CloseOverride_2(t *testing.T) {
	ctx := context.Background()

	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Setup a registry and add one handle, then try to remove mismatching logical id to hit error branches.
	reg := NewRegistry(true, 4, rt, mocks.NewMockClient())
	defer reg.Close()

	table := "c1_r"
	// Ensure table dir exists for writes
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := sop.Handle{LogicalID: sop.NewUUID()}
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: table, IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Attempt remove with the same id twice: second call should error (already deleted), hitting not-found branch.
	rm := newRegistryMap(true, reg.hashmap.hashmap.hashModValue, rt, mocks.NewMockClient())
	lids := []sop.UUID{h.LogicalID}
	if err := rm.remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: table, IDs: lids}}); err != nil {
		t.Fatalf("remove first: %v", err)
	}
	if err := rm.remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: table, IDs: lids}}); err == nil {
		t.Fatalf("expected error on removing already-deleted record")
	}

	// Replicate close override path: force an error from rmCloseOverride().
	// Ensure replication is enabled and not marked failed, and isolate from global state.
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = &ReplicationTrackedDetails{}
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()
	rt.FailedToReplicate = false
	reg2 := NewRegistry(true, 4, rt, mocks.NewMockClient())
	reg2.rmCloseOverride = func() error { return fmt.Errorf("close fail") }
	if err := reg2.Replicate(ctx, nil, nil, nil, nil); err == nil {
		t.Fatalf("expected close override error")
	}
}

// lockFailCache forces Lock to fail to exercise error path in UpdateNoLocks -> set -> updateFileBlockRegion.
type lockFailCache struct{ sop.L2Cache }

func (l lockFailCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (l lockFailCache) IsRestarted(ctx context.Context) bool { return false }

func (c lockFailCache) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}

func (c lockFailCache) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return c.Lock(ctx, d, lk)
}

// Covers registryOnDisk.UpdateNoLocks error path by injecting a failing DirectIO that causes set() to error.
func Test_Registry_UpdateNoLocks_ErrorFromSet(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	// Use a cache that always fails Lock to force set() to error without relying on global DirectIO shims.
	reg := NewRegistry(true, 8, rt, lockFailCache{mocks.NewMockClient()})

	// Ensure table folder exists to avoid early create path; we want set() to proceed then fail on lock.
	tbl := filepath.Join(base, "tbl")
	os.MkdirAll(tbl, 0o755)

	h := sop.Handle{LogicalID: sop.NewUUID()}
	if err := reg.UpdateNoLocks(ctx, false, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "tbl", IDs: []sop.Handle{h}}}); err == nil {
		t.Fatalf("expected error from set due to lock failure")
	}
}

// Covers registryOnDisk.Remove error when item is missing in target (already zeroed) -> returns specific error.
func Test_Registry_Remove_ItemMissing_Error(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	reg := NewRegistry(true, 8, rt, mocks.NewMockClient())

	// No prior Add; Remove should try to find and then fail with item not found when slot empty.
	id := sop.NewUUID()
	if err := reg.Remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "tbl", IDs: []sop.UUID{id}}}); err == nil {
		t.Fatalf("expected error removing missing item")
	}
}

// cache that forces SetStruct to fail to hit the warn path in UpdateNoLocks.
type setStructErrorCache struct{ sop.L2Cache }

func (s setStructErrorCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (s setStructErrorCache) IsRestarted(ctx context.Context) bool { return false }

func (c setStructErrorCache) SetStruct(ctx context.Context, key string, value interface{}, d time.Duration) error {
	return errors.New("setstruct boom")
}

// cache that forces Delete to return error to hit the warn path in Remove's deferred cache eviction.
type deleteErrorCache struct{ sop.L2Cache }

func (d deleteErrorCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (d deleteErrorCache) IsRestarted(ctx context.Context) bool { return false }

func (c deleteErrorCache) Delete(ctx context.Context, keys []string) (bool, error) {
	return false, errors.New("delete boom")
}

func Test_Registry_UpdateNoLocks_SetStructWarn(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())

	// Wrap the mock cache to inject SetStruct error while preserving other behaviors (locks, etc.).
	cache := setStructErrorCache{mocks.NewMockClient()}
	reg := NewRegistry(true, 8, rt, cache)

	// Ensure the target table directory exists under active folder.
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "tbl"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	h := sop.Handle{LogicalID: sop.NewUUID()}
	if err := reg.UpdateNoLocks(ctx, false, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "tbl", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("UpdateNoLocks expected success despite SetStruct error, got %v", err)
	}
}

func Test_Registry_Remove_DeleteWarn(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())

	// Use cache wrapper that returns error on Delete to exercise warn path inside deferred cache eviction.
	cache := deleteErrorCache{mocks.NewMockClient()}
	reg := NewRegistry(true, 8, rt, cache)

	// Seed one handle so Remove performs actual on-disk delete, then triggers cache.Delete warnings.
	h := sop.NewHandle(sop.NewUUID())
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "tbl"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "tbl", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	if err := reg.Remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "tbl", IDs: []sop.UUID{h.LogicalID}}}); err != nil {
		t.Fatalf("Remove expected success, got %v", err)
	}
}

// Covers the error-wrapping branch in registryMap.fetch when underlying hashmap.fetch returns an error.
func Test_registryMap_Fetch_ErrorWrapped(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	rm := newRegistryMap(true, 8, rt, mocks.NewMockClient())

	seg := filepath.Join(base, "tblX", "tblX-1"+registryFileExtension)
	if err := NewFileIO().MkdirAll(ctx, filepath.Dir(seg), permission); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed a properly sized segment file, then remove read/write permissions to force open/read failure.
	if err := NewFileIO().WriteFile(ctx, seg, make([]byte, int64(8)*blockSize), permission); err != nil {
		t.Fatalf("seed seg: %v", err)
	}
	if err := os.Chmod(seg, 0o000); err != nil {
		t.Fatalf("chmod seg: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(seg, 0o644) })

	_, err := rm.fetch(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "tblX", IDs: []sop.UUID{sop.NewUUID()}}})
	if err == nil || err.Error() == "" {
		t.Fatalf("expected wrapped fetch error, got %v", err)
	}
}

// Drives the remove() path to call markDeleteFileRegion and surface write errors by using a read-only hashmap.
func Test_registryMap_Remove_WriteError_From_ReadOnlyHashmap(t *testing.T) {
	// Speed up retries for this test
	prev := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() {
		sop.RetryStartDuration = prev
	})

	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	// Seed a handle using a writable Registry so data exists on disk.
	reg := NewRegistry(true, 8, rt, mocks.NewMockClient())
	defer reg.Close()
	table := "c1_r"
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := sop.NewHandle(sop.NewUUID())
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: table, IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Now create a read-only registryMap pointing at same files; remove should attempt write and fail.
	rmRO := newRegistryMap(false, reg.hashmap.hashmap.hashModValue, rt, mocks.NewMockClient())
	// Should fail (Read-Only)
	if err := rmRO.remove(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: table, IDs: []sop.UUID{h.LogicalID}}}); err == nil {
		t.Fatalf("expected remove to fail due to read-only file handle")
	}
}
