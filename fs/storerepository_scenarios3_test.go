package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// Helper cache mocks consolidated from prior individual test files.
type mockCacheWarn struct{ inner sop.L2Cache }

func newMockCacheWarn() mockCacheWarn { return mockCacheWarn{inner: mocks.NewMockClient()} }

func (m mockCacheWarn) GetType() sop.L2CacheType {
	return sop.Redis
}

func (m mockCacheWarn) Set(ctx context.Context, k, v string, d time.Duration) error {
	return m.inner.Set(ctx, k, v, d)
}
func (m mockCacheWarn) Get(ctx context.Context, k string) (bool, string, error) {
	return m.inner.Get(ctx, k)
}
func (m mockCacheWarn) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return m.inner.GetEx(ctx, k, d)
}
func (m mockCacheWarn) SetStruct(context.Context, string, interface{}, time.Duration) error {
	return errors.New("fail setstruct")
}
func (m mockCacheWarn) SetStructs(context.Context, []string, []interface{}, time.Duration) error {
	return errors.New("fail setstructs")
}
func (m mockCacheWarn) GetStruct(ctx context.Context, k string, tgt interface{}) (bool, error) {
	return m.inner.GetStruct(ctx, k, tgt)
}
func (m mockCacheWarn) GetStructEx(ctx context.Context, k string, tgt interface{}, d time.Duration) (bool, error) {
	return m.inner.GetStructEx(ctx, k, tgt, d)
}
func (m mockCacheWarn) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return m.inner.GetStructs(ctx, keys, targets, expiration)
}
func (m mockCacheWarn) Delete(context.Context, []string) (bool, error) {
	return false, errors.New("fail delete")
}
func (m mockCacheWarn) Ping(ctx context.Context) error { return m.inner.Ping(ctx) }
func (m mockCacheWarn) FormatLockKey(k string) string  { return m.inner.FormatLockKey(k) }
func (m mockCacheWarn) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.inner.CreateLockKeys(keys)
}
func (m mockCacheWarn) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.inner.CreateLockKeysForIDs(keys)
}
func (m mockCacheWarn) IsLockedTTL(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, error) {
	return m.inner.IsLockedTTL(ctx, d, ks)
}
func (m mockCacheWarn) Lock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return m.inner.Lock(ctx, d, ks)
}
func (m mockCacheWarn) IsLocked(ctx context.Context, ks []*sop.LockKey) (bool, error) {
	return m.inner.IsLocked(ctx, ks)
}
func (m mockCacheWarn) DualLock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return m.inner.DualLock(ctx, d, ks)
}
func (m mockCacheWarn) IsLockedByOthers(ctx context.Context, names []string) (bool, error) {
	return m.inner.IsLockedByOthers(ctx, names)
}
func (m mockCacheWarn) IsLockedByOthersTTL(ctx context.Context, names []string, d time.Duration) (bool, error) {
	return m.inner.IsLockedByOthersTTL(ctx, names, d)
}
func (m mockCacheWarn) Unlock(ctx context.Context, ks []*sop.LockKey) error {
	return m.inner.Unlock(ctx, ks)
}
func (m mockCacheWarn) Clear(ctx context.Context) error { return m.inner.Clear(ctx) }
func (m mockCacheWarn) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m mockCacheWarn) IsRestarted(ctx context.Context) bool { return false }

type mockCacheDeleteWarn struct{ sop.L2Cache }

func (m mockCacheDeleteWarn) Delete(context.Context, []string) (bool, error) {
	return false, errors.New("fail delete")
}
func (m mockCacheDeleteWarn) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m mockCacheDeleteWarn) IsRestarted(ctx context.Context) bool { return false }

type mockCacheSetStructWarn struct{ sop.L2Cache }

func (m mockCacheSetStructWarn) SetStruct(context.Context, string, interface{}, time.Duration) error {
	return errors.New("fail setstruct")
}
func (m mockCacheSetStructWarn) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m mockCacheSetStructWarn) IsRestarted(ctx context.Context) bool { return false }

// mockCacheAlwaysLocked forces Lock to report the key(s) are already locked, so Update's
// retry path is exercised and ultimately returns an error.
type mockCacheAlwaysLocked struct{ sop.L2Cache }

