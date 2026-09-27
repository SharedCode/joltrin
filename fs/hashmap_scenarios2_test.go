package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// lockFailOkFalse is a cache that returns ok=false with no error on Lock to simulate contention.
type lockFailOkFalse struct{ sop.L2Cache }

func (m lockFailOkFalse) Lock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return false, sop.NilUUID, nil
}
func (m lockFailOkFalse) DualLock(ctx context.Context, d time.Duration, lk []*sop.LockKey) (bool, sop.UUID, error) {
	return m.Lock(ctx, d, lk)
}
func (m lockFailOkFalse) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

// Ensures setupNewFile surfaces lock-acquisition failure and does not create/truncate the segment.
func TestHashmap_setupNewFile_LockFailure_NoCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 8, rt, lockFailOkFalse{L2Cache: mocks.NewMockClient()})
	dio := newFileDirectIO()

	// Point directly to a would-be segment path; setupNewFile should fail before opening.
	table := "tbl_lockfail"
	seg := filepath.Join(base, table, table+"-1"+registryFileExtension)

	id := sop.NewUUID()
	if _, err := hm.setupNewFile(ctx, true, seg, id, dio); err == nil {
		t.Fatalf("expected lock failure error from setupNewFile")
	}
	if _, err := os.Stat(seg); !os.IsNotExist(err) {
		t.Fatalf("segment should not exist on lock failure; stat err=%v", err)
	}
}

// Ensure EOF on read path returns a valid write location when forWriting=true.
func TestHashmap_findOneFileRegion_EOF_ForWritingReturnsLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	hm := newHashmap(true, 16, rt, mocks.NewMockClient())
	// Use a DirectIO that always returns EOF on ReadAt to force EOF branch.

	table := "tblEOFWrite"
	id := sop.NewUUID()

	// Pre-create the expected segment file path with size >= segment size so findOneFileRegion
	// takes the read path (not setupNewFile), then ReadAt will return EOF.
	segDir := filepath.Join(rt.getActiveBaseFolder(), table)
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(segDir, table+"-1"+registryFileExtension)
	if f, err := os.OpenFile(seg, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg: %v", err)
	} else {
		_ = f.Close()
	}
	if err := os.Truncate(seg, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Inject EOF DirectIO for this segment file path
	dio := newFileDirectIOInjected(directIOReadEOF{})
	if err := dio.open(ctx, seg, os.O_RDWR, 0o644); err != nil {
		t.Fatalf("open injected dio: %v", err)
	}
	hm.fileHandles[seg] = dio

	frd, err := hm.findOneFileRegion(ctx, true, table, id)
	if err != nil {
		t.Fatalf("find(write): %v", err)
	}
	bo, so := hm.getBlockOffsetAndHandleInBlockOffset(id)
	if frd.blockOffset != bo || frd.handleInBlockOffset != so || frd.dio == nil {
		t.Fatalf("unexpected location: %+v want offsets (%d,%d)", frd, bo, so)
	}
}

// Ensures fetch surfaces non-notfound errors from findOneFileRegion (e.g., read error),
// rather than silently skipping as a missing ID.
func TestHashmap_Fetch_PropagatesNonNotFoundError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	hm := newHashmap(true, 16, rt, mocks.NewMockClient())
	// Inject DirectIO that returns a generic error on ReadAt, which should be propagated by fetch.

	table := "tblReadErr"
	// Pre-create segment file at the expected size so findOneFileRegion uses the read path.
	dir := filepath.Join(base, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(dir, table+"-1"+registryFileExtension)
	if f, err := os.OpenFile(seg, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg: %v", err)
	} else {
		_ = f.Close()
	}
	if err := os.Truncate(seg, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	// Inject erroring DirectIO for seg path
	dio := newFileDirectIOInjected(directIOError{})
	if err := dio.open(ctx, seg, os.O_RDWR, 0o644); err != nil {
		t.Fatalf("open injected dio: %v", err)
	}
	hm.fileHandles[seg] = dio

	_, err = hm.fetch(ctx, table, []sop.UUID{sop.NewUUID()})
	if err == nil {
		t.Fatalf("expected propagated error from fetch due to read failure")
	}
}

// fakeDirectIOCloseErr wraps os operations but forces Close to return an error.
type fakeDirectIOCloseErr struct{}

func (fakeDirectIOCloseErr) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	// Ensure parent exists for convenience
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (fakeDirectIOCloseErr) WriteAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.WriteAt(block, offset)
}
func (fakeDirectIOCloseErr) ReadAt(ctx context.Context, file *os.File, block []byte, offset int64) (int, error) {
	return file.ReadAt(block, offset)
}
func (fakeDirectIOCloseErr) Close(file *os.File) error {
	// Close the file, then return a forced error regardless of underlying result.
	_ = file.Close()
	return fmt.Errorf("forced close error")
}

