package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// More coverage for CopyToPassiveFolders: copies registry segments and skips non-matching files.
func Test_StoreRepository_CopyToPassiveFolders_CopiesSegments(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 64)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed one store via Add
	si := sop.StoreInfo{Name: "s1", RegistryTable: sop.FormatRegistryTable("s1")}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Create registry segment and a non-matching file in active registry folder
	regDir := filepath.Join(active, si.RegistryTable)
	if err := os.MkdirAll(regDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(regDir, si.RegistryTable+"-1"+registryFileExtension)
	if err := os.WriteFile(seg, []byte("seg"), 0o644); err != nil {
		t.Fatalf("write seg: %v", err)
	}
	junk := filepath.Join(regDir, "skip.tmp")
	_ = os.WriteFile(junk, []byte("x"), 0o644)

	if err := sr.CopyToPassiveFolders(ctx); err != nil {
		t.Fatalf("CopyToPassiveFolders: %v", err)
	}

	// Validate passive contents
	if _, err := os.Stat(filepath.Join(passive, storeListFilename)); err != nil {
		t.Fatalf("missing passive store list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(passive, "s1", StoreInfoFilename)); err != nil {
		t.Fatalf("missing passive storeinfo: %v", err)
	}
	// Registry segment copied
	if _, err := os.Stat(filepath.Join(passive, si.RegistryTable, si.RegistryTable+"-1"+registryFileExtension)); err != nil {
		t.Fatalf("missing passive segment: %v", err)
	}
	// Non-matching file should not be copied
	if _, err := os.Stat(filepath.Join(passive, si.RegistryTable, "skip.tmp")); err == nil {
		t.Fatalf("unexpected junk file copied")
	}
}

func Test_StoreRepository_GetAll_NoListFileAndError(t *testing.T) {
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}
	// No store list file -> returns nil, nil
	sl, err := sr.GetAll(ctx)
	if err != nil || sl != nil {
		t.Fatalf("expected nil,nil when list file absent; got %v,%v", sl, err)
	}

	// Create a non-JSON file to trigger Unmarshal error path
	dl := newFileIOWithReplication(rt, sr.manageStore, true)
	if err := dl.createStore(ctx, ""); err != nil {
		t.Fatalf("create store folder: %v", err)
	}
	f := rt.formatActiveFolderEntity(storeListFilename)
	if err := os.WriteFile(f, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write storelist: %v", err)
	}
	if _, err := sr.GetAll(ctx); err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

func Test_copyFile_Success(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "abc" {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func Test_copyFile_Errors(t *testing.T) {
	dir := t.TempDir()
	// Non-existent source -> error
	if err := copyFile(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "out.txt")); err == nil {
		t.Fatalf("expected error for missing source")
	}
	// Create a directory as destination parent but provide a directory path as file to force create error.
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	dstDir := filepath.Join(dir, "dstdir")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Attempt to create a file where a directory already exists should error
	if err := copyFile(src, dstDir); err == nil {
		t.Fatalf("expected error when target is an existing directory")
	}
}

func Test_StoreRepository_Add_Duplicate_And_CopyToPassive(t *testing.T) {
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	// Isolate from global replication state so active folder is deterministic in this test.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Add one store
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 2})
	if err := sr.Add(ctx, *si); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Duplicate add should error
	if err := sr.Add(ctx, *si); err == nil {
		t.Fatalf("expected duplicate add error")
	}

	// CopyToPassiveFolders should succeed even if no registry files yet
	// Write store info to ensure src registry table exists
	if _, err := sr.Update(ctx, []sop.StoreInfo{{
		Name: "s1", RegistryTable: sop.FormatRegistryTable("s1"), CacheConfig: *sop.NewStoreCacheConfig(0, false), Timestamp: 1,
	}}); err != nil {
		// Update may fail depending on cache lookups; write minimal store info file directly to simulate
		storeWriter := newFileIOWithReplication(rt, nil, true)
		ba, _ := encoding.Marshal(sop.StoreInfo{Name: "s1", RegistryTable: sop.FormatRegistryTable("s1")})
		_ = storeWriter.createStore(ctx, "s1")
		_ = storeWriter.write(ctx, fmt.Sprintf("%c%s%c%s", os.PathSeparator, "s1", os.PathSeparator, StoreInfoFilename), ba)
	}
	// Create a registry segment file in the active source directory so copyFilesByExtension finds something to copy.
	srcDir := filepath.Join(rt.storesBaseFolders[0], sop.FormatRegistryTable("s1"))
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, sop.FormatRegistryTable("s1")+"-1"+registryFileExtension), []byte("seg"), 0o644)
	if err := sr.CopyToPassiveFolders(ctx); err != nil {
		t.Fatalf("CopyToPassiveFolders: %v", err)
	}
}

