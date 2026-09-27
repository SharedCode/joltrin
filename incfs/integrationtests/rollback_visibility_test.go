//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestRollbackVisibilityRaceCondition(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	path := "/tmp/sop_test/rollback_race"
	keyspace := "db1" // Reusing db1 keyspace for convenience

	// Clean up previous runs
	os.RemoveAll(path)

	// 2. Initialize Database
	config := sop.DatabaseOptions{Keyspace: keyspace, StoresFolders: []string{path}}
	storeName := "race_store"

	// 3. Transaction A: Create Store (but don't commit yet)
	t.Log("Trans A: Beginning transaction...")
	tA, err := database.BeginTransaction(ctx, config, sop.ForWriting)
	if err != nil {
		t.Fatalf("Trans A BeginTransaction failed: %v", err)
	}

	t.Log("Trans A: Creating B-Tree...")
	// This immediately creates the tables in Cassandra
	_, err = database.NewBtree[string, string](ctx, config, storeName, tA, nil)
	if err != nil {
		t.Fatalf("Trans A NewBtree failed: %v", err)
	}

	// 4. Transaction B: Open the same Store (Race Condition)
	t.Log("Trans B: Beginning transaction...")
	tB, err := database.BeginTransaction(ctx, config, sop.ForWriting)
	if err != nil {
		t.Fatalf("Trans B BeginTransaction failed: %v", err)
	}

	t.Log("Trans B: Opening B-Tree (should succeed because tables exist)...")
	storeB, err := database.OpenBtree[string, string](ctx, config, storeName, tB, nil)
	if err != nil {
		t.Fatalf("Trans B OpenBtree failed: %v", err)
	}

	// 5. Transaction A: Rollback
	t.Log("Trans A: Rolling back (should remove tables)...")
	if err := tA.Rollback(ctx); err != nil {
		t.Fatalf("Trans A Rollback failed: %v", err)
	}

	// 6. Transaction B: Try to use the store
	t.Log("Trans B: Adding item (in-memory)...")
	// This usually succeeds as it's just memory buffering
	if _, err := storeB.Add(ctx, "key1", "value1"); err != nil {
		t.Logf("Trans B Add failed (unexpected but acceptable): %v", err)
	}

	// 7. Transaction B: Commit (MUST FAIL)
	t.Log("Trans B: Committing (should fail because tables are gone)...")
	err = tB.Commit(ctx)
	if err == nil {
		t.Fatal("Trans B Commit succeeded, but it should have failed because the store was removed by Trans A!")
	}

	t.Logf("Trans B Commit failed as expected: %v", err)
}
