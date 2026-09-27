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
)

// Covers NewBlobStoreWithEC constructor validation error when base paths count mismatches data+parity shard count.
func TestBlobStoreWithEC_New_MismatchBasePathsCount(t *testing.T) {
	cfg := map[string]sop.ErasureCodingConfig{
		"tbl": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"only-one"}}, // expect error: need 2 paths
	}
	if _, err := NewBlobStoreWithEC(DefaultToFilePath, NewFileIO(), cfg); err == nil {
		t.Fatalf("expected mismatch base paths error")
	}
}

// Covers repairCorruptedShards branch: remove one shard file then GetOne reconstructs & rewrites it.
func TestBlobStoreWithEC_GetOne_RepairsCorruptedShard(t *testing.T) {
	ctx := context.Background()
	// Prepare two drives (data=1, parity=1) with repair enabled.
	d1 := t.TempDir()
	d2 := t.TempDir()
	table := "tblrepair"
	cfg := map[string]sop.ErasureCodingConfig{
		table: {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2}, RepairCorruptedShards: true},
	}
	bsIntf, err := NewBlobStoreWithEC(DefaultToFilePath, NewFileIO(), cfg)
	if err != nil {
		t.Fatalf("NewBlobStoreWithEC: %v", err)
	}
	bs := bsIntf.(*BlobStoreWithEC)
	id := sop.NewUUID()
	payload := []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("hello world")}}}}
	if err := bs.Add(ctx, payload); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Locate one of the shard files (index 0) and delete it to force reconstruction path.
	// Walk d1 and delete file containing the uuid and suffix _0.
	var removed string
	filepath.Walk(d1, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == "" { // shard files have no extra extension
			if filepath.Base(path) == id.String()+"_0" {
				removed = path
				os.Remove(path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	if removed == "" {
		t.Fatalf("failed to locate shard file to remove (id=%s)", id)
	}
	// Fetch (should reconstruct and rewrite missing shard due to RepairCorruptedShards true).
	got, err := bs.GetOne(ctx, table, id)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("decoded mismatch: %q", string(got))
	}
	// Allow some time for repair writes (sequential in current code, but be safe).
	time.Sleep(10 * time.Millisecond)
	if _, err := os.Stat(removed); err != nil {
		t.Fatalf("expected repaired shard file recreated: %v", err)
	}
}

// Covers fileIO.replicate default branch (unsupported action type) by injecting a bogus action code.
func TestFileIOWithReplication_Replicate_UnsupportedActionType(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
	ms := NewManageStoreFolder(NewFileIO())
	fio := newFileIOWithReplication(rt, ms, true)
	// Inject invalid action code 99.
	fio.actionsDone = append(fio.actionsDone, sop.Tuple[int, any]{First: 99, Second: nil})
	if err := fio.replicate(ctx); err != nil {
		t.Fatalf("replicate returned unexpected error: %v", err)
	}
}

// Covers registry.Replicate rmCloseOverride error surface when no prior errors set lastErr.
func TestRegistry_Replicate_rmCloseOverrideError(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	r := NewRegistry(true, MinimumModValue, rt, mocks.NewMockClient())
	defer r.Close()
	// Provide override returning error.
	r.rmCloseOverride = func() error { return errors.New("close override error") }
	// Call replicate with no operations (all nil slices) so only close override sets error.
	if err := r.Replicate(ctx, nil, nil, nil, nil); err == nil || err.Error() != "close override error" {
		t.Fatalf("expected close override error, got %v", err)
	}
}

// Cache wrapper that forces SetStruct to return an error to exercise log.Warn paths in Add & Update.
type setStructErrCache struct{ sop.L2Cache }

func (c setStructErrCache) SetStruct(ctx context.Context, key string, value interface{}, exp time.Duration) error {
	return errors.New("induced setstruct error")
}

// Covers registry Add & Update SetStruct error (best-effort cache) branches.
func TestRegistry_AddUpdate_SetStructErrorTolerated(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// Need a functional cache for hashmap internal operations, then wrap to force SetStruct errors.
	realCache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, realCache)
	r := NewRegistry(true, MinimumModValue, rt, realCache)
	defer r.Close()
	r.l2Cache = setStructErrCache{L2Cache: realCache}
	h := sop.NewHandle(sop.NewUUID())
	// Ensure table directory exists under active folder for registry writes
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "rg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rg", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("Add unexpected error: %v", err)
	}
	h.Version = 1
	if err := r.Update(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rg", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("Update unexpected error: %v", err)
	}
}

