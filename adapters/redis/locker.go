package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sharedcode/joltrin/v5"
)

// IsLockedTTL reports whether all provided lock keys are owned by this process and
// extends their TTL by the specified duration when owned.
func (c client) IsLockedTTL(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, error) {
	r := true
	var lastErr error
	for _, lk := range lockKeys {
		found, readItem, err := c.GetEx(ctx, lk.Key, duration)
		if !found || err != nil {
			lk.IsLockOwner = false
			r = false
			if err != nil {
				lastErr = err
			}
			continue
		}
		// Item found in Redis has different value, means key is locked by a different kind of function.
		if readItem != lk.LockID.String() {
			lk.IsLockOwner = false
			r = false
			continue
		}
		lk.IsLockOwner = true
	}
	// Is locked = true.
	return r, lastErr
}

// Lock attempts to acquire locks for all provided keys using the given TTL duration.
// If any key is already locked by another owner, it returns false and that owner's UUID.
func (c client) Lock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	conn, err := c.getConnection()
	if err != nil {
		return false, sop.NilUUID, err
	}

	// 1. Pipeline SetNX for all keys
	pipe := conn.Client.Pipeline()
	setCmds := make([]*redis.BoolCmd, len(lockKeys))
	for i, lk := range lockKeys {
		setCmds[i] = pipe.SetNX(ctx, lk.Key, lk.LockID.String(), duration)
	}

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, sop.NilUUID, err
	}

	// 2. Identify failed locks
	var failedIndices []int
	for i, cmd := range setCmds {
		set, err := cmd.Result()
		if err != nil && err != redis.Nil {
			return false, sop.NilUUID, err
		}
		if set {
			lockKeys[i].IsLockOwner = true
		} else {
			failedIndices = append(failedIndices, i)
		}
	}

	// 3. If everything succeeded, we are done
	if len(failedIndices) == 0 {
		return true, sop.NilUUID, nil
	}

	// 4. For failed locks, pipeline Get to check ownership
	pipe = conn.Client.Pipeline()
	getCmds := make([]*redis.StringCmd, len(failedIndices))
	for i, idx := range failedIndices {
		getCmds[i] = pipe.Get(ctx, lockKeys[idx].Key)
	}

	// We ignore Exec error here as individual command errors (like Nil) are handled below
	_, _ = pipe.Exec(ctx)

	for i, cmd := range getCmds {
		idx := failedIndices[i]
		readItem, err := cmd.Result()
		if err != nil {
			if err == redis.Nil {
				// Lock was released/expired in the interim; we failed to acquire it
				// and now it's gone. Strictly implies we don't own it.
				return false, sop.NilUUID, nil
			}
			return false, sop.NilUUID, err
		}

		if readItem == lockKeys[idx].LockID.String() {
			// We already own it
			lockKeys[idx].IsLockOwner = true
			continue
		}

		// Owned by someone else
		id, _ := sop.ParseUUID(readItem)
		return false, id, nil
	}

	return true, sop.NilUUID, nil
}

// DualLock attempts to acquire locks for all provided keys using the given TTL duration.
// It calls Lock then IsLocked to ensure the lock is acquired and persisted.
func (c client) DualLock(ctx context.Context, duration time.Duration, lockKeys []*sop.LockKey) (bool, sop.UUID, error) {
	ok, owner, err := c.Lock(ctx, duration, lockKeys)
	if err != nil || !ok {
		return ok, owner, err
	}
	// Verify lock acquisition
	isLocked, err := c.IsLocked(ctx, lockKeys)
	if err != nil {
		return false, sop.NilUUID, err
	}
	if !isLocked {
		return false, sop.NilUUID, nil
	}
	return true, sop.NilUUID, nil
}

// IsLocked reports whether all provided lock keys are currently owned by this process.
func (c client) IsLocked(ctx context.Context, lockKeys []*sop.LockKey) (bool, error) {
	if len(lockKeys) == 0 {
		return true, nil
	}
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}

	pipe := conn.Client.Pipeline()
	cmds := make([]*redis.StringCmd, len(lockKeys))
	for i, lk := range lockKeys {
		cmds[i] = pipe.Get(ctx, lk.Key)
	}

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		// If pipeline fails completely (e.g. connection error), return error
		return false, err
	}

	r := true
	var lastErr error

	for i, cmd := range cmds {
		lk := lockKeys[i]
		readItem, err := cmd.Result()

		if err != nil {
			// Key not found or other error
			lk.IsLockOwner = false
			r = false
			if err != redis.Nil {
				lastErr = err
			}
			continue
		}

		if readItem != lk.LockID.String() {
			lk.IsLockOwner = false
			r = false
			continue
		}

		lk.IsLockOwner = true
	}

	return r, lastErr
}

// IsLockedByOthers reports whether all given lock key names are currently locked by other processes.
func (c client) IsLockedByOthers(ctx context.Context, lockKeyNames []string) (bool, error) {
	if len(lockKeyNames) == 0 {
		return false, nil
	}
	conn, err := c.getConnection()
	if err != nil {
		return false, err
	}

	// Exists returns the number of keys that exist
	n, err := conn.Client.Exists(ctx, lockKeyNames...).Result()
	if err != nil {
		return false, err
	}

	// Returns true only if ALL keys exist
	return n == int64(len(lockKeyNames)), nil
}

// Unlock releases the provided lock keys, deleting only those owned by this process.
func (c client) Unlock(ctx context.Context, lockKeys []*sop.LockKey) error {
	var keysToDelete []string
	for _, lk := range lockKeys {
		if lk.IsLockOwner {
			keysToDelete = append(keysToDelete, lk.Key)
		}
	}
	if len(keysToDelete) == 0 {
		return nil
	}
	// Delete batch
	_, err := c.Delete(ctx, keysToDelete)
	return err
}

// CreateLockKeysForIDs builds lock keys from (name, lockID) tuples, applying the lock namespace prefix.
func (c client) CreateLockKeysForIDs(keys []sop.Tuple[string, sop.UUID]) []*sop.LockKey {
	lockKeys := make([]*sop.LockKey, len(keys))
	for i := range keys {
		lockKeys[i] = &sop.LockKey{
			// Prefix key with "L" to increase uniqueness.
			Key:    c.FormatLockKey(keys[i].First),
			LockID: keys[i].Second,
		}
	}
	return lockKeys
}

// CreateLockKeys creates lock keys using newly generated lock IDs for each provided key name.
func (c client) CreateLockKeys(keys []string) []*sop.LockKey {
	lockKeys := make([]*sop.LockKey, len(keys))
	for i := range keys {
		lockKeys[i] = &sop.LockKey{
			// Prefix key with "L" to increase uniqueness.
			Key:    c.FormatLockKey(keys[i]),
			LockID: sop.NewUUID(),
		}
	}
	return lockKeys
}

// FormatLockKey prefixes the key with 'L' to form the namespaced Redis key used for locking.
func (c client) FormatLockKey(k string) string {
	return fmt.Sprintf("L%s", k)
}
