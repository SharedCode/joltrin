package common

import (
	"context"
	"fmt"
	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/cache"
	"github.com/sharedcode/joltrin/encoding"
)

// Backend facing Node Repository. Part of where the magic happens.
// nodeRepositoryBackend coordinates node lifecycle across:
//   - Local transaction cache (tracked changes)
//   - L1 MRU cache (host-wide MRU, includes Redis-backed metadata)
//   - L2 cache (Redis) for cross-transaction visibility and merging
//   - Blob store for persisted node bodies
// It also interfaces with the registry (virtual IDs) to guard updates and flips active/inactive IDs on commit.

// NodeRepository is the interface that wraps the basic Get, Add, Update, Remove methods.
type NodeRepository interface {
	// Get retrieves a node by its logical ID.
	Get(ctx context.Context, logicalID sop.UUID, target any) (any, error)
	// Add marks a node as new in the repository.
	Add(nodeID sop.UUID, node any)
	// Update modifies an existing node in the repository.
	Update(nodeID sop.UUID, node any)
	// Remove marks a node as removed in the repository.
	Remove(nodeID sop.UUID)
}

// cachedNode represents a node in the cache with its action type.
type cachedNode struct {
	// node is a pointer to btree.Node.
	node   any
	action actionType
}

// nodeRepositoryBackend implementation exposes a standard NodeRepository interface
// that manages b-tree nodes in transaction cache, Redis and File System.
type nodeRepositoryBackend struct {
	transaction *Transaction
	// MRU cache of read but not operated on nodes.
	readNodesCache cache.Cache[sop.UUID, any]
	// cache of all nodes that were operated on, i.e. - created, updated, explitly fetched, removed nodes (and/or its items).
	localCache map[sop.UUID]cachedNode
	// L2 Cache, e.g. - Redis. Used here primarily to allow nodes merging in L2. I.e. - capability to sense
	// changes across different transactions on same or different machines and merge their changes in during transaction commits.
	l2Cache sop.L2Cache
	// L1 Cache is a virtualized in-memory & L2. Used as a global MRU cache of all
	// B-trees (across transactions) running in this host computer.
	l1Cache   *cache.L1Cache
	storeInfo *sop.StoreInfo
	count     int64
}

const (
	readNodesMruMinCapacity = 8
	readNodesMruMaxCapacity = 12
)

// NewNodeRepository initializes caches and wiring for a store's node repository.
func newNodeRepository[TK btree.Ordered, TV any](t *Transaction, storeInfo *sop.StoreInfo) *nodeRepositoryFrontEnd[TK, TV] {
	nr := &nodeRepositoryBackend{
		transaction:    t,
		storeInfo:      storeInfo,
		readNodesCache: cache.NewCache[sop.UUID, any](readNodesMruMinCapacity, readNodesMruMaxCapacity),
		localCache:     make(map[sop.UUID]cachedNode),
		l2Cache:        t.l2Cache,
		l1Cache:        cache.GetGlobalL1Cache(t.l2Cache),
		count:          storeInfo.Count,
	}
	return &nodeRepositoryFrontEnd[TK, TV]{
		nodeRepositoryBackend: nr,
	}
}

// Transaction "session" logic(in NodeRepository):
// Get or Fetch:
// - If not found locally(& no remove marker) & found in blobStore, fetch data & populate local cache(& redis).
//   Return not found if found locally & there is a remove marker on it.
// Add:
// - Add to local cache if not yet, for add to blobStore(& redis) on transaction commit.
//   Mark data as new.
// Update:
// - Update to local cache if not yet, for update to blobStore(& redis) on transaction commit.
//   Mark data as modified if not new.
// Remove:
// - If data is new(found in local cache only), then just remove from local cache.
// - Otherwise, mark data as removed, for actual remove from blobStore(& redis) on transaction commit.