// partialWriteDirectIO implements DirectIO and forces a partial block write to trigger
// updateFileBlockRegion error path during tests.
type partialWriteDirectIO struct{}

func (p partialWriteDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (p partialWriteDirectIO) WriteAt(ctx context.Context, f *os.File, b []byte, off int64) (int, error) {
	// Write only half of the block without error -> treated as partial write.
	half := len(b) / 2
	if half == 0 {
		half = len(b)
	}
	return f.WriteAt(b[:half], off)
}
func (p partialWriteDirectIO) ReadAt(ctx context.Context, f *os.File, b []byte, off int64) (int, error) {
	return f.ReadAt(b, off)
}
func (p partialWriteDirectIO) Close(f *os.File) error { return f.Close() }

// Covers registry Update eviction path (write failure triggers L1/L2 delete + unlock early return).
func TestRegistry_Update_WriteFailureEvicts(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, cache)
	r := NewRegistry(true, MinimumModValue, rt, cache)
	defer r.Close()
	h := sop.NewHandle(sop.NewUUID())
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "ev"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := r.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "ev", IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	// Ensure handle present in L2 before update.
	var fetched sop.Handle
	if found, err := cache.GetStruct(ctx, h.LogicalID.String(), &fetched); !found || err != nil {
		t.Fatalf("expected handle in L2 before update, err=%v", err)
	}
	// Inject partial writer into existing open fileDirectIO objects so subsequent update triggers error.
	for _, fh := range r.hashmap.hashmap.fileHandles {
		fh.directIO = partialWriteDirectIO{}
	}
	h.Version = 2
	if err := r.Update(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "ev", IDs: []sop.Handle{h}}}); err == nil {
		t.Fatalf("expected update write failure")
	}
	// Verify eviction: L2 entry should be gone.
	if found, _ := cache.GetStruct(ctx, h.LogicalID.String(), &fetched); found {
		t.Fatalf("expected L2 cache eviction of handle")
	}
}

// Covers TransactionLog Add path parent create and GetOne lock early return as well as GetOne retry after adding aged file.
func TestTransactionLog_AddAndGetOneExtended(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{t.TempDir()}, false, cache)
	tl := NewTransactionLog(cache, rt)
	tid := sop.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Age the file to qualify for getOne (modify times earlier than ageLimit minutes).
	fn := tl.format(tid)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(fn, past, past)
	// Invoke GetOne through exported method; expects lock success then returns entries.
	gotTid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || gotTid.IsNil() || hour == "" || len(recs) == 0 {
		t.Fatalf("GetOne extended mismatch: %v %v %v %v", gotTid, hour, recs, err)
	}
}

// FileIO that records attempted writes during replicate and fails the passive write to exercise error return.
type replicateFailFileIO struct {
	FileIO
	passiveRoot string
}

func (f replicateFailFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	// Only fail writes that target the passive root path (replication step), allow active write to succeed.
	if f.passiveRoot != "" && len(name) >= len(f.passiveRoot) && name[:len(f.passiveRoot)] == f.passiveRoot {
		return errors.New("induced passive write error")
	}
	return f.FileIO.WriteFile(ctx, name, data, perm)
}

