package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

const (
	storeName = "concurrent_tree"
)

var databaseOptions = sop.DatabaseOptions{
	Type:          sop.Standalone,
	StoresFolders: []string{"data/constd_demo_go"},
}

func main() {
	fmt.Println("--- Concurrent Transactions Demo (Go Standalone) ---")

	maxJitter := flag.Int("maxJitter", 0, "Maximum random jitter in milliseconds to simulate slowness")
	flag.Parse()

	logger := log.New(log.NewJSONHandler(os.Stdout, &log.HandlerOptions{
		Level: log.LevelWarn,
	}))
	log.SetDefault(logger)

	log.Debug("data path", "dbPath", databaseOptions.StoresFolders[0])

	ctx := context.Background()

	// Seed the B-Tree
	seed(ctx)

	// Run concurrent workers
	var wg sync.WaitGroup
	threadCount := 40
	itemsPerThread := 100
	// +1 for seed
	targetCount := int64(threadCount*itemsPerThread + 1)

	fmt.Printf("Launching %d threads, %d items each...\n", threadCount, itemsPerThread)

	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(ctx, id, itemsPerThread, *maxJitter, targetCount)
		}(i)
	}

	wg.Wait()
	fmt.Println("done")

	// Verify
	verify(ctx, int(targetCount))
	// In a real app, you may not want to clear the data.
	database.RemoveBtree(ctx, databaseOptions, storeName)
}

func seed(ctx context.Context) {
	t, err := database.BeginTransaction(ctx, databaseOptions, sop.ForWriting)

	if err != nil {
		log.Error("Seed transaction failed", "err", err)
		return
	}

	storeOpts := sop.StoreOptions{
		SlotLength: 500,
		IsUnique:   true,
	}

	b3, err := database.NewBtree[int, string](ctx, databaseOptions, storeName, t, nil, storeOpts)
	if err != nil {
		log.Error(fmt.Sprintf("Seed NewBtree failed: %v", err))
	}

	b3.Add(ctx, -1, "Root Seed Item")
	if err := t.Commit(ctx); err != nil {
		log.Error(fmt.Sprintf("Seed commit failed: %v", err))
	}
}

func worker(ctx context.Context, id int, itemsPerThread int, maxJitter int, targetCount int64) bool {
	retryCount := 0
	committed := false

	for !committed && retryCount < 10 {
		t, err := database.BeginTransaction(ctx, databaseOptions, sop.ForWriting)
		if err != nil {
			log.Debug(fmt.Sprintf("Thread %d transaction creation failed: %v", id, err))
			return false
		}

		b3, err := database.OpenBtree[int, string](ctx, databaseOptions, storeName, t, nil)
		if err != nil {
			t.Rollback(ctx)
			log.Debug(fmt.Sprintf("Thread %d OpenBtree failed: %v", id, err))
			retryCount++
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			continue
		}

		if b3.Count() >= targetCount {
			log.Info(fmt.Sprintf("Reached target count(%v), exiting", targetCount))
			t.Commit(ctx)
			return true
		}

		for j := 0; j < itemsPerThread; j++ {
			key := (id * itemsPerThread) + j
			value := fmt.Sprintf("Thread %d - Item %d", id, j)
			if ok, err := b3.Add(ctx, key, value); !ok || err != nil {
				log.Debug(fmt.Sprintf("Thread %d add failed for key %d: %v", id, key, err))
			}
			// Simulate work/network latency
			if maxJitter > 0 {
				time.Sleep(time.Duration(rand.Intn(maxJitter)) * time.Millisecond)
			}
		}

		if err := t.Commit(ctx); err != nil {
			retryCount++
			delay := time.Duration(rand.Intn(500)) * time.Millisecond
			log.Debug(fmt.Sprintf("Thread %d conflict detected (Retry %d): %v", id, retryCount, err))
			time.Sleep(delay)
		} else {
			committed = true
		}
	}

	if !committed {
		fmt.Printf("Thread %d failed after retries.\n", id)
	}

	return false
}

func verify(ctx context.Context, expectedCount int) {
	t, err := database.BeginTransaction(ctx, databaseOptions, sop.ForWriting)
	if err != nil {
		log.Error(fmt.Sprintf("Verify transaction failed: %v", err))
	}

	b3, err := database.OpenBtree[int, string](ctx, databaseOptions, storeName, t, nil)
	if err != nil {
		log.Error(fmt.Sprintf("Verify OpenBtree failed: %v", err))
	}
	b3.First(ctx)
	count := 0
	for {
		count++
		ok, err := b3.Next(ctx)
		if err != nil {
			log.Error(err.Error())
			break
		}
		if !ok {
			break
		}
	}

	fmt.Printf("Final Count: %d (Expected: %d)\n", count, expectedCount)
	fmt.Printf("Btree count: %d\n\n", b3.Count())
	if int(count) != expectedCount {
		fmt.Printf("Count mismatch. (Success rate: %.2f%%)\n", float64(count)/float64(expectedCount)*100)
		fmt.Println("Note: Mismatches are expected in this high-concurrency demo due to transaction collisions and retry limits.")
	} else {
		fmt.Println("SUCCESS: Count matches.")
	}
	t.Commit(ctx)
}