// get returns a node by logical ID. It checks:
//  1. transaction-local cache (with remove-marker handling)
//  2. read MRU
//  3. L1 MRU by handle if before commit
//  4. registry -> active ID, then L1 node cache, else blob store
//
// Retrieved nodes are versioned using registry version and cached locally/MRU.
func (nr *nodeRepositoryBackend) get(ctx context.Context, logicalID sop.UUID, target any) (any, error) {
	if v, ok := nr.localCache[logicalID]; ok {
		if v.action == removeAction {
			return nil, nil
		}
		return v.node, nil
	}
	if v := nr.readNodesCache.Get([]sop.UUID{logicalID}); v[0] != nil {
		return v[0], nil
	}

	// Try to fetch node from L1 cache Nodes MRU prior to "commit" time. On commit time (transaction.phaseDone > 0),
	// we only fetch the Handle from L2 cache to get the "true" record.
	if nr.transaction.phaseDone == 0 {
		if h := nr.l1Cache.Handles.Get([]sop.UUID{logicalID}); len(h) == 1 && !h[0].IsEmpty() {
			if n := nr.l1Cache.GetNodeFromMRU(h[0], target); n != nil {
				target = n
				nr.readNodesCache.Set([]sop.KeyValuePair[sop.UUID, any]{{
					Key: logicalID, Value: target,
				}})
				return target, nil
			}
		}
	}

	h, err := nr.transaction.registry.Get(ctx, []sop.RegistryPayload[sop.UUID]{{
		RegistryTable: nr.storeInfo.RegistryTable,
		CacheDuration: nr.storeInfo.CacheConfig.RegistryCacheDuration,
		IsCacheTTL:    nr.storeInfo.CacheConfig.IsRegistryCacheTTL,
		IDs:           []sop.UUID{logicalID},
	}})
	if err != nil {
		return nil, err
	}
	if len(h) == 0 || len(h[0].IDs) == 0 {
		return nil, nil
	}

	nodeID := h[0].IDs[0].GetActiveID()
	// Fetch node from MRU if it is there.
	var n any
	n, err = nr.l1Cache.GetNode(ctx, h[0].IDs[0], target, nr.storeInfo.CacheConfig.IsNodeCacheTTL, nr.storeInfo.CacheConfig.NodeCacheDuration)
	if err != nil {
		return nil, err
	}
	if n != nil {
		target = n
		target.(btree.MetaDataType).SetVersion(h[0].IDs[0].Version)
		nr.readNodesCache.Set([]sop.KeyValuePair[sop.UUID, any]{{
			Key: logicalID, Value: target,
		}})
		return target, nil
	}

	// Fetch from blobStore and cache to Redis/local.
	var ba []byte
	if ba, err = nr.transaction.blobStore.GetOne(ctx, nr.storeInfo.BlobTable, nodeID); err != nil {
		return nil, err
	}
	encoding.BlobMarshaler.Unmarshal(ba, target)
	target.(btree.MetaDataType).SetVersion(h[0].IDs[0].Version)

	// Put to cache layer this node since it got fetched from blob store.
	nr.l1Cache.SetNode(ctx, nodeID, target, nr.storeInfo.CacheConfig.NodeCacheDuration)
	nr.readNodesCache.Set([]sop.KeyValuePair[sop.UUID, any]{{
		Key: logicalID, Value: target,
	}})
	return target, nil
}

// add marks a node as newly added in the local cache.
func (nr *nodeRepositoryBackend) add(nodeID sop.UUID, node any) {
	nr.localCache[nodeID] = cachedNode{
		action: addAction,
		node:   node,
	}
}

// update stages a node change in local cache, preserving addAction when applicable.
func (nr *nodeRepositoryBackend) update(nodeID sop.UUID, node any) {
	if n := nr.readNodesCache.Get([]sop.UUID{nodeID}); n[0] != nil {
		nr.localCache[nodeID] = cachedNode{
			action: defaultAction,
			node:   n[0],
		}
		nr.readNodesCache.Delete([]sop.UUID{nodeID})
	}
	if v, ok := nr.localCache[nodeID]; ok {
		// Update the node and keep the "action" marker if new, otherwise update to "update" action.
		v.node = node
		if v.action != addAction {
			v.action = updateAction
		}
		nr.localCache[nodeID] = v
		return
	}
	// Treat as add if not in local cache, because it should be there unless node is new.
	nr.localCache[nodeID] = cachedNode{
		action: addAction,
		node:   node,
	}
}

// remove marks a node as removed, or drops it entirely if it was only locally added.
func (nr *nodeRepositoryBackend) remove(nodeID sop.UUID) {
	if n := nr.readNodesCache.Get([]sop.UUID{nodeID}); n[0] != nil {
		nr.localCache[nodeID] = cachedNode{
			action: defaultAction,
			node:   n[0],
		}
		nr.readNodesCache.Delete([]sop.UUID{nodeID})
	}
	if v, ok := nr.localCache[nodeID]; ok {
		if v.action == addAction {
			delete(nr.localCache, nodeID)
			return
		}
		v.action = removeAction
		nr.localCache[nodeID] = v
	}
	// Code should not reach this point, as B-tree will not issue a remove if node is not cached locally.
}

