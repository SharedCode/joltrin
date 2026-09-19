package cache

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryCache_LockingContention(t *testing.T) {
	c := NewL2InMemoryCache()
	ctx := context.Background()

	key := "contentionKey"
	lockKeys1 := c.CreateLockKeys([]string{key})
	lockKeys2 := c.CreateLockKeys([]string{key})

	// Client 1 acquires lock
	ok, _, err := c.Lock(ctx, time.Minute, lockKeys1)
	if err != nil {
		t.Fatalf("Client 1 Lock failed: %v", err)
	}
	if !ok {
		t.Fatalf("Client 1 failed to acquire lock")
	}

	// Client 2 tries to acquire lock (should fail)
	ok, _, err = c.Lock(ctx, time.Minute, lockKeys2)
	if err != nil {
		t.Fatalf("Client 2 Lock failed: %v", err)
	}
	if ok {
		t.Errorf("Client 2 acquired lock while held by Client 1")
	}

	// Client 1 unlocks
	err = c.Unlock(ctx, lockKeys1)
	if err != nil {
		t.Fatalf("Client 1 Unlock failed: %v", err)
	}

	// Client 2 tries again (should succeed)
	ok, _, err = c.Lock(ctx, time.Minute, lockKeys2)
	if err != nil {
		t.Fatalf("Client 2 Lock retry failed: %v", err)
	}
	if !ok {
		t.Errorf("Client 2 failed to acquire lock after release")
	}
}

func TestInMemoryCache_LockingExpiration(t *testing.T) {
	c := NewL2InMemoryCache()
	ctx := context.Background()

	key := "expirationKey"
	lockKeys := c.CreateLockKeys([]string{key})

	// Acquire lock with short TTL
	ok, _, err := c.Lock(ctx, 100*time.Millisecond, lockKeys)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	if !ok {
		t.Fatalf("Failed to acquire lock")
	}

	// Verify it is locked
	locked, err := c.IsLocked(ctx, lockKeys)
	if err != nil {
		t.Fatalf("IsLocked failed: %v", err)
	}
	if !locked {
		t.Errorf("IsLocked returned false immediately after lock")
	}

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Verify it is NOT locked
	locked, err = c.IsLocked(ctx, lockKeys)
	if err != nil {
		t.Fatalf("IsLocked failed: %v", err)
	}
	if locked {
		t.Errorf("IsLocked returned true after expiration")
	}

	// Try to acquire again (should succeed)
	ok, _, err = c.Lock(ctx, time.Minute, lockKeys)
	if err != nil {
		t.Fatalf("Re-Lock failed: %v", err)
	}
	if !ok {
		t.Errorf("Failed to re-acquire lock after expiration")
	}
}

func TestInMemoryCache_IsLockedByOthers(t *testing.T) {
	c := NewL2InMemoryCache()
	ctx := context.Background()

	key := "othersKey"
	lockKeys := c.CreateLockKeys([]string{key})

	// Initially not locked
	locked, err := c.IsLockedByOthers(ctx, []string{key})
	if err != nil {
		t.Fatalf("IsLockedByOthers failed: %v", err)
	}
	if locked {
		t.Errorf("IsLockedByOthers returned true for unlocked key")
	}

	// Acquire lock
	ok, _, err := c.Lock(ctx, time.Minute, lockKeys)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	if !ok {
		t.Fatalf("Lock failed")
	}

	// Should be locked by others (from perspective of a fresh check without ID)
	formattedKey := c.FormatLockKey(key)
	locked, err = c.IsLockedByOthers(ctx, []string{formattedKey})
	if err != nil {
		t.Fatalf("IsLockedByOthers failed: %v", err)
	}
	if !locked {
		t.Errorf("IsLockedByOthers returned false for locked key")
	}
}