func (m mockCacheAlwaysLocked) Lock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NewUUID(), nil
}
func (m mockCacheAlwaysLocked) DualLock(ctx context.Context, d time.Duration, ks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NewUUID(), nil
}
func (m mockCacheAlwaysLocked) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m mockCacheAlwaysLocked) IsRestarted(ctx context.Context) bool { return false }

// failingRemoveAll triggers RemoveAll failure for passive replicated path ending with /x1.
type failingRemoveAll struct {
	FileIO
	passiveRoot string
}

func (f failingRemoveAll) RemoveAll(ctx context.Context, p string) error {
	if strings.HasPrefix(p, f.passiveRoot) && strings.HasSuffix(p, string(os.PathSeparator)+"x1") {
		return errors.New("remove all fail")
	}
	return f.FileIO.RemoveAll(ctx, p)
}

// Consolidated table-driven StoreRepository + related fileIO replication scenarios.
func TestStoreRepository_Scenarios(t *testing.T) {
	ctx := context.Background()

	scenarios := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"Update lock failure retries then errors", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(context.Background(), []string{base}, false, mocks.NewMockClient())
			cache := mockCacheAlwaysLocked{L2Cache: mocks.NewMockClient()}
			sr, _ := NewStoreRepository(context.Background(), rt, nil, cache, 0)
			// any update attempt should fail due to inability to acquire locks; use short timeout to avoid long backoff
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 4})
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			_, err := sr.Update(ctx, []sop.StoreInfo{s})
			if err == nil {
				t.Fatalf("expected Update to error when locks cannot be acquired")
			}
		}},
		{"Basic Flow Add/Get/Update/Remove (replication disabled)", func(t *testing.T) {
			l2 := mocks.NewMockClient()
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, l2)
			sr, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue)
			if err != nil {
				t.Fatalf("NewStoreRepository: %v", err)
			}
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 10})
			sj := sop.NewStoreInfo(sop.StoreOptions{Name: "s2", SlotLength: 8})
			if err := sr.Add(ctx, *si, *sj); err != nil {
				t.Fatalf("Add: %v", err)
			}
			names, err := sr.GetAll(ctx)
			if err != nil || len(names) != 2 {
				t.Fatalf("GetAll: %v %v", names, err)
			}
			got, err := sr.Get(ctx, "s1", "s2")
			if err != nil || len(got) != 2 {
				t.Fatalf("Get: %v %v", got, err)
			}
			upd := got[0]
			upd.CountDelta = 5
			if _, err := sr.Update(ctx, []sop.StoreInfo{upd}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if err := sr.Remove(ctx, "s1"); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if err := sr.Replicate(ctx, []sop.StoreInfo{upd}); err != nil {
				t.Fatalf("Replicate disabled no-op: %v", err)
			}
		}},
		{"GetRegistryHashModValue reads from existing file when zero", func(t *testing.T) {
			active, passive := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			fn := filepath.Join(active, RegistryHashModValueFilename)
			if err := os.WriteFile(fn, []byte("777"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			v, err := sr.GetRegistryHashModValue(ctx)
			if err != nil || v != 777 {
				t.Fatalf("got %d err %v", v, err)
			}
		}},
		{"CopyToPassiveFolders no stores (nil GetAll)", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 32)
			if err := sr.CopyToPassiveFolders(ctx); err != nil {
				t.Fatalf("CopyToPassiveFolders: %v", err)
			}
		}},
		{"CopyToPassiveFolders with store + segment file", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 32)
			st := sop.StoreInfo{Name: "s1", RegistryTable: "c1_r"}
			if err := sr.Add(ctx, st); err != nil {
				t.Fatalf("Add: %v", err)
			}
			segDir := filepath.Join(a, st.RegistryTable)
			os.MkdirAll(segDir, 0o755)
			segFile := filepath.Join(segDir, st.RegistryTable+"-1"+registryFileExtension)
			os.WriteFile(segFile, []byte("segment"), 0o644)
			if err := sr.CopyToPassiveFolders(ctx); err != nil {
				t.Fatalf("CopyToPassiveFolders: %v", err)
			}
			if _, err := os.Stat(filepath.Join(p, st.RegistryTable, st.RegistryTable+"-1"+registryFileExtension)); err != nil {
				t.Fatalf("copied seg missing: %v", err)
			}
		}},
		{"CopyToPassiveFolders E2E list + storeinfo + reg", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), MinimumModValue)
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "c1", SlotLength: 10})
			if err := sr.Add(ctx, *si); err != nil {
				t.Fatalf("Add: %v", err)
			}
			regDir := filepath.Join(a, si.RegistryTable)
			os.MkdirAll(regDir, 0o755)
			os.WriteFile(filepath.Join(regDir, "0000-0000.reg"), []byte("x"), 0o644)
			if err := sr.CopyToPassiveFolders(ctx); err != nil {
				t.Fatalf("CopyToPassiveFolders: %v", err)
			}
			for _, fn := range []string{filepath.Join(p, storeListFilename), filepath.Join(p, si.Name, StoreInfoFilename), filepath.Join(p, si.RegistryTable, "0000-0000.reg")} {
				if _, err := os.Stat(fn); err != nil {
					t.Fatalf("expected file: %s err %v", fn, err)
				}
			}
		}},
		{"CopyToPassiveFolders missing registry segment source dir error", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			store := *sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 10})
			sr.Add(ctx, store)
			if err := sr.CopyToPassiveFolders(ctx); err == nil || !strings.Contains(err.Error(), "error reading source directory") {
				t.Fatalf("expected source directory error, got %v", err)
			}
		}},
		{"CopyToPassiveFolders passive target dir create conflict", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "cpy", SlotLength: 10})
			sr.Add(ctx, s)
			regDirActive := filepath.Join(a, s.RegistryTable)
			os.MkdirAll(regDirActive, 0o755)
			seg := filepath.Join(regDirActive, s.RegistryTable+"-1"+registryFileExtension)
			os.WriteFile(seg, []byte("seg"), 0o644)
			passiveConflict := filepath.Join(p, s.RegistryTable)
			os.WriteFile(passiveConflict, []byte("x"), 0o644)
			if err := sr.CopyToPassiveFolders(ctx); err == nil || !strings.Contains(err.Error(), "error creating target directory") {
				t.Fatalf("expected dir create error, got %v", err)
			}
		}},
		{"CopyToPassiveFolders toggler false path with segments", func(t *testing.T) {
			ctx := context.Background()
			f1 := t.TempDir() // passive when toggler=false (active becomes index 1)
			f2 := t.TempDir()
			cache := mocks.NewMockClient()
			rt, err := NewReplicationTracker(ctx, []string{f1, f2}, true, cache)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			rt.ActiveFolderToggler = false
			sr, err := NewStoreRepository(ctx, rt, nil, cache, 4)
			if err != nil {
				t.Fatalf("repo: %v", err)
			}
			stores := []*sop.StoreInfo{
				sop.NewStoreInfo(sop.StoreOptions{Name: "sa", SlotLength: 4}),
				sop.NewStoreInfo(sop.StoreOptions{Name: "sb", SlotLength: 4}),
			}
			if err := sr.Add(ctx, *stores[0], *stores[1]); err != nil {
				t.Fatalf("sr.Add: %v", err)
			}
			for _, si := range stores {
				regDir := filepath.Join(f2, si.RegistryTable)
				if err := os.MkdirAll(regDir, 0o755); err != nil {
					t.Fatalf("mkdir regdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(regDir, si.RegistryTable+"-1"+registryFileExtension), []byte("seg"), 0o644); err != nil {
					t.Fatalf("segment: %v", err)
				}
			}
			if err := sr.CopyToPassiveFolders(ctx); err != nil {
				t.Fatalf("CopyToPassiveFolders: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f1, storeListFilename)); err != nil {
				t.Fatalf("expected storelist in f1: %v", err)
			}
			for _, si := range stores {
				if _, err := os.Stat(filepath.Join(f1, si.Name, StoreInfoFilename)); err != nil {
					t.Fatalf("missing storeinfo in f1: %v", err)
				}
				if _, err := os.Stat(filepath.Join(f1, si.RegistryTable, si.RegistryTable+"-1"+registryFileExtension)); err != nil {
					t.Fatalf("missing segment in f1: %v", err)
				}
			}
		}},
		{"GetRegistryHashModValue invalid content error", func(t *testing.T) {
			ctx := context.Background()
			active := t.TempDir()
			passive := t.TempDir()
			cache := mocks.NewMockClient()
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
			if err != nil {
				t.Fatalf("repo: %v", err)
			}
			if err := os.WriteFile(filepath.Join(active, RegistryHashModValueFilename), []byte("not-an-int"), 0o644); err != nil {
				t.Fatalf("seed invalid: %v", err)
			}
			if _, err := sr.GetRegistryHashModValue(ctx); err == nil {
				t.Fatalf("expected Atoi error for invalid content")
			}
		}},
		{"Replicate writes passive storeinfo", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			l2 := mocks.NewMockClient()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, l2)
			sr, _ := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue)
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "r1", SlotLength: 10})
			sr.Add(ctx, *si)
			si.CountDelta, si.Count = 3, 3
			if err := sr.Replicate(ctx, []sop.StoreInfo{*si}); err != nil {
				t.Fatalf("Replicate: %v", err)
			}
			if _, err := os.Stat(filepath.Join(p, si.Name, StoreInfoFilename)); err != nil {
				t.Fatalf("missing replicated storeinfo: %v", err)
			}
		}},
		{"Replicate write failure passive path conflict", func(t *testing.T) {
			a, p := t.TempDir(), t.TempDir()
			l2 := mocks.NewMockClient()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, l2)
			sr, _ := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue)
			si := sop.NewStoreInfo(sop.StoreOptions{Name: "r2", SlotLength: 10})
			sr.Add(ctx, *si)
			// Replace passive store directory with a file; replicate should fail writing storeinfo due to ENOTDIR.
			passiveDir := filepath.Join(p, si.Name)
			os.RemoveAll(passiveDir)
			if err := os.WriteFile(passiveDir, []byte("x"), 0o644); err != nil {
				t.Fatalf("prep write file: %v", err)
			}
			si.Count = 1
			if err := sr.Replicate(ctx, []sop.StoreInfo{*si}); err == nil {
				t.Fatalf("expected replicate failure due to path conflict")
			}
		}},
		{"Replicate skips when disabled or failed flag set", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			si := *sop.NewStoreInfo(sop.StoreOptions{Name: "sB", SlotLength: 10})
			si2 := *sop.NewStoreInfo(sop.StoreOptions{Name: "sC", SlotLength: 10})
			if err := sr.Replicate(ctx, []sop.StoreInfo{si}); err != nil {
				t.Fatalf("disabled replicate: %v", err)
			}
			rt.replicate = true
			rt.FailedToReplicate = true
			if err := sr.Replicate(ctx, []sop.StoreInfo{si2}); err != nil {
				t.Fatalf("failed flag replicate skip: %v", err)
			}
		}},
		{"Remove cache delete warning path", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			cache := mockCacheDeleteWarn{L2Cache: mocks.NewMockClient()}
			sr, _ := NewStoreRepository(ctx, rt, nil, cache, 0)
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "rmw", SlotLength: 10})
			sr.Add(ctx, s)
			if err := sr.Remove(ctx, s.Name); err != nil {
				t.Fatalf("Remove warn path: %v", err)
			}
			if _, err := os.Stat(filepath.Join(base, s.Name)); !os.IsNotExist(err) {
				t.Fatalf("expected store folder removed, err=%v", err)
			}
		}},
		{"Update cache SetStruct warning", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			failingCache := mockCacheSetStructWarn{L2Cache: mocks.NewMockClient()}
			sr, _ := NewStoreRepository(ctx, rt, nil, failingCache, 0)
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "cw", SlotLength: 10})
			sr.Add(ctx, s)
			s.CountDelta = 2
			s.CacheConfig.StoreInfoCacheDuration = time.Second
			if _, err := sr.Update(ctx, []sop.StoreInfo{s}); err != nil {
				t.Fatalf("Update should succeed: %v", err)
			}
		}},
		{"Update undo on second store failure", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			s1 := sop.NewStoreInfo(sop.StoreOptions{Name: "aaa", SlotLength: 10})
			s2 := sop.NewStoreInfo(sop.StoreOptions{Name: "bbb", SlotLength: 10})
			s1.Timestamp, s2.Timestamp = 111, 222
			sr.Add(ctx, *s1, *s2)
			upd1 := *s1
			upd1.CountDelta, upd1.Timestamp = 3, 999
			upd1.CacheConfig.StoreInfoCacheDuration = time.Minute
			upd2 := *s2
			upd2.CountDelta, upd2.Timestamp = 5, 888
			upd2.CacheConfig.StoreInfoCacheDuration = time.Minute
			s2File := filepath.Join(base, upd2.Name, StoreInfoFilename)
			os.Remove(s2File)
			os.Mkdir(s2File, 0o755)
			if _, err := sr.Update(ctx, []sop.StoreInfo{upd1, upd2}); err == nil {
				t.Fatalf("expected Update error")
			}
			ba, _ := os.ReadFile(filepath.Join(base, s1.Name, StoreInfoFilename))
			var got sop.StoreInfo
			json.Unmarshal(ba, &got)
			if got.Count != 0 || got.Timestamp != 111 {
				t.Fatalf("rollback mismatch %+v", got)
			}
		}},
		{"Update failure on first store write", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			s := sop.NewStoreInfo(sop.StoreOptions{Name: "one", SlotLength: 10})
			s.Timestamp = 123
			sr.Add(ctx, *s)
			infoFile := filepath.Join(base, s.Name, StoreInfoFilename)
			os.Remove(infoFile)
			os.Mkdir(infoFile, 0o755)
			upd := *s
			upd.CountDelta, upd.Timestamp = 5, 999
			upd.CacheConfig.StoreInfoCacheDuration = time.Minute
			if _, err := sr.Update(ctx, []sop.StoreInfo{upd}); err == nil {
				t.Fatalf("expected update error first store")
			}
			if fi, err := os.Stat(infoFile); err != nil || !fi.IsDir() {
				t.Fatalf("expected dir placeholder; err=%v", err)
			}
		}},
		{"Update missing store early return", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			ghost := sop.StoreInfo{Name: "ghost", CountDelta: 1, CacheConfig: sop.StoreCacheConfig{StoreInfoCacheDuration: time.Second}}
			got, err := sr.Update(ctx, []sop.StoreInfo{ghost})
			if err != nil || got != nil {
				t.Fatalf("expected nil slice nil err, got %v %v", got, err)
			}
		}},
		{"Add duplicate names in same batch", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "dup2", SlotLength: 10})
			if err := sr.Add(ctx, s, s); err == nil || !strings.Contains(err.Error(), "can't add store") {
				t.Fatalf("expected duplicate error, got %v", err)
			}
		}},
		{"Add duplicate second call rejected & cache warn paths", func(t *testing.T) {
			base := t.TempDir()
			rt1, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr1, _ := NewStoreRepository(ctx, rt1, nil, mocks.NewMockClient(), 0)
			s := *sop.NewStoreInfo(sop.StoreOptions{Name: "dup", SlotLength: 10})
			sr1.Add(ctx, s)
			if err := sr1.Add(ctx, s); err == nil {
				t.Fatalf("expected duplicate add error")
			}
			rt2, _ := NewReplicationTracker(ctx, []string{t.TempDir()}, false, mocks.NewMockClient())
			sr2, _ := NewStoreRepository(ctx, rt2, nil, newMockCacheWarn(), 0)
			s2 := *sop.NewStoreInfo(sop.StoreOptions{Name: "warn", SlotLength: 10})
			sr2.Add(ctx, s2)
			sr2.Remove(ctx, s2.Name)
			sr2.Remove(ctx, s2.Name)
		}},
		{"GetWithTTL partial cache miss path", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			a := *sop.NewStoreInfo(sop.StoreOptions{Name: "a1", SlotLength: 5})
			b := *sop.NewStoreInfo(sop.StoreOptions{Name: "b1", SlotLength: 5})
			sr.Add(ctx, a, b)
			sr.Get(ctx, a.Name) // prime cache for a
			got, err := sr.GetWithTTL(ctx, false, 0, a.Name, b.Name)
			if err != nil || len(got) != 2 {
				t.Fatalf("GetWithTTL got %v err %v", got, err)
			}
		}},
		{"Update write failure due to directory in place of file", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			c := *sop.NewStoreInfo(sop.StoreOptions{Name: "c1", SlotLength: 5})
			sr.Add(ctx, c)
			infoFile := filepath.Join(base, c.Name, StoreInfoFilename)
			os.Remove(infoFile)
			os.Mkdir(infoFile, 0o755)
			upd := c
			upd.CountDelta = 1
			upd.CacheConfig.StoreInfoCacheDuration = time.Minute
			if _, err := sr.Update(ctx, []sop.StoreInfo{upd}); err == nil {
				t.Fatalf("expected update write failure")
			}
		}},
		{"Update undo JSON integrity first store after second fails", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			s1 := sop.NewStoreInfo(sop.StoreOptions{Name: "j1", SlotLength: 10})
			s2 := sop.NewStoreInfo(sop.StoreOptions{Name: "j2", SlotLength: 10})
			s1.Timestamp, s2.Timestamp = 100, 200
			sr.Add(ctx, *s1, *s2)
			infoFile2 := filepath.Join(base, s2.Name, StoreInfoFilename)
			os.Remove(infoFile2)
			os.Mkdir(infoFile2, 0o755)
			upd1 := *s1
			upd1.CountDelta, upd1.Timestamp = 2, 777
			upd1.CacheConfig.StoreInfoCacheDuration = time.Minute
			upd2 := *s2
			upd2.CountDelta, upd2.Timestamp = 3, 888
			upd2.CacheConfig.StoreInfoCacheDuration = time.Minute
			if _, err := sr.Update(ctx, []sop.StoreInfo{upd1, upd2}); err == nil {
				t.Fatalf("expected update error")
			}
			ba, _ := os.ReadFile(filepath.Join(base, s1.Name, StoreInfoFilename))
			var got sop.StoreInfo
			json.Unmarshal(ba, &got)
			if got.Timestamp != 100 || got.Count != 0 {
				t.Fatalf("rollback integrity mismatch %+v", got)
			}
		}},
		{"GetStoresBaseFolder & getFromCache mixed hit", func(t *testing.T) {
			active := filepath.Join(t.TempDir(), "a")
			passive := filepath.Join(t.TempDir(), "p")
			rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
			rt.ActiveFolderToggler = true
			cache := mocks.NewMockClient()
			sr, _ := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
			if sr.GetStoresBaseFolder() != active {
				t.Fatalf("GetStoresBaseFolder mismatch")
			}
			s1 := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 10})
			cache.SetStruct(ctx, fmt.Sprintf("%s:%s", active, s1.Name), s1, 0)
			res, err := sr.getFromCache(ctx, s1.Name, "missing")
			if err != nil || len(res) != 1 || res[0].Name != s1.Name {
				t.Fatalf("getFromCache mismatch %v %v", res, err)
			}
		}},
		{"GetAll nil -> Add -> Remove -> Remove no-op flow", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			if sl, err := sr.GetAll(ctx); err != nil || sl != nil {
				t.Fatalf("expected nil list: %v %v", sl, err)
			}
			si := *sop.NewStoreInfo(sop.StoreOptions{Name: "sA", SlotLength: 10})
			sr.Add(ctx, si)
			if names, _ := sr.GetAll(ctx); len(names) != 1 {
				t.Fatalf("expected 1 name got %v", names)
			}
			sr.Remove(ctx, "sA")
			sr.Remove(ctx, "sA")
		}},
		{"Update ghost store returns nil slice nil error", func(t *testing.T) {
			base := t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
			sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
			pay := []sop.StoreInfo{{Name: "ghost", CountDelta: 5, CacheConfig: sop.StoreCacheConfig{StoreInfoCacheDuration: time.Minute}}}
			if got, err := sr.Update(ctx, pay); err != nil || got != nil {
				t.Fatalf("expected nil result nil err got %v %v", got, err)
			}
		}},
		{"fileIO replicate removeStore passive failure (action type 3)", func(t *testing.T) {
			active, passive := t.TempDir(), t.TempDir()
			rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
			fio := newFileIOWithReplicationInjected(rt, NewManageStoreFolder(NewFileIO()), true, failingRemoveAll{FileIO: NewFileIO(), passiveRoot: passive})
			if err := fio.createStore(ctx, "x1"); err != nil {
				t.Fatalf("createStore: %v", err)
			}
			if err := fio.removeStore(ctx, "x1"); err != nil {
				t.Fatalf("removeStore active: %v", err)
			}
			if err := fio.replicate(ctx); err == nil {
				t.Fatalf("expected replicate remove store error")
			}
		}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, sc.run)
	}
}

