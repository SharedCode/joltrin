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

// This scenarios suite merges all behaviors previously covered across the various
// prioritylog_* and transactionlog_* test files. It targets:
//  - PriorityLog basic lifecycle (Add/Get/Remove, backups, IsEnabled)
//  - PriorityLog batching (age gating, default batch size, batchSize<=0 path, malformed filename skip)
//  - PriorityLog edge branches (missing file -> nil,nil; unmarshal error; empty eligible set; folder auto-create)
//  - TransactionLog Add / Remove (including second Remove), NewUUID
//  - TransactionLog GetOne (no files, eligible selection, records decoding)
//  - TransactionLog GetOneOfHour (empty hour fast path, bad hour parse error)
//  - getFilesSortedDescByModifiedTime directory creation + filter behavior
//  - Commit changes logging path when enabled via replicationTracker.LogCommitChanges
//  - fileDirectIO close error path

// helper to build a TransactionLog + replication tracker pair.
func newTLRT(t *testing.T) (*TransactionLog, *replicationTracker) {
	t.Helper()
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	return tl, rt
}

func TestTransactionAndPriorityLog_Scenarios(t *testing.T) {
	ctx := context.Background()
	tl, rt := newTLRT(t)
	pl := tl.PriorityLog()

	// PriorityLog IsEnabled
	if !pl.IsEnabled() {
		t.Fatalf("PriorityLog IsEnabled expected true")
	}

	// Priority Add/Get with proper JSON
	tid1 := sop.NewUUID()
	payload1 := []sop.RegistryPayload[sop.Handle]{
		{RegistryTable: "rt1", BlobTable: "bt1", IDs: []sop.Handle{{LogicalID: sop.NewUUID()}}},
	}
	ba1, _ := encoding.DefaultMarshaler.Marshal(payload1)
	if err := pl.Add(ctx, tid1, ba1); err != nil {
		t.Fatalf("Priority Add: %v", err)
	}
	got, err := pl.Get(ctx, tid1)
	if err != nil || len(got) != 1 || got[0].RegistryTable != "rt1" {
		t.Fatalf("Priority Get mismatch: %+v err=%v", got, err)
	}

	// Remove existing then again (graceful)
	if err := pl.Remove(ctx, tid1); err != nil {
		t.Fatalf("Priority Remove: %v", err)
	}
	if err := pl.Remove(ctx, tid1); err != nil {
		t.Fatalf("Priority Remove (second) unexpected err: %v", err)
	}

	// Edge: Get missing => nil,nil
	if r, e := pl.Get(ctx, sop.NewUUID()); e != nil || r != nil {
		t.Fatalf("expected nil,nil for missing priority log, got %v,%v", r, e)
	}

	// Unmarshal error path: write non-json file and Get -> error
	tidBad := sop.NewUUID()
	badFn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tidBad.String()+priorityLogFileExtension))
	if err := os.MkdirAll(filepath.Dir(badFn), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	payload := []byte("not-json")
	blob := make([]byte, len(payload)+4)
	marshalData(payload, blob)
	if err := os.WriteFile(badFn, blob, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if r, e := pl.Get(ctx, tidBad); e == nil || r != nil {
		t.Fatalf("expected unmarshal error, got %v,%v", r, e)
	}

	// Batching scenarios
	// 1. Folder auto-create & nil return when empty
	rtEmptyBase := filepath.Join(t.TempDir(), "emptyA")
	rtEmpty, err := NewReplicationTracker(ctx, []string{rtEmptyBase, filepath.Join(t.TempDir(), "b")}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rtEmpty: %v", err)
	}
	plEmpty := NewTransactionLog(mocks.NewMockClient(), rtEmpty).PriorityLog()
	if batch, e := plEmpty.GetBatch(ctx, 5); e != nil || batch != nil {
		t.Fatalf("expected nil batch on empty folder, got %v err=%v", batch, e)
	}

	// 2. Ready vs not-ready (age gating) + batch size limit
	oldTid := sop.NewUUID()
	newTid := sop.NewUUID()
	logDir := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir logDir: %v", err)
	}
	oldFile := filepath.Join(logDir, oldTid.String()+priorityLogFileExtension)
	// newFile is not needed as pl.Add writes it
	if err := pl.Add(ctx, oldTid, []byte("[]")); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := pl.Add(ctx, newTid, []byte("[]")); err != nil {
		t.Fatalf("write new: %v", err)
	}
	nowHour, _ := time.Parse(DateHourLayout, time.Now().Format(DateHourLayout))
	// Age the file beyond the batching threshold: lock TTL plus an extra 10 minutes buffer.
	past := nowHour.Add(-(LockFileRegionDuration + 10*time.Minute))
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}
	batchOld, err := pl.GetBatch(ctx, 10)
	if err != nil || len(batchOld) == 0 || batchOld[0].Key != oldTid {
		t.Fatalf("expected oldTid in batch, got %v err=%v", batchOld, err)
	}

	// 3. Default batch size path (batchSize<=0) with multiple aged files
	agedIDs := []sop.UUID{sop.NewUUID(), sop.NewUUID(), sop.NewUUID()}
	for _, id := range agedIDs {
		fn := filepath.Join(logDir, id.String()+priorityLogFileExtension)
		if err := pl.Add(ctx, id, []byte("[]")); err != nil {
			t.Fatalf("write aged: %v", err)
		}
		past2 := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(fn, past2, past2); err != nil {
			t.Fatalf("chtimes aged: %v", err)
		}
	}
	if batchDef, e := pl.GetBatch(ctx, 0); e != nil || len(batchDef) == 0 {
		t.Fatalf("expected default batch items, got %v err=%v", batchDef, e)
	}

	// 4. Malformed filename skip
	badName := filepath.Join(logDir, "badname"+priorityLogFileExtension)
	payload2 := []byte("[]")
	blob2 := make([]byte, len(payload2)+4)
	marshalData(payload2, blob2)
	if err := os.WriteFile(badName, blob2, 0o644); err != nil {
		t.Fatalf("write badname: %v", err)
	}
	if batchAfterBad, e := pl.GetBatch(ctx, 0); e != nil || len(batchAfterBad) == 0 {
		t.Fatalf("expected non-empty batch after malformed, got %v err=%v", batchAfterBad, e)
	}

	// TransactionLog Add / Remove / NewUUID
	tidTx := tl.NewUUID()
	if tidTx.IsNil() {
		t.Fatalf("NewUUID returned nil")
	}
	payloads := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma")}
	for i, p := range payloads {
		if err := tl.Add(ctx, tidTx, i, p); err != nil {
			t.Fatalf("Tx Add %d: %v", i, err)
		}
	}
	if err := tl.Remove(ctx, tidTx); err != nil {
		t.Fatalf("Tx Remove: %v", err)
	}
	_ = tl.Remove(ctx, tidTx) // second path (ignore error)

	// Create another eligible transaction log file & an ineligible one to test GetOne
	elig := sop.NewUUID()
	inelig := sop.NewUUID()
	if err := tl.Add(ctx, elig, 1, []byte("e1")); err != nil {
		t.Fatalf("Add elig: %v", err)
	}
	if err := tl.Add(ctx, inelig, 1, []byte("i1")); err != nil {
		t.Fatalf("Add inelig: %v", err)
	}
	fnElig := rt.formatActiveFolderEntity(filepath.Join(logFolder, elig.String()+logFileExtension))
	agedTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(fnElig, agedTime, agedTime); err != nil {
		t.Fatalf("chtimes elig: %v", err)
	}

	// GetOne when eligible exists
	tidGot, hour, recs, err := tl.GetOne(ctx)
	if err != nil || tidGot.IsNil() || hour == "" || len(recs) == 0 {
		t.Fatalf("GetOne expected populated, got tid=%v hour=%s recs=%v err=%v", tidGot, hour, recs, err)
	}

	// GetOne with no eligible (fresh TL instance, empty folder)
	tlEmpty := NewTransactionLog(mocks.NewMockClient(), rt)
	_ = tlEmpty.Remove(ctx, sop.NewUUID()) // no-op just to touch path
	// Temporarily point to new empty folder
	rt2base := filepath.Join(t.TempDir(), "c")
	rt2, _ := NewReplicationTracker(ctx, []string{rt2base, filepath.Join(t.TempDir(), "d")}, false, mocks.NewMockClient())
	tlNoFiles := NewTransactionLog(mocks.NewMockClient(), rt2)
	if tid, hr, rr, e := tlNoFiles.GetOne(ctx); e != nil || !tid.IsNil() || hr != "" || rr != nil {
		t.Fatalf("GetOne expected empty on no files, got %v %s %v %v", tid, hr, rr, e)
	}

	// GetOneOfHour variants
	if tid, recs, e := tl.GetOneOfHour(ctx, ""); e != nil || !tid.IsNil() || recs != nil {
		t.Fatalf("GetOneOfHour empty hour mismatch")
	}
	if _, _, e := tl.GetOneOfHour(ctx, "bad-hour"); e == nil {
		t.Fatalf("expected parse error for bad hour")
	}

	// Commit changes logging path
	rt.LogCommitChanges = true
	tidCommit := sop.NewUUID()
	tl.tid = tidCommit
	if err := tl.PriorityLog().LogCommitChanges(ctx, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("LogCommitChanges: %v", err)
	}
	commitFn := rt.formatActiveFolderEntity(filepath.Join(commitChangesLogFolder, tidCommit.String()+logFileExtension))
	if _, err := os.Stat(commitFn); err != nil {
		t.Fatalf("commit log missing: %v", err)
	}

	// getFilesSortedDescByModifiedTime helper coverage (dir create + filter)
	helperDir := filepath.Join(t.TempDir(), "logs-sub")
	files, err := getFilesSortedDescByModifiedTime(ctx, helperDir, ".xyz", func(de os.DirEntry) bool { return true })
	if err != nil || len(files) != 0 {
		t.Fatalf("helper initial create mismatch: %v len=%d", err, len(files))
	}
	f1 := filepath.Join(helperDir, "a"+priorityLogFileExtension)
	f2 := filepath.Join(helperDir, "b"+priorityLogFileExtension)
	if err := os.WriteFile(f1, []byte("1"), 0o644); err != nil {
		t.Fatalf("write f1: %v", err)
	}
	if err := os.WriteFile(f2, []byte("2"), 0o644); err != nil {
		t.Fatalf("write f2: %v", err)
	}
	pastH := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(f1, pastH, pastH); err != nil {
		t.Fatalf("chtimes f1: %v", err)
	}
	files, err = getFilesSortedDescByModifiedTime(ctx, helperDir, priorityLogFileExtension, func(de os.DirEntry) bool { return de.Name() != "b"+priorityLogFileExtension })
	if err != nil || len(files) != 1 {
		t.Fatalf("helper filtered mismatch: %v len=%d", err, len(files))
	}

	// fileDirectIO close error branch
	fio := newFileDirectIOInjected(failingCloseDirectIO{})
	name := filepath.Join(t.TempDir(), "c.dat")
	if err := fio.open(ctx, name, os.O_RDWR, 0o644); err != nil {
		t.Fatalf("directIO open: %v", err)
	}
	if err := fio.close(); err == nil || err.Error() != "close fail" {
		t.Fatalf("expected close fail error, got %v", err)
	}

	// Commit log write error path: remove commit folder after enabling logging to force internal write error.
	{
		cache := mocks.NewMockClient()
		active := t.TempDir()
		passive := t.TempDir()
		prevGlobal := GlobalReplicationDetails
		GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
		GlobalReplicationDetails.LogCommitChanges = true
		t.Cleanup(func() { GlobalReplicationDetails = prevGlobal })
		rtErr, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
		if err != nil {
			t.Fatalf("rtErr: %v", err)
		}
		rtErr.LogCommitChanges = true
		ms := NewManageStoreFolder(NewFileIO())
		sr, err := NewStoreRepository(ctx, rtErr, ms, cache, MinimumModValue)
		if err != nil {
			t.Fatalf("sr: %v", err)
		}
		si := sop.NewStoreInfo(sop.StoreOptions{Name: "tx1", SlotLength: 10})
		if err := sr.Add(ctx, *si); err != nil {
			t.Fatalf("Add store: %v", err)
		}
		commitDir := rtErr.formatActiveFolderEntity(commitChangesLogFolder)
		if err := os.MkdirAll(commitDir, 0o755); err != nil {
			t.Fatalf("mkdir commitDir: %v", err)
		}
		if err := os.RemoveAll(commitDir); err != nil {
			t.Fatalf("remove commitDir: %v", err)
		}
		reg := NewRegistry(true, MinimumModValue, rtErr, cache)
		h := sop.NewHandle(sop.NewUUID())
		_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})
		if _, err := os.Stat(filepath.Join(active, commitChangesLogFolder)); !os.IsNotExist(err) {
			t.Fatalf("expected commit log dir absent to reflect write error path, err=%v", err)
		}
	}
}