// commitNewRootNodes persists brand new root nodes and registers their virtual IDs.
// Fails fast if a non-empty root exists to force refetch/merge path.
func (nr *nodeRepositoryBackend) commitNewRootNodes(ctx context.Context, nodes []sop.Tuple[*sop.StoreInfo, []any]) (bool, []sop.RegistryPayload[sop.Handle], error) {
	if len(nodes) == 0 {
		return true, nil, nil
	}
	vids := convertToRegistryRequestPayload(nodes)
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return false, nil, err
	}
	blobs := make([]sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]], len(nodes))
	for i := range handles {
		if len(handles[i].IDs) == 0 {
			handles[i].IDs = make([]sop.Handle, len(vids[i].IDs))
		}
		blobs[i].Blobs = make([]sop.KeyValuePair[sop.UUID, []byte], len(handles[i].IDs))
		blobs[i].BlobTable = nodes[i].First.BlobTable
		for ii := range handles[i].IDs {
			// Check if a non-empty root node was found, fail to cause "re-sync & merge".
			if !handles[i].IDs[ii].LogicalID.IsNil() {
				return false, nil, nil
			}
			handles[i].IDs[ii] = sop.NewHandle(vids[i].IDs[ii])
			blobs[i].Blobs[ii].Key = handles[i].IDs[ii].GetActiveID()
			ba, err := encoding.BlobMarshaler.Marshal(nodes[i].Second[ii])
			if err != nil {
				return false, nil, err
			}
			blobs[i].Blobs[ii].Value = ba
		}
	}
	// Persist the nodes blobs to blob store and redis cache.
	if err := nr.transaction.blobStore.Add(ctx, blobs); err != nil {
		return false, nil, err
	}
	for i := range nodes {
		for ii := range nodes[i].Second {
			if err := nr.transaction.l2Cache.SetStruct(ctx, nr.formatKey(handles[i].IDs[ii].GetActiveID().String()),
				nodes[i].Second[ii], nodes[i].First.CacheConfig.NodeCacheDuration); err != nil {
				// Tolerate Redis error, log as Warning.
				log.Warn(fmt.Sprintf("commitNewRootNodes failed redisCache.SetStruct, details: %v", err))
			}
		}
	}
	// Add virtual IDs to registry.
	if err := nr.transaction.registry.Add(ctx, handles); err != nil {
		return false, nil, err
	}
	return true, handles, nil
}

