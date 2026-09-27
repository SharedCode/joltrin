package main

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func main() {
	ctx := context.Background()

	// Database 1 connecting to Redis 1 (e.g. localhost:6379 DB 0)
	db1Config := sop.DatabaseOptions{
		CacheType: sop.Redis,
		RedisConfig: &sop.RedisCacheConfig{
			Address:  "localhost:6379",
			Password: "",
			DB:       0,
		},
	}

	// Database 2 connecting to Redis 2 (e.g. localhost:6379 DB 1)
	// This simulates connecting to a different Redis cluster or instance.
	// You can also specify a different host/port, e.g., "192.168.1.50:6379"
	db2Config := sop.DatabaseOptions{
		CacheType: sop.Redis,
		RedisConfig: &sop.RedisCacheConfig{
			Address:  "localhost:6379",
			Password: "",
			DB:       1,
		},
	}

	fmt.Println("Connecting to Redis DB 0...")
	// Open Database 1 Transaction
	trans1, err := database.BeginTransaction(ctx, db1Config, sop.ForWriting)
	if err != nil {
		// If Redis is not running, this might fail.
		fmt.Printf("Failed to connect to DB1: %v\n", err)
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

	fmt.Println("Connecting to Redis DB 1...")
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

	fmt.Println("Successfully wrote to two different Redis databases/instances.")
}