// Local copy of failingCloseDirectIO (legacy file stubbed out)
type failingCloseDirectIO struct{ DirectIO }

func (f failingCloseDirectIO) Close(file *os.File) error { return errors.New("close fail") }
func (f failingCloseDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(filename, flag|os.O_CREATE, perm)
}
func (f failingCloseDirectIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (f failingCloseDirectIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.ReadAt(block, offset)
}

// --- Additional consolidated micro-tests previously in separate small files ---

// alwaysLockFailCache simulates a cache where Lock never succeeds.
type alwaysLockFailCache struct{ mocksCache sop.L2Cache }

func newAlwaysLockFailCache() *alwaysLockFailCache {
	return &alwaysLockFailCache{mocks.NewMockClient()}
}

func (c *alwaysLockFailCache) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c *alwaysLockFailCache) Set(ctx context.Context, k, v string, d time.Duration) error {
	return c.mocksCache.Set(ctx, k, v, d)
}
func (c *alwaysLockFailCache) Get(ctx context.Context, k string) (bool, string, error) {
	return c.mocksCache.Get(ctx, k)
}
func (c *alwaysLockFailCache) GetEx(ctx context.Context, k string, d time.Duration) (bool, string, error) {
	return c.mocksCache.GetEx(ctx, k, d)
}
func (c *alwaysLockFailCache) Ping(ctx context.Context) error { return nil }
func (c *alwaysLockFailCache) SetStruct(ctx context.Context, k string, v interface{}, d time.Duration) error {
	return c.mocksCache.SetStruct(ctx, k, v, d)
}
func (c *alwaysLockFailCache) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.mocksCache.SetStructs(ctx, keys, values, expiration)
}
func (c *alwaysLockFailCache) GetStruct(ctx context.Context, k string, v interface{}) (bool, error) {
	return c.mocksCache.GetStruct(ctx, k, v)
}
func (c *alwaysLockFailCache) GetStructEx(ctx context.Context, k string, v interface{}, d time.Duration) (bool, error) {
	return c.mocksCache.GetStructEx(ctx, k, v, d)
}
func (c *alwaysLockFailCache) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.mocksCache.GetStructs(ctx, keys, targets, expiration)
}
func (c *alwaysLockFailCache) Delete(ctx context.Context, ks []string) (bool, error) {
	return c.mocksCache.Delete(ctx, ks)
}
func (c *alwaysLockFailCache) FormatLockKey(k string) string { return c.mocksCache.FormatLockKey(k) }
func (c *alwaysLockFailCache) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.mocksCache.CreateLockKeys(keys)
}
func (c *alwaysLockFailCache) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.mocksCache.CreateLockKeysForIDs(keys)
}
func (c *alwaysLockFailCache) IsLockedTTL(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, error) {
	return c.mocksCache.IsLockedTTL(ctx, d, lks)
}
func (c *alwaysLockFailCache) Lock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}
func (c *alwaysLockFailCache) IsLocked(ctx context.Context, lks []*sop.LockKey) (bool, error) {
	return false, nil
}
func (c *alwaysLockFailCache) DualLock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}
func (c *alwaysLockFailCache) IsLockedByOthers(ctx context.Context, ks []string) (bool, error) {
	return c.mocksCache.IsLockedByOthers(ctx, ks)
}
func (c *alwaysLockFailCache) IsLockedByOthersTTL(ctx context.Context, ks []string, d time.Duration) (bool, error) {
	return c.mocksCache.IsLockedByOthersTTL(ctx, ks, d)
}
func (c *alwaysLockFailCache) Unlock(ctx context.Context, lks []*sop.LockKey) error { return nil }
func (c *alwaysLockFailCache) Clear(ctx context.Context) error                      { return c.mocksCache.Clear(ctx) }
func (c *alwaysLockFailCache) IsRestarted(ctx context.Context) bool {
	return c.mocksCache.IsRestarted(ctx)
}
func (c *alwaysLockFailCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

// lostLockCache acquires locks but reports them lost on IsLocked check.
type lostLockCache struct{ *alwaysLockFailCache }

func newLostLockCache() *lostLockCache { return &lostLockCache{newAlwaysLockFailCache()} }
func (c *lostLockCache) Lock(ctx context.Context, d time.Duration, lks []*sop.LockKey) (bool, sop.UUID, error) {
	return true, sop.NilUUID, nil
}
func (c *lostLockCache) IsLocked(ctx context.Context, lks []*sop.LockKey) (bool, error) {
	return false, nil
}
func (c *lostLockCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

// Covers the successful GetOneOfHour path (eligible file within TTL window returning records).
func TestTransactionLog_GetOneOfHour_Success(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	tid := sop.NewUUID()
	for i := 0; i < 3; i++ {
		if err := tl.Add(ctx, tid, i, []byte{byte('a' + i)}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+logFileExtension))
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(fn, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	hour := time.Now().Format(DateHourLayout)
	gotTid, recs, err := tl.GetOneOfHour(ctx, hour)
	if err != nil {
		t.Fatalf("GetOneOfHour err: %v", err)
	}
	if gotTid.IsNil() || gotTid != tid {
		t.Fatalf("expected tid %s got %s", tid, gotTid)
	}
	if len(recs) == 0 {
		t.Fatalf("expected records, got 0")
	}
}

func TestTransactionLog_GetOneOfHour_TooOldUnlock(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	oldHour := time.Now().Add(-5 * time.Hour).Format(DateHourLayout)
	tid, recs, err := tl.GetOneOfHour(ctx, oldHour)
	if err != nil {
		t.Fatalf("GetOneOfHour err: %v", err)
	}
	if !tid.IsNil() || recs != nil {
		t.Fatalf("expected nil result for too-old hour, got %v %v", tid, recs)
	}
}

func TestTransactionLog_getLogsDetails_UnmarshalSkip(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	tid := sop.NewUUID()
	filename := tl.format(tid)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	validKV := sop.KeyValuePair[int, []byte]{Key: 1, Value: []byte("x")}
	vb, _ := encoding.DefaultMarshaler.Marshal(validKV)
	content := []byte("not-json\n" + string(vb) + "\n")
	if err := os.WriteFile(filename, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs, err := tl.getLogsDetails(tid)
	if err != nil {
		t.Fatalf("getLogsDetails err: %v", err)
	}
	if len(recs) != 1 || recs[0].Key != 1 {
		t.Fatalf("expected 1 valid record, got %+v", recs)
	}
}

func TestPriorityLog_GetBatch_WithCorruptFileError(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()
	dir := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tidValid := sop.NewUUID()
	vf := filepath.Join(dir, tidValid.String()+priorityLogFileExtension)
	if err := pl.Add(ctx, tidValid, []byte("[]")); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(vf, past, past); err != nil {
		t.Fatalf("chtimes valid: %v", err)
	}
	tidBad := sop.NewUUID()
	bf := filepath.Join(dir, tidBad.String()+priorityLogFileExtension)
	payload3 := []byte("not-json")
	blob3 := make([]byte, len(payload3)+4)
	marshalData(payload3, blob3)
	if err := os.WriteFile(bf, blob3, 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if err := os.Chtimes(bf, past.Add(1*time.Minute), past.Add(1*time.Minute)); err != nil {
		t.Fatalf("chtimes bad: %v", err)
	}
	batch, err := pl.GetBatch(ctx, 10)
	if err == nil {
		t.Fatalf("expected error due to corrupt file, got nil")
	}
	if len(batch) == 0 {
		t.Fatalf("expected at least one valid batch entry, got 0")
	}
}

func TestPriorityLog_BasicGetRemovePaths_Merged(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	pl := tl.PriorityLog()
	if !pl.IsEnabled() {
		t.Fatalf("expected enabled")
	}
	tid := sop.NewUUID()
	if recs, err := pl.Get(ctx, tid); err != nil || recs != nil {
		t.Fatalf("expected nil,nil get absent, got %v %v", recs, err)
	}
	if err := pl.Remove(ctx, tid); err != nil {
		t.Fatalf("remove absent: %v", err)
	}
	// Removed unused variable payload
}

func TestTransactionLog_RemoveClosesFile_Merged(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	tid := sop.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	if tl.file == nil {
		t.Fatalf("expected file opened")
	}
	if err := tl.Remove(ctx, tid); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(tl.format(tid)); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestTransactionLog_getOne_IgnoresInvalidFiles_Merged(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	base := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	invalid := filepath.Join(base, "not-a-uuid"+logFileExtension)
	os.WriteFile(invalid, []byte("{}"), 0o644)
	os.Chtimes(invalid, past, past)
	tid := sop.NewUUID()
	valid := filepath.Join(base, tid.String()+logFileExtension)
	os.WriteFile(valid, []byte("{}"), 0o644)
	os.Chtimes(valid, past.Add(-1*time.Minute), past.Add(-1*time.Minute))
	hour, gotTid, err := tl.getOne(ctx)
	if err != nil {
		t.Fatalf("getOne err: %v", err)
	}
	if gotTid != tid {
		t.Fatalf("expected tid %s got %s", tid, gotTid)
	}
	if hour == "" {
		t.Fatalf("expected hour string")
	}
}

func TestTransactionLog_GetOne_LockFailure(t *testing.T) {
	ctx := context.Background()
	rt, _ := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	tl := NewTransactionLog(newAlwaysLockFailCache(), rt)
	tid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || !tid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected empty result on lock failure")
	}
}

func TestTransactionLog_GetOne_LostLockAfterRead(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base, t.TempDir()}, false, mocks.NewMockClient())
	cache := newLostLockCache()
	tl := NewTransactionLog(cache, rt)
	tid := sop.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	fn := tl.format(tid)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(fn, past, past)
	gotTid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || !gotTid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected nil results lost-lock path, got %v %v %v %v", gotTid, hour, recs, err)
	}
}
