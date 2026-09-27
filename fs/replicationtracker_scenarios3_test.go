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

type getStructExErrCache struct{ sop.L2Cache }

func (c getStructExErrCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

func (c getStructExErrCache) GetStructEx(ctx context.Context, key string, v interface{}, ttl time.Duration) (bool, error) {
	return false, errors.New("l2 boom")
}

// Covers failover's early return when r.FailedToReplicate is already true.
func TestReplicationTracker_Failover_EarlyReturn_FailedToReplicate(t *testing.T) {
	ctx := context.Background()
	// Preserve global.
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, getStructExErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	prev := rt.ActiveFolderToggler
	rt.FailedToReplicate = true
	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover unexpected error: %v", err)
	}
	if rt.ActiveFolderToggler != prev {
		t.Fatalf("expected no toggle when FailedToReplicate true")
	}
	// Ensure no passive status written as we returned early.
	if _, err := os.Stat(rt.formatPassiveFolderEntity(replicationStatusFilename)); err == nil {
		t.Fatalf("expected no passive status file on early return")
	}
}

// Covers failover's early return when global already reflects the opposite toggler.
func TestReplicationTracker_Failover_EarlyReturn_GlobalAlreadyToggled(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	// Set global to indicate a prior flip already occurred.
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails.ActiveFolderToggler = !rt.ActiveFolderToggler
	globalReplicationDetailsLocker.Unlock()

	prev := rt.ActiveFolderToggler
	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover unexpected error: %v", err)
	}
	if rt.ActiveFolderToggler != prev {
		t.Fatalf("expected no toggle when global already toggled")
	}
}

// Covers readStatusFromHomeFolder branch where both active and passive status files are missing.
func TestReplicationTracker_ReadStatus_BothFilesMissing(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	// Both files are missing; ActiveFolderToggler should remain default true.
	if !rt.ActiveFolderToggler {
		t.Fatalf("expected default active folder when no status files present")
	}
}

// Covers readStatusFromHomeFolder branch where both files exist and the passive file is newer,
// which should flip ActiveFolderToggler and read the passive status content.
func TestReplicationTracker_ReadStatus_PassiveNewerFlips(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()

	// Prepare status files on both sides with different modtimes.
	// Active side (older)
	actPath := filepath.Join(a, replicationStatusFilename)
	actContent, _ := encoding.Marshal(ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false, LogCommitChanges: false})
	if err := os.WriteFile(actPath, actContent, 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}
	// Passive side (newer)
	pasPath := filepath.Join(b, replicationStatusFilename)
	pasContent, _ := encoding.Marshal(ReplicationTrackedDetails{ActiveFolderToggler: false, FailedToReplicate: true, LogCommitChanges: true})
	if err := os.WriteFile(pasPath, pasContent, 0o644); err != nil {
		t.Fatalf("write passive: %v", err)
	}
	// Set modtimes: active older, passive newer.
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now()
	os.Chtimes(actPath, older, older)
	os.Chtimes(pasPath, newer, newer)

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	// Expect flip to passive (index 1) because passive file is newer, and fields read from passive content.
	if rt.ActiveFolderToggler {
		t.Fatalf("expected toggler flipped to false (passive newer)")
	}
	if !rt.FailedToReplicate || !rt.LogCommitChanges {
		t.Fatalf("expected fields populated from passive status file")
	}
}

// Covers handleFailedToReplicate path that writes replication status when not previously failed.
func TestReplicationTracker_HandleFailedToReplicate_WritesStatus(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	// Precondition
	if rt.FailedToReplicate {
		t.Fatalf("unexpected pre-failed state")
	}
	rt.handleFailedToReplicate(ctx)
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate true")
	}
	// Status file written on active side
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		t.Fatalf("expected replication status file on active: %v", err)
	}
	// Global flag set
	globalReplicationDetailsLocker.Lock()
	gf := GlobalReplicationDetails != nil && GlobalReplicationDetails.FailedToReplicate
	globalReplicationDetailsLocker.Unlock()
	if !gf {
		t.Fatalf("expected global FailedToReplicate true")
	}
}

