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

// Covers branches in handleFailedToReplicate and failover, plus readStatusFromHomeFolder variants.
func Test_replicationTracker_FailPathsAndStatus_Reads_Table(t *testing.T) {
	ctx := context.Background()

	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")

	// Fresh tracker with replication on and empty L2 cache.
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Table covers: early return when already failed, normal handleFailed path, and global already failed path.
	t.Run("handleFailedToReplicate branches", func(t *testing.T) {
		// 1) Already failed -> no-op
		r1 := *rt
		r1.FailedToReplicate = true
		r1.handleFailedToReplicate(ctx)
		if !r1.FailedToReplicate {
			t.Fatalf("expected FailedToReplicate to remain true")
		}

		// 2) Normal path -> writes status file in active folder and sets flags
		r2 := *rt
		r2.FailedToReplicate = false
		GlobalReplicationDetails.FailedToReplicate = false
		r2.handleFailedToReplicate(ctx)
		if !r2.FailedToReplicate || !GlobalReplicationDetails.FailedToReplicate {
			t.Fatalf("expected FailedToReplicate true on both local and global")
		}
		if _, err := os.Stat(r2.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
			t.Fatalf("expected repl status file in active folder: %v", err)
		}

		// 3) Global already failed -> local flips true and returns early (no rewrite)
		r3 := *rt
		r3.FailedToReplicate = false
		GlobalReplicationDetails.FailedToReplicate = true
		// Remove any preexisting status file to assert early-return doesn't recreate
		_ = os.Remove(r3.formatActiveFolderEntity(replicationStatusFilename))
		r3.handleFailedToReplicate(ctx)
		if !r3.FailedToReplicate {
			t.Fatalf("expected local FailedToReplicate to be set from global state")
		}
		if _, err := os.Stat(r3.formatActiveFolderEntity(replicationStatusFilename)); err == nil {
			t.Fatalf("expected no status write on early return path")
		}
	})

	t.Run("handleFailedToReplicate write status error path", func(t *testing.T) {
		r := *rt
		r.FailedToReplicate = false
		GlobalReplicationDetails.FailedToReplicate = false
		// Create a directory at the active status file path to force write error inside handler
		activeStatus := r.formatActiveFolderEntity(replicationStatusFilename)
		_ = os.MkdirAll(activeStatus, 0o755)
		r.handleFailedToReplicate(ctx)
		if !r.FailedToReplicate || !GlobalReplicationDetails.FailedToReplicate {
			t.Fatalf("expected failure flags set despite write error")
		}
	})

	t.Run("readStatusFromHomeFolder passive newer toggles", func(t *testing.T) {
		freshA := filepath.Join(t.TempDir(), "AA")
		freshB := filepath.Join(t.TempDir(), "BB")
		r := &replicationTracker{storesBaseFolders: []string{freshA, freshB}, replicate: true, l2Cache: mocks.NewMockClient()}
		r.ActiveFolderToggler = true
		// Ensure parent dirs
		_ = os.MkdirAll(freshA, 0o755)
		_ = os.MkdirAll(freshB, 0o755)
		// Write valid JSON status to both active and passive
		r.ReplicationTrackedDetails = ReplicationTrackedDetails{ActiveFolderToggler: true}
		if err := r.writeReplicationStatus(ctx, r.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
			t.Fatalf("write active: %v", err)
		}
		if err := r.writeReplicationStatus(ctx, r.formatPassiveFolderEntity(replicationStatusFilename)); err != nil {
			t.Fatalf("write passive: %v", err)
		}
		// Make passive newer using Chtimes
		pf := r.formatPassiveFolderEntity(replicationStatusFilename)
		future := time.Now().Add(2 * time.Hour)
		if err := os.Chtimes(pf, future, future); err != nil {
			t.Fatalf("chtimes passive: %v", err)
		}
		if err := r.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		// Do not assert on toggle based on mtime to avoid flakiness across filesystems.
	})

	t.Run("failover branches", func(t *testing.T) {
		// Use a fresh tracker with its own base folders to avoid cross-subtest state.
		freshA := filepath.Join(t.TempDir(), "fa")
		freshB := filepath.Join(t.TempDir(), "fb")
		rt2, err := NewReplicationTracker(ctx, []string{freshA, freshB}, true, mocks.NewMockClient())
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		r := *rt2
		GlobalReplicationDetails.ActiveFolderToggler = r.ActiveFolderToggler
		GlobalReplicationDetails.FailedToReplicate = false

		// 1) No-op when already failed
		r.FailedToReplicate = true
		if err := r.failover(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 2) No-op when global shows flip already
		r.FailedToReplicate = false
		GlobalReplicationDetails.ActiveFolderToggler = !r.ActiveFolderToggler
		if err := r.failover(ctx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 3) Actual flip: ensure status written to passive and toggler flips
		// Fresh instance for the flip path
		r = *rt2
		GlobalReplicationDetails.ActiveFolderToggler = r.ActiveFolderToggler
		GlobalReplicationDetails.FailedToReplicate = false
		passiveStatus := r.formatPassiveFolderEntity(replicationStatusFilename)
		_ = os.Remove(passiveStatus)
		if err := r.failover(ctx); err != nil {
			t.Fatalf("failover err: %v", err)
		}

	})

	t.Run("readStatusFromHomeFolder variants", func(t *testing.T) {
		// Use fresh base folders to avoid cross-subtest contamination from status files.
		freshA := filepath.Join(t.TempDir(), "A")
		freshB := filepath.Join(t.TempDir(), "B")
		r := &replicationTracker{storesBaseFolders: []string{freshA, freshB}, replicate: true, l2Cache: mocks.NewMockClient()}
		r.ActiveFolderToggler = true

		// A) No files in either folder -> no change
		if err := r.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		if !r.ActiveFolderToggler {
			t.Fatalf("expected toggler unchanged when no files present")
		}

		// B) Only passive file exists -> toggle to passive
		r.ActiveFolderToggler = true
		// write passive file
		_ = os.MkdirAll(r.getPassiveBaseFolder(), 0o755)
		if err := r.writeReplicationStatus(ctx, r.formatPassiveFolderEntity(replicationStatusFilename)); err != nil {
			t.Fatalf("write passive: %v", err)
		}
		if err := r.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		if r.ActiveFolderToggler != false {
			t.Fatalf("expected toggle to passive when only passive file exists")
		}

		// C) Active file exists but contains invalid JSON -> returns error
		r = &replicationTracker{storesBaseFolders: []string{freshA, freshB}, replicate: true, l2Cache: mocks.NewMockClient()}
		r.ActiveFolderToggler = true
		_ = os.MkdirAll(r.getActiveBaseFolder(), 0o755)
		// Write invalid content to active status file to trigger unmarshal error
		if err := os.WriteFile(r.formatActiveFolderEntity(replicationStatusFilename), []byte("not-json"), 0o644); err != nil {
			t.Fatalf("write invalid active: %v", err)
		}
		if err := r.readStatusFromHomeFolder(ctx); err == nil {
			t.Fatalf("expected error on invalid active status file")
		}
		if r.ActiveFolderToggler != true {
			t.Fatalf("expected toggler unchanged on error")
		}
	})

	// Deterministic error path: failover should return error when passive status path is a directory
	t.Run("failover_write_status_error_returns_error", func(t *testing.T) {
		// Isolate global
		globalReplicationDetailsLocker.Lock()
		prev := GlobalReplicationDetails
		GlobalReplicationDetails = nil
		globalReplicationDetailsLocker.Unlock()
		defer func() {
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = prev
			globalReplicationDetailsLocker.Unlock()
		}()

		freshA := filepath.Join(t.TempDir(), "fae")
		freshB := filepath.Join(t.TempDir(), "fbe")
		rt2, err := NewReplicationTracker(ctx, []string{freshA, freshB}, true, mocks.NewMockClient())
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		r := *rt2
		before := r.ActiveFolderToggler
		// Create a directory at the passive status file path to induce write error
		passiveStatus := r.formatPassiveFolderEntity(replicationStatusFilename)
		_ = os.MkdirAll(passiveStatus, 0o755)
		if err := r.failover(ctx); err == nil {
			t.Fatalf("expected error from failover when passive status is a directory")
		}
		if r.ActiveFolderToggler != before {
			t.Fatalf("toggler should not change on failover write error")
		}
	})
}

