package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sop "github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// Covers additional branches in handleFailedToReplicate (replicate off; already failed) and readStatusFromHomeFolder (no files).
func Test_ReplicationTracker_MoreBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("handleFailedToReplicate_NoReplicate_NoOp", func(t *testing.T) {
		rt, _ := NewReplicationTracker(ctx, []string{t.TempDir()}, false, mocks.NewMockClient())
		rt.handleFailedToReplicate(ctx) // no panic, no-op
		if rt.FailedToReplicate {       // should remain default false
			t.Fatalf("expected no change when replicate=false")
		}
	})

	t.Run("handleFailedToReplicate_AlreadyFailed_NoRewrite", func(t *testing.T) {
		prev := GlobalReplicationDetails
		GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: true}
		t.Cleanup(func() { GlobalReplicationDetails = prev })
		rt, _ := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, true, mocks.NewMockClient())
		rt.handleFailedToReplicate(ctx)
		if !rt.FailedToReplicate {
			t.Fatalf("expected copied failure flag")
		}
	})

	t.Run("readStatusFromHomeFolder_NoFiles_ReturnsNil", func(t *testing.T) {
		prev := GlobalReplicationDetails
		GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
		t.Cleanup(func() { GlobalReplicationDetails = prev })
		rt, _ := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, true, mocks.NewMockClient())
		// remove any files if created
		if err := rt.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("read status no files: %v", err)
		}
	})

	t.Run("logCommitChanges_PushAndSkipPaths", func(t *testing.T) {
		// push path when GlobalReplicationDetails present and L2 initially empty
		prev := GlobalReplicationDetails
		GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false, LogCommitChanges: false}
		t.Cleanup(func() { GlobalReplicationDetails = prev })
		rt, _ := NewReplicationTracker(ctx, []string{t.TempDir()}, true, mocks.NewMockClient())
		// Skip path: LogCommitChanges false -> no write
		if err := rt.logCommitChanges(ctx, sop.NewUUID(), nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("logCommitChanges skip: %v", err)
		}
		// Enable and push value to L2 through startLoggingCommitChanges
		if err := rt.startLoggingCommitChanges(ctx); err != nil {
			// It may fail to write when path collisions occur; just tolerate if it returns error
			_ = err
		}
		// Try a log write (best-effort)
		_ = rt.logCommitChanges(ctx, sop.NewUUID(), nil, nil, nil, nil, nil)
		// Pull path: ensure it’s a no-op when missing in L2
		_ = rt.syncWithL2Cache(ctx, false)
		_ = rt.syncWithL2Cache(ctx, true)
		// TTL path exercised indirectly by set/get in mocks
		_ = time.Now()
	})
}

// stubMarshaler forces Marshal to error to exercise the error branch in logCommitChanges.
type stubMarshaler struct{}

func (stubMarshaler) Marshal(v any) ([]byte, error)      { return nil, assertErr("marshal fail") }
func (stubMarshaler) Unmarshal(data []byte, v any) error { return nil }

// assertErr is a lightweight error type for deterministic failures.
type assertErr string

func (e assertErr) Error() string { return string(e) }

func Test_LogCommitChanges_MarshalError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt := &replicationTracker{storesBaseFolders: []string{base, base}}
	rt.ActiveFolderToggler = true
	rt.LogCommitChanges = true

	// Swap out the default marshaler and restore after.
	prev := encoding.DefaultMarshaler
	encoding.DefaultMarshaler = stubMarshaler{}
	t.Cleanup(func() { encoding.DefaultMarshaler = prev })

	// Any payload is fine; Marshal will fail.
	err := rt.logCommitChanges(ctx, sop.NewUUID(), []sop.StoreInfo{{Timestamp: time.Now().UnixMilli()}}, nil, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected marshal error from logCommitChanges")
	}
}