// Ensures hashmap.close surfaces the (last) error from closing file handles.
func Test_hashmap_close_ReturnsError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	// Prepare a fileDirectIO with an open file and a DirectIO whose Close returns error.
	// We don't rely on open(); we construct the struct manually to isolate the Close path.
	f := filepath.Join(base, "tmp.dat")
	fh, err := os.Create(f)
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}

	dio := &fileDirectIO{file: fh, filename: f, directIO: fakeDirectIOCloseErr{}}
	hm := &hashmap{fileHandles: map[string]*fileDirectIO{f: dio}}

	if e := hm.close(); e == nil {
		t.Fatalf("expected close to surface error")
	}
	// Subsequent close should be a no-op and not panic.
	if e := hm.close(); e != nil {
		t.Fatalf("second close should be nil, got %v", e)
	}
	_ = rt
}

// Verifies findOneFileRegion(forWriting=true) initializes a new segment file with expected size.
func Test_hashmap_setupNewFile_TruncatesToSegmentSize(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}

	hm := newHashmap(true, 16, rt, mocks.NewMockClient())

	table := "tblNew"
	// Ensure table directory exists under active folder to allow file open/create
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := sop.NewUUID()
	if _, err := hm.findOneFileRegion(ctx, true, table, id); err != nil {
		t.Fatalf("findOneFileRegion(new): %v", err)
	}
	// Validate that segment file exists with the expected preallocated size.
	seg := filepath.Join(rt.getActiveBaseFolder(), table, fmt.Sprintf("%s-1%s", table, registryFileExtension))
	st, err := os.Stat(seg)
	if err != nil {
		t.Fatalf("stat seg: %v", err)
	}
	want := hm.getSegmentFileSize()
	if st.Size() != want {
		t.Fatalf("segment size mismatch: got %d want %d", st.Size(), want)
	}
}

// Covers findOneFileRegion reading across multiple segment files: segment 1 has no match, segment 2 contains the handle.
func TestHashmap_findOneFileRegion_MultiSegmentScan_Read(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	cache := mocks.NewMockClient()
	hm := newHashmap(true, 16, rt, cache)

	table := "mscan"
	id := sop.NewUUID()
	blockOffset, slotOffset := hm.getBlockOffsetAndHandleInBlockOffset(id)

	// Prepare segment 1 and 2 files with full segment size; write the handle into segment 2 only.
	segDir := filepath.Join(base, table)
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg1 := filepath.Join(segDir, table+"-1"+registryFileExtension)
	seg2 := filepath.Join(segDir, table+"-2"+registryFileExtension)
	// Ensure files exist before truncating to full segment size.
	if f, err := os.OpenFile(seg1, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg1: %v", err)
	} else {
		_ = f.Close()
	}
	if f, err := os.OpenFile(seg2, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg2: %v", err)
	} else {
		_ = f.Close()
	}
	if err := os.Truncate(seg1, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate1: %v", err)
	}
	if err := os.Truncate(seg2, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate2: %v", err)
	}
	// Write the handle into segment 2 at the ideal slot within the target block.
	f2, err := os.OpenFile(seg2, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open seg2: %v", err)
	}
	defer f2.Close()
	buf := make([]byte, blockSize)
	h := sop.NewHandle(id)
	mb := encoding.NewHandleMarshaler()
	hb, _ := mb.Marshal(h, make([]byte, 0, sop.HandleSizeInBytes))
	copy(buf[slotOffset:slotOffset+int64(len(hb))], hb)
	if _, err := f2.WriteAt(buf, blockOffset); err != nil {
		t.Fatalf("seed seg2 block: %v", err)
	}

	// Now locate via read; it should scan segment 1 then find in segment 2.
	frd, err := hm.findOneFileRegion(ctx, false, table, id)
	if err != nil {
		t.Fatalf("find(read): %v", err)
	}
	if frd.handle.LogicalID != id {
		t.Fatalf("unexpected handle id: got %s want %s", frd.handle.LogicalID.String(), id.String())
	}
	if frd.dio == nil || frd.dio.filename != table+"-2"+registryFileExtension {
		t.Fatalf("expected segment 2; got %+v", frd.dio)
	}
}

