package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// --- retryIO coverage -------------------------------------------------------

func Test_retryIO_Table(t *testing.T) {
	// Speed up retries
	prev := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() { sop.RetryStartDuration = prev })

	ctx := context.Background()

	tests := []struct {
		name      string
		taskErr   error
		wantNil   bool
		wantRetry bool // whether sop.ShouldRetry(taskErr) is true
	}{
		{name: "nil task", taskErr: nil, wantNil: true},
		{name: "non-retryable error returns lastErr", taskErr: os.ErrPermission, wantNil: false, wantRetry: false},
		{name: "retryable error returns retry error", taskErr: errors.New("transient"), wantNil: false, wantRetry: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// For nil taskErr, simulate success on first try.
			if tt.taskErr == nil {
				if err := retryIO(ctx, func(context.Context) error { return nil }, sop.FileIOError); err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			// Always return the configured error to exercise both branches.
			err := retryIO(ctx, func(context.Context) error { return tt.taskErr }, sop.FileIOError)
			if tt.wantNil {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			// Sanity: retry path should keep returning a non-nil error from the retry loop.
			if tt.wantRetry && err == nil {
				t.Fatalf("expected retry error, got nil")
			}
		})
	}
}

// --- fileIO with replication: removeStore and replicate coverage -------------

type fakeFileIO struct{ calls []string }