// commitUpdatedNodes allocates inactive physical IDs, guards via registry versions, writes blobs,
// and stages Redis cache updates. Returns false to trigger refetch/merge when conflicts are detected.
func (nr *nodeRepositoryBackend) commitUpdatedNodes(ctx context.Context, nodes []sop.Tuple[*sop.StoreInfo, []any]) (bool, []sop.RegistryPayload[sop.Handle], error) {
	if len(nodes) == 0 {
		return true, nil, nil
	}
	// 1st pass, update the virtual ID registry ensuring the set of nodes are only being modified by us.
	vids := convertToRegistryRequestPayload(nodes)
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return false, nil, err
	}
	for i := range handles {
		if len(handles[i].IDs) != len(vids[i].IDs) {
			log.Debug(fmt.Sprintf("commitUpdatedNodes: mismatch handles length for %d. Expected %d, got %d", i, len(vids[i].IDs), len(handles[i].IDs)))
			return false, nil, nil
		}
	}

	blobs := make([]sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]], len(nodes))
	for i := range handles {
		blobs[i].BlobTable = nodes[i].First.BlobTable
		blobs[i].Blobs = make([]sop.KeyValuePair[sop.UUID, []byte], len(handles[i].IDs))
		for ii := range handles[i].IDs {
			log.Debug(fmt.Sprintf("inside commitUpdatedNodes(%d:%d) forloop blobTable %s UUID %s trying to AllocateID", i, ii, blobs[i].BlobTable, handles[i].IDs[ii].LogicalID.String()))
			// Node with such ID is marked deleted or had been updated since reading it.
			if (handles[i].IDs[ii].IsDeleted && !handles[i].IDs[ii].IsExpiredInactive()) || handles[i].IDs[ii].Version != nodes[i].Second[ii].(btree.MetaDataType).GetVersion() {
				log.Debug(fmt.Sprintf("inside commitUpdatedNodes(%d:%d), exiting, ID marked deleted or was updated since read", i, ii))
				return false, nil, nil
			}
			if handles[i].IDs[ii].IsDeleted && handles[i].IDs[ii].IsExpiredInactive() {
				// In case the handle was marked deleted by an incomplete transaction then reset it back to undo it.
				handles[i].IDs[ii].IsDeleted = false
			}
			// Create new phys. UUID and auto-assign it to the available phys. ID(A or B) "ID slot".
			id := handles[i].IDs[ii].AllocateID()
			if id == sop.NilUUID {
				if handles[i].IDs[ii].IsExpiredInactive() {
					handles[i].IDs[ii].ClearInactiveID()
					// Allocate a new ID after clearing the unused inactive ID.
					id = handles[i].IDs[ii].AllocateID()
				}
			}
			if id == sop.NilUUID {
				// Return false as there is an ongoing update on node by another transaction.
				log.Debug(fmt.Sprintf("inside commitUpdatedNodes(%d:%d), exiting, as another transaction has ongoing update", i, ii))
				return false, nil, nil
			}
			blobs[i].Blobs[ii].Key = id
			ba, err := encoding.BlobMarshaler.Marshal(nodes[i].Second[ii])
			if err != nil {
				return false, nil, err
			}
			blobs[i].Blobs[ii].Value = ba
		}
	}
	log.Debug("outside commitUpdatedNodes forloop trying to AllocateID")

	// Reserve/record the new inactive IDs back in the registry as part of the update pre-commit.
	if err := nr.transaction.registry.UpdateNoLocks(ctx, false, handles); err != nil {
		return false, nil, fmt.Errorf("commitUpdatedNodes failed registry.Update: %w", err)
	}

	// 2nd pass, persist the nodes blobs to blob store and redis cache.
	if err := nr.transaction.blobStore.Add(ctx, blobs); err != nil {
		return false, nil, fmt.Errorf("commitUpdatedNodes failed blobStore.Add: %w", err)
	}
	for i := range nodes {
		for ii := range nodes[i].Second {
			if err := nr.transaction.l2Cache.SetStruct(ctx, nr.formatKey(handles[i].IDs[ii].GetInActiveID().String()), nodes[i].Second[ii], nodes[i].First.CacheConfig.NodeCacheDuration); err != nil {
				log.Warn(fmt.Sprintf("commitUpdatedNodes failed redisCache.SetStruct, details: %v", err))
			}
		}
	}
	return true, handles, nil
}

// commitRemovedNodes marks IDs deleted with a work-in-progress timestamp, deferring physical cleanup.
func (nr *nodeRepositoryBackend) commitRemovedNodes(ctx context.Context, nodes []sop.Tuple[*sop.StoreInfo, []any]) (bool, []sop.RegistryPayload[sop.Handle], error) {
	if len(nodes) == 0 {
		return true, nil, nil
	}
	vids := convertToRegistryRequestPayload(nodes)
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return false, nil, err
	}
	rightNow := sop.Now().UnixMilli()
	for i := range handles {
		for ii := range handles[i].IDs {
			// Node with such ID is already marked deleted, is in-flight change or had been updated since reading it,
			// fail it for "refetch" & retry.
			if handles[i].IDs[ii].IsDeleted || handles[i].IDs[ii].Version != nodes[i].Second[ii].(btree.MetaDataType).GetVersion() {
				return false, nil, nil
			}
			// Mark ID as deleted.
			handles[i].IDs[ii].IsDeleted = true
			handles[i].IDs[ii].WorkInProgressTimestamp = rightNow
		}
	}
	// Persist the handles changes.
	if err := nr.transaction.registry.UpdateNoLocks(ctx, false, handles); err != nil {
		return false, nil, err
	}
	return true, handles, nil
}

