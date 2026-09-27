package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// testFileIO is a controllable FileIO fake to induce failures on active or passive paths.
type testFileIO struct {
	failWriteActive  bool
	failWritePassive bool
	failMkdirActive  bool
	failMkdirPassive bool
	failReadActive   bool
	failRemoveActive bool
	data             map[string][]byte
}

func (f *testFileIO) WriteFile(_ context.Context, name string, data []byte, _ os.FileMode) error {
	// permissionType is aliased to os.FileMode in file scope; we don't depend on its value here.
	if f.failWritePassive && strings.Contains(name, "passive") {
		return errors.New("write passive failed")
	}
	if f.failWriteActive && strings.Contains(name, "active") {
		return errors.New("write active failed")
	}
	if f.data == nil {
		f.data = make(map[string][]byte)
	}
	// store payload by absolute path
	f.data[name] = append([]byte(nil), data...)
	return nil
}

func (f *testFileIO) ReadFile(_ context.Context, name string) ([]byte, error) {
	if f.failReadActive && strings.Contains(name, "active") {
		return nil, errors.New("read active failed")
	}
	if f.data == nil {
		return nil, errors.New("not found")
	}
	ba, ok := f.data[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), ba...), nil
}

func (f *testFileIO) Remove(_ context.Context, name string) error {
	if f.failRemoveActive && strings.Contains(name, "active") {
		return errors.New("remove active failed")
	}
	delete(f.data, name)
	return nil
}

func (f *testFileIO) Exists(_ context.Context, _ string) bool     { return true }
func (f *testFileIO) RemoveAll(_ context.Context, _ string) error { return nil }
func (f *testFileIO) MkdirAll(_ context.Context, path string, _ os.FileMode) error {
	if f.failMkdirActive && strings.Contains(path, "active") {
		return errors.New("mkdir active failed")
	}
	if f.failMkdirPassive && strings.Contains(path, "passive") {
		return errors.New("mkdir passive failed")
	}
	return nil
}
func (f *testFileIO) ReadDir(_ context.Context, _ string) ([]os.DirEntry, error) { return nil, nil }
func (f *testFileIO) List(_ context.Context, _ string) ([]string, error)         { return nil, nil }
func (f *testFileIO) Stat(ctx context.Context, path string) (os.FileInfo, error) {
	return dummyFileInfo{}, nil
}

