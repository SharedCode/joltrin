package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
)

func main() {
	count := flag.Int("count", 10000, "Number of items to insert/read/delete")
	path := flag.String("path", "/tmp/sop_bench", "Path to store data")
	slotLength := flag.Int("slotlength", 1000, "Slot length for B-Tree nodes")
	flag.Parse()

	// Register InMemory Cache
	sop.RegisterL2CacheFactory(sop.InMemory, func(options sop.TransactionOptions) sop.L2Cache {
		return cache.NewL2InMemoryCache()
	})

	fmt.Printf("Benchmarking Joltrin with %d items in %s\n", *count, *path)

	// Ensure path exists
	if err := os.MkdirAll(*path, 0755); err != nil {
		fmt.Printf("Failed to create directory: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// 1. Create Store
	fmt.Println("Creating store...")
	t, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{*path},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		fmt.Printf("Failed to create transaction: %v\n", err)
		os.Exit(1)
	}
	if err := t.Begin(ctx); err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		os.Exit(1)
	}

	_, err = infs.NewBtree[string, string](ctx, sop.StoreOptions{
		Name:                     "benchmark_store",
		SlotLength:               *slotLength,
		IsValueDataInNodeSegment: true,
	}, t, nil)
	if err != nil {
		fmt.Printf("Failed to create store: %v\n", err)
		os.Exit(1)
	}

	if err := t.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit store creation: %v\n", err)
		os.Exit(1)
	}

	// 2. Insert
	fmt.Println("Starting Insert benchmark...")
	start := time.Now()

	t, err = infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{*path},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		fmt.Printf("Failed to create transaction: %v\n", err)
		os.Exit(1)
	}
	if err := t.Begin(ctx); err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		os.Exit(1)
	}

	store, err := infs.OpenBtree[string, string](ctx, "benchmark_store", t, nil)
	if err != nil {
		fmt.Printf("Failed to open store: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < *count; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := fmt.Sprintf("value_%d", i)
		if ok, err := store.Add(ctx, key, value); err != nil || !ok {
			fmt.Printf("Failed to add item %d: %v\n", i, err)
			os.Exit(1)
		}
	}

	if err := t.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit inserts: %v\n", err)
		os.Exit(1)
	}

	duration := time.Since(start)
	fmt.Printf("Insert: %d items in %v (%.2f ops/sec)\n", *count, duration, float64(*count)/duration.Seconds())

	// 3. Read
	fmt.Println("Starting Read benchmark...")
	start = time.Now()

	t, err = infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForReading,
		StoresFolders: []string{*path},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		fmt.Printf("Failed to create transaction: %v\n", err)
		os.Exit(1)
	}
	if err := t.Begin(ctx); err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		os.Exit(1)
	}

	store, err = infs.OpenBtree[string, string](ctx, "benchmark_store", t, nil)
	if err != nil {
		fmt.Printf("Failed to open store: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < *count; i++ {
		key := fmt.Sprintf("key_%d", i)
		if found, err := store.Find(ctx, key, false); err != nil || !found {
			fmt.Printf("Failed to find item %d: %v (found=%v)\n", i, err, found)
			os.Exit(1)
		}
		if _, err := store.GetCurrentValue(ctx); err != nil {
			fmt.Printf("Failed to get value %d: %v\n", i, err)
			os.Exit(1)
		}
	}
	// ReadOnly transaction doesn't need commit, but good practice to close/commit
	t.Commit(ctx)

	duration = time.Since(start)
	fmt.Printf("Read: %d items in %v (%.2f ops/sec)\n", *count, duration, float64(*count)/duration.Seconds())

	// 4. Delete
	fmt.Println("Starting Delete benchmark...")
	start = time.Now()

	t, err = infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{*path},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		fmt.Printf("Failed to create transaction: %v\n", err)
		os.Exit(1)
	}
	if err := t.Begin(ctx); err != nil {
		fmt.Printf("Failed to begin transaction: %v\n", err)
		os.Exit(1)
	}

	store, err = infs.OpenBtree[string, string](ctx, "benchmark_store", t, nil)
	if err != nil {
		fmt.Printf("Failed to open store: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < *count; i++ {
		key := fmt.Sprintf("key_%d", i)
		if ok, err := store.Remove(ctx, key); err != nil || !ok {
			fmt.Printf("Failed to remove item %d: %v\n", i, err)
			os.Exit(1)
		}
	}

	if err := t.Commit(ctx); err != nil {
		fmt.Printf("Failed to commit deletes: %v\n", err)
		os.Exit(1)
	}

	duration = time.Since(start)
	fmt.Printf("Delete: %d items in %v (%.2f ops/sec)\n", *count, duration, float64(*count)/duration.Seconds())

	// Cleanup
	os.RemoveAll(*path)
}