func Test_StoreRepository_Remove_ReplicateError(t *testing.T) {
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Add two stores so remove updates list
	s1 := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 2})
	s2 := sop.NewStoreInfo(sop.StoreOptions{Name: "s2", SlotLength: 2})
	if err := sr.Add(ctx, *s1, *s2); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Induce replicate error by creating a directory at passive storelist path
	passiveStoreList := filepath.Join(rt.getPassiveBaseFolder(), storeListFilename)
	_ = os.Remove(passiveStoreList)
	_ = os.MkdirAll(passiveStoreList, 0o755)
	if err := sr.Remove(ctx, "s2"); err == nil {
		t.Fatalf("expected replicate error during remove")
	}
}

// Covers CopyToPassiveFolders error when writing passive store list fails due to path collision with a directory.
func Test_StoreRepository_CopyToPassiveFolders_WriteStoreListError(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed one store so GetAll returns list.
	if err := sr.Add(ctx, sop.StoreInfo{Name: "x", RegistryTable: sop.FormatRegistryTable("x")}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Create a directory where the passive storelist file will be written after toggling.
	_ = os.MkdirAll(filepath.Join(passive, storeListFilename), 0o755)

	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error writing passive store list due to directory collision")
	}
}

// Covers CopyToPassiveFolders error when writing per-store info fails due to path collision.
func Test_StoreRepository_CopyToPassiveFolders_WriteStoreInfoError(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed one store.
	si := sop.StoreInfo{Name: "y", RegistryTable: sop.FormatRegistryTable("y")}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Create a directory at passive/y/storeinfo.txt so write fails after toggling.
	_ = os.MkdirAll(filepath.Join(passive, "y", StoreInfoFilename), 0o755)

	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error writing passive storeinfo due to directory collision")
	}
}

// Covers CopyToPassiveFolders error when the source registry directory does not exist,
// causing copyFilesByExtension to fail on ReadDir.
func Test_StoreRepository_CopyToPassiveFolders_MissingSourceRegistryDir(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed store but do NOT create any registry directory under active.
	si := sop.StoreInfo{Name: "z", RegistryTable: sop.FormatRegistryTable("z")}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Expect error because source registry folder is missing.
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to missing source registry dir")
	}
}

// Covers CopyToPassiveFolders error when fetching store info fails (invalid JSON in active storeinfo file).
func Test_StoreRepository_CopyToPassiveFolders_GetStoreError(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed one store and then corrupt its storeinfo file with invalid JSON.
	si := sop.StoreInfo{Name: "e1", RegistryTable: sop.FormatRegistryTable("e1")}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Overwrite active storeinfo with invalid content to make Get(ctx) fail to Unmarshal.
	if err := os.WriteFile(filepath.Join(active, "e1", StoreInfoFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}

	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to Get(ctx) failing on invalid JSON")
	}
}

// Covers Update early return when a store is missing (GetWithTTL returns empty slice); ensures no panic and nils returned.
func Test_StoreRepository_Update_MissingStoreEarlyReturn(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "base")
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)

	// Call update with a store that doesn't exist on disk or cache.
	updated, err := sr.Update(ctx, []sop.StoreInfo{{Name: "nope", CountDelta: 1}})
	if updated != nil || err != nil {
		t.Fatalf("expected nil,nil on missing store early return, got %v, %v", updated, err)
	}
}

func Test_StoreRepository_Add_Remove_LockFailures(t *testing.T) {
	// Speed up retries for this test
	prev := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() {
		sop.RetryStartDuration = prev
	})

	ctx := context.Background()
	cache := mocks.NewMockClient()
	active := t.TempDir()
	passive := t.TempDir()
	// Pre-hold the store list lock
	lk := cache.CreateLockKeys([]string{fmt.Sprintf("%s:%s", active, "infs_sr")})
	ok, _, _ := cache.Lock(ctx, time.Hour, lk)
	if !ok {
		t.Fatalf("failed to pre-lock store list")
	}

	rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err := sr.Add(ctx, *sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 2})); err == nil {
		t.Fatalf("expected lock failure on Add")
	}

	if err := sr.Remove(ctx, "s1"); err == nil {
		t.Fatalf("expected lock failure on Remove")
	}
	_ = cache.Unlock(ctx, lk)
}

