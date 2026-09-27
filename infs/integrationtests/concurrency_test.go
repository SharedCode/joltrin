//go:build crash
// +build crash

package integrationtests

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/fs"
)

func Test_ConcurrentTransactions(t *testing.T) {
	// Switch to InMemory cache for this test.
	sop.SetCacheFactory(sop.InMemory)

	ctx := context.Background()

	// 1. Setup dependencies.
	testPath := filepath.Join(dataPath, "concurrent_test")
	_ = os.RemoveAll(testPath)
	_ = os.MkdirAll(testPath, 0755)
	defer os.RemoveAll(testPath)

	// Set AgeLimit to 0 to allow immediate cleanup.
	originalAgeLimit := fs.AgeLimit
	fs.AgeLimit = 0
	defer func() { fs.AgeLimit = originalAgeLimit }()

	cache := sop.NewCacheClient()
	storesFolder := filepath.Join(testPath, "store")
	_ = os.MkdirAll(storesFolder, 0755)

	fio := fs.NewFileIO()
	replicationTracker, err := fs.NewReplicationTracker(ctx, []string{storesFolder}, false, cache)
	if err != nil {
		t.Fatalf("Failed to create replication tracker: %v", err)
	}

	mbsf := fs.NewManageStoreFolder(fio)
	sr, err := fs.NewStoreRepository(ctx, replicationTracker, mbsf, cache, 10)
	if err != nil {
		t.Fatalf("Failed to create store repository: %v", err)
	}

	// Helper to create a transaction with its own logger.
	createTx := func(mode sop.TransactionMode) (sop.Transaction, error) {
		reg := fs.NewRegistry(mode == sop.ForWriting, 10, replicationTracker, cache)
		blobStore := fs.NewBlobStore("", fs.DefaultToFilePath, nil)
		// IMPORTANT: Create a NEW TransactionLog for each transaction instance.
		transLog := fs.NewTransactionLog(cache, replicationTracker)

		twoPC, err := common.NewTwoPhaseCommitTransaction(mode, time.Minute*15, true, blobStore, sr, reg, cache, transLog)
		if err != nil {
			return nil, err
		}

		twoPC.HandleReplicationRelatedError = replicationTracker.HandleReplicationRelatedError
		replicationTracker.SetTransactionID(twoPC.GetID())

		return sop.NewTransaction(mode, twoPC, true)
	}

	// 2. Run Concurrent Transactions.
	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Track results
	results := make([]string, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Random sleep to stagger starts
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)

			tx, err := createTx(sop.ForWriting)
			if err != nil {
				t.Errorf("Tx %d creation failed: %v", id, err)
				return
			}
			if err := tx.Begin(ctx); err != nil {
				t.Errorf("Tx %d begin failed: %v", id, err)
				return
			}

			storeName := fmt.Sprintf("store_%d", id)
			store, err := infs.NewBtreeWithReplication[string, string](ctx, sop.StoreOptions{
				Name:       storeName,
				SlotLength: 10,
			}, tx, cmp.Compare[string])
			if err != nil {
				t.Errorf("Tx %d NewBtree failed: %v", id, err)
				return
			}

			if ok, err := store.Add(ctx, "key", "value"); !ok || err != nil {
				t.Errorf("Tx %d Add failed: %v", id, err)
				return
			}

			// Decide: Commit (Even IDs) or Rollback (Odd IDs)
			if id%2 == 0 {
				if err := tx.Commit(ctx); err != nil {
					t.Errorf("Tx %d Commit failed: %v", id, err)
					return
				}
				results[id] = "committed"
			} else {
				if err := tx.Rollback(ctx); err != nil {
					t.Errorf("Tx %d Rollback failed: %v", id, err)
					return
				}
				results[id] = "rolled_back"
			}
		}(i)
	}

	wg.Wait()

	// 3. Verify Results.
	// Check all stores.
	allStores, err := sr.GetAll(ctx)
	if err != nil {
		t.Fatalf("Failed to get all stores: %v", err)
	}

	// Map for quick lookup
	existingStores := make(map[string]bool)
	for _, s := range allStores {
		existingStores[s] = true
	}

	for i := 0; i < numGoroutines; i++ {
		// Create a fresh transaction for each verification to avoid side effects of OpenBtree failure.
		verifyTx, err := createTx(sop.ForReading)
		if err != nil {
			t.Fatalf("VerifyTx creation failed: %v", err)
		}
		if err := verifyTx.Begin(ctx); err != nil {
			t.Fatalf("VerifyTx begin failed: %v", err)
		}

		storeName := fmt.Sprintf("store_%d", i)
		exists := existingStores[storeName]

		if i%2 == 0 {
			// Should exist
			if !exists {
				t.Errorf("Store %s should exist (committed), but was not found in repository.", storeName)
			}
			// Double check by trying to open it
			_, err := infs.OpenBtreeWithReplication[string, string](ctx, storeName, verifyTx, cmp.Compare[string])
			if err != nil {
				t.Errorf("Store %s should be openable, but got error: %v", storeName, err)
			}
		} else {
			// Should NOT exist
			if exists {
				t.Errorf("Store %s should NOT exist (rolled back), but was found in repository.", storeName)
			}
			// Double check that opening it fails
			_, err := infs.OpenBtreeWithReplication[string, string](ctx, storeName, verifyTx, cmp.Compare[string])
			if err == nil {
				t.Errorf("Store %s should fail to open, but succeeded.", storeName)
			}
		}
		verifyTx.Commit(ctx)
	}
}