// Table-driven coverage of typical failure/success paths for fileIO + replication.
func TestFileIOWithReplication_Scenarios(t *testing.T) {
	type scenarioFn func(t *testing.T, ctx context.Context, rt *replicationTracker, fio *fileIO, fake *testFileIO)

	cases := []struct {
		name     string
		fake     *testFileIO
		track    bool
		scenario scenarioFn
	}{
		{
			name:  "replicate fails on passive createStore (action type 2) and actions persist",
			fake:  &testFileIO{},
			track: true,
			scenario: func(t *testing.T, ctx context.Context, _ *replicationTracker, fio *fileIO, _ *testFileIO) {
				if err := fio.createStore(ctx, "s_passive_fail"); err != nil {
					t.Fatalf("createStore active: %v", err)
				}
				if len(fio.actionsDone) != 1 {
					t.Fatalf("expected 1 action after active create, got %d", len(fio.actionsDone))
				}
				// Induce passive mkdir failure for replicate step.
				if tf, ok := fio.fio.(*testFileIO); ok {
					tf.failMkdirPassive = true
				}
				if err := fio.replicate(ctx); err == nil {
					t.Fatalf("expected replicate mkdir passive failure")
				}
				if len(fio.actionsDone) == 0 {
					t.Fatalf("expected actions retained after failed replicate")
				}
			},
		},
		{
			name:  "createStore fails on active",
			fake:  &testFileIO{failMkdirActive: true},
			track: true,
			scenario: func(t *testing.T, ctx context.Context, rt *replicationTracker, fio *fileIO, _ *testFileIO) {
				if err := fio.createStore(ctx, "s1"); err == nil {
					t.Fatalf("expected error on createStore, got nil")
				}
				if len(fio.actionsDone) != 0 {
					t.Fatalf("expected no recorded actions on failure, got %d", len(fio.actionsDone))
				}
			},
		},
		{
			name:  "write fails on active",
			fake:  &testFileIO{failWriteActive: true},
			track: true,
			scenario: func(t *testing.T, ctx context.Context, rt *replicationTracker, fio *fileIO, _ *testFileIO) {
				if err := fio.createStore(ctx, "s2"); err != nil {
					t.Fatalf("unexpected createStore error: %v", err)
				}
				if err := fio.write(ctx, filepath.Join("s2", "file.bin"), []byte("x")); err == nil {
					t.Fatalf("expected error on write, got nil")
				}
				if len(fio.actionsDone) != 1 { // only createStore succeeded
					t.Fatalf("expected 1 recorded action, got %d", len(fio.actionsDone))
				}
			},
		},
		{
			name:  "read fails on active",
			fake:  &testFileIO{failReadActive: true},
			track: false,
			scenario: func(t *testing.T, ctx context.Context, rt *replicationTracker, fio *fileIO, _ *testFileIO) {
				if _, err := fio.read(ctx, filepath.Join("s3", "doesnotmatter.txt")); err == nil {
					t.Fatalf("expected read error, got nil")
				}
			},
		},
		{
			name:  "replicate fails on passive write and actions remain",
			fake:  &testFileIO{},
			track: true,
			scenario: func(t *testing.T, ctx context.Context, rt *replicationTracker, fio *fileIO, _ *testFileIO) {
				if err := fio.createStore(ctx, "s4"); err != nil {
					t.Fatalf("createStore: %v", err)
				}
				if err := fio.write(ctx, filepath.Join("s4", "file.bin"), []byte("payload")); err != nil {
					t.Fatalf("write active: %v", err)
				}
				if len(fio.actionsDone) != 2 {
					t.Fatalf("expected 2 recorded actions, got %d", len(fio.actionsDone))
				}
				// Enable passive write failure only for the replicate step.
				if tf, ok := fio.fio.(*testFileIO); ok {
					tf.failWritePassive = true
				}
				if err := fio.replicate(ctx); err == nil {
					t.Fatalf("expected replicate error, got nil")
				}
				if len(fio.actionsDone) == 0 {
					t.Fatalf("expected actions to remain after failed replicate")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			active := filepath.Join(t.TempDir(), "active")
			passive := filepath.Join(t.TempDir(), "passive")
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			rt.ActiveFolderToggler = true

			// Inject the fake FileIO into this instance only (no globals, no locks)
			fio := newFileIOWithReplicationInjected(rt, NewManageStoreFolder(nil), tc.track, tc.fake)
			tc.scenario(t, ctx, rt, fio, tc.fake)
		})
	}
}

// Covers readStatusFromHomeFolder additional branches and handleFailedToReplicate main path.
func Test_ReadStatus_And_HandleFailedToReplicate_More(t *testing.T) {
	ctx := context.Background()

	t.Run("ActiveMissing_PassivePresent_Flip", func(t *testing.T) {
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

		rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
		if err != nil {
			t.Fatalf("rt: %v", err)
		}
		// Seed only passive status file
		// Seed passive with ActiveFolderToggler=true so read then flip results in false.
		if err := os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
			t.Fatalf("seed passive: %v", err)
		}
		if err := rt.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		if rt.ActiveFolderToggler != false {
			t.Fatalf("expected flip to passive active (false), got %v", rt.ActiveFolderToggler)
		}
	})

	t.Run("BothExist_PassiveNewer_Flip", func(t *testing.T) {
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
		// Write both files
		if err := os.WriteFile(filepath.Join(a, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// Make passive newer
		newer := time.Now().Add(2 * time.Second)
		_ = os.Chtimes(filepath.Join(p, replicationStatusFilename), newer, newer)
		if err := rt.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		if rt.ActiveFolderToggler != false {
			t.Fatalf("expected flip due to newer passive file")
		}
	})

	t.Run("BothExist_ActiveNewer_NoFlip", func(t *testing.T) {
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
		// Write both files
		if err := os.WriteFile(filepath.Join(a, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// Make active newer
		newer := time.Now().Add(2 * time.Second)
		_ = os.Chtimes(filepath.Join(a, replicationStatusFilename), newer, newer)
		if err := rt.readStatusFromHomeFolder(ctx); err != nil {
			t.Fatalf("readStatus: %v", err)
		}
		if rt.ActiveFolderToggler != true {
			t.Fatalf("expected no flip when active newer")
		}
	})

	t.Run("HandleFailedToReplicate_SetsFlags_And_Writes", func(t *testing.T) {
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
		// Ensure starting state
		if rt.FailedToReplicate {
			t.Fatalf("unexpected failed flag start")
		}
		rt.handleFailedToReplicate(ctx)
		if !rt.FailedToReplicate || !GlobalReplicationDetails.FailedToReplicate {
			t.Fatalf("expected flags to be set")
		}
		// Active status file should exist
		if _, err := os.Stat(filepath.Join(a, replicationStatusFilename)); err != nil {
			t.Fatalf("expected status file written: %v", err)
		}
	})
}

// Deterministic error path: make the replstat path a directory before starting ReinstateFailedDrives
// so the initial write in startLoggingCommitChanges fails without timing sensitivity.
func Test_ReinstateFailedDrives_WriteStatusError_Deterministic(t *testing.T) {
	ctx := context.Background()

	// Isolate global state for this test
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	cache := mocks.NewMockClient()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	// Precondition for Reinstate
	rt.FailedToReplicate = true

	// Create a directory at the replstat path to force writeReplicationStatus to fail deterministically
	replPath := rt.formatActiveFolderEntity(replicationStatusFilename)
	if err := os.MkdirAll(replPath, 0o755); err != nil {
		t.Fatalf("mkdir replstat path: %v", err)
	}

	// Act: Reinstate should fail at startLoggingCommitChanges due to write error
	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected ReinstateFailedDrives to fail when replstat path is a directory")
	}
}

// Ensures ReinstateFailedDrives exercises the StoreRepository replication branch (First != nil)
// and completes successfully, removing the commit log.
func Test_ReinstateFailedDrives_WithStoreInfo_ReplicatesAndDeletes(t *testing.T) {
	ctx := context.Background()

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

	a := t.TempDir()
	p := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Precondition for reinstatement
	rt.FailedToReplicate = true

	// Seed a commit log with one StoreInfo so sr.Replicate branch executes; registry payloads empty slices
	storeName := "sRI"
	payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{
		First:  []sop.StoreInfo{{Name: storeName}},
		Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}},
	}
	ba, _ := encoding.DefaultMarshaler.Marshal(payload)
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(dir, sop.NewUUID().String()+logFileExtension)
	if err := os.WriteFile(fn, ba, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// Act
	if err := rt.ReinstateFailedDrives(ctx); err != nil {
		t.Fatalf("ReinstateFailedDrives: %v", err)
	}

	// Assert: log removed and flags cleared
	if _, err := os.Stat(fn); !os.IsNotExist(err) {
		t.Fatalf("expected commit log removed; stat err=%v", err)
	}
	if GlobalReplicationDetails == nil || GlobalReplicationDetails.FailedToReplicate || GlobalReplicationDetails.LogCommitChanges {
		t.Fatalf("expected flags cleared after reinstatement")
	}
}

// Ensures ReinstateFailedDrives returns error if turnOnReplication cannot write replication status
// due to replstat.txt path being turned into a directory after startLoggingCommitChanges.
func Test_Reinstate_TurnOnReplication_WriteStatus_Error(t *testing.T) {
	t.Skip("flaky under race; turnOnReplication write error is covered by direct unit test in reinstate_more_test.go")
	ctx := context.Background()

	// Isolate global replication state
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{}
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	p := t.TempDir()

	// Build tracker with replication on; set FailedToReplicate precondition required by Reinstate
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker: %v", err)
	}
	rt.FailedToReplicate = true
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails.FailedToReplicate = true
	globalReplicationDetailsLocker.Unlock()

	// Path to replstat in the active folder (before any toggles)
	replStatPath := rt.formatActiveFolderEntity(replicationStatusFilename)

	// Background goroutine: wait for startLogging to create replstat file,
	// then replace it with a directory so turnOnReplication's write fails.
	done := make(chan struct{})
	go func() {
		// Poll for up to ~1s
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			fi, err := os.Stat(replStatPath)
			if err == nil && !fi.IsDir() {
				// Swap file with directory
				_ = os.Remove(replStatPath)
				_ = os.Mkdir(replStatPath, 0o755)
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		close(done)
	}()

	// Act
	err = rt.ReinstateFailedDrives(ctx)

	<-done

	if err == nil {
		t.Fatalf("expected ReinstateFailedDrives to fail when turnOnReplication cannot write replstat.txt")
	}

	// Confirm path is a directory as intended
	if fi, statErr := os.Stat(replStatPath); statErr != nil || !fi.IsDir() {
		t.Fatalf("expected replstat path to be a directory; err=%v fi=%v", statErr, fi)
	}

	// Sanity: base folders remained intact
	if _, err := os.Stat(filepath.Clean(a)); err != nil {
		t.Fatalf("active folder missing: %v", err)
	}
	if _, err := os.Stat(filepath.Clean(p)); err != nil {
		t.Fatalf("passive folder missing: %v", err)
	}
}

func Test_ReinstateFailedDrives_ErrWhenReplicateOff(t *testing.T) {
	ctx := context.Background()
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected error when replicate flag is off")
	}
}

func Test_ReinstateFailedDrives_ErrWhenNotFailed(t *testing.T) {
	ctx := context.Background()
	// replicate on but FailedToReplicate false
	rt, err := NewReplicationTracker(ctx, []string{t.TempDir(), t.TempDir()}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	// Ensure global and tracker flags reflect not-failed state
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: rt.ActiveFolderToggler, FailedToReplicate: false}
	rt.ReplicationTrackedDetails.FailedToReplicate = false
	t.Cleanup(func() { GlobalReplicationDetails = prev })

	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected error when FailedToReplicate is false")
	}
}