// Ensures forWriting=true returns the first free slot via scan when the ideal slot is occupied by another ID.
func TestHashmap_findOneFileRegion_ScanFindsFreeSlot_OnWrite(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	hm := newHashmap(true, 32, rt, mocks.NewMockClient())

	table := "scanfree"
	idTarget := sop.NewUUID()
	blockOffset, idealOffset := hm.getBlockOffsetAndHandleInBlockOffset(idTarget)

	// Prepare a full-size segment-1 file and write a block where the ideal slot contains a different handle.
	segDir := filepath.Join(base, table)
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg1 := filepath.Join(segDir, table+"-1"+registryFileExtension)
	if f, err := os.OpenFile(seg1, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg1: %v", err)
	} else {
		_ = f.Close()
	}
	if err := os.Truncate(seg1, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate seg1: %v", err)
	}

	// Seed the block: put another handle at the target's ideal slot; leave other slots zero.
	other := sop.NewHandle(sop.NewUUID())
	mb := encoding.NewHandleMarshaler()
	hb, _ := mb.Marshal(other, make([]byte, 0, sop.HandleSizeInBytes))
	blk := make([]byte, blockSize)
	copy(blk[idealOffset:idealOffset+int64(len(hb))], hb)
	f1, err := os.OpenFile(seg1, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open seg1: %v", err)
	}
	defer f1.Close()
	if _, err := f1.WriteAt(blk, blockOffset); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	// Now request a write location for idTarget. Ideal slot is occupied; function should scan and return a different offset.
	frd, err := hm.findOneFileRegion(ctx, true, table, idTarget)
	if err != nil {
		t.Fatalf("find(write): %v", err)
	}
	if frd.dio == nil {
		t.Fatalf("expected non-nil dio")
	}
	if frd.handleInBlockOffset == idealOffset {
		t.Fatalf("expected scan to choose a free slot different from ideal; got same offset %d", frd.handleInBlockOffset)
	}
}

