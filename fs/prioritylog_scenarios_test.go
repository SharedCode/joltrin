package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Forces priorityLog.Get to hit ReadFile error by making the target path a directory.
func Test_PriorityLog_Get_ReadFile_Error(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	tid := sop.NewUUID()
	// Create a directory at the file path expected by Get.
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+priorityLogFileExtension))
	if err := os.MkdirAll(fn, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := pl.Get(ctx, tid); err == nil {
		t.Fatalf("expected ReadFile error from directory path")
	}
}

// Ensures GetBatch returns error when one eligible file fails to parse via Get.
func Test_PriorityLog_GetBatch_Get_Error(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	// Write an eligible .plg file with invalid JSON content to cause pl.Get -> unmarshal error.
	logDir := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	badID := sop.NewUUID().String()
	fn := filepath.Join(logDir, badID+priorityLogFileExtension)
	payload := []byte("not-json")
	blob := make([]byte, len(payload)+4)
	marshalData(payload, blob)
	if err := os.WriteFile(fn, blob, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(fn, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, e := pl.GetBatch(ctx, 10); e == nil {
		t.Fatalf("expected error from GetBatch due to inner Get failure")
	}
}

func Test_PriorityLog_GetBatch_LimitOne(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	// Create 2 eligible files and ensure only 1 is returned when batchSize=1
	dir := rt.formatActiveFolderEntity(logFolder)
	_ = os.MkdirAll(dir, 0o755)
	tid1 := sop.NewUUID()
	tid2 := sop.NewUUID()
	f1 := filepath.Join(dir, tid1.String()+priorityLogFileExtension)
	f2 := filepath.Join(dir, tid2.String()+priorityLogFileExtension)
	_ = pl.Add(ctx, tid1, []byte("[]"))
	_ = pl.Add(ctx, tid2, []byte("[]"))
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(f1, past, past)
	_ = os.Chtimes(f2, past, past)

	batch, err := pl.GetBatch(ctx, 1)
	if err != nil || len(batch) != 1 {
		t.Fatalf("expected exactly 1 item, got %v err=%v", batch, err)
	}
}

// Verifies PriorityLog.Add returns an error when the log directory is not writable.
func Test_PriorityLog_Add_WriteFile_Error_ReturnsError(t *testing.T) {
	ctx := context.Background()
	// Create active/passive roots and tracker.
	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	// Create the translogs folder and then make it read-only to force a write failure.
	dir := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Make directory read-only (common cause of EACCES/EPERM on write).
	_ = os.Chmod(dir, 0o555)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	tid := sop.NewUUID()
	payload := []byte("[]")
	if err := pl.Add(ctx, tid, payload); err == nil {
		t.Fatalf("expected error from PriorityLog.Add when directory is not writable")
	}
}

// Verifies missing base folder causes GetBatch to create it (or attempt) and return nil, nil early.
func Test_PriorityLog_GetBatch_MissingBase_ReturnsNil(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	// active/translogs does not exist; Exists returns false; function returns nil, nil regardless of mkdir result.
	if batch, err := pl.GetBatch(ctx, 5); err != nil || batch != nil {
		t.Fatalf("expected nil, nil when base missing; got %v, %v", batch, err)
	}
}

// Forces PriorityLog.GetBatch to surface directory read error by making the log folder a file.
func Test_PriorityLog_GetBatch_ReadDir_Error(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()

	// Make the translogs folder a file so directory listing fails.
	bad := active + "/" + logFolder
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()
	if r, e := pl.GetBatch(ctx, 10); e == nil || r != nil {
		t.Fatalf("expected error and nil result, got %v %v", r, e)
	}
}

// Ensures only sufficiently aged .plg files are included by GetBatch (time filter true/false branches).
func Test_PriorityLog_GetBatch_TimeFilter(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	dir := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Recent file should be filtered out.
	tidNew := sop.NewUUID()
	fNew := filepath.Join(dir, tidNew.String()+priorityLogFileExtension)
	if err := pl.Add(ctx, tidNew, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = os.Chtimes(fNew, now, now)

	// Old-enough file should be returned.
	tidOld := sop.NewUUID()
	fOld := filepath.Join(dir, tidOld.String()+priorityLogFileExtension)
	if err := pl.Add(ctx, tidOld, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(fOld, past, past)

	batch, err := pl.GetBatch(ctx, 10)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if len(batch) != 1 || batch[0].Key.Compare(tidOld) != 0 {
		t.Fatalf("expected only old tid, got %v", batch)
	}
}

func Test_PriorityLog_Get_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	tid := sop.NewUUID()
	// Seed invalid JSON in .plg
	fn := rt.formatActiveFolderEntity(filepath.Join(logFolder, tid.String()+priorityLogFileExtension))
	if err := os.MkdirAll(filepath.Dir(fn), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("not-json")
	blob := make([]byte, len(payload)+4)
	marshalData(payload, blob)
	if err := os.WriteFile(fn, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Get(ctx, tid); err == nil {
		t.Fatalf("expected invalid json error")
	}
}

func Test_PriorityLog_GetBatch_Skip_InvalidUUID_And_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	a, p := t.TempDir(), t.TempDir()
	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	pl := NewTransactionLog(mocks.NewMockClient(), rt).PriorityLog()

	base := rt.formatActiveFolderEntity(logFolder)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	// File with invalid UUID in name should be skipped.
	badName := filepath.Join(base, "not-a-uuid"+priorityLogFileExtension)
	payload := []byte("[]")
	blob := make([]byte, len(payload)+4)
	marshalData(payload, blob)
	if err := os.WriteFile(badName, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(badName, past, past)

	// Valid UUID filename but invalid JSON should return error when encountered.
	tid := sop.NewUUID()
	badJSON := filepath.Join(base, tid.String()+priorityLogFileExtension)
	payload2 := []byte("nope")
	blob2 := make([]byte, len(payload2)+4)
	marshalData(payload2, blob2)
	if err := os.WriteFile(badJSON, blob2, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(badJSON, past, past)

	if _, err := pl.GetBatch(ctx, 10); err == nil {
		t.Fatalf("expected error from invalid json in batch")
	}
}
