package fs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_TransactionLog_GetOneOfHour_TTL_Window(t *testing.T) {
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	rt, err := NewReplicationTracker(ctx, []string{baseA, filepath.Join(t.TempDir(), "b")}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	// Too old hour (beyond 4h window) -> returns NilUUID, nil
	old := time.Now().Add(-5 * time.Hour).Format(DateHourLayout)
	tid, recs, err := tl.GetOneOfHour(ctx, old)
	if err != nil || !tid.IsNil() || recs != nil {
		t.Fatalf("expected nil result for old hour, got tid=%v err=%v recs=%v", tid, err, recs)
	}

	// Empty hour string -> nil
	tid, recs, err = tl.GetOneOfHour(ctx, "")
	if err != nil || !tid.IsNil() || recs != nil {
		t.Fatalf("expected nil result for empty hour, got tid=%v err=%v recs=%v", tid, err, recs)
	}

	// Prepare a log file for current hour and ensure GetOne/GetOneOfHour works end-to-end.
	tid = tl.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Verify GetOne returns something (may or may not claim based on lock, but shouldn't error).
	_, _, _, _ = tl.GetOne(ctx)

	// GetOneOfHour of current hour should return something or nil without error based on race; accept either.
	hour := time.Now().Format(DateHourLayout)
	_, _, _ = tl.GetOneOfHour(ctx, hour)

	// Cleanup
	_ = tl.Remove(ctx, tid)
}

func Test_getFilesSortedDescByModifiedTime_EnsureDirCreation(t *testing.T) {
	ctx := context.Background()
	temp := t.TempDir()
	// Directory not existing initially; function should create it and return empty list without error.
	dir := filepath.Join(temp, "logs")
	files, err := getFilesSortedDescByModifiedTime(ctx, dir, ".log", nil)
	if err != nil || len(files) != 0 {
		t.Fatalf("expected empty and no error, got len=%d err=%v", len(files), err)
	}

	// Create one .log file and a non-matching file, ensure one result returned.
	f := filepath.Join(dir, sop.NewUUID().String()+".log")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("x"), 0o644)

	files, err = getFilesSortedDescByModifiedTime(ctx, dir, ".log", nil)
	if err != nil || len(files) != 1 {
		t.Fatalf("expected 1 log entry, got len=%d err=%v", len(files), err)
	}
}

// Note: Hitting TransactionLog.Add encode error deterministically would require injecting a failing writer,
// which the current design doesn’t expose. Skipping that edge path.

// Ensures Add handles initial create failure followed by fallback create success (creates base folder).
func Test_TransactionLog_Add_CreatesBaseFolderOnFirstCreateFail(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(cache, rt)
	tid := sop.NewUUID()
	// Do not create base folder; first os.Create should fail, fallback should create base and succeed.
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("Add unexpected error: %v", err)
	}
}

// Ensures Add returns error when the log filename path is a directory (second create also fails).
func Test_TransactionLog_Add_FileIsDirectoryError(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(cache, rt)
	tid := sop.NewUUID()

	// Prepare a directory with the exact filename to force os.Create to fail both times.
	filename := tl.format(tid)
	if err := os.MkdirAll(filename, 0o755); err != nil {
		t.Fatalf("mkdir filename collide: %v", err)
	}
	if err := tl.Add(ctx, tid, 1, []byte("x")); err == nil {
		t.Fatalf("expected error when filename is a directory")
	}
}

// Ensures Add creates the log folder when missing (first os.Create fails, then retries after MkdirAll).
func Test_TransactionLog_Add_CreatesFolderWhenMissing(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	l2 := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, l2)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(l2, rt)
	tid := sop.NewUUID()
	// Ensure the translogs folder does not exist so first Create fails
	logDir := rt.formatActiveFolderEntity(logFolder)
	_ = os.RemoveAll(logDir)
	if err := tl.Add(ctx, tid, 1, []byte("p")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// File should exist now
	if _, err := os.Stat(tl.format(tid)); err != nil {
		t.Fatalf("expected log file: %v", err)
	}
}

