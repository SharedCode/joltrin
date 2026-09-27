package database_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// Example_standalone demonstrates how to use the Database in Standalone mode.
// This mode uses an in-memory cache and local filesystem storage.
func Example_standalone() {
	// 1. Define storage path.
	storagePath, _ := os.MkdirTemp("", "sop_standalone_example")
	defer os.RemoveAll(storagePath) // Clean up previous run

	// 1. Initialize Database (Standalone or Clustered)
	// Standalone uses in-memory caching; Clustered uses Redis.
	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{storagePath},
		CacheType:     sop.InMemory,
	}) // 3. Start a transaction.
	ctx := context.Background()
	// You can pass options, but defaults are usually sufficient for standalone.
	tx, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		return
	}

	// 4. Create or Open a B-Tree.
	// Using the generic NewBtree helper from Database.
	store, err := database.NewBtree[string, string](ctx, db, "users", tx, nil)
	if err != nil {
		fmt.Printf("Failed to create btree: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 5. Perform operations.
	// Note: The generic store uses 'any' for Key and Value.
	// For strict typing, use infs.NewBtree[K, V] directly with the transaction.
	if _, err := store.Add(ctx, "user1", "Alice"); err != nil {
		fmt.Printf("Failed to add item: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 6. Commit the transaction.
	if err := tx.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit: %v\n", err)
		return
	}

	fmt.Println("Standalone transaction committed successfully.")

	// Verify data
	tx, _ = database.BeginTransaction(ctx, db, sop.ForReading)
	store, _ = database.OpenBtree[string, string](ctx, db, "users", tx, nil)
	found, _ := store.Find(ctx, "user1", false)
	if found {
		val, _ := store.GetCurrentValue(ctx)
		fmt.Printf("Found user1: %v\n", val)
	}
	tx.Commit(ctx)

	// Output:
	// Standalone transaction committed successfully.
	// Found user1: Alice
}

// Example_infs_direct demonstrates how to use the infs package directly.
// This gives you full control over the transaction options and cache.
func Example_infs_direct() {
	// 1. Define storage path.
	storagePath, _ := os.MkdirTemp("", "sop_infs_direct_example")
	defer os.RemoveAll(storagePath)

	// 2. Create Transaction Options.
	// Here we explicitly choose InMemory cache.
	opts := sop.TransactionOptions{
		StoresFolders: []string{storagePath},
		CacheType:     sop.InMemory,
		Mode:          sop.ForWriting,
	}

	// 3. Start a transaction.
	ctx := context.Background()
	tx, err := infs.NewTransaction(ctx, opts)
	if err != nil {
		fmt.Printf("Failed to create transaction: %v\n", err)
		return
	}
	if err := tx.Begin(ctx); err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		return
	}

	// 4. Create a strongly-typed B-Tree.
	// Unlike Database.NewBtree which returns BtreeInterface[any, any],
	// infs.NewBtree allows specific types.
	so := sop.StoreOptions{
		Name:                     "scores",
		SlotLength:               100,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	}
	store, err := infs.NewBtree[string, int](ctx, so, tx, nil)
	if err != nil {
		fmt.Printf("Failed to create btree: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 5. Add data.
	if _, err := store.Add(ctx, "Player1", 100); err != nil {
		fmt.Printf("Failed to add item: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 6. Commit.
	if err := tx.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit: %v\n", err)
		return
	}

	fmt.Println("Direct infs transaction committed successfully.")

	// Output:
	// Direct infs transaction committed successfully.
}

// Example_clustered demonstrates how to use the Database in Clustered mode.
// This mode uses Redis for caching and assumes a shared filesystem (or just local for this demo).
func Example_clustered() {
	// 1. Initialize Redis connection (Required for Clustered mode).
	// In a real app, this is done once at startup.
	redisOpts := redis.Options{
		Address: "localhost:6379",
		DB:      0,
	}
	if _, err := redis.OpenConnection(redisOpts); err != nil {
		// If Redis is not available, we skip this example or handle error.
		fmt.Println("Redis not available, skipping clustered example.")
		return
	}
	defer redis.CloseConnection()

	// 2. Define storage path.
	storagePath, _ := os.MkdirTemp("", "sop_clustered_example")
	defer os.RemoveAll(storagePath)

	// 3. Initialize Database in Clustered mode.
	// This will use the registered Redis cache.
	db, _ := database.ValidateOptions(sop.DatabaseOptions{
		Type:          sop.Clustered,
		StoresFolders: []string{storagePath},
		CacheType:     sop.Redis,
	})

	// 4. Start a transaction.
	ctx := context.Background()
	tx, err := database.BeginTransaction(ctx, db, sop.ForWriting)
	if err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		return
	}

	// 5. Create a B-Tree.
	store, err := database.NewBtree[string, string](ctx, db, "products", tx, nil)
	if err != nil {
		fmt.Printf("Failed to create btree: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 6. Add data.
	if _, err := store.Add(ctx, "p1", "Laptop"); err != nil {
		fmt.Printf("Failed to add item: %v\n", err)
		tx.Rollback(ctx)
		return
	}

	// 7. Commit.
	if err := tx.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit: %v\n", err)
		return
	}

	fmt.Println("Clustered transaction committed successfully.")
}

func TestUsageExamples(t *testing.T) {
	// This test wrapper ensures the examples run during 'go test'.
	t.Run("Standalone", func(t *testing.T) {
		Example_standalone()
	})
	t.Run("Clustered", func(t *testing.T) {
		// Check if Redis is reachable before running
		opts := redis.Options{Address: "localhost:6379"}
		conn, err := redis.OpenConnection(opts)
		if err == nil && conn.Client.Ping(context.Background()).Err() == nil {
			redis.CloseConnection()
			Example_clustered()
		} else {
			t.Skip("Redis not available")
		}
	})
}
