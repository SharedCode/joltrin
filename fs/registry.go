// Package fs provides filesystem-backed implementations of SOP storage primitives:
// registries, blob store, transaction logs and a replication tracker.
package fs

import (
	"context"
	"fmt"
	"io"
	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
)

// registryOnDisk is a filesystem-backed implementation of sop.Registry.
// It stores Handles in hash-partitioned files (via registryMap) and keeps caches in sync:
// - L1 cache (in-process MRU) for rapid handle lookups
// - L2 cache (Redis) for cross-process sharing and TTL semantics
// Replication (active/passive folders) is coordinated through ReplicationTracker.
type registryOnDisk struct {
	hashmap            *registryMap
	replicationTracker *replicationTracker
	l2Cache            sop.L2Cache
	l1Cache            *cache.L1Cache
	// rmCloseOverride, when set (tests), is invoked instead of rm.close() inside Replicate to
	// simulate close errors without altering production behavior.
	rmCloseOverride func() error
}

// Registry extends sop.Registry with io.Closer to allow releasing resources when the transaction completes.
type Registry interface {
	sop.Registry
	io.Closer
}

const (
	// updateAllOrNothingOfHandleSetLockTimeout is the cache-based conflict-check lock TTL for Update operations.
	// Each logical ID is locked individually in Redis to minimize contention between concurrent transactions
	// updating disjoint keys in the same registry table.
	updateAllOrNothingOfHandleSetLockTimeout = time.Duration(10 * time.Minute)
)

// NewRegistry creates a filesystem-backed Registry that manages handles on disk using a hashmap structure.
// readWrite toggles direct write access to files; hashModValue controls file partitioning (fan-out) for the registry table.
// rt provides active/passive routing for replication; l2Cache supplies cross-process caching and locking.
func NewRegistry(readWrite bool, hashModValue int, rt *replicationTracker, l2Cache sop.L2Cache) *registryOnDisk {
	return &registryOnDisk{
		hashmap:            newRegistryMap(readWrite, hashModValue, rt, l2Cache),
		replicationTracker: rt,
		l2Cache:            l2Cache,
		l1Cache:            cache.GetGlobalL1Cache(l2Cache),
	}
}

// Close closes all open file handles used by the registry.
func (r *registryOnDisk) Close() error {
	return r.hashmap.close()
}

// Add persists the provided handles into their corresponding registry files and updates L1/L2 caches.
// This is typically used for new virtual IDs (e.g., new roots or added nodes) before any flips occur.
func (r *registryOnDisk) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if err := r.hashmap.add(ctx, storesHandles); err != nil {
		return err
	}
	// Refresh caches after disk writes. L1 is updated in-bulk; L2 uses LogicalID string keys.
	for _, sh := range storesHandles {
		r.l1Cache.Handles.Set(convertToKvp(sh.IDs))
		for _, h := range sh.IDs {
			if err := r.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
				// Cache is best-effort; tolerate Redis failures.
				log.Warn(fmt.Sprintf("Registry UpdateNoLocks (redis setstruct) failed, details: %v", err))
			}
		}
	}
	return nil
}

// Update writes the provided handles to disk (with per-key locks) and refreshes L1/L2 caches.
// Locking: acquires a short-lived Redis lock per logical ID to serialize conflicting writers across processes.
// On disk write failure, evicts L1/L2 cache entries to force refetch on subsequent reads.
func (r *registryOnDisk) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	for _, sh := range storesHandles {
		// Fail on 1st encountered error. It is non-critical operation, SOP can "heal" those got left.
		for _, h := range sh.IDs {
			// Update registry record.
			// Acquire a short-lived lock per logical ID to avoid concurrent writers updating the same slot.
			lk := r.l2Cache.CreateLockKeys([]string{h.LogicalID.String()})
			if ok, _, err := r.l2Cache.DualLock(ctx, updateAllOrNothingOfHandleSetLockTimeout, lk); !ok || err != nil {
				if err == nil {
					err = fmt.Errorf("lock failed, key %v is already locked by another", lk[0].Key)
				}
				return err
			}
			if err := r.hashmap.set(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: sh.RegistryTable, IDs: []sop.Handle{h}}}); err != nil {
				// On write failure, evict stale cache entries so future reads refetch from disk.
				r.l1Cache.Handles.Delete([]sop.UUID{h.LogicalID})
				r.l2Cache.Delete(ctx, []string{h.LogicalID.String()})
				// Unlock the object Keys before return.
				r.l2Cache.Unlock(ctx, lk)
				return err
			}
			// Tolerate Redis cache failure.
			if err := r.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
				log.Warn(fmt.Sprintf("Registry Update (redis setstruct) failed, details: %v", err))
			}
			// Unlock the object Keys.
			if err := r.l2Cache.Unlock(ctx, lk); err != nil {
				return err
			}
		}
		// After successful writes, refresh L1 cache for this registry table.
		r.l1Cache.Handles.Set(convertToKvp(sh.IDs))
	}
	return nil
}

