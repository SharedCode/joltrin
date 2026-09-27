package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers replicationTracker.failover error path when writing replication status to passive fails.
func Test_Failover_WriteReplicationStatus_Error(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	// Create a regular file to serve as a bogus passive "folder" so writes under it fail with ENOTDIR.
	passiveParent := t.TempDir()
	passiveFile := filepath.Join(passiveParent, "passive_as_file")
	if err := os.WriteFile(passiveFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write passive file: %v", err)
	}

	// Construct tracker with replicate disabled to avoid status probing that expects directories.
	rt, err := NewReplicationTracker(ctx, []string{active, passiveFile}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}

	// Ensure global matches current toggler so failover proceeds past early-return guards.
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: rt.ActiveFolderToggler}
	t.Cleanup(func() { GlobalReplicationDetails = prev })

	if err := rt.failover(ctx); err == nil {
		t.Fatalf("expected failover to return error due to writeReplicationStatus failure")
	}
}

// Covers the pre-lock early return branch: when global already reflects the opposite toggler
// or local is marked FailedToReplicate, failover should no-op.
func Test_Failover_PreLockEarlyReturn_NoOp(t *testing.T) {
	ctx := context.Background()

	// Isolate global state and set it to the opposite of local toggler to trigger early return.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: false}
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a, p := t.TempDir(), t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Local starts with ActiveFolderToggler=true by default; global set to false above.
	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover: %v", err)
	}
	// No toggle expected due to early return.
	if !rt.ActiveFolderToggler {
		t.Fatalf("expected no toggle on early return; got %v", rt.ActiveFolderToggler)
	}
}

// Ensures failover still writes and toggles when L2 push returns error (warn path).
func Test_Failover_PushL2Error_WarnsAndContinues(t *testing.T) {
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

	// Cache: pull not found, push SetStruct error
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, notFoundSetErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Compute expected passive status path before toggling
	prevPassiveStatus := rt.formatPassiveFolderEntity(replicationStatusFilename)

	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover err: %v", err)
	}

	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true")
	}
	if fi, err := os.Stat(prevPassiveStatus); err != nil || fi.IsDir() {
		t.Fatalf("expected passive status file; err=%v", err)
	}
}

// Covers the full success path of failover: writes status on passive, toggles active folder,
// sets FailedToReplicate, updates global, and pushes to L2.
func Test_Failover_Success_WritesPassive_Toggles_And_Pushes(t *testing.T) {
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
	cache := mocks.NewMockClient()

	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Preconditions: early-return guards are false (replicate on, not failed),
	// and global toggler equals local so it won't trigger early return.
	if rt.FailedToReplicate {
		t.Fatalf("precondition: expected not failed")
	}

	// Compute expected passive status path before toggling (failover writes to current passive, then toggles).
	prevPassiveStatus := rt.formatPassiveFolderEntity(replicationStatusFilename)

	// Act
	if err := rt.failover(ctx); err != nil {
		t.Fatalf("failover: %v", err)
	}

	// Assert: status written on passive before toggle
	if fi, err := os.Stat(prevPassiveStatus); err != nil || fi.IsDir() {
		t.Fatalf("expected status file on passive; err=%v", err)
	}
	// Toggled active folder and FailedToReplicate set.
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true after failover")
	}
	if rt.ActiveFolderToggler != false {
		t.Fatalf("expected ActiveFolderToggler toggled to false; got %v", rt.ActiveFolderToggler)
	}
	// Global updated to copy (now toggled)
	globalReplicationDetailsLocker.Lock()
	g := GlobalReplicationDetails
	globalReplicationDetailsLocker.Unlock()
	if g == nil || g.ActiveFolderToggler != rt.ActiveFolderToggler {
		t.Fatalf("expected global toggler updated to %v; got %+v", rt.ActiveFolderToggler, g)
	}
}
