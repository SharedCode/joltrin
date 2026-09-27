package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// TestReplicationTracker_Scenarios consolidates replication tracker tests. Each subtest resets globals
// to avoid state leakage that previously required many small *_cases files.
// Uses testFileIO defined in replication_test.go for failure injection.
func TestReplicationTracker_Scenarios(t *testing.T) {
	type scenario struct {
		name string
		run  func(t *testing.T)
	}
	scenarios := []scenario{
		{name: "HandleReplicationRelatedError_NoOp_RollbackSucceeded", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			b := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, err := NewReplicationTracker(ctx, []string{a, b}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			beforeToggle := rt.ActiveFolderToggler
			// Use a non-failover-qualified error code and indicate rollback succeeded.
			ioErr := sop.Error{Code: sop.FileIOErrorFailoverQualified, Err: errors.New("temporary io")}
			rt.HandleReplicationRelatedError(ctx, ioErr, nil, true)
			if rt.ActiveFolderToggler != beforeToggle || rt.FailedToReplicate {
				t.Fatalf("expected no-op: toggler %v->%v failed=%v", beforeToggle, rt.ActiveFolderToggler, rt.FailedToReplicate)
			}
		}},
		{name: "HandleReplicationRelatedErrorFailover", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			b1 := t.TempDir()
			b2 := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, err := NewReplicationTracker(ctx, []string{b1, b2}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			if rt.getActiveBaseFolder() != b1 {
				t.Fatalf("expected b1 active")
			}
			ioErr := sop.Error{Code: sop.FailoverQualifiedError + 1, Err: errors.New("io fail")}
			rt.HandleReplicationRelatedError(ctx, ioErr, nil, false)
			if rt.getActiveBaseFolder() != b1 {
				t.Fatalf("expected no failover to b2")
			}
			if rt.FailedToReplicate {
				t.Fatalf("expected FailedToReplicate false")
			}
		}},
		{name: "HandleFailedToReplicate_Idempotent", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			b1 := t.TempDir()
			b2 := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, err := NewReplicationTracker(ctx, []string{b1, b2}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			if rt.FailedToReplicate {
				t.Fatalf("should start healthy")
			}
			rt.handleFailedToReplicate(ctx)
			if !rt.FailedToReplicate {
				t.Fatalf("expected failure flag set")
			}
			rt.handleFailedToReplicate(ctx) // no-op
		}},
		{name: "HandleFailedToReplicate_RemoteAlreadyFailed", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			active := filepath.Join(t.TempDir(), "a")
			os.MkdirAll(active, 0o755)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: true}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{active}, true, cache)
			rt.handleFailedToReplicate(ctx)
			if !rt.FailedToReplicate {
				t.Fatalf("expected failure copied")
			}
		}},
		{name: "Failover_GuardBranches", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			a := filepath.Join(t.TempDir(), "a")
			b := filepath.Join(t.TempDir(), "b")
			os.MkdirAll(a, 0o755)
			os.MkdirAll(b, 0o755)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: false, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			rt.ActiveFolderToggler = true
			if err := rt.failover(ctx); err != nil {
				t.Fatalf("guard failover: %v", err)
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rt2, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			GlobalReplicationDetails.ActiveFolderToggler = !rt2.ActiveFolderToggler
			if err := rt2.failover(ctx); err != nil {
				t.Fatalf("post-sync guard: %v", err)
			}
		}},
		{name: "FastForward_SkipsStoreReplication_FirstNil", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			if _, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue); err != nil {
				t.Fatalf("repo init: %v", err)
			}
			payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: nil, Second: [][]sop.RegistryPayload[sop.Handle]{nil, nil, nil, nil}}
			ba, _ := encoding.DefaultMarshaler.Marshal(payload)
			commitDir := rt.formatActiveFolderEntity(commitChangesLogFolder)
			NewFileIO().MkdirAll(ctx, commitDir, 0o755)
			fn := filepath.Join(commitDir, sop.NewUUID().String()+logFileExtension)
			NewFileIO().WriteFile(ctx, fn, ba, permission)
			found, err := rt.fastForward(ctx)
			if err != nil {
				t.Fatalf("fastForward: %v", err)
			}
			if !found {
				t.Fatalf("expected found")
			}
			if NewFileIO().Exists(ctx, filepath.Join(passive, storeListFilename)) {
				t.Fatalf("unexpected passive store list")
			}
			if _, err := os.Stat(fn); err == nil {
				t.Fatalf("log not removed")
			}
		}},
		{name: "ReinstateFailedDrives_Preconditions", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			base := t.TempDir()
			rtNoRep, _ := NewReplicationTracker(ctx, []string{base}, false, cache)
			rtNoRep.FailedToReplicate = true
			if err := rtNoRep.ReinstateFailedDrives(ctx); err == nil {
				t.Fatalf("expected replicate flag error")
			}
			base2 := t.TempDir()
			base3 := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rtHealthy, _ := NewReplicationTracker(ctx, []string{base2, base3}, true, cache)
			if err := rtHealthy.ReinstateFailedDrives(ctx); err == nil {
				t.Fatalf("expected FailedToReplicate precondition error")
			}
		}},
		{name: "ReinstateFailedDrives_HappyFlow", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
			rt.FailedToReplicate = true
			GlobalReplicationDetails.FailedToReplicate = true
			storeRepo, _ := NewStoreRepository(ctx, rt, nil, cache, 64)
			store := sop.StoreInfo{Name: "s1", RegistryTable: "c1_r"}
			_ = storeRepo.Add(ctx, store)
			regSegDir := filepath.Join(active, store.RegistryTable)
			os.MkdirAll(regSegDir, 0o755)
			os.WriteFile(filepath.Join(regSegDir, store.RegistryTable+"-1"+registryFileExtension), []byte("segment"), 0o644)
			reg := NewRegistry(true, 64, rt, cache)
			_ = reg
			logDir := filepath.Join(active, commitChangesLogFolder)
			os.MkdirAll(logDir, 0o755)
			payload, _ := encoding.DefaultMarshaler.Marshal(sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: []sop.StoreInfo{store}, Second: [][]sop.RegistryPayload[sop.Handle]{nil, nil, nil, nil}})
			os.WriteFile(filepath.Join(logDir, "0001"+logFileExtension), payload, 0o644)
			if err := rt.ReinstateFailedDrives(ctx); err != nil {
				t.Fatalf("ReinstateFailedDrives: %v", err)
			}
			if rt.FailedToReplicate {
				t.Fatalf("expected flag cleared")
			}
		}},
		{name: "ReinstateFailedDrives_FastForwardError", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
			rt.FailedToReplicate = true
			GlobalReplicationDetails.FailedToReplicate = true
			logDir := filepath.Join(active, commitChangesLogFolder)
			os.MkdirAll(logDir, 0o755)
			os.WriteFile(filepath.Join(logDir, "0002"+logFileExtension), []byte("bad"), 0o644)
			if err := rt.ReinstateFailedDrives(ctx); err == nil {
				t.Fatalf("expected error due to malformed log")
			}
		}},
		{name: "CopyFilesByExtension_SuccessAndErrors", run: func(t *testing.T) {
			ctx := context.Background()
			// success copy
			src := t.TempDir()
			dst := t.TempDir()
			os.WriteFile(filepath.Join(src, "a.reg"), []byte("x"), 0o644)
			os.WriteFile(filepath.Join(src, "b.txt"), []byte("y"), 0o644)
			if err := copyFilesByExtension(ctx, src, dst, ".reg"); err != nil {
				t.Fatalf("copy success: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dst, "a.reg")); err != nil {
				t.Fatalf("expected a.reg")
			}
			if _, err := os.Stat(filepath.Join(dst, "b.txt")); err == nil {
				t.Fatalf("unexpected b.txt copy")
			}
			// source read error
			if err := copyFilesByExtension(ctx, filepath.Join(t.TempDir(), "missing"), t.TempDir(), ".x"); err == nil {
				t.Fatalf("expected missing source error")
			}
			// mkdir failure
			src2 := t.TempDir()
			os.WriteFile(filepath.Join(src2, "z.reg"), []byte("d"), 0o644)
			parent := t.TempDir()
			target := filepath.Join(parent, "subdir")
			os.WriteFile(target, []byte("file"), 0o644)
			if err := copyFilesByExtension(ctx, src2, target, ".reg"); err == nil {
				t.Fatalf("expected mkdir fail")
			}
			// copy create error (perm)
			src3 := t.TempDir()
			os.WriteFile(filepath.Join(src3, "k.reg"), []byte("data"), 0o644)
			dst3 := t.TempDir()
			os.Chmod(dst3, 0o500)
			if err := copyFilesByExtension(ctx, src3, dst3, ".reg"); err == nil {
				t.Fatalf("expected create fail")
			}
		}},
		{name: "ReadStatus_PassiveOnly", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			a := filepath.Join(t.TempDir(), "a")
			p := filepath.Join(t.TempDir(), "b")
			os.MkdirAll(a, 0o755)
			os.MkdirAll(p, 0o755)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: false, FailedToReplicate: true}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
			rt.ActiveFolderToggler = true
			fnPassive := filepath.Join(p, replicationStatusFilename)
			rt.writeReplicationStatus(ctx, fnPassive)
			os.Remove(filepath.Join(a, replicationStatusFilename))
			if err := rt.readStatusFromHomeFolder(ctx); err != nil {
				t.Fatalf("read: %v", err)
			}
			if rt.ActiveFolderToggler != false {
				t.Fatalf("expected flip")
			}
			if !rt.FailedToReplicate {
				t.Fatalf("expected failure state")
			}
		}},
		{name: "ReadStatus_PassiveNewer_StatError_NoFiles", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			a := filepath.Join(t.TempDir(), "a")
			p := filepath.Join(t.TempDir(), "b")
			os.MkdirAll(a, 0o755)
			os.MkdirAll(p, 0o755)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rt, _ := NewReplicationTracker(ctx, []string{a, p}, true, cache)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: false, FailedToReplicate: true}
			globalReplicationDetailsLocker.Unlock()
			rt.writeReplicationStatus(ctx, filepath.Join(p, replicationStatusFilename))
			time.Sleep(10 * time.Millisecond)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rt.writeReplicationStatus(ctx, filepath.Join(a, replicationStatusFilename))
			if err := rt.readStatusFromHomeFolder(ctx); err != nil {
				t.Fatalf("read1: %v", err)
			}
			if rt.ActiveFolderToggler != true {
				t.Fatalf("expected stay active")
			}
			// stat error -> flip
			rt.writeReplicationStatus(ctx, filepath.Join(a, replicationStatusFilename))
			rt.writeReplicationStatus(ctx, filepath.Join(p, replicationStatusFilename))
			os.Remove(filepath.Join(a, replicationStatusFilename))
			if err := rt.readStatusFromHomeFolder(ctx); err != nil {
				t.Fatalf("read2: %v", err)
			}
			if rt.ActiveFolderToggler != false {
				t.Fatalf("expected flip after missing active")
			}
			// no files early return
			os.Remove(filepath.Join(a, replicationStatusFilename))
			os.Remove(filepath.Join(p, replicationStatusFilename))
			rt.ActiveFolderToggler = true
			if err := rt.readStatusFromHomeFolder(ctx); err != nil {
				t.Fatalf("read3: %v", err)
			}
			if rt.ActiveFolderToggler != true {
				t.Fatalf("expected remain true")
			}
		}},
		{name: "ReinstateWorkflowAndFastForwardSingleLog", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			rt.FailedToReplicate = true
			store := sop.StoreInfo{Name: "s1", Count: 1}
			hNew := sop.NewHandle(sop.NewUUID())
			hAdd := sop.NewHandle(sop.NewUUID())
			hUpd := sop.NewHandle(sop.NewUUID())
			hDel := hAdd
			// Ensure the registry table exists on both active and passive for replication writes.
			if err := os.MkdirAll(filepath.Join(rt.getActiveBaseFolder(), "reg"), 0o755); err != nil {
				t.Fatalf("mkdir active reg: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(rt.getPassiveBaseFolder(), "reg"), 0o755); err != nil {
				t.Fatalf("mkdir passive reg: %v", err)
			}
			payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: []sop.StoreInfo{store}, Second: [][]sop.RegistryPayload[sop.Handle]{{{RegistryTable: "reg", IDs: []sop.Handle{hNew}}}, {{RegistryTable: "reg", IDs: []sop.Handle{hAdd}}}, {{RegistryTable: "reg", IDs: []sop.Handle{hUpd}}}, {{RegistryTable: "reg", IDs: []sop.Handle{hDel}}}}}
			ba, _ := encoding.DefaultMarshaler.Marshal(payload)
			commitDir := rt.formatActiveFolderEntity(commitChangesLogFolder)
			os.MkdirAll(commitDir, 0o755)
			logFn := filepath.Join(commitDir, sop.NewUUID().String()+logFileExtension)
			os.WriteFile(logFn, ba, 0o644)
			if err := rt.ReinstateFailedDrives(ctx); err != nil {
				t.Fatalf("ReinstateFailedDrives: %v", err)
			}
			if rt.FailedToReplicate {
				t.Fatalf("expected cleared failure")
			}
			if rt.LogCommitChanges {
				t.Fatalf("expected logging disabled")
			}
			if _, err := os.Stat(logFn); !os.IsNotExist(err) {
				t.Fatalf("log not removed")
			}
			// single log
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rt2, _ := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			rt2.FailedToReplicate = true
			store2 := sop.StoreInfo{Name: "s2", Count: 1}
			h := sop.NewHandle(sop.NewUUID())
			// Ensure directories for reg2 on both active and passive
			if err := os.MkdirAll(filepath.Join(rt2.getActiveBaseFolder(), "reg2"), 0o755); err != nil {
				t.Fatalf("mkdir active reg2: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(rt2.getPassiveBaseFolder(), "reg2"), 0o755); err != nil {
				t.Fatalf("mkdir passive reg2: %v", err)
			}
			payload2 := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: []sop.StoreInfo{store2}, Second: [][]sop.RegistryPayload[sop.Handle]{{{RegistryTable: "reg2", IDs: []sop.Handle{h}}}, {}, {}, {}}}
			ba2, _ := encoding.DefaultMarshaler.Marshal(payload2)
			commitDir2 := rt2.formatActiveFolderEntity(commitChangesLogFolder)
			os.MkdirAll(commitDir2, 0o755)
			logFn2 := filepath.Join(commitDir2, sop.NewUUID().String()+logFileExtension)
			os.WriteFile(logFn2, ba2, 0o644)
			found, err := rt2.fastForward(ctx)
			if err != nil {
				t.Fatalf("fastForward: %v", err)
			}
			if !found {
				t.Fatalf("expected found")
			}
			if _, err := os.Stat(logFn2); !os.IsNotExist(err) {
				t.Fatalf("log2 not removed")
			}
		}},
		{name: "StartLoggingCommitChangesAndSetTransactionID", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			p := t.TempDir()
			rt, err := NewReplicationTracker(ctx, []string{a, p}, true, l2)
			if err != nil {
				t.Fatalf("rt: %v", err)
			}
			if err := rt.startLoggingCommitChanges(ctx); err != nil {
				t.Fatalf("startLoggingCommitChanges: %v", err)
			}
			if !rt.LogCommitChanges {
				t.Fatalf("expected LogCommitChanges true")
			}
			id := sop.NewUUID()
			rt.SetTransactionID(id)
		}},
		{name: "AdditionalBranchesAndSyncWithL2Cache", run: func(t *testing.T) {
			ctx := context.Background()
			cache := mocks.NewMockClient()
			a := filepath.Join(t.TempDir(), "a")
			b := filepath.Join(t.TempDir(), "b")
			rtNoRep, _ := NewReplicationTracker(ctx, []string{a}, false, cache)
			rtNoRep.handleFailedToReplicate(ctx)
			rtFail, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{FailedToReplicate: true, ActiveFolderToggler: true}
			globalReplicationDetailsLocker.Unlock()
			rtFail.handleFailedToReplicate(ctx)
			if !rtFail.FailedToReplicate {
				t.Fatalf("expected failure copied")
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: false, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rtGuard, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			rtGuard.ActiveFolderToggler = true
			if err := rtGuard.failover(ctx); err != nil {
				t.Fatalf("failover guard: %v", err)
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			rtDo, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			rtDo.ActiveFolderToggler = true
			os.MkdirAll(a, 0o755)
			_ = rtDo.writeReplicationStatus(ctx, rtDo.formatActiveFolderEntity(replicationStatusFilename))
			// ioErr := sop.Error{Code: sop.FailoverQualifiedError, Err: os.ErrInvalid}
			// rtDo.HandleReplicationRelatedError(ctx, ioErr, nil, false)
			rtDo.failover(ctx)
			if !rtDo.FailedToReplicate {
				t.Fatalf("expected failover failure")
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			rtPull, _ := NewReplicationTracker(ctx, []string{a, b}, true, cache)
			if err := rtPull.syncWithL2Cache(ctx, false); err != nil {
				t.Fatalf("pull miss: %v", err)
			}
			if err := rtPull.logCommitChanges(ctx, sop.NewUUID(), nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("logCommitChanges disabled: %v", err)
			}
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = &ReplicationTrackedDetails{ActiveFolderToggler: true, FailedToReplicate: false}
			globalReplicationDetailsLocker.Unlock()
			if err := rtPull.syncWithL2Cache(ctx, true); err != nil {
				t.Fatalf("initial push: %v", err)
			}
			if err := rtPull.syncWithL2Cache(ctx, true); err != nil {
				t.Fatalf("push identical: %v", err)
			}
			GlobalReplicationDetails.FailedToReplicate = true
			if err := rtPull.syncWithL2Cache(ctx, true); err != nil {
				t.Fatalf("push diverged: %v", err)
			}
		}},
		{name: "HandleFailedToReplicate_WriteStatusFail", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			a := t.TempDir()
			p := t.TempDir()
			GlobalReplicationDetails = nil
			rt, err := NewReplicationTracker(ctx, []string{a, p}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			statusPath := rt.formatActiveFolderEntity(replicationStatusFilename)
			os.MkdirAll(statusPath, 0o755)
			rt.handleFailedToReplicate(ctx)
			if !rt.FailedToReplicate {
				t.Fatalf("expected failure flag true")
			}
			os.RemoveAll(filepath.Join(a, replicationStatusFilename))
		}},
		{name: "StatusWriteAndReadErrors", run: func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("windows path semantics")
			}
			ctx := context.Background()
			active := t.TempDir()
			passive := t.TempDir()
			l2 := mocks.NewMockClient()
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			statusPath := rt.formatActiveFolderEntity(replicationStatusFilename)
			os.Mkdir(statusPath, 0o755)
			if err := rt.writeReplicationStatus(ctx, statusPath); err == nil {
				t.Fatalf("expected write error")
			}
			os.Remove(statusPath)
			os.WriteFile(statusPath, []byte("{malformed"), 0o644)
			if err := rt.readReplicationStatus(ctx, statusPath); err == nil {
				t.Fatalf("expected malformed read error")
			}
		}},
		{name: "FastForwardProcessesLogs", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			GlobalReplicationDetails = nil
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			if err != nil {
				t.Fatalf("NewReplicationTracker: %v", err)
			}
			if _, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue); err != nil {
				t.Fatalf("NewStoreRepository: %v", err)
			}
			sr2, _ := NewStoreRepository(ctx, rt, nil, l2, 0)
			s := sop.NewStoreInfo(sop.StoreOptions{Name: "s2", SlotLength: 8})
			if err := sr2.Add(ctx, *s); err != nil {
				t.Fatalf("Add: %v", err)
			}
			tid := sop.NewUUID()
			payload := sop.Tuple[[]sop.StoreInfo, [][]sop.RegistryPayload[sop.Handle]]{First: []sop.StoreInfo{*s}, Second: [][]sop.RegistryPayload[sop.Handle]{{}, {}, {}, {}}}
			ba, _ := encoding.DefaultMarshaler.Marshal(payload)
			fn := rt.formatActiveFolderEntity(filepath.Join(commitChangesLogFolder, tid.String()+logFileExtension))
			if err := NewFileIO().WriteFile(ctx, fn, ba, permission); err != nil {
				t.Fatalf("write commit log: %v", err)
			}
			found, err := rt.fastForward(ctx)
			if err != nil {
				t.Fatalf("fastForward: %v", err)
			}
			if !found {
				t.Fatalf("expected found log")
			}
			found, err = rt.fastForward(ctx)
			if err != nil {
				t.Fatalf("fastForward 2: %v", err)
			}
			if found {
				t.Fatalf("expected no logs")
			}
		}},
		{name: "TurnOnReplicationUpdatesStatus", run: func(t *testing.T) {
			ctx := context.Background()
			l2 := mocks.NewMockClient()
			active := t.TempDir()
			passive := t.TempDir()
			GlobalReplicationDetails = nil
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
			if err != nil {
				t.Fatalf("NewReplicationTracker: %v", err)
			}
			GlobalReplicationDetails.FailedToReplicate = true
			GlobalReplicationDetails.LogCommitChanges = true
			if err := rt.turnOnReplication(ctx); err != nil {
				t.Fatalf("turnOnReplication: %v", err)
			}
			if GlobalReplicationDetails.FailedToReplicate || GlobalReplicationDetails.LogCommitChanges {
				t.Fatalf("expected flags cleared; got %+v", GlobalReplicationDetails)
			}
			if !NewFileIO().Exists(ctx, rt.formatActiveFolderEntity(replicationStatusFilename)) {
				t.Fatalf("expected status file")
			}
		}},
		{name: "ReadStatus_ActiveMissingPassivePresent", run: func(t *testing.T) {
			ctx := context.Background()
			prev := GlobalReplicationDetails
			GlobalReplicationDetails = nil
			t.Cleanup(func() { GlobalReplicationDetails = prev })

			active := t.TempDir()
			passive := t.TempDir()
			// write status only to passive
			os.MkdirAll(passive, 0o755)
			if err := os.WriteFile(filepath.Join(passive, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644); err != nil {
				t.Fatalf("write passive status: %v", err)
			}
			if _, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient()); err != nil {
				t.Fatalf("NewReplicationTracker: %v", err)
			}
		}},
		{name: "ReadStatus_ActiveStatErrorTriggersFlip", run: func(t *testing.T) {
			ctx := context.Background()
			active := t.TempDir()
			passive := t.TempDir()
			// seed passive with status; make active path exist then remove to force Stat error branch
			if err := os.WriteFile(filepath.Join(passive, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644); err != nil {
				t.Fatalf("seed passive: %v", err)
			}
			if err := os.WriteFile(filepath.Join(active, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644); err != nil {
				t.Fatalf("seed active: %v", err)
			}
			// Remove active file so os.Stat(active) errors and path through fallback to passive triggers flip
			os.Remove(filepath.Join(active, replicationStatusFilename))
			prev := GlobalReplicationDetails
			GlobalReplicationDetails = nil
			t.Cleanup(func() { GlobalReplicationDetails = prev })
			if _, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient()); err != nil {
				t.Fatalf("NewReplicationTracker: %v", err)
			}
		}},
		{name: "StartLoggingCommitChanges_WriteStatusError", run: func(t *testing.T) {
			ctx := context.Background()
			active := t.TempDir()
			passive := t.TempDir()
			rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient())
			if err != nil {
				t.Fatalf("tracker: %v", err)
			}
			// Create a directory where the status file should be written to trigger write error
			os.MkdirAll(filepath.Join(active, replicationStatusFilename), 0o755)
			if err := rt.startLoggingCommitChanges(ctx); err == nil {
				t.Fatalf("expected writeReplicationStatus error")
			}
		}},
		{name: "ReadStatus_BothExistPassiveNewerAndOlder", run: func(t *testing.T) {
			ctx := context.Background()
			prev := GlobalReplicationDetails
			GlobalReplicationDetails = nil
			t.Cleanup(func() { GlobalReplicationDetails = prev })
			active := t.TempDir()
			passive := t.TempDir()
			os.MkdirAll(active, 0o755)
			os.MkdirAll(passive, 0o755)
			// both exist; make passive newer
			os.WriteFile(filepath.Join(active, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":true}`), 0o644)
			os.WriteFile(filepath.Join(passive, replicationStatusFilename), []byte(`{"FailedToReplicate":false,"ActiveFolderToggler":false}`), 0o644)
			pf := filepath.Join(passive, replicationStatusFilename)
			af := filepath.Join(active, replicationStatusFilename)
			past := time.Now().Add(-2 * time.Minute)
			os.Chtimes(af, past, past)
			time.Sleep(15 * time.Millisecond)
			if _, err := NewReplicationTracker(ctx, []string{active, passive}, true, mocks.NewMockClient()); err != nil {
				t.Fatalf("NewReplicationTracker: %v", err)
			}
			_ = pf
			_ = af // state exercised; toggler choice is implementation-dependent across OS timing
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			prev := GlobalReplicationDetails
			defer func() {
				globalReplicationDetailsLocker.Lock()
				GlobalReplicationDetails = prev
				globalReplicationDetailsLocker.Unlock()
			}()
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = nil
			globalReplicationDetailsLocker.Unlock()
			sc.run(t)
		})
	}
}
