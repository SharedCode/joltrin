package fs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

type fakeDirectIO struct{}

func (f *fakeDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (f *fakeDirectIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (f *fakeDirectIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.ReadAt(block, offset)
}
func (f *fakeDirectIO) Close(file *os.File) error { return file.Close() }

type failingDirectIO struct {
	openErr      error
	partialRead  bool
	partialWrite bool
}

func (f *failingDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (f *failingDirectIO) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	if f.partialWrite {
		return len(block) - 10, nil
	}
	return file.WriteAt(block, offset)
}
func (f *failingDirectIO) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	if f.partialRead {
		return len(block) - 20, nil
	}
	return file.ReadAt(block, offset)
}
func (f *failingDirectIO) Close(file *os.File) error { return file.Close() }

type mockCacheHashmap struct {
	base           sop.L2Cache
	lockFail       bool
	isLockedAlways bool
}

func (m *mockCacheHashmap) GetType() sop.L2CacheType {
	return sop.Redis
}

func (m *mockCacheHashmap) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	if m.lockFail {
		return false, sop.NilUUID, nil
	}
	return m.base.Lock(ctx, d, lk)
}
func (m *mockCacheHashmap) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	if m.lockFail {
		return false, sop.NilUUID, nil
	}
	return m.base.DualLock(ctx, d, lk)
}
func (m *mockCacheHashmap) IsLocked(ctx context.Context, lk []*sop.LockKey) (bool, error) {
	if m.isLockedAlways {
		return true, nil
	}
	return m.base.IsLocked(ctx, lk)
}
func (m *mockCacheHashmap) Set(ctx context.Context, key, value string, exp time.Duration) error {
	return m.base.Set(ctx, key, value, exp)
}
func (m *mockCacheHashmap) Get(ctx context.Context, key string) (bool, string, error) {
	return m.base.Get(ctx, key)
}
func (m *mockCacheHashmap) GetEx(ctx context.Context, key string, exp time.Duration) (bool, string, error) {
	return m.base.GetEx(ctx, key, exp)
}
func (m *mockCacheHashmap) Ping(ctx context.Context) error { return m.base.Ping(ctx) }
func (m *mockCacheHashmap) SetStruct(ctx context.Context, key string, v interface{}, exp time.Duration) error {
	return m.base.SetStruct(ctx, key, v, exp)
}
func (m *mockCacheHashmap) SetStructs(ctx context.Context, keys []string, values []interface{}, expiration time.Duration) error {
	return m.base.SetStructs(ctx, keys, values, expiration)
}
func (m *mockCacheHashmap) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	return m.base.GetStruct(ctx, key, target)
}
func (m *mockCacheHashmap) GetStructEx(ctx context.Context, key string, target interface{}, exp time.Duration) (bool, error) {
	return m.base.GetStructEx(ctx, key, target, exp)
}
func (m *mockCacheHashmap) GetStructs(ctx context.Context, keys []string, targets []interface{}, expiration time.Duration) ([]bool, error) {
	return m.base.GetStructs(ctx, keys, targets, expiration)
}
func (m *mockCacheHashmap) Delete(ctx context.Context, keys []string) (bool, error) {
	return m.base.Delete(ctx, keys)
}
func (m *mockCacheHashmap) FormatLockKey(k string) string { return m.base.FormatLockKey(k) }
func (m *mockCacheHashmap) CreateLockKeys(keys []string) []*sop.LockKey {
	return m.base.CreateLockKeys(keys)
}
func (m *mockCacheHashmap) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	return m.base.CreateLockKeysForIDs(keys)
}
func (m *mockCacheHashmap) IsLockedTTL(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, error) {
	return m.base.IsLockedTTL(ctx, d, lk)
}
func (m *mockCacheHashmap) IsLockedByOthers(ctx context.Context, names []string) (bool, error) {
	return m.base.IsLockedByOthers(ctx, names)
}
func (m *mockCacheHashmap) IsLockedByOthersTTL(ctx context.Context, names []string, d time.Duration) (bool, error) {
	return m.base.IsLockedByOthersTTL(ctx, names, d)
}
func (m *mockCacheHashmap) Unlock(ctx context.Context, lk []*sop.LockKey) error {
	return m.base.Unlock(ctx, lk)
}
func (m *mockCacheHashmap) Clear(ctx context.Context) error { return m.base.Clear(ctx) }
func (m *mockCacheHashmap) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}
func (m *mockCacheHashmap) IsRestarted(ctx context.Context) bool {
	return false
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	max := len(s) - len(sub) + 1
	for i := 0; i < max; i++ {
		if s[i] == sub[0] && s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = errors.New

// Consolidated hashmap scenarios covering: find/update/delete, scan branch, error branches
// (lock fail, partial read/write, open fail), helper functions (offset calc, getIDs), fetch flow,
// mark delete + not found, setupNewFile lock failure, and isZeroData.
func TestHashmap_AllScenarios(t *testing.T) {
	prev := lockSectorRetryTimeoutDuration
	lockSectorRetryTimeoutDuration = 100 * time.Millisecond
	prevRetry := sop.RetryStartDuration
	sop.RetryStartDuration = 10 * time.Millisecond
	t.Cleanup(func() {
		lockSectorRetryTimeoutDuration = prev
		sop.RetryStartDuration = prevRetry
	})
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	cache := mocks.NewMockClient()
	hm := newHashmap(true, 32, rt, cache)
	// --- Scenario: write + read + delete lifecycle ---
	table := "regcase"
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := sop.NewUUID()
	frd, err := hm.findOneFileRegion(ctx, true, table, id)
	if err != nil {
		t.Fatalf("findOneFileRegion(write): %v", err)
	}
	frd.handle = sop.NewHandle(id)
	if err := hm.updateFileRegion(ctx, []fileRegionDetails{frd}); err != nil {
		t.Fatalf("updateFileRegion(prepare): %v", err)
	}
	if err := hm.updateFileRegion(ctx, []fileRegionDetails{frd}); err != nil {
		t.Fatalf("updateFileRegion(commit): %v", err)
	}
	frdRead, err := hm.findOneFileRegion(ctx, false, table, id)
	if err != nil || frdRead.handle.LogicalID != id {
		t.Fatalf("read locate mismatch: %v", err)
	}
	if err := hm.markDeleteFileRegion(ctx, []fileRegionDetails{frdRead}); err != nil {
		t.Fatalf("markDelete(prepare): %v", err)
	}
	if err := hm.markDeleteFileRegion(ctx, []fileRegionDetails{frdRead}); err != nil {
		t.Fatalf("markDelete(commit): %v", err)
	}
	if _, err := hm.findOneFileRegion(ctx, false, table, id); err == nil {
		t.Fatalf("expected not found after delete")
	}

	// --- Scenario: scan branch (handle in non-ideal slot) ---
	hmScan := newHashmap(true, 64, rt, cache)
	id2 := sop.NewUUID()
	bo, ideal := hmScan.getBlockOffsetAndHandleInBlockOffset(id2)
	// Place the handle at a non-ideal offset to force a scan. Ensure it stays within block bounds.
	var alt int64 = ideal + sop.HandleSizeInBytes
	if alt+int64(sop.HandleSizeInBytes) > blockSize {
		alt = 0 // wrap to start if it would overflow the block
	}
	blk := make([]byte, blockSize)
	m := sop.NewHandle(id2)
	// marshal via encoding marshaler
	mar := encoding.NewHandleMarshaler()
	hb, _ := mar.Marshal(m, make([]byte, 0, sop.HandleSizeInBytes))
	copy(blk[alt:alt+int64(len(hb))], hb)
	scanDir := filepath.Join(base, "scanmm")
	os.MkdirAll(scanDir, 0o755)
	segFile := filepath.Join(scanDir, "scanmm-1"+registryFileExtension)
	f, _ := os.OpenFile(segFile, os.O_CREATE|os.O_RDWR, 0o644)
	f.Truncate(hmScan.getSegmentFileSize())
	f.WriteAt(blk, bo)
	f.Close()
	if r, err := hmScan.fetch(ctx, "scanmm", []sop.UUID{id2}); err != nil || len(r) != 1 {
		t.Fatalf("scan fetch: %v", err)
	}

	// --- Scenario: setupNewFile lock acquisition failure ---
	mcLockFail := &mockCacheHashmap{base: mocks.NewMockClient(), lockFail: true}
	hmLock := newHashmap(true, 16, rt, mcLockFail)
	if _, err := hmLock.findOneFileRegion(ctx, true, "tblLock", sop.NewUUID()); err == nil {
		t.Fatalf("expected lock fail")
	}

	// --- Scenario: partial read & partial write in updateFileBlockRegion (via injected dio) ---
	mcAlways := &mockCacheHashmap{base: mocks.NewMockClient(), isLockedAlways: true}
	hmPart := newHashmap(true, 16, rt, mcAlways)
	// Create a temp segment file and open with injected DirectIO to control read/write behavior.
	segPart := filepath.Join(base, "partio-1.reg")
	if err := os.WriteFile(segPart, make([]byte, blockSize), 0o644); err != nil {
		t.Fatalf("seed seg for partials: %v", err)
	}
	// Case 1: partial read without error -> expect explicit error
	dioPR := newFileDirectIOInjected(&failingDirectIO{partialRead: true})
	if err := dioPR.open(ctx, segPart, os.O_RDWR, permission); err != nil {
		t.Fatalf("open seg (pr): %v", err)
	}
	if err := hmPart.updateFileBlockRegion(ctx, dioPR, 0, 0, make([]byte, sop.HandleSizeInBytes)); err == nil {
		t.Fatalf("expected partial read err")
	}
	_ = dioPR.close()
	// Case 2: full read, partial write (n != blockSize, err=nil) -> expect explicit error
	dioPW := newFileDirectIOInjected(&failingDirectIO{partialWrite: true})
	if err := dioPW.open(ctx, segPart, os.O_RDWR, permission); err != nil {
		t.Fatalf("open seg (pw): %v", err)
	}
	if err := hmPart.updateFileBlockRegion(ctx, dioPW, 0, 0, make([]byte, sop.HandleSizeInBytes)); err == nil {
		t.Fatalf("expected partial write err")
	}
	_ = dioPW.close()

	// --- Scenario: write error in updateFileBlockRegion (err != nil branch) ---
	// Covered separately from partial write above which exercises (err == nil && n != blockSize).
	dioWriteErr := newFileDirectIO()
	segFileWE := filepath.Join(base, "writeerr-seg.reg")
	if err := dioWriteErr.open(ctx, segFileWE, os.O_CREATE|os.O_RDWR, permission); err != nil {
		t.Fatalf("open seg: %v", err)
	}
	// Seed a full block so read succeeds (avoids partial read branch).
	seed := make([]byte, blockSize)
	if n, err := dioWriteErr.file.WriteAt(seed, 0); err != nil || n != blockSize {
		t.Fatalf("seed full block: n=%d err=%v", n, err)
	}
	dioWriteErr.file.Close()
	dioRO := newFileDirectIO()
	if err := dioRO.open(ctx, segFileWE, os.O_RDONLY, permission); err != nil {
		t.Fatalf("ro open: %v", err)
	}
	handleData := make([]byte, sop.HandleSizeInBytes)
	// Should fail as it writes to read-only file
	if err := hm.updateFileBlockRegion(ctx, dioRO, 0, 0, handleData); err == nil {
		t.Fatalf("expected write error branch")
	}

	// --- Scenario: open failure path (simulate via permissions) ---
	{
		hmOpenErr := newHashmap(false, 8, rt, mocks.NewMockClient())
		table := "tblOpen"
		dir := filepath.Join(base, table)
		_ = os.MkdirAll(dir, 0o755)
		seg := filepath.Join(dir, table+"-1"+registryFileExtension)
		// Create a properly sized segment and make it unreadable to force open error.
		if err := os.WriteFile(seg, make([]byte, hmOpenErr.getSegmentFileSize()), 0o000); err != nil {
			t.Fatalf("seed unreadable seg: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(seg, 0o644) })
		if _, err := hmOpenErr.findOneFileRegion(ctx, false, table, sop.NewUUID()); err == nil {
			t.Fatalf("expected open error")
		}
	}

	// --- Scenario: helpers getBlockOffset, getIDs, isZeroData ---
	high, low := id.Split()
	_ = high
	_ = low
	if boCalc, _ := hm.getBlockOffsetAndHandleInBlockOffset(id); boCalc < 0 {
		t.Fatalf("offset negative")
	}
	ids := getIDs([]sop.Handle{sop.NewHandle(id)})
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("getIDs mismatch")
	}
	if !isZeroData(make([]byte, sop.HandleSizeInBytes)) {
		t.Fatalf("isZeroData false")
	}

	// Close to ensure close path executed.
	if err := hm.close(); err != nil {
		t.Fatalf("close err: %v", err)
	}
}

// Additional: fetch returns only existing IDs when mixed (existing + missing)
func TestHashmap_Fetch_MixedIDs_Scenario(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	hm := newHashmap(true, 32, rt, mocks.NewMockClient())
	idExisting := sop.NewUUID()
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "tblmix"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	frd, err := hm.findOneFileRegion(ctx, true, "tblmix", idExisting)
	if err != nil {
		t.Fatalf("prep frd: %v", err)
	}
	frd.handle = sop.NewHandle(idExisting)
	if err := hm.updateFileRegion(ctx, []fileRegionDetails{frd}); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	missing := sop.NewUUID()
	r, err := hm.fetch(ctx, "tblmix", []sop.UUID{idExisting, missing})
	if err != nil || len(r) != 1 || r[0].LogicalID != idExisting {
		t.Fatalf("unexpected fetch result: %v %+v", err, r)
	}
}

// directIOReadEOF simulates a DirectIO that returns io.EOF on ReadAt for the first read,
// allowing findOneFileRegion(forWriting=false) to hit the EOF branch and continue/return not found.
type directIOReadEOF struct{}

func (directIOReadEOF) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	// Ensure file exists to trigger reads; create empty file.
	if flag&os.O_CREATE != 0 {
		f, err := os.OpenFile(filename, flag, perm)
		return f, err
	}
	// Open read-only when not creating; create if missing.
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, perm)
	return f, err
}
func (directIOReadEOF) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (directIOReadEOF) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	// Always signal EOF to simulate short file (smaller than one block).
	return 0, io.EOF
}
func (directIOReadEOF) Close(file *os.File) error { return file.Close() }

