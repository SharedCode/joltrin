package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// errMarshaler forces Marshal to fail; Unmarshal is a no-op to satisfy the interface.
type errMarshaler struct{}

func (e errMarshaler) Marshal(v any) ([]byte, error)      { return nil, errors.New("forced marshal error") }
func (e errMarshaler) Unmarshal(data []byte, v any) error { return nil }

func TestStoreRepository_Update_MarshalError_Undo(t *testing.T) {
	// This test exercises the Update path when encoding.Marshal fails, ensuring
	// the function aborts and previously persisted data remains unchanged.
	// Not parallel: modifies global encoding.BlobMarshaler.

	ctx := context.Background()
	base := t.TempDir()

	// Build a replication tracker with no replication.
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	cache := mocks.NewMockClient()
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}

	// Seed one store and persist.
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "u1", SlotLength: 8})
	if err := sr.Add(ctx, *si); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Override the marshaler to force an encode error during Update.
	prev := encoding.BlobMarshaler
	encoding.BlobMarshaler = errMarshaler{}
	t.Cleanup(func() { encoding.BlobMarshaler = prev })

	// Attempt to update Count via CountDelta; this should fail on Marshal and not modify file.
	_, uerr := sr.Update(ctx, []sop.StoreInfo{{Name: "u1", CountDelta: 1, CacheConfig: si.CacheConfig}})
	if uerr == nil || uerr.Error() != "forced marshal error" {
		t.Fatalf("expected forced marshal error, got %v", uerr)
	}

	// Verify storeinfo on disk did not change Count (remains 0) due to undo/abort.
	fn := filepath.Join(rt.getActiveBaseFolder(), "u1", StoreInfoFilename)
	ba, rerr := NewFileIO().ReadFile(ctx, fn)
	if rerr != nil {
		t.Fatalf("ReadFile: %v", rerr)
	}
	var got sop.StoreInfo
	if err := encoding.Unmarshal(ba, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Count != 0 {
		t.Fatalf("expected Count to remain 0, got %d", got.Count)
	}

	// Small sanity: file exists.
	if _, statErr := os.Stat(fn); statErr != nil {
		t.Fatalf("Stat storeinfo: %v", statErr)
	}
}

func Test_copyFilesByExtension_ErrorPaths_Table(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	cases := []struct {
		name          string
		sourceDir     string
		targetDir     string
		wantErrSubstr string
		prep          func()
	}{
		{
			name:          "source read error (missing dir)",
			sourceDir:     filepath.Join(tmp, "missing-src"),
			targetDir:     filepath.Join(tmp, "dst1"),
			wantErrSubstr: "error reading source directory",
		},
		{
			name:          "target mkdir error (target is file)",
			sourceDir:     filepath.Join(tmp, "src2"),
			targetDir:     filepath.Join(tmp, "dst-file"),
			wantErrSubstr: "error creating target directory",
			prep: func() {
				_ = os.MkdirAll(filepath.Join(tmp, "src2"), 0o755)
				// Create a file at the targetDir path to force MkdirAll error
				_ = os.WriteFile(filepath.Join(tmp, "dst-file"), []byte("x"), 0o644)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prep != nil {
				tc.prep()
			}
			err := copyFilesByExtension(ctx, tc.sourceDir, tc.targetDir, ".reg")
			if err == nil || (tc.wantErrSubstr != "" && !contains(err.Error(), tc.wantErrSubstr)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
			}
		})
	}
}

// Covers GetRegistryHashModValue read error branch when the path exists but is a directory.
func Test_StoreRepository_GetRegistryHashModValue_ReadError(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Create a directory in place of the file so Exists() returns true and ReadFile fails.
	if err := os.MkdirAll(filepath.Join(a, RegistryHashModValueFilename), 0o755); err != nil {
		t.Fatalf("prep: %v", err)
	}
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if _, err := sr.GetRegistryHashModValue(ctx); err == nil {
		t.Fatalf("expected read error for registry hash mod when path is a directory")
	}
}

func Test_StoreRepository_GetWithTTL_TTLPath_And_JSONError(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()

	// Seed a store using one repository + cache instance
	cache1 := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache1)
	sr1, _ := NewStoreRepository(ctx, rt, nil, cache1, 0)
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "t1", SlotLength: 4})
	s.CacheConfig.StoreInfoCacheDuration = time.Second
	if err := sr1.Add(ctx, s); err != nil {
		t.Fatalf("add: %v", err)
	}

	// New repository with a fresh cache to force a cache miss and TTL path to hit disk.
	cache2 := mocks.NewMockClient()
	sr2, _ := NewStoreRepository(ctx, rt, nil, cache2, 0)
	if got, err := sr2.GetWithTTL(ctx, true, s.CacheConfig.StoreInfoCacheDuration, s.Name); err != nil || len(got) != 1 {
		t.Fatalf("GetWithTTL TTL miss-to-disk path: got %v err %v", got, err)
	}

	// Corrupt the on-disk JSON and use a third fresh cache to force disk read error branch.
	if err := os.WriteFile(filepath.Join(a, s.Name, StoreInfoFilename), []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	cache3 := mocks.NewMockClient()
	sr3, _ := NewStoreRepository(ctx, rt, nil, cache3, 0)
	if _, err := sr3.GetWithTTL(ctx, true, s.CacheConfig.StoreInfoCacheDuration, s.Name); err == nil {
		t.Fatalf("expected JSON unmarshal error in GetWithTTL")
	}
}