// UpdateNoLocks writes the provided handles to disk without acquiring per-key locks and updates caches.
// Used when the transaction manager has already acquired locks for the scope (e.g., batch updates in commit).
func (r *registryOnDisk) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	if err := r.hashmap.set(ctx, storesHandles); err != nil {
		return err
	}

	// Sync L1/L2 cache with updated values.
	for _, sh := range storesHandles {
		r.l1Cache.Handles.Set(convertToKvp(sh.IDs))
		for _, h := range sh.IDs {
			if err := r.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
				log.Warn(fmt.Sprintf("Registry UpdateNoLocks (redis setstruct) failed, details: %v", err))
			}
		}
	}
	return nil
}

// Get loads handles by logical IDs. It first checks L2 cache (with optional TTL) and then falls back to disk.
// Any misses from L2 are fetched from the hashed files, and both L1 and L2 caches are refreshed for those.
func (r *registryOnDisk) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	storesHandles := make([]sop.RegistryPayload[sop.Handle], 0, len(storesLids))
	for _, storeLids := range storesLids {
		handles := make([]sop.Handle, 0, len(storeLids.IDs))
		lids := make([]sop.UUID, 0, len(storeLids.IDs))

		keys := make([]string, len(storeLids.IDs))
		targets := make([]any, len(storeLids.IDs))
		handleTargets := make([]sop.Handle, len(storeLids.IDs))
		for i := range storeLids.IDs {
			keys[i] = storeLids.IDs[i].String()
			targets[i] = &handleTargets[i]
		}
		var duration time.Duration
		if storeLids.IsCacheTTL {
			duration = storeLids.CacheDuration
		}
		found, err := r.l2Cache.GetStructs(ctx, keys, targets, duration)
		if err != nil {
			log.Warn(fmt.Sprintf("Registry Get (redis getstructs) failed, details: %v", err))
		}

		for i := range storeLids.IDs {
			if err != nil || !found[i] {
				lids = append(lids, storeLids.IDs[i])
				continue
			}
			handles = append(handles, handleTargets[i])
		}

		if len(lids) == 0 {
			storesHandles = append(storesHandles, sop.RegistryPayload[sop.Handle]{
				RegistryTable: storeLids.RegistryTable,
				BlobTable:     storeLids.BlobTable,
				CacheDuration: storeLids.CacheDuration,
				IsCacheTTL:    storeLids.IsCacheTTL,
				IDs:           handles,
			})
			continue
		}

		mh, err := r.hashmap.fetch(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: storeLids.RegistryTable, IDs: lids}})
		if err != nil {
			return nil, err
		}

		// Add to the handles list the "missing from cache" handles read from registry file.
		for _, handle := range mh[0].IDs {
			handles = append(handles, handle)
			if err := r.l2Cache.SetStruct(ctx, handle.LogicalID.String(), &handle, storeLids.CacheDuration); err != nil {
				log.Warn(fmt.Sprintf("Registry Set (redis setstruct) failed, details: %v", err))
			}
		}
		storesHandles = append(storesHandles, sop.RegistryPayload[sop.Handle]{
			RegistryTable: storeLids.RegistryTable,
			BlobTable:     storeLids.BlobTable,
			CacheDuration: storeLids.CacheDuration,
			IsCacheTTL:    storeLids.IsCacheTTL,
			IDs:           handles,
		})
	}
	return storesHandles, nil
}