// commitAddedNodes registers virtual IDs for new nodes, writes blobs, and updates Redis cache.
func (nr *nodeRepositoryBackend) commitAddedNodes(ctx context.Context, nodes []sop.Tuple[*sop.StoreInfo, []any]) ([]sop.RegistryPayload[sop.Handle], error) {
	/* UUID to Virtual ID story:
	   - (on commit) New(added) nodes will have their IDs converted to virtual ID with empty
	     phys IDs(or same ID with active & virtual ID).
	   - On get, 'will read the Node using currently active ID.
	   - (on commit) On update, 'will save and register the node phys ID to the "inactive ID" part of the virtual ID.
	   - On finalization of commit, inactive will be switched to active (node) IDs.
	*/
	if len(nodes) == 0 {
		return nil, nil
	}
	handles := make([]sop.RegistryPayload[sop.Handle], len(nodes))
	blobs := make([]sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]], len(nodes))
	for i := range nodes {
		handles[i].RegistryTable = nodes[i].First.RegistryTable
		handles[i].IDs = make([]sop.Handle, len(nodes[i].Second))
		handles[i].CacheDuration = nodes[i].First.CacheConfig.RegistryCacheDuration
		blobs[i].BlobTable = nodes[i].First.BlobTable
		blobs[i].Blobs = make([]sop.KeyValuePair[sop.UUID, []byte], len(handles[i].IDs))
		for ii := range nodes[i].Second {
			metaData := nodes[i].Second[ii].(btree.MetaDataType)
			// Add node to blob store.
			h := sop.NewHandle(metaData.GetID())
			// Increment version.
			h.Version++
			blobs[i].Blobs[ii].Key = metaData.GetID()
			ba, err := encoding.BlobMarshaler.Marshal(nodes[i].Second[ii])
			if err != nil {
				return nil, err
			}
			blobs[i].Blobs[ii].Value = ba
			handles[i].IDs[ii] = h
			// Add node to Redis cache.
			if err := nr.transaction.l2Cache.SetStruct(ctx, nr.formatKey(metaData.GetID().String()), nodes[i].Second[ii], nodes[i].First.CacheConfig.NodeCacheDuration); err != nil {
				log.Warn(fmt.Sprintf("commitAddedNodes failed redisCache.SetStruct, details: %v", err))
			}
		}
	}
	// Register virtual IDs(a.k.a. handles).
	if err := nr.transaction.registry.Add(ctx, handles); err != nil {
		return nil, err
	}
	// Add nodes to blob store.
	if err := nr.transaction.blobStore.Add(ctx, blobs); err != nil {
		return nil, err
	}
	return handles, nil
}

// areFetchedItemsIntact revalidates registry versions for nodes read earlier to detect concurrent changes.
func (nr *nodeRepositoryBackend) areFetchedItemsIntact(ctx context.Context, nodes []sop.Tuple[*sop.StoreInfo, []any]) (bool, error) {
	if len(nodes) == 0 {
		return true, nil
	}
	// Check if the Items read for each fetchedNode are intact.
	vids := convertToRegistryRequestPayload(nodes)
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return false, err
	}
	for i := range handles {
		if len(handles[i].IDs) != len(vids[i].IDs) {
			return false, nil
		}
		for ii := range handles[i].IDs {
			// Node with ID had been updated(or deleted) since reading it.
			if handles[i].IDs[ii].Version != nodes[i].Second[ii].(btree.MetaDataType).GetVersion() {
				return false, nil
			}
		}
	}
	return true, nil
}

// rollbackNewRootNodes deletes newly created roots from blob store and cache and unregisters IDs if needed.
func (nr *nodeRepositoryBackend) rollbackNewRootNodes(ctx context.Context, rollbackData sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]) error {
	if len(rollbackData.First) == 0 {
		return nil
	}
	vids := rollbackData.First
	bibs := rollbackData.Second
	if len(vids) == 0 {
		return nil
	}

	var lastErr error
	// Undo on blob store & redis.
	if err := nr.transaction.blobStore.Remove(ctx, bibs); err != nil {
		lastErr = fmt.Errorf("unable to undo new root nodes, %v: %w", bibs, err)
	}
	for i := range vids {
		for ii := range vids[i].IDs {
			if _, err := nr.transaction.l2Cache.Delete(ctx, []string{nr.formatKey(vids[i].IDs[ii].String())}); err != nil {
				if lastErr == nil {
					lastErr = fmt.Errorf("unable to undo new root nodes in redis: %w", err)
				}
				log.Warn(fmt.Sprintf("unable to undo new root nodes in redis: %v", err))
			}
		}
	}

	// If we haven't committed the new root nodes (i.e. moved past the commitNewRootNodes state),
	// then we shouldn't unregister them, as we might not have added them yet (or they might be from another transaction/pre-existing).
	if nr.transaction.logger.committedState <= commitNewRootNodes {
		return lastErr
	}

	// If we're able to commit roots in registry then they are "ours", we need to unregister.
	// We need to check if they exist first because crash can happen before they are registered.
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		lastErr = fmt.Errorf("unable to check new root nodes existence, %v: %w", vids, err)
	} else {
		vidsToRemove := make([]sop.RegistryPayload[sop.UUID], 0, len(vids))
		for i := range handles {
			if len(handles[i].IDs) == 0 {
				continue
			}
			ids := make([]sop.UUID, len(handles[i].IDs))
			for ii := range handles[i].IDs {
				ids[ii] = handles[i].IDs[ii].LogicalID
			}
			vidsToRemove = append(vidsToRemove, sop.RegistryPayload[sop.UUID]{
				RegistryTable: handles[i].RegistryTable,
				IDs:           ids,
			})
		}
		if len(vidsToRemove) > 0 {
			if err := nr.transaction.registry.Remove(ctx, vidsToRemove); err != nil {
				lastErr = fmt.Errorf("unable to undo new root nodes registration, %v: %w", vidsToRemove, err)
			}
		}
	}
	return lastErr
}