// Triggers sr.GetWithTTL error inside Update by making storeinfo.txt a directory and clearing cache.
func Test_StoreRepository_Update_GetWithTTLError(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	s := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 10, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: 1 * time.Minute}})
	if err := sr.Add(ctx, *s); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Clear cache so Update forces disk read path in GetWithTTL.
	_ = cache.Clear(ctx)

	// Make storeinfo.txt path a directory to cause ReadFile error.
	base := sr.GetStoresBaseFolder()
	target := filepath.Join(base, s.Name, StoreInfoFilename)
	_ = os.Remove(target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	s.CountDelta = 1
	if _, err := sr.Update(ctx, []sop.StoreInfo{*s}); err == nil {
		t.Fatalf("expected update error due to GetWithTTL read failure")
	}
}

func Test_StoreRepository_Update_SetStructWarnPaths(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	cache := setStructErrCache{L2Cache: mocks.NewMockClient()}
	rt, err := NewReplicationTracker(ctx, []string{base}, false, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	s := sop.NewStoreInfo(sop.StoreOptions{Name: "sx", SlotLength: 5, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: time.Minute}})
	if err := sr.Add(ctx, *s); err != nil {
		t.Fatalf("add: %v", err)
	}

	s.CountDelta = 1
	updated, err := sr.Update(ctx, []sop.StoreInfo{*s})
	if err != nil || len(updated) != 1 || updated[0].Count == 0 {
		t.Fatalf("expected update to succeed despite SetStruct warnings; got %v err=%v", updated, err)
	}
}

// marshaler that fails only for sop.StoreInfo with Name == failOn
type storeMarshalerFail struct{ failOn string }

func (m storeMarshalerFail) Marshal(v any) ([]byte, error) {
	if si, ok := v.(sop.StoreInfo); ok && si.Name == m.failOn {
		return nil, errors.New("marshal fail for target store")
	}
	return encoding.DefaultMarshaler.Marshal(v)
}
func (m storeMarshalerFail) Unmarshal(data []byte, v any) error {
	return encoding.DefaultMarshaler.Unmarshal(data, v)
}

func Test_StoreRepository_Update_UndoOnMarshalError(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	ms := NewManageStoreFolder(NewFileIO())
	sr, err := NewStoreRepository(ctx, rt, ms, cache, MinimumModValue)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed two stores
	sA := sop.NewStoreInfo(sop.StoreOptions{Name: "a", SlotLength: 10, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: 5 * time.Minute}})
	sB := sop.NewStoreInfo(sop.StoreOptions{Name: "b", SlotLength: 10, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: 5 * time.Minute}})
	if err := sr.Add(ctx, *sA, *sB); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Remove the persisted metadata for the second store so the fast patch path
	// cannot be used; the fallback path must then hit the marshal error hook.
	if err := os.Remove(filepath.Join(sr.GetStoresBaseFolder(), "b", StoreInfoFilename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove b metadata: %v", err)
	}

	updA := *sA
	updA.CountDelta = 1
	updB := *sB
	updB.CountDelta = 2

	prev := encoding.BlobMarshaler
	encoding.BlobMarshaler = storeMarshalerFail{failOn: "b"}
	t.Cleanup(func() { encoding.BlobMarshaler = prev })

	if _, err := sr.Update(ctx, []sop.StoreInfo{updA, updB}); err == nil {
		t.Fatalf("expected update to error on marshal for store 'b'")
	}
}

// Ensures Update triggers undo path on write error after successful marshal and GetWithTTL from cache.
func Test_StoreRepository_Update_WriteError_Undo(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	s := sop.NewStoreInfo(sop.StoreOptions{Name: "sx", SlotLength: 10, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: 1 * time.Minute}})
	if err := sr.Add(ctx, *s); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Do not clear cache so Update.GetWithTTL uses cache and proceeds to marshal then write.
	// Sabotage the target storeinfo.txt path to a directory to cause write error.
	base := sr.GetStoresBaseFolder()
	target := filepath.Join(base, s.Name, StoreInfoFilename)
	_ = os.Remove(target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	s.CountDelta = 2
	if _, err := sr.Update(ctx, []sop.StoreInfo{*s}); err == nil {
		t.Fatalf("expected update to error due to write failure, triggering undo")
	}
}