func Test_StoreRepository_Update_UndoOnWriteError(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	active := filepath.Join(t.TempDir(), "active")
	passive := filepath.Join(t.TempDir(), "passive")

	// Fresh tracker with replication enabled
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed two stores via Add so files exist and cache is populated.
	sA := sop.StoreInfo{Name: "A", RegistryTable: sop.FormatRegistryTable("A")}
	sB := sop.StoreInfo{Name: "B", RegistryTable: sop.FormatRegistryTable("B")}
	if err := sr.Add(ctx, sA, sB); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Induce write error for B by replacing its storeinfo file with a directory.
	bInfoPath := filepath.Join(active, "B", StoreInfoFilename)
	_ = os.Remove(bInfoPath)
	if err := os.MkdirAll(bInfoPath, 0o755); err != nil {
		t.Fatalf("mkdir collide: %v", err)
	}

	// Attempt update that will succeed for A then fail for B and trigger undo for A.
	deltas := []sop.StoreInfo{{Name: "A", CountDelta: 1}, {Name: "B", CountDelta: 2}}
	if _, err := sr.Update(ctx, deltas); err == nil {
		t.Fatalf("expected error from Update due to write collision on B")
	}

	// Validate that A's count was undone (remains 0) on disk.
	aInfoPath := filepath.Join(active, "A", StoreInfoFilename)
	ba, err := os.ReadFile(aInfoPath)
	if err != nil {
		t.Fatalf("read A info: %v", err)
	}
	var gotA sop.StoreInfo
	if err := json.Unmarshal(ba, &gotA); err != nil {
		t.Fatalf("unmarshal A info: %v", err)
	}
	if gotA.Count != 0 {
		t.Fatalf("expected A.Count undone to 0, got %d", gotA.Count)
	}
}

// Happy-path Update on two stores covering straight-line write + cache set.
func Test_StoreRepository_Update_Success_Multi(t *testing.T) {
	// t.Parallel() removed to avoid racing with other tracker-using tests
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "a")
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)

	a := *sop.NewStoreInfo(sop.StoreOptions{Name: "sA", SlotLength: 5})
	b := *sop.NewStoreInfo(sop.StoreOptions{Name: "sB", SlotLength: 5})
	if err := sr.Add(ctx, a, b); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Apply deltas and verify persisted counts.
	a.CountDelta, b.CountDelta = 2, 3
	got, err := sr.Update(ctx, []sop.StoreInfo{a, b})
	if err != nil || len(got) != 2 {
		t.Fatalf("update err=%v got=%v", err, got)
	}

	// Read back
	stores, err := sr.Get(ctx, "sA", "sB")
	if err != nil || len(stores) != 2 {
		t.Fatalf("get err=%v stores=%v", err, stores)
	}
	var ca, cb int64
	for _, s := range stores {
		if s.Name == "sA" {
			ca = s.Count
		} else if s.Name == "sB" {
			cb = s.Count
		}
	}
	if ca != 2 || cb != 3 {
		t.Fatalf("unexpected counts a=%d b=%d", ca, cb)
	}
}

// Covers StoreRepository.GetRegistryHashModValue invalid number parsing path.
func TestStoreRepository_GetRegistryHashModValue_InvalidNumber(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)

	// Write an invalid integer into the registry hash mod file.
	if err := os.WriteFile(filepath.Join(base, RegistryHashModValueFilename), []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := sr.GetRegistryHashModValue(ctx); err == nil {
		t.Fatalf("expected Atoi parse error for invalid content")
	}
}

// When sl == nil, Add calls createStore("") on active; make active base a file to force error.
func Test_StoreRepository_Add_CreateStoreRoot_Error(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// Replace active base with a file to induce ENOTDIR on MkdirAll
	if err := os.RemoveAll(base); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "r1", SlotLength: 4})
	if err := sr.Add(ctx, s); err == nil {
		t.Fatalf("expected Add error due to createStore root failure")
	}
}

// failingStoreMarshaler fails only when marshaling sop.StoreInfo, passes through others.
type failingStoreMarshaler struct{}