// Verifies ReinstateFailedDrives processes a single commit log end-to-end and deletes it.
func Test_ReinstateFailedDrives_WithOneCommitLog_Succeeds(t *testing.T) {
	ctx := context.Background()

	// Isolate global
	prev := GlobalReplicationDetails
	globalReplicationDetailsLocker.Lock()
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	a := t.TempDir()
	p := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}

	// Precondition for reinstate
	rt.FailedToReplicate = true

	// Create one commit log with no stores (First=nil) and empty registry payloads (4 slices)
	payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{
		First:  nil,
		Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}},
	}
	ba, _ := encoding.DefaultMarshaler.Marshal(payload)
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(dir, sop.NewUUID().String()+logFileExtension)
	if err := os.WriteFile(fn, ba, 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	// Act
	if err := rt.ReinstateFailedDrives(ctx); err != nil {
		t.Fatalf("ReinstateFailedDrives: %v", err)
	}

	// Assert: log file removed and flags cleared by turnOnReplication
	if _, err := os.Stat(fn); !os.IsNotExist(err) {
		t.Fatalf("expected commit log removed; stat err=%v", err)
	}
	if GlobalReplicationDetails == nil || GlobalReplicationDetails.FailedToReplicate || GlobalReplicationDetails.LogCommitChanges {
		t.Fatalf("expected flags cleared after reinstatement")
	}
}

