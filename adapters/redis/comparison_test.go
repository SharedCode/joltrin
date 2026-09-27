package redis

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
)

func TestCompareCaches(t *testing.T) {
	if os.Getenv("SOP_REDIS_TEST") != "1" {
		t.Skip("skipping Redis comparison test; set SOP_REDIS_TEST=1 to run")
	}

	// Setup Redis
	option := DefaultOptions()
	OpenConnection(option)
	defer CloseConnection()

	db1Config := sop.DatabaseOptions{
		CacheType: sop.Redis,
		RedisConfig: &sop.RedisCacheConfig{
			Address:  "localhost:6379",
			Password: "",
			DB:       0,
		},
	}
	var to sop.TransactionOptions
	db1Config.CopyTo(&to)

	redisCache := NewClient(to)
	memCache := cache.NewL2InMemoryCache()

	ctx := context.Background()

	// Ensure Redis is clean/reachable
	if err := redisCache.Ping(ctx); err != nil {
		t.Skipf("skipping Redis comparison test; Redis not reachable: %v", err)
	}
	// Clear both caches to start fresh
	if err := redisCache.Clear(ctx); err != nil {
		t.Fatalf("Failed to clear Redis: %v", err)
	}
	// InMemoryCache might not need explicit clear if new, but good practice if reused.
	// Assuming Clear is part of interface.
	if err := memCache.Clear(ctx); err != nil {
		t.Fatalf("Failed to clear MemCache: %v", err)
	}

	t.Run("Set and Get", func(t *testing.T) {
		key := "cmp_key_1"
		value := "value1"
		duration := time.Minute

		errR := redisCache.Set(ctx, key, value, duration)
		errM := memCache.Set(ctx, key, value, duration)
		compareErrors(t, "Set", errR, errM)

		foundR, valR, errR := redisCache.Get(ctx, key)
		foundM, valM, errM := memCache.Get(ctx, key)
		compareResults(t, "Get", foundR, valR, errR, foundM, valM, errM)
	})

	t.Run("Get Non-Existent", func(t *testing.T) {
		key := "cmp_key_nonexistent"

		foundR, valR, errR := redisCache.Get(ctx, key)
		foundM, valM, errM := memCache.Get(ctx, key)
		compareResults(t, "Get(NonExistent)", foundR, valR, errR, foundM, valM, errM)
	})

	t.Run("SetStruct and GetStruct", func(t *testing.T) {
		key := "cmp_key_struct"
		user := struct {
			ID   int
			Name string
		}{ID: 1, Name: "Test"}
		duration := time.Minute

		errR := redisCache.SetStruct(ctx, key, &user, duration)
		errM := memCache.SetStruct(ctx, key, &user, duration)
		compareErrors(t, "SetStruct", errR, errM)

		var targetR, targetM struct {
			ID   int
			Name string
		}

		foundR, errR := redisCache.GetStruct(ctx, key, &targetR)
		foundM, errM := memCache.GetStruct(ctx, key, &targetM)

		if foundR != foundM {
			t.Errorf("GetStruct found mismatch: Redis=%v, Mem=%v", foundR, foundM)
		}
		compareErrors(t, "GetStruct", errR, errM)
		if !reflect.DeepEqual(targetR, targetM) {
			t.Errorf("GetStruct value mismatch: Redis=%v, Mem=%v", targetR, targetM)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "cmp_key_delete"
		value := "to_delete"
		duration := time.Minute

		_ = redisCache.Set(ctx, key, value, duration)
		_ = memCache.Set(ctx, key, value, duration)

		foundR, errR := redisCache.Delete(ctx, []string{key})
		foundM, errM := memCache.Delete(ctx, []string{key})

		if foundR != foundM {
			t.Errorf("Delete found mismatch: Redis=%v, Mem=%v", foundR, foundM)
		}
		compareErrors(t, "Delete", errR, errM)

		// Verify deletion
		fR, _, _ := redisCache.Get(ctx, key)
		fM, _, _ := memCache.Get(ctx, key)
		if fR || fM {
			t.Errorf("Item still exists after delete: Redis=%v, Mem=%v", fR, fM)
		}
	})

	t.Run("Locking", func(t *testing.T) {
		key := "cmp_lock_key"
		duration := time.Minute

		// Create LockKeys
		// We need to ensure we use the same LockID for both to simulate "same process/transaction"
		// But CreateLockKeys generates new UUIDs.
		// So we must manually construct them or sync them.

		// Let's use CreateLockKeysForIDs to control the UUID
		lockID := sop.NewUUID()
		tuple := sop.Tuple[string, sop.UUID]{First: key, Second: lockID}

		keysR := redisCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple})
		keysM := memCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple})

		// 1. Lock
		okR, ownerR, errR := redisCache.Lock(ctx, duration, keysR)
		okM, ownerM, errM := memCache.Lock(ctx, duration, keysM)

		if okR != okM {
			t.Errorf("Lock success mismatch: Redis=%v, Mem=%v", okR, okM)
		}
		if ownerR != ownerM {
			t.Errorf("Lock owner mismatch: Redis=%v, Mem=%v", ownerR, ownerM)
		}
		compareErrors(t, "Lock", errR, errM)

		// 2. IsLocked
		okR, errR = redisCache.IsLocked(ctx, keysR)
		okM, errM = memCache.IsLocked(ctx, keysM)
		if okR != okM {
			t.Errorf("IsLocked mismatch: Redis=%v, Mem=%v", okR, okM)
		}
		compareErrors(t, "IsLocked", errR, errM)

		// 3. IsLockedTTL
		okR, errR = redisCache.IsLockedTTL(ctx, duration, keysR)
		okM, errM = memCache.IsLockedTTL(ctx, duration, keysM)
		if okR != okM {
			t.Errorf("IsLockedTTL mismatch: Redis=%v, Mem=%v", okR, okM)
		}
		compareErrors(t, "IsLockedTTL", errR, errM)

		// 4. Unlock
		errR = redisCache.Unlock(ctx, keysR)
		errM = memCache.Unlock(ctx, keysM)
		compareErrors(t, "Unlock", errR, errM)

		// 5. Verify Unlocked
		okR, errR = redisCache.IsLocked(ctx, keysR)
		okM, errM = memCache.IsLocked(ctx, keysM)
		compareErrors(t, "IsLocked (after Unlock)", errR, errM)
		// Expect false
		if okR != false || okM != false {
			t.Errorf("IsLocked after Unlock mismatch (expected false): Redis=%v, Mem=%v", okR, okM)
		}
	})

	t.Run("Lock Conflict", func(t *testing.T) {
		key := "cmp_lock_conflict"
		duration := time.Minute

		lockID1 := sop.NewUUID()
		lockID2 := sop.NewUUID()

		tuple1 := sop.Tuple[string, sop.UUID]{First: key, Second: lockID1}
		tuple2 := sop.Tuple[string, sop.UUID]{First: key, Second: lockID2}

		keys1R := redisCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple1})
		keys1M := memCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple1})

		keys2R := redisCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple2})
		keys2M := memCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple2})

		// Lock with ID1
		redisCache.Lock(ctx, duration, keys1R)
		memCache.Lock(ctx, duration, keys1M)

		// Try Lock with ID2 (Should fail)
		okR, ownerR, errR := redisCache.Lock(ctx, duration, keys2R)
		okM, ownerM, errM := memCache.Lock(ctx, duration, keys2M)

		if okR != false || okM != false {
			t.Errorf("Lock conflict success mismatch (expected false): Redis=%v, Mem=%v", okR, okM)
		}
		// Owner returned should be lockID1
		if ownerR != lockID1 {
			t.Errorf("Lock conflict owner mismatch Redis: expected %v, got %v", lockID1, ownerR)
		}
		if ownerM != lockID1 {
			t.Errorf("Lock conflict owner mismatch Mem: expected %v, got %v", lockID1, ownerM)
		}
		compareErrors(t, "Lock Conflict", errR, errM)

		// Cleanup
		redisCache.Unlock(ctx, keys1R)
		memCache.Unlock(ctx, keys1M)
	})

	t.Run("IsLockedTTL Extension", func(t *testing.T) {
		key := "cmp_lock_ttl"
		shortDuration := 2 * time.Second
		extension := 10 * time.Second

		tuple := sop.Tuple[string, sop.UUID]{First: key, Second: sop.NewUUID()}
		keysR := redisCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple})
		keysM := memCache.CreateLockKeysForIDs([]sop.Tuple[string, sop.UUID]{tuple})

		// Lock with short duration
		redisCache.Lock(ctx, shortDuration, keysR)
		memCache.Lock(ctx, shortDuration, keysM)

		// Extend
		okR, errR := redisCache.IsLockedTTL(ctx, extension, keysR)
		okM, errM := memCache.IsLockedTTL(ctx, extension, keysM)
		compareResults(t, "IsLockedTTL Extend", okR, "", errR, okM, "", errM)

		// Wait for short duration to pass
		time.Sleep(3 * time.Second)

		// Should still be locked
		okR, errR = redisCache.IsLocked(ctx, keysR)
		okM, errM = memCache.IsLocked(ctx, keysM)
		compareErrors(t, "IsLocked (after Extend)", errR, errM)

		if !okR {
			t.Error("Redis lock expired despite extension")
		}
		if !okM {
			t.Error("Mem lock expired despite extension")
		}

		// Cleanup
		redisCache.Unlock(ctx, keysR)
		memCache.Unlock(ctx, keysM)
	})
}

func compareErrors(t *testing.T, op string, errR, errM error) {
	t.Helper()
	hasErrR := errR != nil
	hasErrM := errM != nil
	if hasErrR != hasErrM {
		t.Errorf("%s error mismatch: Redis=%v, Mem=%v", op, errR, errM)
	}
}

func compareResults(t *testing.T, op string, foundR bool, valR string, errR error, foundM bool, valM string, errM error) {
	t.Helper()
	compareErrors(t, op, errR, errM)
	if foundR != foundM {
		t.Errorf("%s found mismatch: Redis=%v, Mem=%v", op, foundR, foundM)
	}
	if valR != valM {
		t.Errorf("%s value mismatch: Redis='%s', Mem='%s'", op, valR, valM)
	}
}