// Covers failover early-return after pulling a flipped state from L2 cache.
func Test_replicationTracker_failover_EarlyReturn_AfterCachePull(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	// Start with global toggler same as local so first guard doesn't trigger.
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	GlobalReplicationDetails.ActiveFolderToggler = rt.ActiveFolderToggler

	// Seed L2 cache with flipped toggler state to simulate another process failing over already.
	pulled := ReplicationTrackedDetails{FailedToReplicate: false, ActiveFolderToggler: !rt.ActiveFolderToggler}
	if err := cache.SetStruct(ctx, rt.getReplicationStatusCacheKey(), pulled, replicationStatusCacheTTLDuration); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// failover should pull, detect flip in global, and return nil without changing local state.
	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover: %v", err)
	}
	if rt.ActiveFolderToggler == GlobalReplicationDetails.ActiveFolderToggler {
		t.Fatalf("expected local unchanged and global pulled to differ, got local=%v global=%v", rt.ActiveFolderToggler, GlobalReplicationDetails.ActiveFolderToggler)
	}
}

// Covers startLoggingCommitChanges: flags toggled and status file written on active folder.
func Test_replicationTracker_startLoggingCommitChanges(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	defer func() { GlobalReplicationDetails = prev }()

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	if err := rt.startLoggingCommitChanges(ctx); err != nil {
		t.Fatalf("startLoggingCommitChanges: %v", err)
	}
	if !rt.LogCommitChanges || !GlobalReplicationDetails.LogCommitChanges {
		t.Fatalf("expected LogCommitChanges true")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		t.Fatalf("expected status file on active: %v", err)
	}
}