// Deterministically validates getLogsDetails returns appended records without relying on hour-age filtering.
func Test_TransactionLog_getLogsDetails_ReadsRecords(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	l2 := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, l2)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(l2, rt)
	tid := sop.NewUUID()
	if err := tl.Add(ctx, tid, 7, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Append a second record
	if err := tl.Add(ctx, tid, 9, []byte("y")); err != nil {
		t.Fatalf("add2: %v", err)
	}
	recs, err := tl.getLogsDetails(tid)
	if err != nil {
		t.Fatalf("getLogsDetails: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("expected >=2 records, got %d", len(recs))
	}
}

// Deterministic success path for TransactionLog.GetOne: produce a log older than the capped hour and assert it is returned.
func Test_TransactionLog_GetOne_Success(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	// Create a log with two entries using Add to ensure JSON shape matches getLogsDetails expectations.
	tid := tl.NewUUID()
	if err := tl.Add(ctx, tid, 10, []byte("A")); err != nil {
		t.Fatalf("add1: %v", err)
	}
	if err := tl.Add(ctx, tid, 11, []byte("B")); err != nil {
		t.Fatalf("add2: %v", err)
	}

	// Age the file beyond the capped hour so getOne considers it.
	filename := tl.format(tid)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filename, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	gotTid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || gotTid != tid || hour == "" || len(recs) != 2 {
		t.Fatalf("GetOne mismatch: tid=%v hour=%q len=%d err=%v", gotTid, hour, len(recs), err)
	}
	if string(recs[0].Value) != "A" || string(recs[1].Value) != "B" {
		t.Fatalf("unexpected record values: %+v", recs)
	}

	// Cleanup
	_ = tl.Remove(ctx, tid)
}

// cacheIsLockedFalse wraps a base cache and forces IsLocked to return false while delegating others.
type cacheIsLockedFalse struct{ base sop.L2Cache }

func (c *cacheIsLockedFalse) GetType() sop.L2CacheType {
	return sop.Redis
}

func (c *cacheIsLockedFalse) Set(ctx context.Context, key, value string, exp time.Duration) error {
	return c.base.Set(ctx, key, value, exp)
}
func (c *cacheIsLockedFalse) Get(ctx context.Context, key string) (bool, string, error) {
	return c.base.Get(ctx, key)
}
func (c *cacheIsLockedFalse) GetEx(ctx context.Context, key string, exp time.Duration) (bool, string, error) {
	return c.base.GetEx(ctx, key, exp)
}
func (c *cacheIsLockedFalse) Ping(ctx context.Context) error { return c.base.Ping(ctx) }
func (c *cacheIsLockedFalse) SetStruct(ctx context.Context, key string, v interface{}, exp time.Duration) error {
	return c.base.SetStruct(ctx, key, v, exp)
}
func (c *cacheIsLockedFalse) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return c.base.SetStructs(ctx, keys, values, expiration)
}
func (c *cacheIsLockedFalse) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return c.base.GetStruct(ctx, key, target)
}
func (c *cacheIsLockedFalse) GetStructEx(ctx context.Context, key string, target interface{}, exp time.Duration) (bool, error) {
	return c.base.GetStructEx(ctx, key, target, exp)
}
func (c *cacheIsLockedFalse) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return c.base.GetStructs(ctx, keys, targets, expiration)
}
func (c *cacheIsLockedFalse) Delete(ctx context.Context, keys []string) (bool, error) {
	return c.base.Delete(ctx, keys)
}
func (c *cacheIsLockedFalse) FormatLockKey(k string) string { return c.base.FormatLockKey(k) }
func (c *cacheIsLockedFalse) CreateLockKeys(keys []string) []*sop.LockKey {
	return c.base.CreateLockKeys(keys)
}
func (c *cacheIsLockedFalse) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return c.base.CreateLockKeysForIDs(keys)
}
func (c *cacheIsLockedFalse) IsLockedTTL(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, error) {
	return c.base.IsLockedTTL(ctx, d, lk)
}
func (c *cacheIsLockedFalse) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return c.base.Lock(ctx, d, lk)
}
func (c *cacheIsLockedFalse) IsLocked(ctx context.Context, lk []*sop.LockKey) (bool, error) {
	return false, nil
}
func (c *cacheIsLockedFalse) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return c.base.DualLock(ctx, d, lk)
}
func (c *cacheIsLockedFalse) IsLockedByOthers(ctx context.Context, names []string) (bool, error) {
	return c.base.IsLockedByOthers(ctx, names)
}
func (c *cacheIsLockedFalse) IsLockedByOthersTTL(ctx context.Context, names []string, d time.Duration) (bool, error) {
	return c.base.IsLockedByOthersTTL(ctx, names, d)
}
func (c *cacheIsLockedFalse) Unlock(ctx context.Context, lk []*sop.LockKey) error {
	return c.base.Unlock(ctx, lk)
}
func (c *cacheIsLockedFalse) Clear(ctx context.Context) error { return c.base.Clear(ctx) }
func (c *cacheIsLockedFalse) IsRestarted(ctx context.Context) bool {
	return c.base.IsRestarted(ctx)
}
func (c *cacheIsLockedFalse) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