// rollbackAddedNodes removes blobs, unregisters IDs, and evicts from Redis.
func (nr *nodeRepositoryBackend) rollbackAddedNodes(ctx context.Context, rollbackData sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]) error {
	vids := rollbackData.First
	bibs := rollbackData.Second
	if len(vids) == 0 {
		return nil
	}
	var lastErr error
	if err := nr.transaction.blobStore.Remove(ctx, bibs); err != nil {
		lastErr = fmt.Errorf("unable to undo added nodes, %v: %w", bibs, err)
	}
	// Unregister nodes IDs.
	if err := nr.transaction.registry.Remove(ctx, vids); err != nil {
		if lastErr != nil {
			lastErr = fmt.Errorf("unable to undo added nodes registration, %v: %w\n%w", vids, err, lastErr)
		} else {
			lastErr = fmt.Errorf("unable to undo added nodes registration, %v: %w", vids, err)
		}
	}
	// Remove nodes from Redis cache.
	for i := range vids {
		for ii := range vids[i].IDs {
			if _, err := nr.transaction.l2Cache.Delete(ctx, []string{nr.formatKey(vids[i].IDs[ii].String())}); err != nil {
				if lastErr == nil {
					lastErr = fmt.Errorf("unable to undo added nodes in redis: %w", err)
				}
				log.Warn(fmt.Sprintf("unable to undo added nodes in redis: %v", err))
			}
		}
	}
	return lastErr
}

// rollbackUpdatedNodes reverts a partial update:
// - deletes any staged inactive blobs that were written
// - clears inactive IDs from the corresponding handles
// - restores registry state using UpdateNoLocks when nodes are already locked by us
// - removes staged entries from Redis
func (nr *nodeRepositoryBackend) rollbackUpdatedNodes(ctx context.Context, nodesAreLocked bool, vids []sop.RegistryPayload[sop.UUID]) error {
	if len(vids) == 0 {
		return nil
	}
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return err
	}
	blobsIDs := make([]sop.BlobsPayload[sop.UUID], len(vids))
	for i := range handles {
		blobsIDs[i].BlobTable = vids[i].BlobTable
		blobsIDs[i].Blobs = make([]sop.UUID, 0, len(handles[i].IDs))
		for ii := range handles[i].IDs {
			if handles[i].IDs[ii].GetInActiveID().IsNil() {
				handles[i].IDs[ii].WorkInProgressTimestamp = 0
				continue
			}
			blobsIDs[i].Blobs = append(blobsIDs[i].Blobs, handles[i].IDs[ii].GetInActiveID())
			handles[i].IDs[ii].ClearInactiveID()
		}
	}
	var lastErr error
	// Undo the nodes blobs to blob store.
	if err = nr.transaction.blobStore.Remove(ctx, blobsIDs); err != nil {
		lastErr = fmt.Errorf("unable to undo updated nodes, %v: %w", blobsIDs, err)
	}
	// Undo changes in virtual ID registry.
	if nodesAreLocked {
		if err = nr.transaction.registry.UpdateNoLocks(ctx, false, handles); err != nil {
			if lastErr != nil {
				lastErr = fmt.Errorf("unable to undo updated nodes registration, %v: %w\n%w", handles, err, lastErr)
			} else {
				lastErr = fmt.Errorf("unable to undo updated nodes registration, %v: %w", handles, err)
			}
		}
	} else {
		if err = nr.transaction.registry.Update(ctx, handles); err != nil {
			if lastErr != nil {
				lastErr = fmt.Errorf("unable to undo updated nodes registration, %v: %w\n%w", handles, err, lastErr)
			} else {
				lastErr = fmt.Errorf("unable to undo updated nodes registration, %v: %w", handles, err)
			}
		}
	}
	// Undo changes in redis.
	for i := range blobsIDs {
		for ii := range blobsIDs[i].Blobs {
			if _, err = nr.transaction.l2Cache.Delete(ctx, []string{nr.formatKey(blobsIDs[i].Blobs[ii].String())}); err != nil {
				if lastErr == nil {
					lastErr = fmt.Errorf("unable to undo updated nodes in redis: %w", err)
				}
				log.Warn(fmt.Sprintf("unable to undo updated nodes in redis: %v", err))
			}
		}
	}
	return lastErr
}

