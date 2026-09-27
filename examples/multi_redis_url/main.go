package main

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func main() {
	ctx := context.Background()

	fmt.Println("--- Multi-Tenancy with Redis URLs Demo (Go) ---")

	// Database 1: Connects to Redis instance A (simulated with DB 0)
	// Using the new URL configuration style.
	db1Config := sop.DatabaseOptions{
		Type: sop.Clustered,
		RedisConfig: &sop.RedisCacheConfig{
			URL: "redis://localhost:6379/0",
		},
		// Ensure transaction logs are written to a local directory we have permissions for
		StoresFolders: []string{"./data/db1"},
	}

	// Database 2: Connects to Redis instance B (simulated with DB 1)
	// Using the new URL configuration style.
	db2Config := sop.DatabaseOptions{
		Type: sop.Clustered,
		RedisConfig: &sop.RedisCacheConfig{
			URL: "redis://localhost:6379/1",
		},
		StoresFolders: []string{"./data/db2"},
	}

	fmt.Println("Connecting to Redis DB 0 via URL...")
	// Open Database 1 Transaction
	trans1, err := database.BeginTransaction(ctx, db1Config, sop.ForWriting)
	if err != nil {
		fmt.Printf("Failed to connect to DB1: %v\n", err)
		// If Redis is not running, we exit gracefully
		return
	}

	store1, err := database.NewBtree[string, string](ctx, db1Config, "store1", trans1, nil, sop.StoreOptions{
		Name:       "store1",
		SlotLength: 10,
	})
	if err != nil {
		panic(err)
	}

	store1.Add(ctx, "key1", "value1-db1")
	if err := trans1.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("Wrote 'key1' -> 'value1-db1' to DB 0")

	fmt.Println("Connecting to Redis DB 1 via URL...")
	// Open Database 2 Transaction
	trans2, err := database.BeginTransaction(ctx, db2Config, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	store2, err := database.NewBtree[string, string](ctx, db2Config, "store2", trans2, nil, sop.StoreOptions{
		Name:       "store2",
		SlotLength: 10,
	})
	if err != nil {
		panic(err)
	}

	store2.Add(ctx, "key1", "value1-db2")
	if err := trans2.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("Wrote 'key1' -> 'value1-db2' to DB 1")

	fmt.Println("Success! Two separate Redis databases used via URL config.")
}