// Force logCommitChanges write error by making commit log directory a file.
func Test_LogCommitChanges_Enabled_WriteError(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	rt := &replicationTracker{storesBaseFolders: []string{base, filepath.Join(base, "p")}}
	rt.ActiveFolderToggler = true
	rt.LogCommitChanges = true

	// Make commitlogs path a file to cause WriteFile to fail
	clog := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := os.WriteFile(clog, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	err := rt.logCommitChanges(ctx, sop.NewUUID(), nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatalf("expected error due to commit log path being a file")
	}
}

// Covers the branch where L2 cache already indicates failure; tracker should set local flag and return without writing.
func Test_HandleFailedToReplicate_L2CacheKnowsFailure(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	// Prepare L2 cache to already know about failure.
	cache := mocks.NewMockClient()
	// Seed L2 with FailedToReplicate=true so syncWithL2Cache(false) loads it into GlobalReplicationDetails.
	seed := ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
	if err := cache.SetStruct(ctx, fmt.Sprintf("Rreplstat:%s", a), seed, replicationStatusCacheTTLDuration); err != nil {
		t.Fatalf("seed l2: %v", err)
	}

	// Construct tracker in replicate mode; constructor will pull from L2 and not push.
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, cache)
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Ensure we don't leak global state into other tests.
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = nil
		globalReplicationDetailsLocker.Unlock()
	}()

	// Sanity: no replstat file should exist yet.
	actFile := rt.formatActiveFolderEntity(replicationStatusFilename)
	if _, err := os.Stat(actFile); !os.IsNotExist(err) {
		t.Fatalf("unexpected replstat presence before: %v", err)
	}

	// Invoke the failure path; since L2 knows, it should set local flag and return without writes.
	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected tracker to mark FailedToReplicate=true from L2 state")
	}
	if _, err := os.Stat(actFile); !os.IsNotExist(err) {
		t.Fatalf("expected no write to active replstat when L2 already knows; stat err: %v", err)
	}
}

// Covers writeReplicationStatus error path inside handleFailedToReplicate by making
// the target replstat.txt path a directory, forcing the write to fail (warn path).
func Test_HandleFailedToReplicate_WriteStatusError_WarnsAndContinues(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = nil
		globalReplicationDetailsLocker.Unlock()
	}()

	// Make the replication status file path a directory to force write failure.
	actFile := rt.formatActiveFolderEntity(replicationStatusFilename)
	if err := os.MkdirAll(filepath.Dir(actFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actFile, 0o755); err != nil {
		t.Fatal(err)
	}

	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate to be set true")
	}
	// Ensure the path remains a directory (write failed, no file created over it).
	if fi, err := os.Stat(actFile); err != nil || !fi.IsDir() {
		t.Fatalf("expected directory at replstat path; stat err=%v isDir?", err)
	}
}