// Delete a list of Nodes from the Blob store & from cache. Used in "dead" (not in-flight) incomplete transactions data cleanup.
func (nr *nodeRepositoryBackend) removeNodes(ctx context.Context, blobsIDs []sop.BlobsPayload[sop.UUID]) error {
	if len(blobsIDs) == 0 {
		return nil
	}
	var lastErr error
	// Undo the nodes blobs to blob store.
	if err := nr.transaction.blobStore.Remove(ctx, blobsIDs); err != nil {
		lastErr = fmt.Errorf("unable to undo updated nodes, %v: %w", blobsIDs, err)
	}
	// Undo changes in redis.
	for i := range blobsIDs {
		for ii := range blobsIDs[i].Blobs {
			if _, err := nr.transaction.l2Cache.Delete(ctx, []string{nr.formatKey(blobsIDs[i].Blobs[ii].String())}); err != nil {
				if lastErr == nil {
					lastErr = fmt.Errorf("unable to undo updated nodes in redis: %w", err)
				}
				log.Warn(fmt.Sprintf("unable to undo updated nodes in redis: %v", err))
			}
		}
	}
	return lastErr
}

// rollbackRemovedNodes clears deleted/timestamp flags in registry to undo a removal mark.
func (nr *nodeRepositoryBackend) rollbackRemovedNodes(ctx context.Context, nodesAreLocked bool, vids []sop.RegistryPayload[sop.UUID]) error {
	if len(vids) == 0 {
		return nil
	}
	handles, err := nr.transaction.registry.Get(ctx, vids)
	if err != nil {
		return fmt.Errorf("unable to fetch removed nodes from registry, %v: %w", vids, err)
	}
	handlesForRollback := make([]sop.RegistryPayload[sop.Handle], len(handles))
	for i := range handles {
		handlesForRollback[i] = sop.RegistryPayload[sop.Handle]{
			RegistryTable: handles[i].RegistryTable,
			IDs:           make([]sop.Handle, 0, len(handles[i].IDs)),
		}
		for ii := range handles[i].IDs {
			// Undo the deleted mark for ID.
			if handles[i].IDs[ii].IsDeleted || handles[i].IDs[ii].WorkInProgressTimestamp > 0 {
				handles[i].IDs[ii].IsDeleted = false
				handles[i].IDs[ii].WorkInProgressTimestamp = 0
				handlesForRollback[i].IDs = append(handlesForRollback[i].IDs, handles[i].IDs[ii])
			}
		}
	}

	// Persist the handles changes.
	if nodesAreLocked {
		if err := nr.transaction.registry.UpdateNoLocks(ctx, false, handlesForRollback); err != nil {
			return fmt.Errorf("unable to undo removed nodes in registry, %v: %w", handlesForRollback, err)
		}
	} else {
		if err := nr.transaction.registry.Update(ctx, handlesForRollback); err != nil {
			return fmt.Errorf("unable to undo removed nodes in registry, %v: %w", handlesForRollback, err)
		}
	}
	return nil
}

// activateInactiveNodes flips inactive IDs to active and bumps versions; marks WIP timestamp for later cleanup.
func (nr *nodeRepositoryBackend) activateInactiveNodes(handles []sop.RegistryPayload[sop.Handle]) ([]sop.RegistryPayload[sop.Handle], error) {
	for i := range handles {
		for ii := range handles[i].IDs {
			// Set the inactive as active ID.
			handles[i].IDs[ii].FlipActiveID()
			// Increment version, we are finalizing the commit for the node.
			handles[i].IDs[ii].Version++
			// Set work in progress timestamp to now as safety. After flipping inactive to active,
			// the previously active ID if not "cleaned up" then this timestamp will allow future
			// transactions to clean it up(self healing).
			handles[i].IDs[ii].WorkInProgressTimestamp = 1
		}
	}
	// All or nothing batch update.
	return handles, nil
}