// Adds coverage for ReinstateFailedDrives no-logs path and startLoggingCommitChanges error branch.
func Test_Reinstate_MoreCoverage(t *testing.T) {
	ctx := context.Background()
	pushPop := func() func() {
		prev := GlobalReplicationDetails
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = nil
		globalReplicationDetailsLocker.Unlock()
		return func() {
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = prev
			globalReplicationDetailsLocker.Unlock()
		}
	}

	t.Run("Reinstate_NoLogs_JustToggles", func(t *testing.T) {
		l2 := mocks.NewMockClient()
		defer pushPop()()
		active := t.TempDir()
		passive := t.TempDir()
		rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		rt.FailedToReplicate = true
		if err := rt.ReinstateFailedDrives(ctx); err != nil {
			t.Fatalf("reinstate no logs: %v", err)
		}
		if rt.FailedToReplicate {
			t.Fatalf("expected cleared failure after reinstatement")
		}
	})

	t.Run("StartLoggingCommitChanges_WriteStatusError_Branch", func(t *testing.T) {
		l2 := mocks.NewMockClient()
		defer pushPop()()
		active := t.TempDir()
		passive := t.TempDir()
		rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		// Turn on failed state to allow Reinstate path to call startLogging...
		rt.FailedToReplicate = true
		// Create a directory where the status file should be written to trigger error in startLoggingCommitChanges
		os.MkdirAll(rt.formatActiveFolderEntity(replicationStatusFilename), 0o755)
		// Now call Reinstate and expect it to bubble the error from startLoggingCommitChanges
		if err := rt.ReinstateFailedDrives(ctx); err == nil {
			t.Fatalf("expected startLoggingCommitChanges error")
		}
	})

	t.Run("TurnOnReplication_WriteStatusError", func(t *testing.T) {
		l2 := mocks.NewMockClient()
		defer pushPop()()
		active := t.TempDir()
		passive := t.TempDir()
		rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		os.MkdirAll(rt.formatActiveFolderEntity(replicationStatusFilename), 0o755)
		if err := rt.turnOnReplication(ctx); err == nil {
			t.Fatalf("expected writeReplicationStatus error")
		}
		// cleanup for sanity
		os.RemoveAll(filepath.Join(active, replicationStatusFilename))
	})
}

// Forces ReinstateFailedDrives to fail in startLoggingCommitChanges by making the
// active repl status filename collide with a directory.
func Test_ReinstateFailedDrives_StartLogging_WriteError(t *testing.T) {
	ctx := context.Background()

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

	a := t.TempDir()
	p := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	rt.FailedToReplicate = true

	// Create a directory at the active replstatus path so write fails.
	actStat := rt.formatActiveFolderEntity(replicationStatusFilename)
	if err := os.MkdirAll(actStat, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected error bubbling from startLoggingCommitChanges writeReplicationStatus")
	}
}

// Forces ReinstateFailedDrives to fail during fastForward due to invalid commit log JSON.
func Test_ReinstateFailedDrives_FastForward_UnmarshalError(t *testing.T) {
	ctx := context.Background()

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

	a := t.TempDir()
	p := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{a, p}, true, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	rt.FailedToReplicate = true

	// Seed a bad commit log in active commitlogs folder.
	dir := rt.formatActiveFolderEntity(commitChangesLogFolder)
	if err := NewFileIO().MkdirAll(ctx, dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fn := filepath.Join(dir, "bad"+logFileExtension)
	if err := os.WriteFile(fn, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	if err := rt.ReinstateFailedDrives(ctx); err == nil {
		t.Fatalf("expected error from fastForward unmarshal error")
	}
}