// Covers NewReplicationTracker readStatusFromHomeFolder error when active status file has invalid JSON.
func TestReplicationTracker_New_ReadStatus_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	// Write invalid JSON to active repl status file
	if err := os.WriteFile(filepath.Join(a, replicationStatusFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient()); err == nil {
		t.Fatalf("expected error due to invalid status JSON")
	}
}

// Covers failover path continuing despite syncWithL2Cache pull error; expects flip and status write on passive.
func TestReplicationTracker_Failover_SyncL2ErrorStillFlips(t *testing.T) {
	ctx := context.Background()
	globalReplicationDetailsLocker.Lock()
	oldGlobal := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = oldGlobal
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	// Use a cache that errors on GetStructEx to exercise warn branch.
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, getStructExErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Trigger failover-qualified error with rollbackSucceeded=false.
	// ioErr := sop.Error{Code: sop.FailoverQualifiedError, Err: errors.New("boom")}
	// rt.HandleReplicationRelatedError(ctx, ioErr, nil, false)
	rt.failover(ctx)

	// Expect toggler flipped to passive (false), and status file written on passive.
	if rt.ActiveFolderToggler {
		t.Fatalf("expected ActiveFolderToggler flipped to false")
	}
	if _, err := os.Stat(filepath.Join(b, replicationStatusFilename)); err != nil {
		t.Fatalf("expected passive status file written: %v", err)
	}
}

// Covers handleFailedToReplicate early return when GlobalReplicationDetails is already marked failed.
func TestReplicationTracker_HandleFailedToReplicate_GlobalAlreadyFailed(t *testing.T) {
	ctx := context.Background()

	// Save and set global replication details to an already-failed state.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: true}
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Invoke handler; should set local flag and return without writing repl status file.
	rt.handleFailedToReplicate(ctx)
	if !rt.FailedToReplicate {
		t.Fatalf("expected local FailedToReplicate set to true")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err == nil {
		t.Fatalf("unexpected replication status file written to active")
	}
	if _, err := os.Stat(rt.formatPassiveFolderEntity(replicationStatusFilename)); err == nil {
		t.Fatalf("unexpected replication status file written to passive")
	}
}

// Covers failover error path when writeReplicationStatus fails because the passive status filename collides with a directory.
func TestReplicationTracker_Failover_WriteReplicationStatusError(t *testing.T) {
	ctx := context.Background()

	// Ensure clean global state.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	b := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Create a directory at the passive status filename to force write error in failover.
	pasStat := filepath.Join(rt.getPassiveBaseFolder(), replicationStatusFilename)
	if err := os.MkdirAll(pasStat, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Trigger failover by calling HandleReplicationRelatedError with a failover-qualified code and rollbackSucceeded=false.
	// ioErr := sop.Error{Code: sop.FailoverQualifiedError, Err: errors.New("invalid argument")}
	// rt.HandleReplicationRelatedError(ctx, ioErr, nil, false)
	rt.failover(ctx)
	// Expect that ActiveFolderToggler did not change due to error writing status file.
	if rt.ActiveFolderToggler != true {
		t.Fatalf("expected no toggler change on failover error")
	}
}

// Table-driven tests for readStatusFromHomeFolder covering cases:
// - neither file exists (keeps default toggler)
// - only passive exists (reads and flips to passive)
// - both exist with passive newer (flips to passive)
func Test_readStatusFromHomeFolder_Table(t *testing.T) {
	ctx := context.Background()
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")

	// Helper to write a minimal replication status file
	writeStat := func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f := filepath.Join(dir, replicationStatusFilename)
		return os.WriteFile(f, []byte("{}"), 0o644)
	}

	// Case 1: no files present
	// Isolate global state used by replication tracker
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt1, _ := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	// Ensure no files exist
	_ = os.Remove(filepath.Join(baseA, replicationStatusFilename))
	_ = os.Remove(filepath.Join(baseB, replicationStatusFilename))
	if err := rt1.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("case1 read: %v", err)
	}
	if !rt1.ActiveFolderToggler {
		t.Fatalf("case1: expected default active folder toggler true")
	}

	// Case 2: only passive has status -> flip to passive
	rt2, _ := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err := writeStat(baseB); err != nil {
		t.Fatalf("write passive: %v", err)
	}
	if err := rt2.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("case2 read: %v", err)
	}
	if rt2.ActiveFolderToggler {
		t.Fatalf("case2: expected toggler flipped to passive (false)")
	}

	// Case 3: both exist; make passive newer -> flip
	rt3, _ := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err := writeStat(baseA); err != nil {
		t.Fatalf("write active: %v", err)
	}
	if err := writeStat(baseB); err != nil {
		t.Fatalf("write passive: %v", err)
	}
	// Ensure passive mtime is later by touching it after active
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(baseB, replicationStatusFilename), later, later)
	if err := rt3.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("case3 read: %v", err)
	}
	if rt3.ActiveFolderToggler {
		t.Fatalf("case3: expected toggler flipped to passive (false)")
	}
}

