package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// Ensures fastForward returns error when registry hash mod file contains invalid content.
func Test_FastForward_InvalidRegHashMod_ReturnsError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	active := t.TempDir()
	passive := t.TempDir()
	// Fresh global state for this test
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	rt.FailedToReplicate = true
	GlobalReplicationDetails.FailedToReplicate = true

	// Create invalid reghashmod file on active side
	if err := os.WriteFile(filepath.Join(active, RegistryHashModValueFilename), []byte("abc"), 0o644); err != nil {
		t.Fatalf("seed bad reghashmod: %v", err)
	}

	// Create a minimal commit log to force fastForward path into GetRegistryHashModValue
	payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: []sop.StoreInfo{{Name: "s1"}}, Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}}}
	ba, _ := encoding.DefaultMarshaler.Marshal(payload)
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(dir, sop.NewUUID().String()+logFileExtension)
	if err := os.WriteFile(fn, ba, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if _, err := rt.fastForward(ctx); err == nil {
		t.Fatalf("expected error due to bad reghashmod content")
	}
}

// Ensures fastForward returns error when deleting a processed log fails.
func Test_FastForward_Delete_Error(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Create a valid empty payload file to pass unmarshal; then switch the file into a directory to fail Remove.
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Minimal valid payload: JSON encodes tuple with First=nil, Second=[[],[],[],[]]
	fn := filepath.Join(dir, sop.NewUUID().String()+".log")
	if err := os.WriteFile(fn, []byte("{\"First\":null,\"Second\":[[],[],[],[]]}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Replace file with a directory of the same name to induce delete error.
	if err := os.Remove(fn); err != nil {
		t.Fatalf("pre-remove: %v", err)
	}
	if err := os.MkdirAll(fn, 0o755); err != nil {
		t.Fatalf("mkdir in place of file: %v", err)
	}

	if _, err := rt.fastForward(ctx); err == nil {
		t.Fatalf("expected delete error")
	}
}

// Covers fastForward branches: Count override from cache and remove failure.
func Test_FastForward_CountOverride_And_RemoveError(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	active := t.TempDir()
	passive := t.TempDir()
	prev := GlobalReplicationDetails
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()
	// Initialize tracker and repo with hash mod value
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	rt.FailedToReplicate = true
	GlobalReplicationDetails.FailedToReplicate = true
	if _, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue); err != nil {
		t.Fatalf("repo: %v", err)
	}

	// Seed a store and put a cached copy with a specific Count to hit count override path
	s := sop.StoreInfo{Name: "sf1", RegistryTable: "tb1", Count: 1}
	if err := l2.SetStruct(ctx, s.Name, &s, time.Minute); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Log data with First non-nil so fastForward triggers sr.Replicate() and count override branch
	payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{
		First:  []sop.StoreInfo{{Name: s.Name, RegistryTable: s.RegistryTable}},
		Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}},
	}
	ba, _ := encoding.DefaultMarshaler.Marshal(payload)
	commitDir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, commitDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logFn := filepath.Join(commitDir, sop.NewUUID().String()+logFileExtension)
	if err := os.WriteFile(logFn, ba, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	// Make directory non-writable to force Remove error at end of processing
	if err := os.Chmod(commitDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_, err = rt.fastForward(ctx)
	if err == nil {
		t.Fatalf("expected remove error due to directory perms")
	}
	// Restore perms and cleanup the log to avoid TempDir cleanup failure or test interference
	_ = os.Chmod(commitDir, 0o755)
	_ = os.Remove(logFn)
}

// Ensures fastForward returns error when commitlogs path is a file (ReadDir error path).
func Test_FastForward_ReadDir_Error_On_FilePath(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Create a file at the path where a directory is expected.
	cl := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := os.WriteFile(cl, []byte("x"), 0o644); err != nil {
		t.Fatalf("prepare file at commitlogs path: %v", err)
	}

	if _, err := rt.fastForward(ctx); err == nil {
		t.Fatalf("expected ReadDir error when commitlogs path is a file")
	}
}

// Causes fastForward to fail when StoreRepository.Replicate cannot write to passive due to path conflict.
func Test_FastForward_StoreReplicate_Failure(t *testing.T) {
	ctx := context.Background()
	l2 := mocks.NewMockClient()
	active := t.TempDir()
	passive := t.TempDir()

	// Fresh global
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	// Ensure replication can proceed inside fastForward (it flips FailedToReplicate=false).

	// Create a commit log with one store so fastForward will invoke StoreRepository.Replicate.
	storeName := "ffx"
	payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{
		First:  []sop.StoreInfo{{Name: storeName}},
		Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}},
	}
	ba, _ := encoding.DefaultMarshaler.Marshal(payload)
	commitDir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, commitDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(commitDir, sop.NewUUID().String()+logFileExtension)
	if err := os.WriteFile(fn, ba, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// Prepare passive conflict: make passive/<store> a file so write of storeinfo fails.
	if err := os.WriteFile(filepath.Join(passive, storeName), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed conflict: %v", err)
	}

	if _, err := rt.fastForward(ctx); err == nil {
		t.Fatalf("expected fastForward to return error due to replicate failure on passive path conflict")
	}
}

// Ensures fastForward returns error when commit log payload cannot be unmarshaled.
func Test_FastForward_Unmarshal_Error(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())

	// Create commitlogs directory and a malformed log file.
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(dir, sop.NewUUID().String()+".log")
	if err := os.WriteFile(fn, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := rt.fastForward(ctx); err == nil {
		t.Fatalf("expected unmarshal error")
	}
}