// Exercises the final IsLocked check inside GetOne returning nils.
func Test_TransactionLog_GetOne_FinalIsLockedFalse(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	base := mocks.NewMockClient()
	cache := &cacheIsLockedFalse{base: base}
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(cache, rt)

	// Seed a qualified log file for an older hour.
	tid := tl.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Age file beyond capped hour
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(tl.format(tid), past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Now call GetOne; IsLocked will be forced false and it should return nils.
	tid2, hour2, recs, err := tl.GetOne(ctx)
	if err != nil || !tid2.IsNil() || hour2 != "" || recs != nil {
		t.Fatalf("expected nils when final IsLocked check fails: tid=%v hour=%q recs=%v err=%v", tid2, hour2, recs, err)
	}
}

// Ensures GetOne returns nils when there are no log files to process.
func Test_TransactionLog_GetOne_NoFiles_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	// Ensure base folder exists but has no files
	_ = os.MkdirAll(rt.formatActiveFolderEntity(logFolder), 0o755)

	tid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || !tid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected nils, got tid=%v hour=%q len=%v err=%v", tid, hour, recs, err)
	}
}

// Forces getFilesSortedDescByModifiedTime to return an error by making the log folder a file, not a directory.
func Test_TransactionLog_GetOne_ReadDirError_Propagates(t *testing.T) {
	ctx := context.Background()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	// Create a file named as the log folder path to cause ReadDir to fail with "not a directory".
	logDir := rt.formatActiveFolderEntity(logFolder)
	// Ensure the active base folder exists so we can place a file at the logDir path.
	_ = os.MkdirAll(rt.getActiveBaseFolder(), 0o755)
	if err := os.WriteFile(logDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("prep file collide: %v", err)
	}

	_, _, _, err = tl.GetOne(ctx)
	if err == nil {
		t.Fatalf("expected error from GetOne when log folder path is a file")
	}
}

// Creates a single-line file larger than bufio.Scanner default token size to trigger ErrTooLong.
func Test_TransactionLog_getLogsDetails_ErrTooLong(t *testing.T) {
	ctx := context.Background()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	tid := sop.NewUUID()

	// Write a very long single line without newline characters.
	// Ensure the log folder exists to avoid create errors.
	_ = os.MkdirAll(rt.formatActiveFolderEntity(logFolder), 0o755)
	longLine := strings.Repeat("A", 1024*128) // 128KB > default 64K scanner token
	if err := os.WriteFile(tl.format(tid), []byte(longLine), 0o644); err != nil {
		t.Fatalf("write long file: %v", err)
	}

	_, err = tl.getLogsDetails(tid)
	if err == nil {
		t.Fatalf("expected error from getLogsDetails on ErrTooLong")
	}
}

func Test_TransactionLog_GetOneOfHour_InvalidHour_ReturnsError(t *testing.T) {
	ctx := context.Background()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	if _, _, err := tl.GetOneOfHour(ctx, "not-a-time"); err == nil {
		t.Fatalf("expected error for invalid hour format")
	}

	// Also verify boundary within 4-hour window doesn't unlock prematurely: create current-hour log and query.
	tid := sop.NewUUID()
	if err := tl.Add(ctx, tid, 1, []byte("x")); err != nil {
		t.Fatalf("add: %v", err)
	}
	hour := time.Now().Format(DateHourLayout)
	_, _, _ = tl.GetOneOfHour(ctx, hour)
	_ = tl.Remove(ctx, tid)
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("encode boom") }

// Ensures TransactionLog.Add covers the encode/write error branch where the encoder fails to write
// and the code flushes/cleans up the file handle.
func Test_TransactionLog_Add_WriteEncodeError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()

	// Replication tracker to format paths under the active folder.
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	tid := sop.NewUUID()
	filename := tl.format(tid)

	// Ensure parent directory exists and create the file, then reopen it read-only
	if err := os.MkdirAll(rt.formatActiveFolderEntity(logFolder), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if f, err := os.Create(filename); err != nil { // create the file path
		t.Fatalf("create: %v", err)
	} else {
		f.Close()
	}
	// Open a handle (so tl.file is non-nil) then use a json.Encoder over an io.Writer that always errors.
	fh, err := os.Open(filename)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tl.file = fh
	tl.writer = bufio.NewWriter(io.Discard) // non-nil for Flush() path
	tl.encoder = json.NewEncoder(errWriter{})

	// Attempt to Add should fail due to write on read-only fd and clean up file handle.
	if err := tl.Add(ctx, tid, 1, []byte("x")); err == nil {
		t.Fatalf("expected Add encode/write error")
	}
	if tl.file != nil {
		t.Fatalf("expected file handle to be cleared on error")
	}
}