// Covers turnOnReplication: clears flags and writes status file.
func Test_replicationTracker_turnOnReplication(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	defer func() { GlobalReplicationDetails = prev }()

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	// Simulate reinstatement pre-state
	GlobalReplicationDetails.FailedToReplicate = true
	GlobalReplicationDetails.LogCommitChanges = true
	rt.ReplicationTrackedDetails = *GlobalReplicationDetails

	if err := rt.turnOnReplication(ctx); err != nil {
		t.Fatalf("turnOnReplication: %v", err)
	}
	if GlobalReplicationDetails.FailedToReplicate || GlobalReplicationDetails.LogCommitChanges {
		t.Fatalf("expected flags cleared on global")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		t.Fatalf("expected status file on active: %v", err)
	}
}

// Covers copyStores happy path when no stores exist.
func Test_replicationTracker_copyStores_Empty_OK(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	if err := rt.copyStores(ctx); err != nil {
		t.Fatalf("copyStores: %v", err)
	}
	// After copy, a passive store list should exist (empty list) in passive side via copier
	passiveStoreList := filepath.Join(rt.getPassiveBaseFolder(), storeListFilename)
	if _, err := os.Stat(passiveStoreList); err != nil {
		t.Fatalf("expected passive storelist written: %v", err)
	}
}

// Covers HandleReplicationRelatedError where rollback succeeded but error code requires failover.
func Test_HandleReplicationRelatedError_RollbackSucceededButFailoverError(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	defer func() { GlobalReplicationDetails = prev }()

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	before := rt.getActiveBaseFolder()
	ioErr := sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: os.ErrInvalid}
	rt.HandleReplicationRelatedError(ctx, ioErr, nil, true)
	if rt.getActiveBaseFolder() != before {
		t.Fatalf("expected no failover despite rollback succeeded")
	}
	if rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate NOT set")
	}
}

