//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestMultiDatabase(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	path1 := "/tmp/sop_test/db1"
	path2 := "/tmp/sop_test/db2"
	// Use unique keyspaces to avoid conflicts with previous runs
	keyspace1 := fmt.Sprintf("db_%s", strings.ReplaceAll(sop.NewUUID().String(), "-", ""))
	keyspace2 := fmt.Sprintf("db_%s", strings.ReplaceAll(sop.NewUUID().String(), "-", ""))

	// Clean up previous runs
	os.RemoveAll(path1)
	os.RemoveAll(path2)

	// 2. Initialize Databases
	config1 := sop.DatabaseOptions{Keyspace: keyspace1, StoresFolders: []string{path1}}
	config2 := sop.DatabaseOptions{Keyspace: keyspace2, StoresFolders: []string{path2}}

	storeName := "shared_store_name"

	// 3. Database 1: Add Data
	t.Logf("Database 1: Adding data to keyspace %s...", keyspace1)
	tx1, err := database.BeginTransaction(ctx, config1, sop.ForWriting)
	if err != nil {
		t.Fatalf("Database 1 BeginTransaction failed: %v", err)
	}

	store1, err := database.NewBtree[string, string](ctx, config1, storeName, tx1, nil)
	if err != nil {
		t.Fatalf("Database 1 NewBtree failed: %v", err)
	}

	if _, err := store1.Add(ctx, "key1", "value_for_db1"); err != nil {
		t.Fatalf("Database 1 Add failed: %v", err)
	}

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("Database 1 Commit failed: %v", err)
	}

	// 4. Database 2: Add Data (Same Store Name, Same Key, Different Value)
	t.Logf("Database 2: Adding data to keyspace %s...", keyspace2)
	tx2, err := database.BeginTransaction(ctx, config2, sop.ForWriting)
	if err != nil {
		t.Fatalf("Database 2 BeginTransaction failed: %v", err)
	}

	store2, err := database.NewBtree[string, string](ctx, config2, storeName, tx2, nil)
	if err != nil {
		// If the store already exists (which it shouldn't in a clean DB2), this will fail.
		// But wait, if db1 and db2 share the same Redis instance (which they do in this test setup),
		// and if the store name is the same, there might be a collision in Redis if the keyspace isn't part of the Redis key.
		// Let's check if Redis keys are namespaced by keyspace.
		// If not, we might need to ensure they are, or use different store names if that's the limitation.
		// However, the goal is to test isolation.
		// If NewBtree fails saying "exists & has different configuration", it implies it found something.
		t.Fatalf("Database 2 NewBtree failed: %v", err)
	}

	if _, err := store2.Add(ctx, "key1", "value_for_db2"); err != nil {
		t.Fatalf("Database 2 Add failed: %v", err)
	}

	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("Database 2 Commit failed: %v", err)
	}

	// 5. Verify Database 1 Data
	t.Log("Database 1: Verifying data...")
	tx1b, err := database.BeginTransaction(ctx, config1, sop.ForReading)
	if err != nil {
		t.Fatalf("Database 1 BeginTransaction (Read) failed: %v", err)
	}
	store1b, err := database.OpenBtree[string, string](ctx, config1, storeName, tx1b, nil)
	if err != nil {
		t.Fatalf("Database 1 OpenBtree failed: %v", err)
	}
	found1, err := store1b.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Database 1 Find failed: %v", err)
	}
	if !found1 {
		t.Fatalf("Database 1: key1 not found")
	}
	val1, err := store1b.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("Database 1 GetCurrentValue failed: %v", err)
	}
	if val1 != "value_for_db1" {
		t.Errorf("Database 1: expected 'value_for_db1', got '%v'", val1)
	}
	tx1b.Commit(ctx)

	// 6. Verify Database 2 Data
	t.Log("Database 2: Verifying data...")
	tx2b, err := database.BeginTransaction(ctx, config2, sop.ForReading)
	if err != nil {
		t.Fatalf("Database 2 BeginTransaction (Read) failed: %v", err)
	}
	store2b, err := database.OpenBtree[string, string](ctx, config2, storeName, tx2b, nil)
	if err != nil {
		t.Fatalf("Database 2 OpenBtree failed: %v", err)
	}
	found2, err := store2b.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Database 2 Find failed: %v", err)
	}
	if !found2 {
		t.Fatalf("Database 2: key1 not found")
	}
	val2, err := store2b.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("Database 2 GetCurrentValue failed: %v", err)
	}
	if val2 != "value_for_db2" {
		t.Errorf("Database 2: expected 'value_for_db2', got '%v'", val2)
	}
	tx2b.Commit(ctx)

	t.Log("Multi-database test passed!")
}