// directIOShortRead simulates a partial successful read (n < blockSize, no error) to hit the
// partial-read error branch.
type directIOShortRead struct{}

func (directIOShortRead) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, perm)
	return f, err
}
func (directIOShortRead) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (directIOShortRead) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	// Return a small number to simulate partial read without error.
	if len(block) > 16 {
		return 16, nil
	}
	return len(block), nil
}
func (directIOShortRead) Close(file *os.File) error { return file.Close() }

func TestHashmap_findOneFileRegion_ReadEOFAndPartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	hm := newHashmap(true, 8, rt, mocks.NewMockClient())
	// Pre-create segment file so Open succeeds.
	seg := filepath.Join(rt.getActiveBaseFolder(), "tblEOF", "tblEOF-1.reg")
	if err := os.MkdirAll(filepath.Dir(seg), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(seg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close()

	// Inject a DirectIO that always returns EOF via the fileDirectIO wrapper.
	dioEOF := newFileDirectIOInjected(directIOReadEOF{})
	// Open the injected dio on the segment file and place it into hashmap's fileHandles.
	if err := dioEOF.open(ctx, seg, os.O_RDWR, permission); err != nil {
		t.Fatalf("open injected EOF dio: %v", err)
	}
	hm.fileHandles[seg] = dioEOF

	if _, err := hm.findOneFileRegion(ctx, false, "tblEOF", sop.NewUUID()); err == nil {
		t.Fatalf("expected not found error on EOF for read path")
	}
	// Case 2: partial read without error triggers explicit partial-read error.
	seg2 := filepath.Join(rt.getActiveBaseFolder(), "tblPartial", "tblPartial-1.reg")
	if err := os.MkdirAll(filepath.Dir(seg2), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f2, err := os.Create(seg2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f2.Close()

	dioShort := newFileDirectIOInjected(directIOShortRead{})
	if err := dioShort.open(ctx, seg2, os.O_RDWR, permission); err != nil {
		t.Fatalf("open injected short dio: %v", err)
	}
	hm.fileHandles[seg2] = dioShort

	if _, err := hm.findOneFileRegion(ctx, false, "tblPartial", sop.NewUUID()); err == nil || err.Error() == "" {
		t.Fatalf("expected partial-read error, got %v", err)
	}
}

// Ensure fetch() skips IDs missing with not found errors and returns other errors.
func TestHashmap_fetch_MissingIDsSkipsAndError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())

	hm := newHashmap(true, 8, rt, mocks.NewMockClient())

	// Case 1: Small segment file triggers idNotFound path; fetch should skip and return empty slice.
	segDir := filepath.Join(base, "tblF")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(segDir, "tblF-1"+registryFileExtension)
	if err := os.WriteFile(seg, make([]byte, blockSize/2), 0o644); err != nil {
		t.Fatalf("seed small seg: %v", err)
	}
	items, err := hm.fetch(ctx, "tblF", []sop.UUID{sop.NewUUID(), sop.NewUUID()})
	if err != nil {
		t.Fatalf("fetch unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %d", len(items))
	}

	// Case 2: Propagate non-notfound error by making segment unreadable.
	seg2 := filepath.Join(base, "tblF2", "tblF2-1"+registryFileExtension)
	if err := os.MkdirAll(filepath.Dir(seg2), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Proper size so code takes the read path; then chmod 0 to cause open/read failure.
	if err := os.WriteFile(seg2, make([]byte, int64(8)*blockSize), 0o000); err != nil {
		t.Fatalf("seed unreadable seg: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(seg2, 0o644) })
	if _, err := hm.fetch(ctx, "tblF2", []sop.UUID{sop.NewUUID()}); err == nil {
		t.Fatalf("expected error from fetch on non-notfound path")
	}
}

// directIOError returns a generic error on ReadAt to exercise the error return path in fetch.
type directIOError struct{}

func (directIOError) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, perm)
	return f, err
}
func (directIOError) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (directIOError) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return 0, errors.New("boom")
}
func (directIOError) Close(file *os.File) error { return file.Close() }

func Test_findOneFileRegion_AdditionalBranches_Table(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())

	cases := []struct {
		name          string
		prep          func(t *testing.T) (*hashmap, string, sop.UUID)
		forWriting    bool
		wantErrSubstr string
		wantNoErr     bool
	}{
		{
			name: "existing small file and forReading -> idNotFound",
			prep: func(t *testing.T) (*hashmap, string, sop.UUID) {
				hm := newHashmap(true, 32, rt, mocks.NewMockClient())
				table := "tblA"
				// Create small segment file to trigger size<segment branch
				dir := filepath.Join(base, table)
				_ = os.MkdirAll(dir, 0o755)
				f := filepath.Join(dir, table+"-1"+registryFileExtension)
				_ = os.WriteFile(f, make([]byte, blockSize/2), 0o644)
				return hm, table, sop.NewUUID()
			},
			forWriting:    false,
			wantErrSubstr: idNotFoundErr,
		},
		{
			name: "partial read returns specific error",
			prep: func(t *testing.T) (*hashmap, string, sop.UUID) {
				hm := newHashmap(true, 32, rt, mocks.NewMockClient())
				table := "tblB"
				dir := filepath.Join(base, table)
				_ = os.MkdirAll(dir, 0o755)
				f := filepath.Join(dir, table+"-1"+registryFileExtension)
				// Ensure file is large enough so open path doesn't try to preallocate
				_ = os.WriteFile(f, make([]byte, int(hm.getSegmentFileSize())), 0o644)
				// Seed injected partial-read DirectIO for this file path and open it
				dio := newFileDirectIOInjected(&failingDirectIO{partialRead: true})
				if err := dio.open(ctx, f, os.O_RDWR, permission); err != nil {
					t.Fatalf("open injected dio: %v", err)
				}
				hm.fileHandles[f] = dio
				return hm, table, sop.NewUUID()
			},
			forWriting:    false,
			wantErrSubstr: "only partially",
		},
		{
			name: "existing small file and forWriting -> setupNewFile path",
			prep: func(t *testing.T) (*hashmap, string, sop.UUID) {
				hm := newHashmap(true, 32, rt, mocks.NewMockClient())
				table := "tblC"
				dir := filepath.Join(base, table)
				_ = os.MkdirAll(dir, 0o755)
				f := filepath.Join(dir, table+"-1"+registryFileExtension)
				_ = os.WriteFile(f, make([]byte, 10), 0o644)
				return hm, table, sop.NewUUID()
			},
			forWriting: true,
			wantNoErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hm, table, id := tc.prep(t)
			_, err := hm.findOneFileRegion(ctx, tc.forWriting, table, id)
			if tc.wantNoErr {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if err == nil || (tc.wantErrSubstr != "" && !contains(err.Error(), tc.wantErrSubstr)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, err)
			}
		})
	}
}