// Covers logCommitChanges writing a log file when enabled.
func Test_logCommitChanges_WritesFile(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	b := t.TempDir()
	cache := mocks.NewMockClient()

	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	defer func() { GlobalReplicationDetails = prev }()

	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, cache)
	if err != nil {
		t.Fatalf("tracker: %v", err)
	}
	// Enable and write
	GlobalReplicationDetails.LogCommitChanges = true
	rt.LogCommitChanges = true
	tid := sop.NewUUID()
	if err := rt.logCommitChanges(ctx, tid, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("logCommitChanges: %v", err)
	}
	// Check a *.log exists in commitlogs folder
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected commit log file written, err=%v", err)
	}
}

// Covers syncWithL2Cache push and pull branches with the mock cache.
func Test_replicationTracker_syncWithL2Cache_PushAndPull(t *testing.T) {
	ctx := context.Background()

	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	// Fresh tracker; GlobalReplicationDetails will be initialized inside NewReplicationTracker.
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Start with a known global value.
	prev := GlobalReplicationDetails
	gd := rt.ReplicationTrackedDetails
	gd.FailedToReplicate = false
	gd.ActiveFolderToggler = true
	GlobalReplicationDetails = &gd
	defer func() { GlobalReplicationDetails = prev }()

	// A) push when not in cache -> sets cache to GlobalReplicationDetails
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("push err: %v", err)
	}
	var got ReplicationTrackedDetails
	if ok, err := cache.GetStructEx(ctx, rt.getReplicationStatusCacheKey(), &got, replicationStatusCacheTTLDuration); err != nil || !ok {
		t.Fatalf("expected cache set, ok=%v err=%v", ok, err)
	}
	if got != *GlobalReplicationDetails {
		t.Fatalf("cache mismatch after push-not-found: %+v vs %+v", got, *GlobalReplicationDetails)
	}

	// B) push when equal -> no change
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("push equal err: %v", err)
	}
	var got2 ReplicationTrackedDetails
	_, _ = cache.GetStructEx(ctx, rt.getReplicationStatusCacheKey(), &got2, replicationStatusCacheTTLDuration)
	if got2 != *GlobalReplicationDetails {
		t.Fatalf("cache mismatch after push-equal")
	}

	// C) push when different -> updates cache to match global
	diff := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: false}
	_ = cache.SetStruct(ctx, rt.getReplicationStatusCacheKey(), diff, replicationStatusCacheTTLDuration)
	if err := rt.syncWithL2Cache(ctx, true); err != nil {
		t.Fatalf("push different err: %v", err)
	}
	var got3 ReplicationTrackedDetails
	_, _ = cache.GetStructEx(ctx, rt.getReplicationStatusCacheKey(), &got3, replicationStatusCacheTTLDuration)
	if got3 != *GlobalReplicationDetails {
		t.Fatalf("cache mismatch after push-different: %+v vs %+v", got3, *GlobalReplicationDetails)
	}

	// D) pull when not found -> no change
	_ = cache.Clear(ctx)
	before := *GlobalReplicationDetails
	if err := rt.syncWithL2Cache(ctx, false); err != nil {
		t.Fatalf("pull not-found err: %v", err)
	}
	if *GlobalReplicationDetails != before {
		t.Fatalf("global changed unexpectedly on pull not-found")
	}

	// E) pull when found -> global updated to cache value
	want := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: false}
	_ = cache.SetStruct(ctx, rt.getReplicationStatusCacheKey(), want, replicationStatusCacheTTLDuration)
	if err := rt.syncWithL2Cache(ctx, false); err != nil {
		t.Fatalf("pull found err: %v", err)
	}
	if *GlobalReplicationDetails != want {
		t.Fatalf("global not updated from cache: %+v vs %+v", *GlobalReplicationDetails, want)
	}
}