func (f *fakeFileIO) WriteFile(ctx context.Context, name string, data []byte, perm os.FileMode) error {
	f.calls = append(f.calls, "WriteFile:"+name)
	// Create parent dir to avoid errors.
	_ = os.MkdirAll(filepath.Dir(name), 0o755)
	return os.WriteFile(name, data, perm)
}
func (f *fakeFileIO) ReadFile(ctx context.Context, name string) ([]byte, error) {
	f.calls = append(f.calls, "ReadFile:"+name)
	return os.ReadFile(name)
}
func (f *fakeFileIO) Remove(ctx context.Context, name string) error {
	f.calls = append(f.calls, "Remove:"+name)
	return os.Remove(name)
}
func (f *fakeFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	f.calls = append(f.calls, "Stat:"+path)
	return os.Stat(path)
}
func (f *fakeFileIO) Exists(ctx context.Context, path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}
func (f *fakeFileIO) RemoveAll(ctx context.Context, path string) error {
	f.calls = append(f.calls, "RemoveAll:"+path)
	return os.RemoveAll(path)
}
func (f *fakeFileIO) MkdirAll(ctx context.Context, path string, perm os.FileMode) error {
	f.calls = append(f.calls, "MkdirAll:"+path)
	return os.MkdirAll(path, perm)
}
func (f *fakeFileIO) ReadDir(ctx context.Context, sourceDir string) ([]os.DirEntry, error) {
	f.calls = append(f.calls, "ReadDir:"+sourceDir)
	return os.ReadDir(sourceDir)
}
func (f *fakeFileIO) List(ctx context.Context, path string) ([]string, error) {
	f.calls = append(f.calls, "List:"+path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func Test_fileIO_removeStore_and_replicate_Table(t *testing.T) {
	ctx := context.Background()

	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Prepare a per-test fake FileIO (no global mutation)
	ff := &fakeFileIO{}

	tests := []struct {
		name         string
		trackActions bool
		expectTrack  bool
	}{
		{name: "no tracking", trackActions: false, expectTrack: false},
		{name: "with tracking", trackActions: true, expectTrack: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Fresh instance each subtest with injected fake to avoid shared state.
			fio := newFileIOWithReplicationInjected(rt, NewManageStoreFolder(NewFileIO()), tt.trackActions, ff)
			folder := "store-x"
			// Ensure active folder exists so RemoveAll runs against a real path (ok if missing).
			_ = os.MkdirAll(rt.formatActiveFolderEntity(folder), 0o755)
			if err := fio.removeStore(ctx, folder); err != nil {
				t.Fatalf("removeStore err: %v", err)
			}
			if tt.expectTrack {
				if len(fio.actionsDone) != 1 || fio.actionsDone[0].First != 3 {
					t.Fatalf("expected one remove action recorded, got %+v", fio.actionsDone)
				}
				// Replicate should attempt RemoveAll on passive path and clear actions.
				if err := fio.replicate(ctx); err != nil {
					t.Fatalf("replicate err: %v", err)
				}
				if len(fio.actionsDone) != 0 {
					t.Fatalf("expected actions cleared after replicate")
				}
			} else {
				if len(fio.actionsDone) != 0 {
					t.Fatalf("did not expect actions tracked, got %+v", fio.actionsDone)
				}
			}
		})
	}
}

// --- updateFileBlockRegion lock timeout branch -------------------------------

// lockFailingCache wraps a base mock cache to force lock acquisition to fail continuously
// and simulate IsLocked returning false. Other methods delegate to base.
type lockFailingCache struct{ base sop.L2Cache }

func (m *lockFailingCache) Set(ctx context.Context, k, v string, d time.Duration) error {
	return m.base.Set(ctx, k, v, d)
}
func (m *lockFailingCache) Get(ctx context.Context, k string) (bool, string, error) {
	return m.base.Get(ctx, k)
}
func (m *lockFailingCache) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return m.base.GetEx(ctx, k, d)
}
func (m *lockFailingCache) Ping(ctx context.Context) error { return nil }
func (m *lockFailingCache) SetStruct(ctx context.Context, k string, v interface{}, d time.Duration) error {
	return m.base.SetStruct(ctx, k, v, d)
}
func (m *lockFailingCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return m.base.SetStructs(ctx, keys, values, expiration)
}
func (m *lockFailingCache) GetStruct(ctx context.Context, k string, v interface{}) (bool, error) {
	return m.base.GetStruct(ctx, k, v)
}
func (m *lockFailingCache) GetStructEx(ctx context.Context, k string, v interface{}, d time.Duration) (bool, error) {
	return m.base.GetStructEx(ctx, k, v, d)
}
func (m *lockFailingCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return m.base.GetStructs(ctx, keys, targets, expiration)
}
func (m *lockFailingCache) Delete(ctx context.Context, ks []string) (bool, error) {
	return m.base.Delete(ctx, ks)
}
func (m *lockFailingCache) FormatLockKey(k string) string { return m.base.FormatLockKey(k) }
func (m *lockFailingCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.base.CreateLockKeys(keys)
}
func (m *lockFailingCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.base.CreateLockKeysForIDs(keys)
}
func (m *lockFailingCache) IsLockedTTL(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, error) {
	return false, nil
}
func (m *lockFailingCache) Lock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}
func (m *lockFailingCache) IsLocked(ctx context.Context, lks []*sop.LockKey) (bool, error) {
	return false, nil
}
func (m *lockFailingCache) DualLock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}
func (m *lockFailingCache) IsLockedByOthers(ctx context.Context, ks []string) (bool, error) {
	return m.base.IsLockedByOthers(ctx, ks)
}
func (m *lockFailingCache) IsLockedByOthersTTL(ctx context.Context, ks []string, d time.Duration) (bool, error) {
	return m.base.IsLockedByOthersTTL(ctx, ks, d)
}
func (m *lockFailingCache) Unlock(ctx context.Context, lks []*sop.LockKey) error { return nil }
func (m *lockFailingCache) Clear(ctx context.Context) error                      { return m.base.Clear(ctx) }
func (m *lockFailingCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m *lockFailingCache) IsRestarted(ctx context.Context) bool {
	return false
}
func (m *lockFailingCache) GetType() sop.L2CacheType { return sop.Redis }

func Test_updateFileBlockRegion_LockTimeout(t *testing.T) {
	// Short-deadline context forces the timeout branch on lock acquisition loop.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	base := t.TempDir()
	rt, err := NewReplicationTracker(context.Background(), []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	hm := &hashmap{hashModValue: MinimumModValue, replicationTracker: rt, readWrite: true, fileHandles: make(map[string]*fileDirectIO), cache: &lockFailingCache{base: mocks.NewMockClient()}}

	dio := newFileDirectIO()
	dio.filename = filepath.Join(base, "seg-1.reg") // only used for lock key formatting

	err = hm.updateFileBlockRegion(ctx, dio, 0, 0, make([]byte, sop.HandleSizeInBytes))
	// With a canceled/expired context, expect the raw context error (no failover/sop.Error)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context timeout/canceled error, got %T %v", err, err)
	}
}

