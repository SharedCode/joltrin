package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

func TestInMemoryCache_Concurrency(t *testing.T) {
	c := NewL2InMemoryCache()
	ctx := context.Background()
	key := "concurrent_key"

	var wg sync.WaitGroup
	threadCount := 10
	iterations := 100

	// Track who holds the lock
	var lockHolder string
	var lockHolderMu sync.Mutex

	for i := 0; i < threadCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workerID := fmt.Sprintf("worker-%d", id)

			for j := 0; j < iterations; j++ {
				var lockKeys []*sop.LockKey
				lockKeys = c.CreateLockKeys([]string{key})

				// Try to acquire lock
				locked, _, err := c.Lock(ctx, time.Second, lockKeys)
				if err != nil {
					t.Errorf("Lock error: %v", err)
					return
				}

				if locked {
					// Critical section
					lockHolderMu.Lock()
					if lockHolder != "" {
						t.Errorf("Race condition! %s acquired lock while held by %s", workerID, lockHolder)
					}
					lockHolder = workerID
					lockHolderMu.Unlock()

					// Simulate work
					time.Sleep(time.Millisecond)

					// Release critical section
					lockHolderMu.Lock()
					lockHolder = ""
					lockHolderMu.Unlock()

					// Unlock
					err = c.Unlock(ctx, lockKeys)
					if err != nil {
						t.Errorf("Unlock error: %v", err)
					}
				} else {
					// Retry backoff
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	wg.Wait()
}
