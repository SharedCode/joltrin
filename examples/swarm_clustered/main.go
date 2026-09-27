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
	Type:          sop.Clustered,
	StoresFolders: []string{"data/concurrent_demo_go"},
	RedisConfig: &sop.RedisCacheConfig{
		URL: "redis://localhost:6379",
	},
}

func main() {
	fmt.Println("--- Concurrent Transactions Demo (Go Clustered) ---")

	maxJitter := flag.Int("maxJitter", 0, "Maximum random jitter in milliseconds to simulate slowness")
	reverse := flag.Int("reverse", 0, "Populate in reverse, 0 is false, 1 is true")
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
	itemsPerThread := 300
	// +1 for seed
	targetCount := int64(threadCount*itemsPerThread + 1)

	fmt.Printf("Launching %d threads, %d items each...\n", threadCount, itemsPerThread)

	if *reverse > 0 {
		for i := threadCount; i > 0; i-- {
			wg.Add(1)
			go runWorker(ctx, &wg, i, itemsPerThread, *maxJitter, targetCount)
		}
	} else {
		for i := 0; i < threadCount; i++ {
			wg.Add(1)
			go runWorker(ctx, &wg, i, itemsPerThread, *maxJitter, targetCount)
		}
	}

	wg.Wait()
	fmt.Println("done")

	// Verify
	verify(ctx, int(targetCount))
	// In a real app, you might not want to clear the data.
	database.RemoveBtree(ctx, databaseOptions, storeName)
}

func runWorker(ctx context.Context, wg *sync.WaitGroup, id int, itemsPerThread int, maxJitter int, targetCount int64) {
	defer wg.Done()
	worker(ctx, id, itemsPerThread, maxJitter, targetCount)
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
		fmt.Println("FAILURE: Count mismatch.")
	} else {
		fmt.Println("SUCCESS: Count matches.")
	}
	t.Commit(ctx)
}