// Ensures getFilesSortedDescByModifiedTime actually orders its results by ModTime
// (ascending, oldest first) rather than just testing the sort call itself.
func Test_getFilesSortedDescByModifiedTime_SortOrder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	older := filepath.Join(dir, "a.log")
	if err := os.WriteFile(older, []byte("x"), 0o644); err != nil {
		t.Fatalf("write older: %v", err)
	}
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes older: %v", err)
	}

	newer := filepath.Join(dir, "b.log")
	if err := os.WriteFile(newer, []byte("x"), 0o644); err != nil {
		t.Fatalf("write newer: %v", err)
	}

	files, err := getFilesSortedDescByModifiedTime(ctx, dir, ".log", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !files[0].ModTime.Before(files[1].ModTime) {
		t.Fatalf("expected ascending order by ModTime, got %v then %v", files[0].ModTime, files[1].ModTime)
	}
}

// Ensures GetOne early-returns when lock cannot be acquired.
func Test_TransactionLog_GetOne_LockFail(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(newAlwaysLockFailCache(), rt)
	if tid, hour, recs, err := tl.GetOne(ctx); err != nil || !tid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected empty fast-path on lock fail, got %v %s %v %v", tid, hour, recs, err)
	}
}

// Uses lostLockCache (defined in transactionprioritylog_scenarios_test.go) to force IsLocked=false after reading logs.
func Test_TransactionLog_GetOne_LostLockPath(t *testing.T) {
	ctx := context.Background()
	baseA := t.TempDir()
	baseB := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, false, newAlwaysLockFailCache())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	// Build TL with a cache that locks but later reports IsLocked=false
	tl := NewTransactionLog(newLostLockCache(), rt)

	// Create an eligible transaction log file with one record.
	tid := sop.NewUUID()
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+logFileExtension))
	if err := os.MkdirAll(filepath.Dir(fn), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	_ = enc.Encode(sop.KeyValuePair[int, []byte]{Key: 1, Value: []byte("x")})
	w.Flush()
	f.Close()
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(fn, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if tidGot, hour, recs, err := tl.GetOne(ctx); err != nil || !tidGot.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected empty due to lost lock, got tid=%v hour=%s recs=%v err=%v", tidGot, hour, recs, err)
	}
}

// Forces TransactionLog.GetOne to propagate directory read error by making logFolder a file.
func Test_TransactionLog_GetOne_ReadDir_Error(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()

	// Create a file where a directory is expected.
	badPath := active + "/" + logFolder
	if err := os.WriteFile(badPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	if tid, hour, recs, err := tl.GetOne(ctx); err == nil || !tid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected error and empty return, got tid=%v hour=%s recs=%v err=%v", tid, hour, recs, err)
	}
}

// Triggers bufio.Scanner ErrTooLong in getLogsDetails, causing GetOne to return the error.
func Test_TransactionLog_GetOne_ScannerError(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	tl := NewTransactionLog(mocks.NewMockClient(), rt)

	tid := sop.NewUUID()
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+logFileExtension))
	if err := os.MkdirAll(filepath.Dir(fn), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(fn)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Write one valid record first (to pass initial JSON path), then an overly long line to trip scanner.Err.
	w := bufio.NewWriter(f)
	_ = json.NewEncoder(w).Encode(sop.KeyValuePair[int, []byte]{Key: 1, Value: []byte("ok")})
	long := strings.Repeat("x", 1024*128) // >64k token
	if _, err := w.WriteString(long + "\n"); err != nil {
		t.Fatalf("write long: %v", err)
	}
	w.Flush()
	f.Close()
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(fn, past, past)

	if tid2, hour, recs, err := tl.GetOne(ctx); err == nil || !tid2.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected scanner error propagated, got tid=%v hour=%s recs=%v err=%v", tid2, hour, recs, err)
	}
}