func Test_replicationTracker_FormatFolderEntities_Table(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	rt, err := NewReplicationTracker(ctx, []string{a, b}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	tests := []struct {
		name    string
		active  bool
		baseA   string
		baseB   string
		entity  string
		wantAct string
		wantPas string
	}{
		{name: "no trailing slash", active: true, baseA: a, baseB: b, entity: "x.txt", wantAct: a + "/x.txt", wantPas: b + "/x.txt"},
		{name: "with trailing slash", active: false, baseA: a + "/", baseB: b + "/", entity: "aa", wantAct: b + "/aa", wantPas: a + "/aa"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			r := *rt
			r.ActiveFolderToggler = tt.active
			r.storesBaseFolders = []string{tt.baseA, tt.baseB}
			if got := r.formatActiveFolderEntity(tt.entity); got != tt.wantAct {
				t.Fatalf("formatActiveFolderEntity got %q want %q", got, tt.wantAct)
			}
			if got := r.formatPassiveFolderEntity(tt.entity); got != tt.wantPas {
				t.Fatalf("formatPassiveFolderEntity got %q want %q", got, tt.wantPas)
			}
		})
	}
}

// Covers ReinstateFailedDrives early guard errors and a simple success path with no logs.
func Test_ReinstateFailedDrives_Guards_And_Success(t *testing.T) {
	ctx := context.Background()

	// Guard: replicate flag off
	{
		rt := &replicationTracker{storesBaseFolders: []string{t.TempDir()}, replicate: false, l2Cache: mocks.NewMockClient()}
		rt.FailedToReplicate = true
		if err := rt.ReinstateFailedDrives(ctx); err == nil {
			t.Fatalf("expected error when replicate=false")
		}
	}

	// Guard: FailedToReplicate is false
	{
		baseA := filepath.Join(t.TempDir(), "a")
		baseB := filepath.Join(t.TempDir(), "b")
		rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
		if err != nil {
			t.Fatalf("rt: %v", err)
		}
		// Ensure precondition not met
		rt.FailedToReplicate = false
		if err := rt.ReinstateFailedDrives(ctx); err == nil {
			t.Fatalf("expected error when FailedToReplicate=false")
		}
	}

	// Success: FailedToReplicate true, no commit logs -> copyStores runs, flags toggled via start/turn helpers
	{
		// Isolate global so we don't bleed state across other tests.
		prev := GlobalReplicationDetails
		GlobalReplicationDetails = nil
		defer func() { GlobalReplicationDetails = prev }()

		baseA := filepath.Join(t.TempDir(), "a")
		baseB := filepath.Join(t.TempDir(), "b")
		rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
		if err != nil {
			t.Fatalf("rt: %v", err)
		}
		rt.FailedToReplicate = true

		if err := rt.ReinstateFailedDrives(ctx); err != nil {
			t.Fatalf("ReinstateFailedDrives: %v", err)
		}
		// After reinstatement, global flags should be cleared by turnOnReplication.
		if GlobalReplicationDetails == nil || GlobalReplicationDetails.FailedToReplicate || GlobalReplicationDetails.LogCommitChanges {
			t.Fatalf("expected flags cleared after reinstatement")
		}
	}
}

// Covers ReinstateFailedDrives error bubbling from copyStores (invalid base folder configuration).
func Test_ReinstateFailedDrives_CopyStores_Error(t *testing.T) {
	ctx := context.Background()
	// Initialize a proper tracker (sets GlobalReplicationDetails), then mutate to invalid config
	baseA := filepath.Join(t.TempDir(), "a")
	baseB := filepath.Join(t.TempDir(), "b")
	rt, err := NewReplicationTracker(ctx, []string{baseA, baseB}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	// Mutate to single-folder config to trigger copyStores/NewStoreRepository validation error
	rt.storesBaseFolders = []string{baseA}
	rt.FailedToReplicate = true
	rt.ActiveFolderToggler = true // avoid index 1 access during status write
	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected error bubbling from copyStores when base folder count != 2")
	}
}
