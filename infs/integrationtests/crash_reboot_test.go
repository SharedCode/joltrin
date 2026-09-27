//go:build crash
// +build crash

package integrationtests

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/fs"
)

func Test_CrashReboot(t *testing.T) {
	// Switch to InMemory cache for this test to avoid Redis dependency.
	sop.SetCacheFactory(sop.InMemory)

	ctx := context.Background()

	// 1. Setup dependencies.
	// Use a separate data path for this test to avoid conflicts.
	testPath := filepath.Join(dataPath, "crash_reboot_test")
	_ = os.RemoveAll(testPath)
	_ = os.MkdirAll(testPath, 0755)
	defer os.RemoveAll(testPath)

	// Set AgeLimit to 0 to allow immediate cleanup.
	// Save original value to restore later.
	originalAgeLimit := fs.AgeLimit
	fs.AgeLimit = 0
	defer func() { fs.AgeLimit = originalAgeLimit }()

	cache := sop.NewCacheClient()

	// Stores folder.
	storesFolder := filepath.Join(testPath, "store")
	_ = os.MkdirAll(storesFolder, 0755)

	fio := fs.NewFileIO()
	replicationTracker, err := fs.NewReplicationTracker(ctx, []string{storesFolder}, false, cache)
	if err != nil {
		t.Fatalf("Failed to create replication tracker: %v", err)
	}

	mbsf := fs.NewManageStoreFolder(fio)
	sr, err := fs.NewStoreRepository(ctx, replicationTracker, mbsf, cache, 10) // 10 is arbitrary hash mod
	if err != nil {
		t.Fatalf("Failed to create store repository: %v", err)
	}

	// Use REAL TransactionLog.
	realLog := fs.NewTransactionLog(cache, replicationTracker)

	// Determine where the logs are stored.
	// fs.TransactionLog uses "translogs" folder relative to the store base.
	// We need to find the active folder.
	// Since we passed [storesFolder], and it's the only one, it should be there.
	transLogsFolder := filepath.Join(storesFolder, "translogs")

	// Helper to create a transaction.
	createTx := func(mode sop.TransactionMode) (sop.Transaction, error) {
		reg := fs.NewRegistry(mode == sop.ForWriting, 10, replicationTracker, cache)
		blobStore := fs.NewBlobStore("", fs.DefaultToFilePath, nil)

		twoPC, err := common.NewTwoPhaseCommitTransaction(mode, time.Minute*15, true, blobStore, sr, reg, cache, realLog)
		if err != nil {
			return nil, err
		}

		twoPC.HandleReplicationRelatedError = replicationTracker.HandleReplicationRelatedError
		replicationTracker.SetTransactionID(twoPC.GetID())

		return sop.NewTransaction(mode, twoPC, true)
	}

	// 2. Start Transaction 1 (The "Crasher").
	t1, err := createTx(sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to create T1: %v", err)
	}
	if err := t1.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin T1: %v", err)
	}

	storeName := "crash_store"
	s1, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
		Name:       storeName,
		SlotLength: 10,
	}, t1, cmp.Compare[string])
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if ok, err := s1.Add(ctx, "key1", "value1"); !ok || err != nil {
		t.Fatalf("Failed to add item: %v", err)
	}

	// 3. Commit Phase 1.
	// We need to access the underlying TwoPhaseCommitTransaction to call Phase1Commit.
	twoPCT1 := t1.GetPhasedTransaction()
	if err := twoPCT1.Phase1Commit(ctx); err != nil {
		t.Fatalf("Phase 1 commit failed: %v", err)
	}

	t1ID := t1.GetID()
	t.Logf("T1 (Crasher) ID: %s", t1ID)

	// 4. Verify Log Exists.
	logPath := filepath.Join(transLogsFolder, fmt.Sprintf("%s.log", t1ID))
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Fatalf("Log file should exist after Phase 1: %s", logPath)
	}

	// 5. "Crash" - Do NOT call Phase2Commit.
	// Just let t1 go out of scope (or close it without commit).
	// t1.Close() might do cleanup? No, Close just closes registry files.
	t1.Close()

	// 6. Start Transaction 2 (The "Restarter").
	// This transaction's Phase1Commit should trigger cleanup of T1.
	t2, err := createTx(sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to create T2: %v", err)
	}
	if err := t2.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin T2: %v", err)
	}

	// We need to do something to commit.
	// Open the same store.
	s2, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
		Name:       storeName,
		SlotLength: 10,
	}, t2, cmp.Compare[string])
	if err != nil {
		t.Fatalf("Failed to open store in T2: %v", err)
	}

	// Add another item.
	if ok, err := s2.Add(ctx, "key2", "value2"); !ok || err != nil {
		t.Fatalf("Failed to add item in T2: %v", err)
	}

	// Debug: Check if file exists before T2 commit
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Fatalf("Log file disappeared before T2 commit!")
	}
	entries, _ := os.ReadDir(transLogsFolder)
	t.Logf("Files in translogs before T2 commit: %d", len(entries))
	for _, e := range entries {
		t.Logf(" - %s", e.Name())
	}

	// 7. Commit T2.
	// This calls Phase1Commit, which calls onIdle, which calls processExpiredTransactionLogs.
	// Since fs.AgeLimit is 0, it should pick up T1's log.
	common.ResetOnIdleTimers()
	// T2 might fail because T1 rollback deletes the store T2 is using (since T1 created it).
	// We don't strictly require T2 to succeed, we require T1 to be rolled back.
	_ = t2.Commit(ctx)

	// 8. Verify T1 Log is Gone.
	// Give it some time as onIdle is async.
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(logPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("Log file for T1 should have been deleted: %s", logPath)
	} else {
		t.Logf("T1 Log file successfully deleted.")
	}

	// 9. Verify Data Integrity.
	// "key1" should NOT exist (rolled back).
	// "key2" should NOT exist (T2 failed or store deleted).

	t3, err := createTx(sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to create T3: %v", err)
	}
	if err := t3.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin T3: %v", err)
	}
	// This will create a new empty store if the old one was deleted.
	s3, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
		Name:       storeName,
		SlotLength: 10,
	}, t3, cmp.Compare[string])
	if err != nil {
		t.Fatalf("Failed to open store in T3: %v", err)
	}

	found, err := s3.Find(ctx, "key1", false)
	if err != nil {
		t.Logf("Find key1 failed (expected if store is deleted): %v", err)
	} else if found {
		t.Errorf("key1 should have been rolled back, but was found.")
	}

	found, err = s3.Find(ctx, "key2", false)
	if err != nil {
		t.Logf("Find key2 failed (expected if store is deleted): %v", err)
	} else if found {
		t.Errorf("key2 should not exist (T2 failed).")
	}

	t3.Commit(ctx)

	// 10. Verify System Health (Can we start fresh?).

	// Start T4 (Writer).
	t4, err := createTx(sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to create T4: %v", err)
	}
	if err := t4.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin T4: %v", err)
	}

	// Create/Open the store again.
	s4, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
		Name:       storeName,
		SlotLength: 10,
	}, t4, cmp.Compare[string])
	if err != nil {
		t.Fatalf("Failed to open store in T4: %v", err)
	}

	// Add a new item.
	if ok, err := s4.Add(ctx, "key3", "value3"); !ok || err != nil {
		t.Fatalf("Failed to add item in T4: %v", err)
	}

	// Commit T4.
	if err := t4.Commit(ctx); err != nil {
		t.Fatalf("T4 Commit failed: %v", err)
	}

	// 11. Verify Final State.
	// Start T5 (Reader).
	t5, err := createTx(sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to create T5: %v", err)
	}
	if err := t5.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin T5: %v", err)
	}

	s5, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
		Name:       storeName,
		SlotLength: 10,
	}, t5, cmp.Compare[string])
	if err != nil {
		t.Fatalf("Failed to open store in T5: %v", err)
	}

	// "key3" SHOULD exist.
	found, err = s5.Find(ctx, "key3", false)
	if err != nil {
		t.Fatalf("Error finding key3: %v", err)
	}
	if !found {
		t.Errorf("key3 should exist (T4 committed).")
	}

	// "key1" should NOT exist.
	found, err = s5.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Error finding key1: %v", err)
	}
	if found {
		t.Errorf("key1 should not exist (T1 rolled back).")
	}

	// "key2" should NOT exist.
	found, err = s5.Find(ctx, "key2", false)
	if err != nil {
		t.Fatalf("Error finding key2: %v", err)
	}
	if found {
		t.Errorf("key2 should not exist (T2 failed).")
	}

	t5.Commit(ctx)
}