// Covers invalid integer in reghashmod.txt path of GetRegistryHashModValue.
func Test_StoreRepository_GetRegistryHashModValue_Invalid(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Seed invalid content
	if err := os.WriteFile(filepath.Join(a, RegistryHashModValueFilename), []byte("not-int"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if _, err := sr.GetRegistryHashModValue(ctx); err == nil {
		t.Fatalf("expected invalid atoi error")
	}
}

// Covers Add lock conflict path where store list key is locked by someone else.
func Test_StoreRepository_Add_LockConflict(t *testing.T) {
	// Speed up retries for this test
	prev := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() {
		sop.RetryStartDuration = prev
	})

	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	cache := mocks.NewMockClient()

	// Pre-lock the store list key with a foreign owner.
	_ = cache.Set(ctx, cache.FormatLockKey(fmt.Sprintf("%s:%s", a, lockStoreListKey)), sop.NewUUID().String(), 0)

	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "L", SlotLength: 8})
	if err := sr.Add(ctx, s); err == nil {
		t.Fatalf("expected lock conflict error")
	}
}

// writeDenyFileIO returns error if attempting to write into the passive folder.
type writeDenyFileIO struct {
	FileIO
	passiveRoot string
}

func (w writeDenyFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	if len(w.passiveRoot) > 0 && len(name) >= len(w.passiveRoot) && name[:len(w.passiveRoot)] == w.passiveRoot {
		return errors.New("deny write to passive")
	}
	return w.FileIO.WriteFile(ctx, name, data, perm)
}

