package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_AcquireLocks_GeneratesCorrectLockKeys(t *testing.T) {
	// 1. Setup
	mockL2 := mocks.NewMockClient()
	// We need a transaction logger that exposes acquireLocks or we test it via a public method that calls it.
	// acquireLocks is private in transactionLog.
	// However, we can use the fact that we are in the `common` package to access it directly if we create a transactionLog.

	mockTL := mocks.NewMockTransactionLog()
	tl := newTransactionLogger(mockTL, true)

	// Create a dummy transaction with the mock L2 cache
	trans := &Transaction{
		l2Cache: mockL2,
	}

	// 2. Prepare Input Data
	// Create some Handles with known Logical IDs
	lid1 := sop.NewUUID()
	lid2 := sop.NewUUID()

	handles := []sop.RegistryPayload[sop.Handle]{
		{
			RegistryTable: "table1",
			IDs: []sop.Handle{
				{LogicalID: lid1},
				{LogicalID: lid2},
			},
		},
	}

	// 3. Call acquireLocks
	// We need a transaction ID for the "owner" check
	tid := sop.NewUUID()

	ctx := context.Background()
	lockKeys, err := tl.acquireLocks(ctx, trans, tid, handles)
	if err != nil {
		t.Fatalf("acquireLocks failed: %v", err)
	}

	// 4. Verify Output
	// We expect 2 lock keys, corresponding to "L" + UUID string.
	if len(lockKeys) != 2 {
		t.Errorf("Expected 2 lock keys, got %d", len(lockKeys))
	}

	// The mock L2 cache's FormatLockKey prepends "L".
	expectedKey1 := "L" + lid1.String()
	expectedKey2 := "L" + lid2.String()

	found1 := false
	found2 := false

	for _, lk := range lockKeys {
		if lk.Key == expectedKey1 {
			found1 = true
		}
		if lk.Key == expectedKey2 {
			found2 = true
		}
		// Verify the lock is owned by us (since we acquired it)
		if !lk.IsLockOwner {
			t.Errorf("Expected lock key %s to be owned, but it wasn't", lk.Key)
		}
		// Verify the LockID matches the transaction ID (since we acquired it fresh)
		// Wait, acquireLocks uses DualLock. If successful, it returns keys.
		// The mock DualLock sets the value to LockID.String().
		// But acquireLocks doesn't necessarily set the LockID of the returned keys to TID unless it took over.
		// Let's check the implementation of acquireLocks again.
		// It calls DualLock. If successful, it returns keys.
		// The keys passed to DualLock are created via CreateLockKeys, which generates NEW LockIDs.
		// So the LockID in the returned keys will be random UUIDs, NOT the tid.
		// UNLESS we took over a dead transaction, in which case we set LockID = tid.
		// In this test, we are acquiring fresh locks, so they should have their own LockIDs.
	}

	if !found1 {
		t.Errorf("Expected lock key %s not found", expectedKey1)
	}
	if !found2 {
		t.Errorf("Expected lock key %s not found", expectedKey2)
	}

	// 5. Verify Locks in Mock L2
	// Ensure they are actually locked in the mock
	isLocked1, _ := mockL2.IsLocked(ctx, []*sop.LockKey{lockKeys[0]})
	if !isLocked1 {
		t.Errorf("Key %s should be locked in L2 cache", lockKeys[0].Key)
	}
	isLocked2, _ := mockL2.IsLocked(ctx, []*sop.LockKey{lockKeys[1]})
	if !isLocked2 {
		t.Errorf("Key %s should be locked in L2 cache", lockKeys[1].Key)
	}
}

func Test_AcquireLocks_TakeoverDeadTransaction(t *testing.T) {
	// 1. Setup
	mockL2 := mocks.NewMockClient()
	mockTL := mocks.NewMockTransactionLog()
	tl := newTransactionLogger(mockTL, true)
	trans := &Transaction{l2Cache: mockL2}

	lid := sop.NewUUID()
	handles := []sop.RegistryPayload[sop.Handle]{
		{IDs: []sop.Handle{{LogicalID: lid}}},
	}

	// Dead transaction ID
	deadTID := sop.NewUUID()
	// Current transaction ID (trying to resurrect)
	// resurrectingTID := deadTID // In resurrection, we are acting on behalf of the dead transaction, so we pass the dead TID.

	// 2. Simulate Dead Transaction holding the lock
	ctx := context.Background()
	lockKeyName := "L" + lid.String()

	// Manually lock it in Mock L2 with the dead TID as the value
	// The mock's Lock/DualLock sets the value to the LockID of the key.
	// But here we want to simulate that the VALUE in Redis is the TID (which is how SOP does it? Let's verify).

	// Checking common/transactionlogger.go acquireLocks:
	// if ok, ownerTID, err := t.l2Cache.DualLock(...)
	// DualLock in SOP (Redis) usually stores the LockID.
	// Wait, let's check how SOP stores the owner.
	// In `adapters/redis/locker.go`:
	// set, err := conn.Client.SetNX(ctx, lk.Key, lk.LockID.String(), duration).Result()
	// It stores the LockID.

	// But `acquireLocks` logic says:
	// if ownerTID.Compare(tid) != 0 { ... }
	// This implies `DualLock` returns the owner TID?
	// Let's check `DualLock` signature and behavior.
	// `DualLock(ctx, duration, keys) (bool, UUID, error)`
	// It returns (success, ownerUUID, error).

	// If `DualLock` fails (returns false), it returns the UUID of the owner.
	// In `adapters/redis/locker.go`:
	// If SetNX fails, it does a Get to return the value.
	// So the value stored in Redis IS the LockID.

	// So `acquireLocks` compares `ownerTID` (which is the LockID from Redis) with `tid`.
	// This means for `acquireLocks` to succeed in taking over, the LockID stored in Redis MUST be `tid`.

	// So, if we want to simulate a "dead transaction" that we can take over,
	// the lock in Redis must ALREADY be held with a value equal to `tid`.

	// So:
	mockL2.Set(ctx, lockKeyName, deadTID.String(), 1*time.Hour)

	// 3. Call acquireLocks
	// We pass `deadTID` as the `tid` argument, because we are resurrecting THAT transaction.
	lockKeys, err := tl.acquireLocks(ctx, trans, deadTID, handles)
	if err != nil {
		t.Fatalf("acquireLocks failed during takeover: %v", err)
	}

	// 4. Verify
	if len(lockKeys) != 1 {
		t.Fatalf("Expected 1 lock key")
	}

	// The returned key should have LockID = deadTID and IsLockOwner = true
	if lockKeys[0].LockID != deadTID {
		t.Errorf("Expected LockID to be %s, got %s", deadTID, lockKeys[0].LockID)
	}
	if !lockKeys[0].IsLockOwner {
		t.Errorf("Expected to be lock owner")
	}
}
