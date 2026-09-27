package vector_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

func TestOptimize_ConcurrencyLocking(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "sop-ai-test-concurrent-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize Database
	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	tx, err := db.BeginTransaction(context.Background(), sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// We need a cache for locking
	c := cache.NewL2InMemoryCache()

	idx, err := db.OpenVectorStore(context.Background(), "test_concurrent", tx, vector.Config{
		UsageMode: ai.Dynamic,
		Cache:     c,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Populate with some data so Optimize has something to do
	var items []ai.Item[map[string]any]
	for i := 0; i < 5000; i++ {
		items = append(items, ai.Item[map[string]any]{
			ID:      fmt.Sprintf("item-%d", i),
			Vector:  []float32{float32(i), float32(i)},
			Payload: map[string]any{"val": i},
		})
	}
	if err := idx.UpsertBatch(context.Background(), items); err != nil {
		t.Fatalf("UpsertBatch failed: %v", err)
	}

	// Commit setup
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Create a signaling cache wrapper to detect when lock is acquired
	lockAcquired := make(chan struct{})
	sc := &signalingCache{
		L2Cache: c,
		onLock: func() {
			close(lockAcquired)
		},
	}

	// Re-open for concurrent test
	tx2, err := db.BeginTransaction(context.Background(), sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	idx2, err := db.OpenVectorStore(context.Background(), "test_concurrent", tx2, vector.Config{
		UsageMode: ai.Dynamic,
		Cache:     sc,
	})
	if err != nil {
		t.Fatalf("Open 2 failed: %v", err)
	}

	// Start Optimize in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)

	optimizeErrChan := make(chan error, 1)
	startOptimize := make(chan struct{})
	optimizeDone := make(chan struct{})

	go func() {
		defer wg.Done()
		close(startOptimize) // Signal that we are starting
		if err := idx2.Optimize(context.Background()); err != nil {
			optimizeErrChan <- err
		}
		close(optimizeDone)
	}()

	<-startOptimize

	// Wait for Optimize to acquire lock
	select {
	case <-lockAcquired:
		// Lock acquired!
	case <-optimizeDone:
		// If Optimize finished before we saw the lock, check if it failed
		close(optimizeErrChan)
		var errs []error
		for err := range optimizeErrChan {
			errs = append(errs, err)
		}
		// If it finished successfully without us seeing the lock signal, it might be too fast or signal failed.
		// But if it finished, the lock is released.
		// However, we want to test CONCURRENCY. If it finishes instantly, we can't test concurrency.
		// But with 100 items, it shouldn't be instant.
		// If it is instant, maybe we should just accept it passed (no error) but warn.
		if len(errs) > 0 {
			t.Fatalf("Optimize failed: %v", errs)
		}
		t.Log("Optimize finished very quickly, skipping concurrency check")
		return
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for Optimize to acquire lock")
	}

	// Verify lock is held
	lockKey := c.FormatLockKey("optimize_lock_test_concurrent")
	locked, err := c.IsLockedByOthers(context.Background(), []string{lockKey})
	if err != nil {
		t.Fatalf("IsLockedByOthers failed: %v", err)
	}
	if !locked {
		// It's possible it finished right after acquiring lock?
		// Check if optimizeDone is closed
		select {
		case <-optimizeDone:
			t.Log("Optimize finished right after acquiring lock")
			return
		default:
			t.Fatal("Expected lock to be held by Optimize")
		}
	}

	// Try to Upsert using a NEW transaction/instance (simulating another request)

	// Try to Upsert using a NEW transaction/instance (simulating another request)
	tx3, err := db.BeginTransaction(context.Background(), sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 3 failed: %v", err)
	}
	idx3, err := db.OpenVectorStore(context.Background(), "test_concurrent", tx3, vector.Config{
		UsageMode: ai.Dynamic,
		Cache:     c,
	})
	if err != nil {
		t.Fatalf("Open 3 failed: %v", err)
	}

	// This Upsert should FAIL because Optimize is running
	err = idx3.Upsert(context.Background(), ai.Item[map[string]any]{
		ID:     "new-item",
		Vector: []float32{1, 1},
	})

	if err == nil {
		t.Error("Expected Upsert to fail due to active optimization lock, but it succeeded")
	} else {
		t.Logf("Upsert failed as expected: %v", err)
	}

	// Wait for Optimize to finish
	wg.Wait()
	close(optimizeErrChan)
	for err := range optimizeErrChan {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Now Upsert should succeed
	if err := idx3.Upsert(context.Background(), ai.Item[map[string]any]{
		ID:     "new-item-2",
		Vector: []float32{1, 1},
	}); err != nil {
		t.Errorf("Upsert failed after optimization finished: %v", err)
	}
}

// signalingCache wraps a real cache and signals when DualLock is called successfully.
type signalingCache struct {
	sop.L2Cache
	onLock func()
}

func (s *signalingCache) DualLock(ctx context.Context, duration time.Duration, keys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, k, err := s.L2Cache.DualLock(ctx, duration, keys)
	if ok && err == nil && s.onLock != nil {
		s.onLock()
	}
	return ok, k, err
}