// Ensures read path finds a handle placed at a non-ideal slot via block scan.
func TestHashmap_findOneFileRegion_ScanFindsNonIdeal_OnRead(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	hm := newHashmap(true, 32, rt, mocks.NewMockClient())

	table := "scanread"
	id := sop.NewUUID()
	blockOffset, idealOffset := hm.getBlockOffsetAndHandleInBlockOffset(id)

	// Prepare full-size segment and seed the block with the handle at a non-ideal slot.
	dir := filepath.Join(base, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(dir, table+"-1"+registryFileExtension)
	if f, err := os.OpenFile(seg, os.O_CREATE|os.O_RDWR, 0o644); err != nil {
		t.Fatalf("create seg: %v", err)
	} else {
		_ = f.Close()
	}
	if err := os.Truncate(seg, hm.getSegmentFileSize()); err != nil {
		t.Fatalf("truncate seg: %v", err)
	}

	// Choose a non-ideal offset inside the block.
	alt := idealOffset + int64(sop.HandleSizeInBytes)
	if alt+int64(sop.HandleSizeInBytes) > blockSize {
		alt = 0
		if idealOffset == 0 {
			alt = int64(sop.HandleSizeInBytes)
		}
	}

	blk := make([]byte, blockSize)
	h := sop.NewHandle(id)
	m := encoding.NewHandleMarshaler()
	hb, _ := m.Marshal(h, make([]byte, 0, sop.HandleSizeInBytes))
	copy(blk[alt:alt+int64(len(hb))], hb)

	f, err := os.OpenFile(seg, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open seg: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteAt(blk, blockOffset); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	frd, err := hm.findOneFileRegion(ctx, false, table, id)
	if err != nil {
		t.Fatalf("find(read): %v", err)
	}
	if frd.handle.LogicalID != id || frd.handleInBlockOffset != alt {
		t.Fatalf("unexpected result: handle %s offset %d (want offset %d)", frd.handle.LogicalID.String(), frd.handleInBlockOffset, alt)
	}
}

// Covers hashmap.fetch branches: skip idNotFound error; return on unexpected error from findOneFileRegion.
func Test_Hashmap_Fetch_SkipNotFound_And_Error(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	hm := newHashmap(false, MinimumModValue, rt, mocks.NewMockClient())

	table := "tabf"
	dir := filepath.Join(base, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a proper-sized segment file but make it unreadable to induce open/read error.
	seg := filepath.Join(dir, table+"-1"+registryFileExtension)
	f, err := os.Create(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(hm.getSegmentFileSize()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(seg, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(seg, 0o644) })

	ids := []sop.UUID{sop.NewUUID(), sop.NewUUID()}
	// First ID will hit default path (file exists but unreadable -> error), so place a guard: call fetch with a missing table first to hit idNotFound.
	hm2 := newHashmap(false, MinimumModValue, rt, mocks.NewMockClient())
	if got, err := hm2.fetch(ctx, "missingtbl", []sop.UUID{ids[0]}); err != nil || len(got) != 0 {
		t.Fatalf("expected skip on not found; got %v err=%v", got, err)
	}

	if _, err := hm.fetch(ctx, table, []sop.UUID{ids[1]}); err == nil {
		t.Fatalf("expected error from underlying findOneFileRegion")
	}
}

func Test_Hashmap_Fetch_Success(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	cache := mocks.NewMockClient()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Ensure table dir exists; then seed one handle via Registry, which writes using hashmap/updateFileRegion.
	reg := NewRegistry(true, MinimumModValue, rt, cache)
	defer reg.Close()
	table := "tabhs"
	if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), table), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := sop.NewHandle(sop.NewUUID())
	if err := reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: table, IDs: []sop.Handle{h}}}); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	hm := newHashmap(false, MinimumModValue, rt, cache)
	got, err := hm.fetch(ctx, table, []sop.UUID{h.LogicalID})
	if err != nil || len(got) != 1 || got[0].LogicalID.Compare(h.LogicalID) != 0 {
		t.Fatalf("expected 1 handle back, got %v err=%v", got, err)
	}
}

// Ensures findOneFileRegion surfaces open error when segment file exists with required size but is unreadable.
func Test_Hashmap_FindOneFileRegion_OpenError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	hm := newHashmap(false, MinimumModValue, rt, mocks.NewMockClient())
	table := "tabopn"
	dir := filepath.Join(base, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, table+"-1"+registryFileExtension)

	// Create a file sized to the expected segment size and make it unreadable.
	f, err := os.Create(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(hm.getSegmentFileSize()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chmod(seg, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(seg, 0o644) })

	if _, err := hm.findOneFileRegion(ctx, false, table, sop.NewUUID()); err == nil {
		t.Fatalf("expected open/read error")
	}
}

// directIO shim that returns partial reads without EOF to trigger partial-read error path.
type shortReadDirectIO struct{ DirectIO }

func (s shortReadDirectIO) ReadAt(ctx context.Context, f *os.File, block []byte, offset int64) (int, error) {
	if len(block) < 4 {
		return len(block), nil
	}
	return len(block) / 2, nil // short without EOF
}

func (s shortReadDirectIO) Open(ctx context.Context, filename string, flag int, perm os.FileMode) (*os.File, error) {
	if dir := filepath.Dir(filename); dir != "." {
		_ = os.MkdirAll(dir, perm)
	}
	return os.OpenFile(filename, flag, perm)
}
func (s shortReadDirectIO) WriteAt(ctx context.Context, f *os.File, b []byte, off int64) (int, error) {
	return f.WriteAt(b, off)
}
func (s shortReadDirectIO) Close(f *os.File) error { return f.Close() }