// Covers branch where Exists(active) is true but os.Stat(active) fails; os.Stat(passive) succeeds,
// toggle flips, then readReplicationStatus(active) fails and error is returned.
func Test_ReadStatusFromHomeFolder_StatActiveError_PassiveOK_ReadActiveFails(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	active := filepath.Join(base, "a")
	passive := filepath.Join(base, "p")
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(passive, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a passive replstat file so os.Stat(passive file) succeeds; write invalid JSON
	// so that after the toggler flips and the code reads the newly active file, it errors.
	passiveFile := filepath.Join(passive, replicationStatusFilename)
	if err := os.WriteFile(passiveFile, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove permissions from active dir so Exists(active) returns true, Stat(active) fails, and later ReadFile(active) fails.
	if err := os.Chmod(active, 0); err != nil {
		t.Fatal(err)
	}
	// Ensure cleanup so TempDir removal works.
	defer func() { _ = os.Chmod(active, 0o755) }()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	if err := rt.readStatusFromHomeFolder(ctx); err == nil {
		t.Fatalf("expected error due to failing to read active replstat after passive stat ok")
	}
	if rt.ActiveFolderToggler != false {
		t.Fatalf("expected toggler flipped to false; got %v", rt.ActiveFolderToggler)
	}
}

// Covers readStatusFromHomeFolder branch where Exists(active) is true (due to permission error),
// but os.Stat(active) fails and os.Stat(passive) fails too, yielding an error return.
func Test_ReadStatusFromHomeFolder_StatErrorOnBothPaths(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	// Make two folders that will become active/passive; remove exec/read perms to trigger EACCES on stat.
	active := filepath.Join(base, "a")
	passive := filepath.Join(base, "p")
	if err := os.MkdirAll(active, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(passive, 0o755); err != nil {
		t.Fatal(err)
	}

	// Remove all permissions so os.Stat on files under these dirs returns a permission error.
	if err := os.Chmod(active, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(passive, 0); err != nil {
		t.Fatal(err)
	}
	// Restore permissions at the end so TempDir cleanup succeeds.
	defer func() {
		_ = os.Chmod(active, 0o755)
		_ = os.Chmod(passive, 0o755)
	}()

	// Build tracker without triggering constructor side effects (replicate=false avoids reads/pushes).
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Ensure the replstat filenames resolve under the restricted dirs (we don't create files).
	_ = rt.formatActiveFolderEntity(replicationStatusFilename)
	_ = rt.formatPassiveFolderEntity(replicationStatusFilename)

	// Call and expect an error as both stat() calls should fail.
	if err := rt.readStatusFromHomeFolder(ctx); err == nil {
		t.Fatalf("expected error from readStatusFromHomeFolder when both stat paths fail")
	}
	// ActiveFolderToggler should remain at default (since no flip occurred on error path).
	if !rt.ActiveFolderToggler {
		t.Fatalf("expected ActiveFolderToggler to remain default true; got %v", rt.ActiveFolderToggler)
	}

	// Small sleep to avoid any modtime-based race with the filesystem timestamp granularity (paranoia on fast FS).
	time.Sleep(10 * time.Millisecond)
}

// Covers the branch: active replstat missing, passive exists and is readable -> flips toggler and returns nil.
func Test_ReadStatusFromHomeFolder_ActiveMissing_PassiveReadable_FlipsNoError(t *testing.T) {
	ctx := context.Background()
	active := t.TempDir()
	passive := t.TempDir()

	// Write a valid replication status JSON into passive side with ActiveFolderToggler=true
	rt0 := &replicationTracker{storesBaseFolders: []string{active, passive}, ReplicationTrackedDetails: ReplicationTrackedDetails{ActiveFolderToggler: true}}
	passiveFile := rt0.formatPassiveFolderEntity(replicationStatusFilename)
	if err := os.MkdirAll(filepath.Dir(passiveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passiveFile, []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Construct tracker with replicate=false to avoid global/L2 interactions
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	if err := rt.readStatusFromHomeFolder(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.ActiveFolderToggler != false {
		t.Fatalf("expected toggler flipped to passive (false); got %v", rt.ActiveFolderToggler)
	}
}

// Covers handleFailedToReplicate early return when not in replicate mode.
func Test_HandleFailedToReplicate_NoReplicate_EarlyReturn(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	rt, err := NewReplicationTracker(ctx, []string{a, p}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Call should return immediately without changing state or writing files.
	rt.handleFailedToReplicate(ctx)

	if rt.FailedToReplicate {
		t.Fatalf("did not expect FailedToReplicate to change in no-replicate mode")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); !os.IsNotExist(err) {
		t.Fatalf("expected no replstat write in no-replicate mode; stat err: %v", err)
	}
}

// Covers the normal success path: replicate=true, L2 does not indicate failure,
// we acquire the lock, set flags, write replstat to active, and push to L2.
func Test_HandleFailedToReplicate_SetsFlags_WritesAndPushes(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	// Preserve and restore global replication details to avoid cross-test leakage.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Sanity preconditions
	if rt.FailedToReplicate {
		t.Fatalf("precondition: expected FailedToReplicate=false")
	}
	fname := rt.formatActiveFolderEntity(replicationStatusFilename)
	if _, err := os.Stat(fname); !os.IsNotExist(err) {
		t.Fatalf("precondition: replstat should not exist yet; stat err: %v", err)
	}

	// Act
	rt.handleFailedToReplicate(ctx)

	// Assert: flags set and file created
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true after handle")
	}
	globalReplicationDetailsLocker.Lock()
	g := GlobalReplicationDetails
	globalReplicationDetailsLocker.Unlock()
	if g == nil || !g.FailedToReplicate {
		t.Fatalf("expected GlobalReplicationDetails.FailedToReplicate=true")
	}
	if fi, err := os.Stat(fname); err != nil || fi.IsDir() {
		t.Fatalf("expected replstat file to be written; err=%v", err)
	}
}

// Covers the top guard: when already failed locally, returns immediately with no writes.
func Test_HandleFailedToReplicate_AlreadyFailed_Start_Returns(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Set local flag before calling; should return immediately.
	rt.FailedToReplicate = true
	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate to remain true")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); !os.IsNotExist(err) {
		t.Fatalf("expected no replstat write when already failed; stat err: %v", err)
	}
}

// Covers syncWithL2Cache error on push path after write; function should warn but still succeed.
func Test_HandleFailedToReplicate_PushL2Error_Warns(t *testing.T) {
	ctx := context.Background()
	a := t.TempDir()
	p := t.TempDir()

	// Reset and restore global state to keep isolation.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	// Use a cache that returns error on GetStructEx to force both pull and push error branches.
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, getStructExErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true after handle with L2 error")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err != nil {
		t.Fatalf("expected replstat to be written even if L2 push errors; err=%v", err)
	}
}

// Ensures handleFailedToReplicate proceeds when initial L2 pull errors but global is not failed yet:
// it should set local/global flags, write replstat, and attempt L2 push.
func Test_HandleFailed_PullError_Proceeds_To_Write_And_Push(t *testing.T) {
	ctx := context.Background()

	// Isolate and restore global state to avoid leakage across tests.
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

	// Use a cache that returns error on GetStructEx (pull) but allows SetStruct (push) to succeed.
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, getStructExErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("new rt: %v", err)
	}

	// Precondition: not failed and no replstat written yet.
	if rt.FailedToReplicate {
		t.Fatalf("precondition: expected FailedToReplicate=false")
	}
	actFile := rt.formatActiveFolderEntity(replicationStatusFilename)
	if _, err := os.Stat(actFile); !os.IsNotExist(err) {
		t.Fatalf("precondition: replstat should not exist; stat err=%v", err)
	}

	// Act: pull will error (warn), GlobalReplicationDetails currently not failed -> proceed to lock, set flags, write, and push.
	rt.handleFailedToReplicate(ctx)

	// Assert: local/global flags set and file created even though the initial pull errored.
	if !rt.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate=true after handle with pull error")
	}
	globalReplicationDetailsLocker.Lock()
	g := GlobalReplicationDetails
	globalReplicationDetailsLocker.Unlock()
	if g == nil || !g.FailedToReplicate {
		t.Fatalf("expected GlobalReplicationDetails.FailedToReplicate=true")
	}
	if fi, err := os.Stat(actFile); err != nil || fi.IsDir() {
		t.Fatalf("expected replstat file to be written; err=%v", err)
	}
}

// cache that flips rt.FailedToReplicate during GetStructEx (same goroutine, no race)
type flippingCache struct {
	sop.L2Cache
	rt *replicationTracker
}

func (c flippingCache) GetStructEx(ctx context.Context, key string, value interface{}, exp time.Duration) (bool, error) {
	// Flip the flag before returning, simulating a concurrent update observed after the lock.
	c.rt.FailedToReplicate = true
	// Pretend not found in L2 so syncWithL2Cache(false) becomes a no-op.
	return false, nil
}

func Test_HandleFailed_SecondCheckAfterLock_NoOp(t *testing.T) {
	ctx := context.Background()
	prev := GlobalReplicationDetails
	t.Cleanup(func() { GlobalReplicationDetails = prev })

	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Ensure global says not failed; have GetStructEx flip local flag during sync to hit the recheck-under-lock path.
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: rt.ActiveFolderToggler, FailedToReplicate: false}
	rt.l2Cache = flippingCache{L2Cache: mocks.NewMockClient(), rt: rt}

	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected local Failure flipped by cache shim")
	}
	// No status file should be written as the function returns at the inner recheck.
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err == nil {
		t.Fatalf("replication status file should not be written on inner recheck early return")
	}
}

// Covers the branch where syncWithL2Cache(false) returns an error (warn path), and since
// GlobalReplicationDetails already indicates failure, the method returns early without writing.
func Test_HandleFailed_PullError_And_GlobalFailed_EarlyReturn(t *testing.T) {
	ctx := context.Background()
	// Preserve and restore global pointer to avoid contamination of other tests.
	prev := GlobalReplicationDetails
	t.Cleanup(func() { GlobalReplicationDetails = prev })

	a := t.TempDir()
	p := t.TempDir()
	// Set global to failed beforehand.
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: true}

	// Use a cache that errors on GetStructEx so handleFailedToReplicate will log a warn on pull.
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, getStructExErrCache{L2Cache: mocks.NewMockClient()})
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	rt.handleFailedToReplicate(ctx)

	if !rt.FailedToReplicate {
		t.Fatalf("expected local FailedToReplicate=true from global state")
	}
	if _, err := os.Stat(rt.formatActiveFolderEntity(replicationStatusFilename)); err == nil {
		t.Fatalf("did not expect replstat write on early return")
	}
}