// Ensures readStatusFromHomeFolder does not flip when only passive status file exists but is invalid JSON.
func TestReplicationTracker_ReadStatus_PassiveOnly_InvalidJSON_NoFlip(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	a := filepath.Join(t.TempDir(), "a")
	p := filepath.Join(t.TempDir(), "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(p, 0o755)

	// Isolate global
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	// Force initial state: active=true
	rt.ActiveFolderToggler = true

	// Write malformed status only on passive
	os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte("{"), 0o644)
	os.Remove(filepath.Join(a, replicationStatusFilename))

	if err := rt.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("unexpected error reading status: %v", err)
	}
	if rt.ActiveFolderToggler != true {
		t.Fatalf("expected no flip when passive-only is invalid JSON")
	}
}

// Ensures handleFailedToReplicate returns early when GlobalReplicationDetails already indicates failure
// (coming from L2 or prior process), and does not write a replication status file.
func TestReplicationTracker_HandleFailedToReplicate_GlobalAlreadyFailed_NoWrite(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	// Isolate and preset global to FailedToReplicate=true
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: true}
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Precondition: no status file yet
	actPath := filepath.Join(a, replicationStatusFilename)
	if _, err := os.Stat(actPath); err == nil {
		t.Fatalf("unexpected pre-existing status file")
	}

	// Invoke; should mark r.FailedToReplicate and return without writing a file.
	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected r.FailedToReplicate true")
	}
	if _, err := os.Stat(actPath); err == nil {
		t.Fatalf("expected no status file write on early return")
	}
}

func Test_ReplicationTracker_HandleFailedToReplicate_TrackerAlreadyFailed_NoOp(t *testing.T) {
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
	// Mark tracker itself already failed; early-return guard should trigger.
	rt.FailedToReplicate = true
	rt.handleFailedToReplicate(ctx)
	// Should remain failed and not panic; status file should not be rewritten again implicitly.
	// We can't assert file touches easily; simply assert the flag remains set.
	if !rt.FailedToReplicate {
		t.Fatalf("expected no-op with tracker already failed")
	}
}

func Test_ReadStatus_PassivePresent_InvalidJSON_NoFlip_NoError(t *testing.T) {
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
	// Seed only passive file with invalid JSON; active missing.
	if err := os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("seed passive: %v", err)
	}
	rt.ActiveFolderToggler = true
	if err := rt.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("expected no error when passive has invalid json (branch ignores error)")
	}
	if rt.ActiveFolderToggler != true {
		t.Fatalf("expected no flip on invalid passive json")
	}
}

