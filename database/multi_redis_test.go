package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestMultiRedisConnections(t *testing.T) {
	if os.Getenv("SOP_REDIS_TEST") != "1" {
		t.Skip("skipping Redis integration test; set SOP_REDIS_TEST=1 to run")
	}

	ctx := context.Background()

	// Database 1 connecting to Redis 1 (e.g. localhost:6379 DB 0)
	db1Config := sop.DatabaseOptions{
		CacheType:     sop.Redis,
		StoresFolders: []string{filepath.Join(t.TempDir(), "db1")},
		RedisConfig: &sop.RedisCacheConfig{
			Address:  "localhost:6379",
			Password: "",
			DB:       0,
		},
	}

	// Database 2 connecting to Redis 2 (e.g. localhost:6379 DB 1)
	db2Config := sop.DatabaseOptions{
		CacheType:     sop.Redis,
		StoresFolders: []string{filepath.Join(t.TempDir(), "db2")},
		RedisConfig: &sop.RedisCacheConfig{
			Address:  "localhost:6379",
			Password: "",
			DB:       1,
		},
	}

	// Database 3 connecting to Redis 3 (e.g. localhost:6379 DB 2) using URL
	db3Config := sop.DatabaseOptions{
		CacheType:     sop.Redis,
		StoresFolders: []string{filepath.Join(t.TempDir(), "db3")},
		RedisConfig: &sop.RedisCacheConfig{
			URL: "redis://localhost:6379/2",
		},
	}

	// Open Database 1 Transaction
	trans1, err := BeginTransaction(ctx, db1Config, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to connect to DB1: %v", err)
	}

	store1, err := NewBtree[string, string](ctx, db1Config, "store1", trans1, nil, sop.StoreOptions{
		SlotLength: 10,
	})
	if err != nil {
		t.Fatalf("Failed to create store1: %v", err)
	}

	if _, err := store1.Add(ctx, "key1", "value1-db1"); err != nil {
		t.Fatalf("Failed to add to store1: %v", err)
	}
	if err := trans1.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit trans1: %v", err)
	}

	// Open Database 2 Transaction
	trans2, err := BeginTransaction(ctx, db2Config, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to connect to DB2: %v", err)
	}

	store2, err := NewBtree[string, string](ctx, db2Config, "store2", trans2, nil, sop.StoreOptions{
		SlotLength: 10,
	})
	if err != nil {
		t.Fatalf("Failed to create store2: %v", err)
	}

	if _, err := store2.Add(ctx, "key1", "value1-db2"); err != nil {
		t.Fatalf("Failed to add to store2: %v", err)
	}
	if err := trans2.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit trans2: %v", err)
	}

	// Verify DB1 has its value
	trans1Read, err := BeginTransaction(ctx, db1Config, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to begin transaction for DB1 read: %v", err)
	}
	store1Read, err := OpenBtree[string, string](ctx, db1Config, "store1", trans1Read, nil)
	if err != nil {
		t.Fatalf("Failed to open store1: %v", err)
	}
	found, err := store1Read.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Failed to find in store1: %v", err)
	}
	if !found {
		t.Errorf("Expected to find 'key1' in DB1")
	} else {
		val, err := store1Read.GetCurrentValue(ctx)
		if err != nil {
			t.Fatalf("Failed to get value in store1: %v", err)
		}
		if val != "value1-db1" {
			t.Errorf("Expected 'value1-db1' in DB1, got '%v'", val)
		}
	}
	trans1Read.Commit(ctx)

	// Verify DB2 has its value
	trans2Read, err := BeginTransaction(ctx, db2Config, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to begin transaction for DB2 read: %v", err)
	}
	store2Read, err := OpenBtree[string, string](ctx, db2Config, "store2", trans2Read, nil)
	if err != nil {
		t.Fatalf("Failed to open store2: %v", err)
	}
	found, err = store2Read.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Failed to find in store2: %v", err)
	}
	if !found {
		t.Errorf("Expected to find 'key1' in DB2")
	} else {
		val, err := store2Read.GetCurrentValue(ctx)
		if err != nil {
			t.Fatalf("Failed to get value in store2: %v", err)
		}
		if val != "value1-db2" {
			t.Errorf("Expected 'value1-db2' in DB2, got '%v'", val)
		}
	}
	trans2Read.Commit(ctx)

	// Open Database 3 Transaction (URL based)
	trans3, err := BeginTransaction(ctx, db3Config, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to connect to DB3: %v", err)
	}

	store3, err := NewBtree[string, string](ctx, db3Config, "store3", trans3, nil, sop.StoreOptions{
		SlotLength: 10,
	})
	if err != nil {
		t.Fatalf("Failed to create store3: %v", err)
	}

	if _, err := store3.Add(ctx, "key1", "value1-db3"); err != nil {
		t.Fatalf("Failed to add to store3: %v", err)
	}
	if err := trans3.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit trans3: %v", err)
	}

	// Verify DB3 has its value
	trans3Read, err := BeginTransaction(ctx, db3Config, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to begin transaction for DB3 read: %v", err)
	}
	store3Read, err := OpenBtree[string, string](ctx, db3Config, "store3", trans3Read, nil)
	if err != nil {
		t.Fatalf("Failed to open store3: %v", err)
	}
	found, err = store3Read.Find(ctx, "key1", false)
	if err != nil {
		t.Fatalf("Failed to find in store3: %v", err)
	}
	if !found {
		t.Errorf("Expected to find 'key1' in DB3")
	} else {
		val, err := store3Read.GetCurrentValue(ctx)
		if err != nil {
			t.Fatalf("Failed to get value in store3: %v", err)
		}
		if val != "value1-db3" {
			t.Errorf("Expected 'value1-db3' in DB3, got '%v'", val)
		}
	}
	trans3Read.Commit(ctx)
}