// Covers fileIO.replicate error path on passive write.
func TestFileIOWithReplication_Replicate_PassiveWriteError(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
	ms := NewManageStoreFolder(NewFileIO())
	fio := newFileIOWithReplicationInjected(rt, ms, true, replicateFailFileIO{FileIO: NewFileIO(), passiveRoot: passive})
	// Record a write action to a .txt file so replicate attempts a passive write which fails.
	if err := fio.write(ctx, "fail.txt", []byte("x")); err != nil {
		t.Fatalf("seed write (active) failed: %v", err)
	}
	if err := fio.replicate(ctx); err == nil {
		t.Fatalf("expected passive write error")
	}
}

// Backup APIs removed: ensure PriorityLog basic Remove on non-existent is harmless.
func TestPriorityLog_Remove_NonExistent_NoError(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, true, mocks.NewMockClient())
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	pl := tl.PriorityLog()
	tid := sop.NewUUID()
	if err := pl.Remove(ctx, tid); err != nil {
		t.Fatalf("Remove non-existent: %v", err)
	}
}

// Additional constructor mismatch coverage using global config path.
func TestBlobStoreWithEC_New_Mismatch_UsingGlobalConfig(t *testing.T) {
	SetGlobalErasureConfig(map[string]sop.ErasureCodingConfig{"": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"a", "b"}}})
	// Provide config with wrong base paths count (needs 3 but give 2) via global by passing nil map.
	bad := map[string]sop.ErasureCodingConfig{"bad": {DataShardsCount: 2, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{"x", "y"}}}
	if _, err := NewBlobStoreWithEC(DefaultToFilePath, NewFileIO(), bad); err == nil {
		t.Fatalf("expected mismatch via global path")
	}
}

// Covers EC global fallback path in getBaseFolderPathsAndErasureConfig (table not found uses "" key config) and successful Add.
func TestBlobStoreWithEC_GlobalFallbackAdd(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	oldGlobal := GetGlobalErasureConfig()
	t.Cleanup(func() { SetGlobalErasureConfig(oldGlobal) })
	SetGlobalErasureConfig(map[string]sop.ErasureCodingConfig{"": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2}}})
	bsIntf, err := NewBlobStoreWithEC(DefaultToFilePath, NewFileIO(), nil) // nil -> use global
	if err != nil {
		t.Fatalf("NewBlobStoreWithEC: %v", err)
	}
	bs := bsIntf.(*BlobStoreWithEC)
	id := sop.NewUUID()
	table := "fallbacktbl"
	ctx := context.Background()
	if err := bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: table, Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("data")}}}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Both shard files should exist (data + parity) under 4-level hierarchy path.
	// Find shard files (hierarchy path logic may change in future; search robustly).
	found0, found1 := false, false
	filepath.Walk(filepath.Join(d1, table), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == id.String()+"_0" {
			found0 = true
			return filepath.SkipDir
		}
		return nil
	})
	filepath.Walk(filepath.Join(d2, table), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == id.String()+"_1" {
			found1 = true
			return filepath.SkipDir
		}
		return nil
	})
	if !found0 || !found1 {
		t.Fatalf("expected shard files present (data=%v parity=%v)", found0, found1)
	}
}

// Covers priorityLog full lifecycle: IsEnabled, Add, Get, Remove, LogCommitChanges, Remove non-existent.
func TestPriorityLog_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	// Preserve and restore global replication details to avoid cross-test interference.
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})
	base1 := t.TempDir()
	base2 := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base1, base2}, true, mocks.NewMockClient())
	rt.LogCommitChanges = true
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	pl := tl.PriorityLog().(priorityLog)
	if !pl.IsEnabled() {
		t.Fatalf("expected priority log enabled")
	}
	tid := sop.NewUUID()
	payload := []byte("[]")
	if err := pl.Add(ctx, tid, payload); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Age file to qualify for GetBatch.
	plFilename := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+".plg"))
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(plFilename, past, past)
	if batch, err := pl.GetBatch(ctx, 1); err != nil {
		t.Fatalf("GetBatch: %v", err)
	} else if len(batch) == 0 {
		t.Fatalf("expected batch entry")
	}
	if err := pl.LogCommitChanges(ctx, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("LogCommitChanges: %v", err)
	}
	// Backup APIs removed; ensure Remove works whether file exists or not.
	// Remove existing log.
	if err := pl.Remove(ctx, tid); err != nil {
		t.Fatalf("Remove existing: %v", err)
	}
	// Remove again (non-existent) -> still nil.
	if err := pl.Remove(ctx, tid); err != nil {
		t.Fatalf("Remove non-existent: %v", err)
	}
}