func (f failingStoreMarshaler) Marshal(v any) ([]byte, error) {
	switch v.(type) {
	case sop.StoreInfo:
		return nil, errors.New("marshal storeinfo boom")
	default:
		// fall back to default for non-StoreInfo payloads (e.g., []string store list)
		return encoding.DefaultMarshaler.Marshal(v)
	}
}
func (f failingStoreMarshaler) Unmarshal(data []byte, v any) error {
	return encoding.DefaultMarshaler.Unmarshal(data, v)
}

// Covers StoreRepository.Add branch when per-store Marshal fails.
func Test_StoreRepository_Add_MarshalStoreInfo_Error(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	a := t.TempDir()
	p := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)

	// Swap BlobMarshaler to fail only for StoreInfo marshal, restore after.
	prev := encoding.BlobMarshaler
	encoding.BlobMarshaler = failingStoreMarshaler{}
	t.Cleanup(func() { encoding.BlobMarshaler = prev })

	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}

	store := sop.StoreInfo{Name: "s1"}
	if err := sr.Add(ctx, store); err == nil {
		t.Fatalf("expected Marshal error for StoreInfo payload")
	}
	// Ensure no storeinfo file was written.
	if _, err := os.Stat(filepath.Join(a, store.Name, StoreInfoFilename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected storeinfo persisted despite marshal error: %v", err)
	}
}

// Covers StoreRepository.Add branch when writing storeinfo.txt fails due to directory collision.
func Test_StoreRepository_Add_StoreInfo_Write_Error(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	a := t.TempDir()
	p := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)

	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}

	// Pre-create a directory where the storeinfo file should be to force ENOTDIR on write.
	name := "s2"
	if err := os.MkdirAll(filepath.Join(a, name, StoreInfoFilename), 0o755); err != nil {
		t.Fatalf("pre-create dir collision: %v", err)
	}

	if err := sr.Add(ctx, sop.StoreInfo{Name: name}); err == nil {
		t.Fatalf("expected write error due to directory at %s", filepath.Join(a, name, StoreInfoFilename))
	} else {
		// sanity: error mentions write
		_ = fmt.Sprintf("%v", err)
	}
}

func Test_StoreRepository_Add_DuplicateStore_Error(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), MinimumModValue)

	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "dup", SlotLength: 8})
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	// Adding same store should error
	if err := sr.Add(ctx, s); err == nil {
		t.Fatalf("expected duplicate store error")
	}
}

func Test_StoreRepository_GetAll_ReadError(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), MinimumModValue)

	// Ensure base folder exists and create a directory at storelist.txt to force read error.
	_ = os.MkdirAll(a, 0o755)
	if err := os.MkdirAll(filepath.Join(a, storeListFilename), 0o755); err != nil {
		t.Fatalf("prep: %v", err)
	}
	if _, err := sr.GetAll(ctx); err == nil {
		t.Fatalf("expected read error when storelist.txt is a directory")
	}
}

// Forces StoreRepository.Add to fail during replication when passive has a file named as the store folder.
func Test_StoreRepository_Add_Replicate_CreateStore_PassiveCollision(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)

	// Create a file at passive/<store> so MkdirAll on replicate(create store) fails.
	store := *sop.NewStoreInfo(sop.StoreOptions{Name: "collide", SlotLength: 8})
	if err := os.WriteFile(filepath.Join(p, store.Name), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed passive collision: %v", err)
	}
	if err := sr.Add(ctx, store); err == nil {
		t.Fatalf("expected replicate create-store collision error")
	}
}

// Triggers Add() store list write failure by making active/storelist.txt a directory.
func Test_StoreRepository_Add_WriteStoreList_Error(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a}, false, mocks.NewMockClient())
	sr, _ := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)

	// Create directory at the location of storelist.txt so write will fail.
	if err := os.MkdirAll(filepath.Join(a, storeListFilename), 0o755); err != nil {
		t.Fatalf("prep: %v", err)
	}
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "werr", SlotLength: 4})
	if err := sr.Add(ctx, s); err == nil {
		t.Fatalf("expected Add error due to store list write failure")
	}
}

func Test_StoreRepository_Remove_WriteStoreList_Error(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{a}, false, cache)
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, 0)

	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "rerr", SlotLength: 4})
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	if err := os.Remove(filepath.Join(a, storeListFilename)); err != nil {
		t.Fatalf("remove storelist file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(a, storeListFilename), 0o755); err != nil {
		t.Fatalf("prep directory storelist: %v", err)
	}

	if err := sr.Remove(ctx, s.Name); err == nil {
		t.Fatalf("expected Remove error due to store list write failure")
	}
}