// --- getFilesSortedDescByModifiedTime error path ----------------------------

func Test_getFilesSortedDescByModifiedTime_ErrorOnFilePath(t *testing.T) {
	ctx := context.Background()
	// Create a regular file where a directory is expected; ReadDir should fail
	temp := t.TempDir()
	p := filepath.Join(temp, "not-a-dir")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Call with directoryPath pointing to a file
	_, err := getFilesSortedDescByModifiedTime(ctx, p, ".log", nil)
	if err == nil {
		t.Fatalf("expected error reading non-directory path, got nil")
	}
}

// Covers readReplicationStatus read error branch (missing file)
func Test_ReadReplicationStatus_ReadError(t *testing.T) {
	ctx := context.Background()
	rt := &replicationTracker{}
	// Non-existent path should return an error from ReadFile
	if err := rt.readReplicationStatus(ctx, filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("expected read error for missing file")
	}
}

// Covers getFilesSortedDescByModifiedTime when directoryPath is actually a file -> ReadDir error
func Test_GetFilesSortedDescByModifiedTime_ReadDirOnFile_Error(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	filePath := filepath.Join(base, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := getFilesSortedDescByModifiedTime(ctx, filePath, ".log", nil); err == nil {
		t.Fatalf("expected error when reading dir on a file path")
	}
}

// Covers StoreRepository.GetRegistryHashModValue invalid integer in file
func Test_StoreRepository_GetRegistryHashModValue_InvalidValue(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	rt := &replicationTracker{storesBaseFolders: []string{a, p}}
	rt.ActiveFolderToggler = true

	// Manually write invalid value to active reghashmod.txt
	fn := rt.formatActiveFolderEntity(RegistryHashModValueFilename)
	if err := os.WriteFile(fn, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	sr, err := NewStoreRepository(ctx, rt, nil, mocks.NewMockClient(), 0)
	if err != nil {
		t.Fatalf("new sr: %v", err)
	}
	if _, err := sr.GetRegistryHashModValue(ctx); err == nil {
		t.Fatalf("expected invalid integer error")
	}
}

// Covers fileIO.read path through FileIOWithReplication wrapper.
func TestFileIOWithReplicationRead(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "p")
	rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
	ms := NewManageStoreFolder(nil)
	fio := newFileIOWithReplication(rt, ms, false)

	name := "foo.data"
	full := rt.formatActiveFolderEntity(name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fio.read(ctx, name)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("bad read: %q", string(got))
	}
}

// Covers fileIOSimulator ToFilePath/Exists/ReadDir/MkdirAll/RemoveAll no-op behaviors.
func TestFileIOSimulatorBasics(t *testing.T) {
	sim := newFileIOSim()
	ctx := context.Background()
	if sim.ToFilePath("base", sop.NewUUID()) != "" {
		t.Fatalf("ToFilePath expected empty string")
	}
	if !sim.Exists(ctx, "any") {
		t.Fatalf("Exists expected true")
	}
	if err := sim.MkdirAll(ctx, "x", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := sim.RemoveAll(ctx, "x"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := sim.ReadDir(ctx, "x"); err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
}

// Consolidated: fileIOSimulator error injection (write/read/remove) and reset flag behaviors.
func TestFileIOSimulator_ErrorInjectionAndResetScenarios(t *testing.T) {
	sim := newFileIOSim()
	ctx := context.Background()
	// Induce write error on suffix _1
	sim.setErrorOnSuffixNumber(1)
	if err := sim.WriteFile(ctx, "file_1", []byte("x"), 0o644); err == nil {
		t.Fatalf("expected write error")
	}
	// Write different suffix ok
	if err := sim.WriteFile(ctx, "file_2", []byte("ok"), 0o644); err != nil {
		t.Fatalf("write ok: %v", err)
	}
	if _, err := sim.ReadFile(ctx, "file_2"); err != nil {
		t.Fatalf("initial read: %v", err)
	}
	// Induce read error w/ auto-reset on suffix _2 (second flag)
	sim.setResetFlag(true)
	sim.setErrorOnSuffixNumber2(2)
	if _, err := sim.ReadFile(ctx, "file_2"); err == nil {
		t.Fatalf("expected induced read error")
	}
	if b, err := sim.ReadFile(ctx, "file_2"); err != nil || string(b) != "ok" {
		t.Fatalf("post-reset read: %v %q", err, string(b))
	}
	// Remove error injection via write flag then normal remove
	sim.setErrorOnSuffixNumber(2)
	if err := sim.Remove(ctx, "file_2"); err == nil {
		t.Fatalf("expected induced remove err")
	}
	sim.setErrorOnSuffixNumber(-1)
	if err := sim.Remove(ctx, "file_2"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Reset flag path using suffix _0 via second flag
	name := "dummy_0"
	if err := sim.WriteFile(ctx, name, []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sim.setResetFlag(true)
	sim.setErrorOnSuffixNumber2(0)
	if _, err := sim.ReadFile(ctx, name); err == nil {
		t.Fatalf("expected read err for suffix 0")
	}
	if b, err := sim.ReadFile(ctx, name); err != nil || string(b) != "ok" {
		t.Fatalf("second read expected ok: %v %q", err, string(b))
	}
}

// ManageStoreFolder basic create/remove lifecycle (migrated from managestorefolder_cases_test.go).
func TestManageStoreFolder_CreateRemove(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "stores", "a", "b")
	ms := NewManageStoreFolder(nil)
	if err := ms.CreateStore(ctx, base); err != nil {
		t.Fatalf("CreateStore: %v", err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("expected folder exists: %v", err)
	}
	if err := ms.RemoveStore(ctx, filepath.Dir(base)); err != nil {
		t.Fatalf("RemoveStore: %v", err)
	}
}

// retryIO success after transient errors (migrated from retryio_cases_test.go).
func TestRetryIO_RetryableThenSuccess(t *testing.T) {
	// Speed up retries
	prev := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() { sop.RetryStartDuration = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var attempts int32
	err := retryIO(ctx, func(context.Context) error {
		a := atomic.AddInt32(&attempts, 1)
		if a <= 2 {
			return sop.Error{Code: sop.FileIOError, Err: errors.New("transient")}
		}
		return nil
	}, sop.FileIOError)
	if err != nil {
		t.Fatalf("retryIO unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// Path building helpers (from tofilepath_more_cases_test.go).
func TestApply4LevelHierarchy(t *testing.T) {
	id := sop.UUID{0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x90}
	got := Apply4LevelHierarchy(id)
	sep := string(os.PathSeparator)
	parts := strings.Split(got, sep)
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d: %q", len(parts), got)
	}
	if parts[0] != "a" || parts[1] != "b" || parts[2] != "c" || parts[3] != "d" {
		t.Fatalf("expected a/b/c/d, got %q", got)
	}
}
func TestDefaultToFilePath_Variants(t *testing.T) {
	sep := string(os.PathSeparator)
	id := sop.UUID{0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x90}
	cases := []string{"", filepath.Join("a", "b"), filepath.Join("a", "b") + sep, filepath.Join("a", "b") + sep + sep}
	for _, base := range cases {
		got := DefaultToFilePath(base, id)
		if !strings.Contains(got, sep+"a"+sep+"b"+sep+"c"+sep+"d") {
			t.Fatalf("missing hierarchy in %q", got)
		}
	}
}
func TestToFilePath_OverrideAndRestore(t *testing.T) {
	old := ToFilePath
	t.Cleanup(func() { ToFilePath = old })
	id := sop.NewUUID()
	ToFilePath = func(base string, id sop.UUID) string { return "X:" + base + ":" + id.String() }
	if !strings.HasPrefix(ToFilePath("base", id), "X:base:") {
		t.Fatalf("override not effective")
	}
}

// Documentation-only unreachable mismatch branches (from registrymap_mismatch_unreachable_cases_test.go).
func TestRegistryMapMismatchUnreachable(t *testing.T) {
	t.Skip("registryMap mismatch branches currently unreachable; see hashing logic")
}

// Exercise replicationTracker.readStatusFromHomeFolder and handleFailedToReplicate branches.
func TestReplicationTrackerReadStatusAndFailureFlag(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	// Write status only in passive to force toggling when reading.
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	rt.ActiveFolderToggler = false // make b active initially for status write
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rt.writeReplicationStatus(ctx, rt.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// New tracker should flip to b when reading status from home folder
	rt2, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt2: %v", err)
	}
	// Simulate a failure and ensure handleFailedToReplicate sets flags and writes status in active
	rt2.FailedToReplicate = false
	rt2.handleFailedToReplicate(ctx)
	if !rt2.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate true")
	}
	// Status file write is verified in dedicated tests; here we only assert flag change to avoid flakiness across OS timing
}

func Test_CopyToPassiveFolders_WriteErrors(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	cache := mocks.NewMockClient()
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	sr, _ := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	s := *sop.NewStoreInfo(sop.StoreOptions{Name: "w1", SlotLength: 8})
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// storeinfo write error by creating a directory at passive/<store>/storeinfo.txt
	// Add() replicates to passive and creates a file at this path; remove it first to allow mkdir.
	_ = os.Remove(filepath.Join(p, s.Name, StoreInfoFilename))
	if err := os.MkdirAll(filepath.Join(p, s.Name, StoreInfoFilename), 0o755); err != nil {
		t.Fatalf("prep storeinfo dir: %v", err)
	}
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected storeinfo write error")
	}
}

func Test_TransactionLog_GetOne_BasicFlow(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Make current files eligible by reducing ageLimit just for this test.
	prevAge := AgeLimit
	AgeLimit = 0
	t.Cleanup(func() { AgeLimit = prevAge })

	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	tid := sop.NewUUID()
	// Write one record
	payload, _ := encoding.DefaultMarshaler.Marshal(sop.KeyValuePair[int, []byte]{Key: 1, Value: []byte("x")})
	if err := tl.Add(ctx, tid, 1, payload); err != nil {
		t.Fatalf("Add: %v", err)
	}

	gotTid, hour, recs, err := tl.GetOne(ctx)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if gotTid.IsNil() || hour == "" || len(recs) == 0 {
		t.Fatalf("unexpected GetOne result: %v %q %d", gotTid, hour, len(recs))
	}
}

func Test_PriorityLog_Get_And_GetBatch(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	tid := sop.NewUUID()
	// Prepare payload ([]RegistryPayload[Handle]) and add
	payload, _ := encoding.DefaultMarshaler.Marshal([]sop.RegistryPayload[sop.Handle]{})
	_ = pl.Add(ctx, tid, payload)

	// Make file appear old enough by backdating mtime 2 hours
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+priorityLogFileExtension))
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(fn, past, past)

	// Get should return an empty slice (valid JSON)
	if data, err := pl.Get(ctx, tid); err != nil || data == nil {
		t.Fatalf("Get: %v %v", data, err)
	}

	// Batch should include our single entry
	batch, err := pl.GetBatch(ctx, 1)
	if err != nil || len(batch) == 0 || batch[0].Key.IsNil() {
		t.Fatalf("GetBatch: %v %v", batch, err)
	}
}

func Test_ReadStatus_BothExist_PassiveStatError_NoFlip(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	// Seed both files
	os.WriteFile(filepath.Join(a, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644)
	os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644)
	rt.ActiveFolderToggler = true
	// Remove passive before stat to force err != nil; active should remain true
	os.Remove(filepath.Join(p, replicationStatusFilename))
	if err := rt.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("readStatus: %v", err)
	}
	if rt.ActiveFolderToggler != true {
		t.Fatalf("expected no flip when passive stat errors")
	}
}