// touchNodes bumps versions and clears WIP timestamps to finalize updates without flipping IDs.
func (nr *nodeRepositoryBackend) touchNodes(handles []sop.RegistryPayload[sop.Handle]) ([]sop.RegistryPayload[sop.Handle], error) {
	if len(handles) == 0 {
		return nil, nil
	}
	for i := range handles {
		for ii := range handles[i].IDs {
			// Update upsert time, we are finalizing the commit for the node.
			handles[i].IDs[ii].Version++
			handles[i].IDs[ii].WorkInProgressTimestamp = 0
		}
	}
	// All or nothing batch update.
	return handles, nil
}

// Helper mappers for converting between store tuples and registry/blob payloads.
// extractInactiveBlobsIDs returns the blob IDs for any inactive (staged) IDs referenced by the handles.
func extractInactiveBlobsIDs(nodesHandles []sop.RegistryPayload[sop.Handle]) []sop.BlobsPayload[sop.UUID] {
	bibs := make([]sop.BlobsPayload[sop.UUID], len(nodesHandles))
	for i := range nodesHandles {
		bibs[i] = sop.BlobsPayload[sop.UUID]{
			BlobTable: nodesHandles[i].BlobTable,
			Blobs:     make([]sop.UUID, 0, len(nodesHandles[i].IDs)),
		}
		for ii := range nodesHandles[i].IDs {
			if nodesHandles[i].IDs[ii].GetInActiveID().IsNil() {
				continue
			}
			bibs[i].Blobs = append(bibs[i].Blobs, nodesHandles[i].IDs[ii].GetInActiveID())
		}
	}
	return bibs
}

// convertToBlobRequestPayload converts "node + store" tuples into a list of blob-table requests keyed by node IDs.
func convertToBlobRequestPayload(nodes []sop.Tuple[*sop.StoreInfo, []any]) []sop.BlobsPayload[sop.UUID] {
	bibs := make([]sop.BlobsPayload[sop.UUID], len(nodes))
	for i := range nodes {
		bibs[i] = sop.BlobsPayload[sop.UUID]{
			BlobTable: nodes[i].First.BlobTable,
			Blobs:     make([]sop.UUID, len(nodes[i].Second)),
		}
		for ii := range nodes[i].Second {
			bibs[i].Blobs[ii] = nodes[i].Second[ii].(btree.MetaDataType).GetID()
		}
	}
	return bibs
}

// convertToRegistryRequestPayload converts nodes into registry lookups keyed by logical (virtual) IDs.
func convertToRegistryRequestPayload(nodes []sop.Tuple[*sop.StoreInfo, []any]) []sop.RegistryPayload[sop.UUID] {
	vids := make([]sop.RegistryPayload[sop.UUID], len(nodes))
	for i := range nodes {
		vids[i] = sop.RegistryPayload[sop.UUID]{
			RegistryTable: nodes[i].First.RegistryTable,
			BlobTable:     nodes[i].First.BlobTable,
			CacheDuration: nodes[i].First.CacheConfig.RegistryCacheDuration,
			IsCacheTTL:    nodes[i].First.CacheConfig.IsRegistryCacheTTL,
			IDs:           make([]sop.UUID, len(nodes[i].Second)),
		}
		for ii := range nodes[i].Second {
			vids[i].IDs[ii] = nodes[i].Second[ii].(btree.MetaDataType).GetID()
		}
	}
	return vids
}

// extractUUIDs extracts the logical IDs from a list of nodes for convenience in diagnostics/logging.
func extractUUIDs(nodes []sop.Tuple[*sop.StoreInfo, []any]) []sop.UUID {
	uuids := make([]sop.UUID, 0, len(nodes))
	for i := range nodes {
		for ii := range nodes[i].Second {
			uuids = append(uuids, nodes[i].Second[ii].(btree.MetaDataType).GetID())
		}
	}
	return uuids
}

// formatKey builds a namespaced cache key for nodes in Redis/L1.
func (nr *nodeRepositoryBackend) formatKey(k string) string {
	return fmt.Sprintf("N%s", k)
}