func Test_ReadStatus_ActivePresent_InvalidJSON_ReturnsError(t *testing.T) {
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
	// Seed active file with invalid JSON so readReplicationStatus returns error.
	if err := os.WriteFile(filepath.Join(a, replicationStatusFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	if err := rt.readStatusFromHomeFolder(ctx); err == nil {
		t.Fatalf("expected error from invalid active json")
	}
}

// --- helper mock caches for syncWithL2Cache ---

type getStructExErrCache2 struct{ sop.L2Cache }

func (c getStructExErrCache2) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

func (c getStructExErrCache2) GetStructEx(ctx context.Context, key string, v interface{}, ttl time.Duration) (bool, error) {
	return false, errors.New("getstructex err2")
}

type getStructExNotFoundCache struct{ sop.L2Cache }

func (c getStructExNotFoundCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

func (c getStructExNotFoundCache) GetStructEx(ctx context.Context, key string, v interface{}, ttl time.Duration) (bool, error) {
	return false, nil
}

type getStructExFoundCache struct {
	sop.L2Cache
	val ReplicationTrackedDetails
}

func (c getStructExFoundCache) GetStructEx(ctx context.Context, key string, v interface{}, ttl time.Duration) (bool, error) {
	// populate v with c.val
	if out, ok := v.(*ReplicationTrackedDetails); ok {
		*out = c.val
	}
	return true, nil
}

type setStructErrCache2 struct{ sop.L2Cache }

func (c setStructErrCache2) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

func (c setStructErrCache2) SetStruct(ctx context.Context, key string, value interface{}, exp time.Duration) error {
	return errors.New("setstruct err2")
}

// combined wrapper: GetStructEx returns not found, SetStruct returns error
type notFoundSetErrCache struct{ sop.L2Cache }

func (c notFoundSetErrCache) Info(ctx context.Context, section string) (string, error) {
	return "# Server\nrun_id:mock\n", nil
}

func (c notFoundSetErrCache) GetStructEx(ctx context.Context, key string, v interface{}, ttl time.Duration) (bool, error) {
	return false, nil
}
func (c notFoundSetErrCache) SetStruct(ctx context.Context, key string, value interface{}, exp time.Duration) error {
	return errors.New("setstruct err2")
}

// --- logCommitChanges ---

func Test_LogCommitChanges_Disabled_NoOp(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt := &replicationTracker{storesBaseFolders: []string{base, filepath.Join(base, "p")}}
	rt.ActiveFolderToggler = true
	rt.LogCommitChanges = false

	tid := sop.NewUUID()
	if err := rt.logCommitChanges(ctx, tid, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No file should be created
	fn := rt.formatActiveFolderEntity(filepath.Join(commitChangesLogFolder, tid.String()+logFileExtension))
	if _, err := os.Stat(fn); !os.IsNotExist(err) {
		t.Fatalf("expected no log file; stat err=%v", err)
	}
}

func Test_LogCommitChanges_Enabled_Writes(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt := &replicationTracker{storesBaseFolders: []string{base, filepath.Join(base, "p")}}
	rt.ActiveFolderToggler = true
	rt.LogCommitChanges = true

	// minimal payloads
	stores := []sop.StoreInfo{{Name: "s"}}
	tid := sop.NewUUID()
	if err := rt.logCommitChanges(ctx, tid, stores, nil, nil, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fn := rt.formatActiveFolderEntity(filepath.Join(commitChangesLogFolder, tid.String()+logFileExtension))
	if fi, err := os.Stat(fn); err != nil || fi.IsDir() {
		t.Fatalf("expected commit log file; err=%v", err)
	}
}

// --- readReplicationStatus ---

func Test_ReadReplicationStatus_Success_And_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	okf := filepath.Join(base, "ok.json")
	badf := filepath.Join(base, "bad.json")

	// write valid JSON
	if err := os.WriteFile(okf, []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// write invalid JSON
	if err := os.WriteFile(badf, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := &replicationTracker{}
	if err := rt.readReplicationStatus(ctx, okf); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !rt.ActiveFolderToggler || rt.FailedToReplicate {
		t.Fatalf("unexpected values loaded: %+v", rt.ReplicationTrackedDetails)
	}
	if err := rt.readReplicationStatus(ctx, badf); err == nil {
		t.Fatalf("expected unmarshal error for invalid JSON")
	}
}

// --- syncWithL2Cache branches ---

func Test_SyncWithL2Cache_Push_NotFound_Sets(t *testing.T) {
	ctx := context.Background()
	// prepare global value
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	g := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
	GlobalReplicationDetails = &g
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt := &replicationTracker{l2Cache: getStructExNotFoundCache{L2Cache: mocks.NewMockClient()}}
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func Test_SyncWithL2Cache_Push_FoundEqual_Returns(t *testing.T) {
	ctx := context.Background()
	val := ReplicationTrackedDetails{FailedToReplicate: false, ActiveFolderToggler: true}
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &val
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt := &replicationTracker{l2Cache: getStructExFoundCache{L2Cache: mocks.NewMockClient(), val: val}}
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func Test_SyncWithL2Cache_Push_FoundDifferent_Sets(t *testing.T) {
	ctx := context.Background()
	val := ReplicationTrackedDetails{FailedToReplicate: false, ActiveFolderToggler: true}
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: false}
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt := &replicationTracker{l2Cache: getStructExFoundCache{L2Cache: mocks.NewMockClient(), val: val}}
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func Test_SyncWithL2Cache_Push_GetErr_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	rt := &replicationTracker{l2Cache: getStructExErrCache2{L2Cache: mocks.NewMockClient()}}
	if err := rt.syncWithL2Cache(ctx, true); err == nil {
		t.Fatalf("expected error")
	}
}

func Test_SyncWithL2Cache_Push_NotFound_SetErr_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	// set global to any value
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	g := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
	GlobalReplicationDetails = &g
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	// Cache that reports not found on GetStructEx and errors on SetStruct
	rt := &replicationTracker{l2Cache: notFoundSetErrCache{L2Cache: mocks.NewMockClient()}}
	if err := rt.syncWithL2Cache(ctx, true); err == nil {
		t.Fatalf("expected error on SetStruct")
	}
}

func Test_SyncWithL2Cache_Pull_NotFound_NoChange(t *testing.T) {
	ctx := context.Background()
	rt := &replicationTracker{l2Cache: getStructExNotFoundCache{L2Cache: mocks.NewMockClient()}}
	if err := rt.syncWithL2Cache(ctx, false); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

// Ensure handleFailedToReplicate warns on push when SetStruct fails (after a successful write).
func Test_HandleFailed_PushSetStructError_WarnsAfterWrite(t *testing.T) {
	ctx := context.Background()

	// Isolate global state
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	p := t.TempDir()

	// Use cache that returns not found on pull and errors on push SetStruct.
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, notFoundSetErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Act
	rt.handleFailedToReplicate(ctx)

	// Assert flags set and replstat written despite push SetStruct error.
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true")
	}
	fname := rt.formatActiveFolderEntity(replicationStatusFilename)
	if fi, err := os.Stat(fname); err != nil || fi.IsDir() {
		t.Fatalf("expected replstat file; err=%v", err)
	}
}

func Test_SyncWithL2Cache_Pull_GetErr_ReturnsErr(t *testing.T) {
	ctx := context.Background()
	rt := &replicationTracker{l2Cache: getStructExErrCache2{L2Cache: mocks.NewMockClient()}}
	if err := rt.syncWithL2Cache(ctx, false); err == nil {
		t.Fatalf("expected error")
	}
}

func Test_SyncWithL2Cache_Pull_Found_UpdatesGlobal(t *testing.T) {
	ctx := context.Background()
	val := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: false}
	rt := &replicationTracker{l2Cache: getStructExFoundCache{L2Cache: mocks.NewMockClient(), val: val}}

	// Snapshot and restore global
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	if err := rt.syncWithL2Cache(ctx, false); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if GlobalReplicationDetails == nil || *GlobalReplicationDetails != val {
		t.Fatalf("expected global to be updated from cache; got %+v", GlobalReplicationDetails)
	}
}