// Remove deletes handles from disk and evicts them from L1/L2 caches.
// The cache eviction is deferred to always run, ensuring cache coherence even if disk removal fails
// and will be retried later by the caller.
func (r *registryOnDisk) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	// Flush out the failing records from cache.
	// deleteFromCache evicts entries from both L1 and L2 to keep caches consistent
	// even when the disk removal fails and will be retried later by callers.
	deleteFromCache := func(storesLids []sop.RegistryPayload[sop.UUID]) {
		for _, storeLids := range storesLids {
			r.l1Cache.Handles.Delete(storeLids.IDs)
			for _, id := range storeLids.IDs {
				if _, err := r.l2Cache.Delete(ctx, []string{id.String()}); err != nil {
					log.Warn(fmt.Sprintf("Registry Delete (redis delete) failed, details: %v", err))
				}
			}
		}
	}
	defer deleteFromCache(storesLids)
	return r.hashmap.remove(ctx, storesLids)
}

// Replicate writes registry updates to the passive destination, if replication is enabled.
// It opens a registry map pointing at the passive folder, applies add/set/remove operations,
// and marks the replication tracker as failed on any I/O error so future operations can fail over.
func (r *registryOnDisk) Replicate(ctx context.Context, newRootNodesHandles, addedNodesHandles,
	updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {

	if !r.replicationTracker.replicate || r.replicationTracker.FailedToReplicate {
		log.Debug(fmt.Sprintf("replicate %v, FailedToReplicate %v, current target %s",
			r.replicationTracker.replicate, r.replicationTracker.FailedToReplicate,
			r.replicationTracker.getActiveBaseFolder()))
		return nil
	}

	// Open the hashmaps on the passive destination(s). Write the nodes' handle(s) on each.
	// Close the hashmaps files.
	af := r.replicationTracker.ActiveFolderToggler
	copy := *r.replicationTracker
	copy.ActiveFolderToggler = !af

	rm := newRegistryMap(true, r.hashmap.hashmap.hashModValue, &copy, r.l2Cache)

	var lastErr error

	if err := rm.add(ctx, newRootNodesHandles); err != nil {
		log.Debug(fmt.Sprintf("error replicating new root nodes, details: %v", err))
		r.replicationTracker.handleFailedToReplicate(ctx)
		lastErr = err
	}
	if err := rm.add(ctx, addedNodesHandles); err != nil {
		log.Debug(fmt.Sprintf("error replicating added nodes, details: %v", err))
		r.replicationTracker.handleFailedToReplicate(ctx)
		lastErr = err
	}

	if err := rm.set(ctx, updatedNodesHandles); err != nil {
		log.Debug(fmt.Sprintf("error replicating updated nodes, details: %v", err))
		r.replicationTracker.handleFailedToReplicate(ctx)
		lastErr = err
	}

	if err := rm.remove(ctx, sop.ExtractLogicalIDs(removedNodesHandles)); err != nil {
		log.Debug(fmt.Sprintf("error replicating removed nodes, details: %v", err))
		r.replicationTracker.handleFailedToReplicate(ctx)
		lastErr = err
	}

	if r.rmCloseOverride != nil {
		if err := r.rmCloseOverride(); err != nil && lastErr == nil {
			lastErr = err
		}
	} else {
		if err := rm.close(); err != nil && lastErr == nil {
			lastErr = err
		}
	}

	return lastErr
}

// Unlock releases a set of keys.
func (r *registryOnDisk) Unlock(ctx context.Context, lockKey *sop.LockKey) error {
	return r.l2Cache.Unlock(ctx, []*sop.LockKey{lockKey})
}

// convertToKvp maps a slice of Handles to L1 cache entries keyed by LogicalID.
func convertToKvp(handles []sop.Handle) []sop.KeyValuePair[sop.UUID, sop.Handle] {
	items := make([]sop.KeyValuePair[sop.UUID, sop.Handle], len(handles))
	for i := range handles {
		items[i] = sop.KeyValuePair[sop.UUID, sop.Handle]{Key: handles[i].LogicalID, Value: handles[i]}
	}
	return items
}