func Test_StoreRepository_Remove_Replicate_Failure(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)

	// Seed a store via real repo so storelist contains entry and folder exists.
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, 0)
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "rx", SlotLength: 6})
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Now perform a remove using direct fileIO wrapper with injected write-deny to cause replicate() to fail
	inj := writeDenyFileIO{FileIO: NewFileIO(), passiveRoot: p}
	fio := newFileIOWithReplicationInjected(rt, NewManageStoreFolder(NewFileIO()), true, inj)

	// Stage actions: update storelist and remove store folder on active, then attempt replicate.
	if err := fio.write(ctx, storeListFilename, []byte("[]")); err != nil {
		t.Fatalf("write list: %v", err)
	}
	if err := fio.removeStore(ctx, filepath.Join("", s.Name)); err != nil {
		t.Fatalf("remove active store: %v", err)
	}

	if err := fio.replicate(ctx); err == nil {
		t.Fatalf("expected replicate failure on Remove path")
	}
}

// failingStoreMarshaler2 mirrors failing behavior for sop.StoreInfo marshal to exercise Replicate error branch.
type failingStoreMarshaler2 struct{}

func (f failingStoreMarshaler2) Marshal(v any) ([]byte, error) {
	switch v.(type) {
	case sop.StoreInfo:
		return nil, errors.New("marshal storeinfo boom2")
	default:
		return encoding.DefaultMarshaler.Marshal(v)
	}
}
func (f failingStoreMarshaler2) Unmarshal(data []byte, v any) error {
	return encoding.DefaultMarshaler.Unmarshal(data, v)
}

func Test_StoreRepository_Replicate_Marshal_Error(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	a := t.TempDir()
	b := t.TempDir()
	// Ensure global replication state doesn't short-circuit Replicate due to prior tests.
	prevGlob := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{}
	t.Cleanup(func() { GlobalReplicationDetails = prevGlob })
	rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)

	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}

	prev := encoding.BlobMarshaler
	encoding.BlobMarshaler = failingStoreMarshaler2{}
	t.Cleanup(func() { encoding.BlobMarshaler = prev })

	if err := sr.Replicate(ctx, []sop.StoreInfo{{Name: "s"}}); err == nil {
		t.Fatalf("expected marshal error from Replicate")
	}
}
