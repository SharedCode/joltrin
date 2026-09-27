package cache

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

func TestL2InMemoryCache_Lock_Duplicates(t *testing.T) {
	c := NewL2InMemoryCache()
	ctx := context.Background()

	key := "duplicate_key"
	id := sop.NewUUID()
	lock1 := &sop.LockKey{
		Key:    c.(*L2InMemoryCache).FormatLockKey(key),
		LockID: id,
	}
	lock2 := &sop.LockKey{
		Key:    c.(*L2InMemoryCache).FormatLockKey(key),
		LockID: id,
	}

	// Try to lock with duplicate keys
	keys := []*sop.LockKey{lock1, lock2}
	ok, _, err := c.Lock(ctx, time.Minute, keys)
	if err != nil {
		t.Fatalf("Lock failed with error: %v", err)
	}
	if !ok {
		t.Errorf("Lock failed (returned false) for duplicate keys")
	}

	if !lock1.IsLockOwner {
		t.Errorf("lock1 should be owner")
	}
	if !lock2.IsLockOwner {
		t.Errorf("lock2 should be owner")
	}

	// Unlock
	err = c.Unlock(ctx, keys)
	if err != nil {
		t.Errorf("Unlock failed: %v", err)
	}

	// Verify unlocked
	isLocked, err := c.IsLocked(ctx, []*sop.LockKey{lock1})
	if err != nil {
		t.Errorf("IsLocked failed: %v", err)
	}
	if isLocked {
		t.Errorf("Key should be unlocked")
	}
}