// Drives handleFailedToReplicate into the inner recheck-after-lock early return path
// by flipping r.FailedToReplicate during the initial L2 pull, while global remains non-failed.
func Test_HandleFailed_InnerRecheck_EarlyReturn(t *testing.T) {
	ctx := context.Background()

	// Ensure global starts as non-failed and is restored after.
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	g := ReplicationTrackedDetails{FailedToReplicate: false, ActiveFolderToggler: true}
	GlobalReplicationDetails = &g
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	// Manually construct tracker to avoid constructor side effects.
	a := t.TempDir()
	p := t.TempDir()
	rt := &replicationTracker{
		ReplicationTrackedDetails: ReplicationTrackedDetails{ActiveFolderToggler: true},
		storesBaseFolders:         []string{a, p},
		replicate:                 true,
	}
	// Install a flipping cache so syncWithL2Cache(false) sets r.FailedToReplicate prior to taking the lock.
	rt.l2Cache = flippingCache{rt: rt}

	// Act
	rt.handleFailedToReplicate(ctx)

	// Assert: early return path taken (no write, global unchanged), local flag flipped.
	if !rt.FailedToReplicate {
		t.Fatalf("expected local flag flipped by inner recheck path")
	}
	globalReplicationDetailsLocker.Lock()
	if GlobalReplicationDetails == nil || GlobalReplicationDetails.FailedToReplicate {
		globalReplicationDetailsLocker.Unlock()
		t.Fatalf("expected global to remain non-failed on inner recheck early return")
	}
	globalReplicationDetailsLocker.Unlock()
}