func Test_Hashmap_FindOneFileRegion_PartialRead(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	// Set up replication tracker with a real folder.
	base := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{base}, false, l2)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Build hashmap with small mod value to keep test light; file size = 250*4096 by default.
	hm := newHashmap(false, MinimumModValue, rt, l2)

	// Prepare expected file path layout: <active>/<table>/<table>-1.reg
	table := "tabx"
	dir := filepath.Join(base, table)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seg := filepath.Join(dir, table+"-1"+registryFileExtension)
	f, err := os.Create(seg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Ensure file exists; size content not important since we inject short read without EOF.
	f.Close()

	// Inject short-read DirectIO shim for this file path.
	fio := newFileDirectIOInjected(shortReadDirectIO{})
	if err := fio.open(ctx, seg, os.O_RDWR, 0o644); err != nil {
		t.Fatalf("open injected dio: %v", err)
	}
	hm.fileHandles[seg] = fio

	// Attempt to read region for a random ID, expecting partial-read error.
	id := sop.NewUUID()
	if _, err := hm.findOneFileRegion(ctx, false, table, id); err == nil {
		t.Fatalf("expected partial-read error, got nil")
	}
}

// Ensures setupNewFile returns an error when the preallocation lock is held by another owner.
func Test_Hashmap_SetupNewFile_LockConflict(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()

	cache := mocks.NewMockClient()

	// Reset global replication state for isolation.
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
	hm := newHashmap(true, MinimumModValue, rt, cache)

	dio := newFileDirectIO()
	fn := filepath.Join(rt.getActiveBaseFolder(), "seg-1.reg")

	// Pre-lock the preallocateFileLockKey with a foreign owner.
	// We must match the key generation logic in setupNewFile which now includes the path.
	lockKey := preallocateFileLockKey + rt.formatActiveFolderEntity(fn)
	_ = cache.Set(ctx, "L"+lockKey, sop.NewUUID().String(), 0)

	if _, err := hm.setupNewFile(ctx, true, fn, sop.NewUUID(), dio); err == nil {
		t.Fatalf("expected lock conflict error from setupNewFile")
	}
}

// Success path: acquires lock, opens/creates file, truncates to segment size, returns computed offsets.
func Test_Hashmap_SetupNewFile_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()

	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, cache)
	hm := newHashmap(true, 8, rt, cache)

	// Ensure directory exists; filename inside active folder
	fn := filepath.Join(rt.getActiveBaseFolder(), "seg-ok.reg")
	dio := newFileDirectIO()
	id := sop.NewUUID()

	frd, err := hm.setupNewFile(ctx, true, fn, id, dio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// File should exist and have expected size
	fi, err := os.Stat(fn)
	if err != nil || fi.IsDir() {
		t.Fatalf("expected file; err=%v", err)
	}
	if fi.Size() != hm.getSegmentFileSize() {
		t.Fatalf("expected size %d, got %d", hm.getSegmentFileSize(), fi.Size())
	}
	// Offsets should match the helper computation
	bo, ho := hm.getBlockOffsetAndHandleInBlockOffset(id)
	if frd.blockOffset != bo || frd.handleInBlockOffset != ho || frd.dio == nil {
		t.Fatalf("unexpected frd: %+v (expected bo=%d ho=%d)", frd, bo, ho)
	}
}

// Open error path: parent directory does not exist -> dio.open fails and function returns error.
func Test_Hashmap_SetupNewFile_OpenError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()

	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, cache)
	hm := newHashmap(true, 8, rt, cache)

	// Make a path segment that is a file, so opening a child path fails with ENOTDIR
	nd := filepath.Join(rt.getActiveBaseFolder(), "missing")
	if err := os.WriteFile(nd, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed parent file: %v", err)
	}
	fn := filepath.Join(nd, "seg-err.reg")
	dio := newFileDirectIO()
	if _, err := hm.setupNewFile(ctx, true, fn, sop.NewUUID(), dio); err == nil {
		t.Fatalf("expected open error when parent dir missing")
	}
}

// Truncate error path: open read-only (forWriting=false), then truncate should fail.
func Test_Hashmap_SetupNewFile_TruncateError_ReadOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := t.TempDir()

	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{base}, false, cache)
	hm := newHashmap(true, 8, rt, cache)

	// Create empty file first so O_RDONLY open succeeds
	fn := filepath.Join(rt.getActiveBaseFolder(), "seg-ro.reg")
	if err := os.WriteFile(fn, []byte{}, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	dio := newFileDirectIO()
	if _, err := hm.setupNewFile(ctx, false, fn, sop.NewUUID(), dio); err == nil {
		t.Fatalf("expected truncate error with read-only open")
	}
}
