package fs

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
)

type registryMap struct {
	hashmap *hashmap
}

func newRegistryMap(readWrite bool, hashModValue int, replicationTracker *replicationTracker, cache sop.L2Cache) *registryMap {
	return &registryMap{
		hashmap: newHashmap(readWrite, hashModValue, replicationTracker, cache),
	}
}

// add writes the given handle records to their on-disk locations based on hash mapping.
func (rm registryMap) add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	// Individually write to the file area occupied by the handle so we don't create "lock pressure".
	for _, item := range storesHandles {
		for _, h := range item.IDs {
			if err := rm.hashmap.findAndAdd(ctx, item.RegistryTable, h); err != nil {
				return fmt.Errorf("registryMap.add failed, details: %w", err)
			}
		}
	}
	return nil
}

// set updates existing handle records at their hashed locations.
func (rm registryMap) set(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	for _, item := range storesHandles {
		frds, err := rm.hashmap.findFileRegion(ctx, item.RegistryTable, getIDs(item.IDs))
		if err != nil {
			return err
		}
		// Update the Handles read w/ the items' values.
		for i := range frds {
			// Check if the record in the target file region is different.
			if !frds[i].handle.IsEmpty() && frds[i].handle.LogicalID != item.IDs[i].LogicalID {
				// Fail if the record on target is different.
				return fmt.Errorf("registryMap.set failed, an item(target lid=%v) at offset=%v is different (source lid=%v)",
					frds[i].handle.LogicalID, frds[i].getOffset(), item.IDs[i].LogicalID)
			}
			// Update the handle with incoming.
			frds[i].handle = item.IDs[i]
		}

		if err := rm.hashmap.updateFileRegion(ctx, frds); err != nil {
			return err
		}
	}
	return nil
}

// fetch retrieves handle records by logical ID from on-disk files.
func (rm registryMap) fetch(ctx context.Context, keys []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	result := make([]sop.RegistryPayload[sop.Handle], 0, len(keys))
	for _, k := range keys {
		handles, err := rm.hashmap.fetch(ctx, k.RegistryTable, k.IDs)
		if err != nil {
			return nil, fmt.Errorf("registryMap.fetch failed, details: %w", err)
		}
		result = append(result, sop.RegistryPayload[sop.Handle]{
			RegistryTable: k.RegistryTable,
			IDs:           handles,
		})
	}
	return result, nil
}

// remove marks the specified handle records as deleted, making their slots reusable.
func (rm registryMap) remove(ctx context.Context, keys []sop.RegistryPayload[sop.UUID]) error {
	// Individually delete the file area occupied by the handle so we don't create "lock pressure".
	for _, key := range keys {

		frds, err := rm.hashmap.findFileRegion(ctx, key.RegistryTable, key.IDs)
		if err != nil {
			return err
		}
		for i := range frds {
			// If read handle is empty, it means the item is already marked deleted in disk.
			if frds[i].handle.IsEmpty() {
				// Fail if there is no record on target, can't delete a missing item.
				return fmt.Errorf("registryMap.remove failed, an item at offset=%v was not found, can't delete a missing item", frds[i].getOffset())
			}
			// Check if the record in the target file region is different.
			if frds[i].handle.LogicalID != key.IDs[i] {
				// Fail if the found record on target is different.
				return fmt.Errorf("registryMap.remove failed, an item(target lid=%v) at offset=%v is different (source lid=%v)",
					frds[i].handle.LogicalID, frds[i].getOffset(), key.IDs[i])
			}
		}

		if err := rm.hashmap.markDeleteFileRegion(ctx, frds); err != nil {
			return err
		}
	}
	return nil
}

// close releases all open file handles associated with this registry map.
func (rm registryMap) close() error {
	return rm.hashmap.close()
}

func getIDs(items []sop.Handle) []sop.UUID {
	IDs := make([]sop.UUID, len(items))
	for i := range items {
		IDs[i] = items[i].LogicalID
	}
	return IDs
}