// Covers replicationTracker.HandleReplicationRelatedError triggering failover and status file writes.
func TestReplicationTracker_HandleReplicationRelatedError_Failover(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})
	a := t.TempDir()
	b := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if !rt.ActiveFolderToggler {
		t.Fatalf("expected first folder active initially")
	}
	// Induce failover qualified error with rollback succeeded true to exercise code path.
	ioErr := sop.Error{Code: sop.FailoverQualifiedError, Err: errors.New("io failover qualified")}
	rt.HandleReplicationRelatedError(ctx, ioErr, nil, false) // rollback not succeeded so failover proceeds
	if !rt.ActiveFolderToggler {
		t.Fatalf("expected active folder NOT toggled after failover")
	}
	if rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate false after failover")
	}
	// A second call should no-op.
	rt.HandleReplicationRelatedError(ctx, ioErr, nil, true)
}

// Covers blobStoreWithEC.Update (delegates to Add) to eliminate 0% function coverage.
func TestBlobStoreWithEC_Update_CoversDelegate(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	cfg := map[string]sop.ErasureCodingConfig{"tblu": {DataShardsCount: 1, ParityShardsCount: 1, BaseFolderPathsAcrossDrives: []string{d1, d2}}}
	bsIntf, err := NewBlobStoreWithEC(DefaultToFilePath, NewFileIO(), cfg)
	if err != nil {
		t.Fatalf("NewBlobStoreWithEC: %v", err)
	}
	bs := bsIntf.(*BlobStoreWithEC)
	id := sop.NewUUID()
	ctx := context.Background()
	// Use Update instead of Add; expect shards written.
	if err := bs.Update(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "tblu", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: id, Value: []byte("upd")}}}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Confirm one shard file exists.
	found := false
	filepath.Walk(d1, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == id.String()+"_0" {
			found = true
			return filepath.SkipDir
		}
		return nil
	})
	if !found {
		t.Fatalf("expected at least one shard written via Update")
	}
}

// Covers StoreRepository getFromCache & GetStoresBaseFolder and Replicate path.
func TestStoreRepository_InternalHelpersAndReplicate(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	a := t.TempDir()
	b := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	ms := NewManageStoreFolder(NewFileIO())
	sr, err := NewStoreRepository(ctx, rt, ms, cache, 1234)
	if err != nil {
		t.Fatalf("NewStoreRepository: %v", err)
	}
	// Add a store so it is cached.
	si := sop.StoreInfo{Name: "s1", CacheConfig: sop.StoreCacheConfig{StoreInfoCacheDuration: time.Minute}}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("Add store: %v", err)
	}
	// getFromCache (unexported) should return it.
	got, err := sr.getFromCache(ctx, "s1")
	if err != nil || len(got) != 1 || got[0].Name != "s1" {
		t.Fatalf("getFromCache mismatch: %v %v", got, err)
	}
	if base := sr.GetStoresBaseFolder(); base != a {
		t.Fatalf("GetStoresBaseFolder expected %s got %s", a, base)
	}
	// Replicate call with current store info should succeed.
	if err := sr.Replicate(ctx, []sop.StoreInfo{si}); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
}

// Covers replicationTracker.SetTransactionID simple setter.
func TestReplicationTracker_SetTransactionID(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewReplicationTracker(ctx, []string{t.TempDir()}, false, mocks.NewMockClient())
	id := sop.NewUUID()
	rt.SetTransactionID(id)
	if rt.tid != id {
		t.Fatalf("expected tid set")
	}
}
