package common

import (
	"context"
	"fmt"
	log "log/slog"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

const (
	priorityRollbackCheckIntervalSeconds      = 5 * 60
	priorityRollbackQuickCheckIntervalSeconds = 2 * 60
	cleanupCheckIntervalMinutes               = 4 * 60
	cleanupQuickCheckIntervalMinutes          = 5
)

func (t *Transaction) cleanup(ctx context.Context) error {
	// Cleanup resources not needed anymore.
	if err := t.logger.log(ctx, deleteObsoleteEntries, nil); err != nil {
		return err
	}
	obsoleteEntries := t.getToBeObsoleteEntries()
	t.deleteObsoleteEntries(ctx, obsoleteEntries.First, obsoleteEntries.Second)

	if err := t.logger.log(ctx, deleteTrackedItemsValues, nil); err != nil {
		return err
	}
	t.deleteTrackedItemsValues(ctx, t.getObsoleteTrackedItemsValues())

	// Remove unneeded transaction logs since commit is done.
	t.logger.removeLogs(ctx)
	return nil
}

func (t *Transaction) getToBeObsoleteEntries() sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]] {
	// Cleanup resources not needed anymore.
	unusedNodeIDs := make([]sop.BlobsPayload[sop.UUID], 0, len(t.updatedNodeHandles)+len(t.removedNodeHandles))
	for i := range t.updatedNodeHandles {
		blobsIDs := sop.BlobsPayload[sop.UUID]{
			BlobTable: t.updatedNodeHandles[i].BlobTable,
			Blobs:     make([]sop.UUID, len(t.updatedNodeHandles[i].IDs)),
		}
		for ii := range t.updatedNodeHandles[i].IDs {
			// Since we've flipped the inactive to active, the new inactive ID is to be flushed out of Redis cache.
			blobsIDs.Blobs[ii] = t.updatedNodeHandles[i].IDs[ii].GetInActiveID()
		}
		unusedNodeIDs = append(unusedNodeIDs, blobsIDs)
	}

	// Package the logically deleted IDs for actual physical deletes.
	deletedIDs := make([]sop.RegistryPayload[sop.UUID], len(t.removedNodeHandles))
	for i := range t.removedNodeHandles {
		deletedIDs[i].RegistryTable = t.removedNodeHandles[i].RegistryTable
		deletedIDs[i].IDs = make([]sop.UUID, len(t.removedNodeHandles[i].IDs))
		blobsIDs := sop.BlobsPayload[sop.UUID]{
			BlobTable: t.removedNodeHandles[i].BlobTable,
			Blobs:     make([]sop.UUID, len(t.removedNodeHandles[i].IDs)),
		}
		for ii := range t.removedNodeHandles[i].IDs {
			// Removed nodes are marked deleted, thus, its active node ID can be safely removed.
			deletedIDs[i].IDs[ii] = t.removedNodeHandles[i].IDs[ii].LogicalID
			blobsIDs.Blobs[ii] = t.removedNodeHandles[i].IDs[ii].GetActiveID()
		}
		unusedNodeIDs = append(unusedNodeIDs, blobsIDs)
	}

	return sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
		First:  deletedIDs,
		Second: unusedNodeIDs,
	}
}

// cleanupMaxDuration bounds how long cleanup after a failed commit may run.
const cleanupMaxDuration = 5 * time.Minute

// cleanupContext returns a context for releasing locks and undoing work after a
// failure. It keeps the caller's values but not its cancellation. A caller whose
// context was canceled, for example because a sibling goroutine failed, still
// has locks to release. With the canceled context every cache and registry call
// fails at once and the locks stay held until their TTL, up to the transaction's
// maxTime, which makes every other transaction on those nodes wait.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupMaxDuration)
}

func (t *Transaction) rollback(ctx context.Context, rollbackTrackedItemsValues bool) error {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	var lastErr error

	// Rollback pre commit logged items.
	if t.logger.committedState == addActivelyPersistedItem {
		itemsForDelete := t.getForRollbackTrackedItemsValues()
		if err := t.deleteTrackedItemsValues(ctx, itemsForDelete); err != nil {
			lastErr = err
		}
		// Transaction got rolled back, no need for the logs.
		t.logger.removeLogs(ctx)
		// Rewind the transaction log in case retry will check it.
		t.logger.log(ctx, unknown, nil)
		return lastErr
	}

	// Rollback on commit logged items.
	if t.logger.committedState > finalizeCommit {
		// This state should not be reached and rollback invoked, but return an error about it, in case.
		return fmt.Errorf("transaction got committed, 'can't rollback it")
	}

	if t.logger.committedState >= beforeFinalize {
		// We just need to remove the priority log file in live rollback.
		if err := t.logger.PriorityLog().Remove(ctx, t.GetID()); err != nil {
			lastErr = err
		}
	}

	updatedNodes, removedNodes, addedNodes, _, rootNodes := t.classifyModifiedNodes()

	if t.logger.committedState > commitStoreInfo {
		rollbackStoresInfo := t.getRollbackStoresInfo()
		var storesToUpdate []sop.StoreInfo
		for i, s := range rollbackStoresInfo {
			if !t.btreesBackend[i].created {
				storesToUpdate = append(storesToUpdate, s)
			}
		}
		if len(storesToUpdate) > 0 {
			if _, err := t.StoreRepository.Update(ctx, storesToUpdate); err != nil {
				lastErr = err
			}
		}
	}
	if t.logger.committedState > commitAddedNodes {
		bibs := convertToBlobRequestPayload(addedNodes)
		vids := convertToRegistryRequestPayload(addedNodes)
		bv := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}
		if err := t.btreesBackend[0].nodeRepository.rollbackAddedNodes(ctx, bv); err != nil {
			lastErr = err
		}
	}
	if t.logger.committedState > commitRemovedNodes {
		vids := convertToRegistryRequestPayload(removedNodes)
		if err := t.btreesBackend[0].nodeRepository.rollbackRemovedNodes(ctx, t.nodesKeysExist(), vids); err != nil {
			lastErr = err
		}
	}
	if t.logger.committedState > commitUpdatedNodes {
		vids := convertToRegistryRequestPayload(updatedNodes)
		if err := t.btreesBackend[0].nodeRepository.rollbackUpdatedNodes(ctx, t.nodesKeysExist(), vids); err != nil {
			lastErr = err
		}
	}
	// Safe to release the nodes keys' locks so other(s) waiting can get served.
	t.unlockNodesKeys(ctx)
	if t.logger.committedState > commitNewRootNodes {
		bibs := convertToBlobRequestPayload(rootNodes)
		vids := convertToRegistryRequestPayload(rootNodes)
		bv := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}
		if err := t.btreesBackend[0].nodeRepository.rollbackNewRootNodes(ctx, bv); err != nil {
			lastErr = err
		}
	}
	if rollbackTrackedItemsValues && t.logger.committedState >= commitTrackedItemsValues {
		itemsForDelete := t.getForRollbackTrackedItemsValues()
		if err := t.deleteTrackedItemsValues(ctx, itemsForDelete); err != nil {
			lastErr = err
		}
	}
	if t.logger.committedState >= lockTrackedItems {
		if err := t.unlockTrackedItems(ctx); err != nil {
			lastErr = err
		}
	}

	if t.logger.committedState >= createStore {
		for i := range t.btreesBackend {
			if t.btreesBackend[i].created {
				if err := t.StoreRepository.Remove(ctx, t.btreesBackend[i].getStoreInfo().Name); err != nil {
					lastErr = err
				}
			}
		}
	}

	// Transaction got rolled back, no need for the logs.
	t.logger.removeLogs(ctx)
	// Rewind the transaction log in case retry will check it.
	t.logger.log(ctx, unknown, nil)

	return lastErr
}

func (t *Transaction) commitTrackedItemsValues(ctx context.Context) error {
	for i := range t.btreesBackend {
		if err := t.btreesBackend[i].commitTrackedItemsValues(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transaction) getForRollbackTrackedItemsValues() []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]] {
	r := make([]sop.Tuple[bool, sop.BlobsPayload[sop.UUID]], 0, 5)
	for i := range t.btreesBackend {
		itemsForDelete := t.btreesBackend[i].getForRollbackTrackedItemsValues()
		if itemsForDelete != nil && len(itemsForDelete.Blobs) > 0 {
			r = append(r, sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
				First:  t.btreesBackend[i].getStoreInfo().IsValueDataGloballyCached,
				Second: *itemsForDelete,
			})
		}
	}
	return r
}

func (t *Transaction) getObsoleteTrackedItemsValues() []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]] {
	r := make([]sop.Tuple[bool, sop.BlobsPayload[sop.UUID]], 0, 5)
	for i := range t.btreesBackend {
		itemsForDelete := t.btreesBackend[i].getObsoleteTrackedItemsValues()
		if itemsForDelete != nil && len(itemsForDelete.Blobs) > 0 {
			r = append(r, sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]{
				First:  t.btreesBackend[i].getStoreInfo().IsValueDataGloballyCached,
				Second: *itemsForDelete,
			})
		}
	}
	return r
}

func (t *Transaction) deleteTrackedItemsValues(ctx context.Context, itemsForDelete []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]) error {
	var lastErr error
	for i := range itemsForDelete {
		// First field of the Tuple specifies whether we need to delete from Redis cache the blob IDs specified in Second.
		if itemsForDelete[i].First {
			for ii := range itemsForDelete[i].Second.Blobs {
				if _, err := t.l2Cache.Delete(ctx, []string{formatItemKey(itemsForDelete[i].Second.Blobs[ii].String())}); err != nil {
					lastErr = err
				}
			}
		}
		if err := t.blobStore.Remove(ctx, []sop.BlobsPayload[sop.UUID]{itemsForDelete[i].Second}); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Checks if fetched items are intact.
func (t *Transaction) commitForReaderTransaction(ctx context.Context) error {
	if t.mode == sop.ForWriting {
		return nil
	}
	if !t.hasTrackedItems() {
		return nil
	}
	// For a reader transaction, conflict check is enough.
	startTime := sop.Now()
	for {
		log.Debug(fmt.Sprintf("inside reader trans phase 2 commit, tid: %v", t.GetID()))

		if err := t.timedOut(ctx, startTime); err != nil {
			return err
		}
		// Check items if have not changed since fetching.
		_, _, _, fetchedNodes, _ := t.classifyModifiedNodes()
		if ok, err := t.btreesBackend[0].nodeRepository.areFetchedItemsIntact(ctx, fetchedNodes); err != nil {
			return err
		} else if ok {
			log.Debug(fmt.Sprintf("reader trans phase 2 commit succeeded, tid: %v", t.GetID()))
			return nil
		}

		sop.RandomSleep(ctx)

		log.Debug(fmt.Sprintf("reader trans phase 2 commit before 'refetchAndMergeModifications', tid: %v", t.GetID()))

		// Recreate the fetches on latest committed nodes & check if fetched Items are unchanged.
		if err := t.refetchAndMergeModifications(ctx); err != nil {
			return err
		}
	}
}

// Use tracked Items to refetch their Nodes(using B-Tree) and merge the changes in, if there is no conflict.
func (t *Transaction) refetchAndMergeModifications(ctx context.Context) error {
	log.Debug("same node(s) are being modified elsewhere, 'will refetch and re-merge changes in...")
	for i := range t.btreesBackend {
		if err := t.btreesBackend[i].refetchAndMerge(ctx); err != nil {
			return err
		}
	}
	return nil
}

// classifyModifiedNodes will classify modified Nodes into 5 kinds & return them:
// a. updated Nodes, b. removed Nodes, c. added Nodes, d. fetched Nodes, e. root Nodes.
func (t *Transaction) classifyModifiedNodes() ([]sop.Tuple[*sop.StoreInfo, []any],
	[]sop.Tuple[*sop.StoreInfo, []any],
	[]sop.Tuple[*sop.StoreInfo, []any],
	[]sop.Tuple[*sop.StoreInfo, []any],
	[]sop.Tuple[*sop.StoreInfo, []any]) {
	var storesUpdatedNodes, storesRemovedNodes, storesAddedNodes, storesFetchedNodes, storesRootNodes []sop.Tuple[*sop.StoreInfo, []any]
	for _, s := range t.btreesBackend {
		var updatedNodes, removedNodes, addedNodes, fetchedNodes, rootNodes []any
		for _, cacheNode := range s.nodeRepository.localCache {
			// Allow newly created root nodes to get merged between transactions.
			if s.nodeRepository.count == 0 &&
				cacheNode.action == addAction && s.getStoreInfo().RootNodeID == cacheNode.node.(btree.MetaDataType).GetID() {
				rootNodes = append(rootNodes, cacheNode.node)
				continue
			}
			switch cacheNode.action {
			case updateAction:
				updatedNodes = append(updatedNodes, cacheNode.node)
			case removeAction:
				removedNodes = append(removedNodes, cacheNode.node)
			case addAction:
				addedNodes = append(addedNodes, cacheNode.node)
			case getAction:
				fetchedNodes = append(fetchedNodes, cacheNode.node)
			}
		}
		if len(updatedNodes) > 0 {
			storesUpdatedNodes = append(storesUpdatedNodes, sop.Tuple[*sop.StoreInfo, []any]{
				First:  s.getStoreInfo(),
				Second: updatedNodes,
			})
		}
		if len(removedNodes) > 0 {
			storesRemovedNodes = append(storesRemovedNodes, sop.Tuple[*sop.StoreInfo, []any]{
				First:  s.getStoreInfo(),
				Second: removedNodes,
			})
		}
		if len(addedNodes) > 0 {
			storesAddedNodes = append(storesAddedNodes, sop.Tuple[*sop.StoreInfo, []any]{
				First:  s.getStoreInfo(),
				Second: addedNodes,
			})
		}
		if len(fetchedNodes) > 0 {
			storesFetchedNodes = append(storesFetchedNodes, sop.Tuple[*sop.StoreInfo, []any]{
				First:  s.getStoreInfo(),
				Second: fetchedNodes,
			})
		}
		if len(rootNodes) > 0 {
			storesRootNodes = append(storesRootNodes, sop.Tuple[*sop.StoreInfo, []any]{
				First:  s.getStoreInfo(),
				Second: rootNodes,
			})
		}
	}
	return storesUpdatedNodes, storesRemovedNodes, storesAddedNodes, storesFetchedNodes, storesRootNodes
}

func (t *Transaction) commitStores(ctx context.Context) ([]sop.StoreInfo, error) {
	stores, modified := t.getCommitStoresInfo()
	if !modified {
		return stores, nil
	}
	return t.StoreRepository.Update(ctx, stores)
}

func (t *Transaction) getCommitStoresInfo() ([]sop.StoreInfo, bool) {
	stores := make([]sop.StoreInfo, 0, len(t.btreesBackend))
	var modified bool
	for i := range t.btreesBackend {
		store := t.btreesBackend[i].getStoreInfo()
		s2 := *store
		// Compute the count delta so Store Repository can reconcile for commit.
		s2.CountDelta = s2.Count - t.btreesBackend[i].nodeRepository.count
		if s2.CountDelta != 0 {
			modified = true
			s2.Timestamp = sop.Now().UnixMilli()
			stores = append(stores, s2)
		}
	}
	return stores, modified
}

func (t *Transaction) getRollbackStoresInfo() []sop.StoreInfo {
	stores := make([]sop.StoreInfo, len(t.btreesBackend))
	for i := range t.btreesBackend {
		store := t.btreesBackend[i].getStoreInfo()
		s2 := *store
		// Compute the count delta so Store Repository can reconcile for rollback.
		s2.CountDelta = t.btreesBackend[i].nodeRepository.count - s2.Count
		stores[i] = s2
	}
	return stores
}

func (t *Transaction) hasTrackedItems() bool {
	for _, s := range t.btreesBackend {
		if s.hasTrackedItems() {
			return true
		}
	}
	return false
}

// Check Tracked items for conflict, this pass is to remove any race condition.
func (t *Transaction) checkTrackedItems(ctx context.Context) error {
	for _, s := range t.btreesBackend {
		if s.hasTrackedItems() {
			if err := s.checkTrackedItems(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Transaction) lockTrackedItems(ctx context.Context) error {
	for _, s := range t.btreesBackend {
		if s.hasTrackedItems() {
			if err := s.lockTrackedItems(ctx, t.maxTime); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Transaction) unlockTrackedItems(ctx context.Context) error {
	var lastErr error
	for _, s := range t.btreesBackend {
		if s.hasTrackedItems() {
			if err := s.unlockTrackedItems(ctx); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// Delete the registry entries and unused node blobs.
func (t *Transaction) deleteObsoleteEntries(ctx context.Context,
	deletedRegistryIDs []sop.RegistryPayload[sop.UUID], unusedNodeIDs []sop.BlobsPayload[sop.UUID]) error {
	var lastErr error
	if len(unusedNodeIDs) > 0 {
		// Delete from Redis & BlobStore the unused/inactive nodes.
		for i := range unusedNodeIDs {
			if _, err := t.l1Cache.DeleteNodes(ctx, unusedNodeIDs[i].Blobs); err != nil {
				lastErr = err
				log.Warn(fmt.Sprintf("Redis delete failed, details: %v", err))
			}
		}
		if err := t.blobStore.Remove(ctx, unusedNodeIDs); err != nil {
			lastErr = err
		}
		// End of block.
	}
	// Delete from registry the deleted Registry IDs (it manages redis cache internally).
	if err := t.registry.Remove(ctx, deletedRegistryIDs); err != nil {
		lastErr = err
	}
	return lastErr
}

func (t *Transaction) timedOut(ctx context.Context, startTime time.Time) error {
	return sop.TimedOut(ctx, "transaction", startTime, t.maxTime)
}

func (t *Transaction) unlockNodesKeys(ctx context.Context) error {
	if t.nodesKeys == nil {
		return nil
	}
	err := t.l2Cache.Unlock(ctx, t.nodesKeys)
	t.nodesKeys = nil
	return err
}

func (t *Transaction) nodesKeysExist() bool {
	return len(t.nodesKeys) > 0
}

// Return true if nodesKeys is nil or all keys are still locked.
func (t *Transaction) nodesKeysNilOrLocked(ctx context.Context) (bool, error) {
	if len(t.nodesKeys) == 0 {
		return true, nil
	}
	if ok, err := t.l2Cache.IsLocked(ctx, t.nodesKeys); !ok || err != nil {
		return false, fmt.Errorf("node locks lost or expired during commit phase")
	}
	return true, nil
}

func (t *Transaction) mergeNodesKeys(ctx context.Context, updatedNodes []sop.Tuple[*sop.StoreInfo, []any], removedNodes []sop.Tuple[*sop.StoreInfo, []any]) {
	// Create lock keys so we can lock updated & removed handles then unlock them later when locks no longer needed.
	// Keys are sorted by UUID as high, low int64 bit pair so we can order the cache lock call in a uniform manner and thus, reduce risk of dead lock.
	if len(updatedNodes) == 0 && len(removedNodes) == 0 {
		// Release the held lock for a node key that we no longer care about.
		t.l2Cache.Unlock(ctx, t.nodesKeys)
		t.nodesKeys = nil
		return
	}

	lids := extractUUIDs(updatedNodes)
	rids := extractUUIDs(removedNodes)
	log.Debug(fmt.Sprintf("mergeNodesKeys: updated lids: %v, removed lids: %v", lids, rids))

	lookupByUUID := inmemory.NewBtree[sop.UUID, *sop.LockKey](true)
	for _, id := range lids {
		lookupByUUID.Add(id, t.l2Cache.CreateLockKeys([]string{id.String()})[0])
	}
	for _, id := range rids {
		lookupByUUID.Add(id, t.l2Cache.CreateLockKeys([]string{id.String()})[0])
	}

	lookupByKeyName := make(map[string]sop.UUID, lookupByUUID.Count())
	lookupByUUID.First()
	for {
		v := lookupByUUID.GetCurrentValue()
		lookupByKeyName[v.Key] = lookupByUUID.GetCurrentKey()
		if !lookupByUUID.Next() {
			break
		}
	}

	for _, nk := range t.nodesKeys {
		if v, ok := lookupByKeyName[nk.Key]; ok {
			lookupByUUID.Update(v, nk)
			continue
		} else {
			log.Info(fmt.Sprintf("Releasing lock on nodeKey(%v) as not part of new set", nk))
			// Release the held lock for a node key that we no longer care about.
			t.l2Cache.Unlock(ctx, []*sop.LockKey{nk})
		}
	}

	// Map into an array of LockKeys sorted by UUID high, low int64 bit values.
	lookupByUUID.First()
	keys := make([]*sop.LockKey, 0, lookupByUUID.Count())
	for {
		keys = append(keys, lookupByUUID.GetCurrentValue())
		if !lookupByUUID.Next() {
			break
		}
	}

	t.nodesKeys = keys
}

var lastOnIdleRunTime int64
var locker = sync.Mutex{}

var lastPriorityOnIdleTime int64
var priorityLocker = sync.Mutex{}
var priorityLogFound bool

var onStartUpFlag bool = true

func (t *Transaction) onStartUp() bool {
	if t.l2Cache == nil {
		return false
	}

	r := onStartUpFlag
	if r && t.l2Cache.GetType() == sop.InMemory {
		onStartUpFlag = false
		return r
	}
	return false
}

// Background task processing for dead transaction logs' restore & cleanup.
// Elegant solution, not so elegant code but works.
func (t *Transaction) onIdle(ctx context.Context) {
	// Required to have a backend btree to do cleanup.
	if len(t.btreesBackend) == 0 {
		return
	}

	t.processNewerPriorityLogsLocksResurrection(ctx)
	t.processPriorityRollbackOnRestart(ctx)
	t.processScheduledPriorityRollback(ctx)
	t.processExpiredLogs(ctx)
}

func (t *Transaction) processNewerPriorityLogsLocksResurrection(ctx context.Context) {
	// We only support locks' resurrection when in Clustered mode, i.e. - Redis is the L2Cache.
	if t.l2Cache.GetType() == sop.InMemory {
		return
	}

	const notRestartedToken = "notrestarted"
	const slideTime = 24 * time.Hour

	// Check if the token exists. If it does, Redis is healthy (or at least hasn't restarted since we last checked).
	// We use IsLockedByOthersTTL to check AND extend the TTL, acting as a heartbeat.
	if ok, err := t.l2Cache.IsLockedByOthersTTL(ctx, []string{t.l2Cache.FormatLockKey(notRestartedToken)}, slideTime); ok {
		// Token exists, so we assume no restart occurred. We can skip the resurrection pass.
		return
	} else if err != nil {
		// If Redis failed then most likely, we will fail as well trying to resurrect the locks, just do nothing.
		log.Warn(fmt.Sprintf("processNewerPriorityLogsLocksResurrection: redis read failed, details: %v", err))
		return
	}

	// Token is missing! This implies a Redis restart (or first run).
	// Attempt to acquire the token to become the coordinator for this resurrection pass.
	lks := t.l2Cache.CreateLockKeys([]string{notRestartedToken})
	ok, _, err := t.l2Cache.DualLock(ctx, slideTime, lks)
	if err != nil {
		log.Warn(fmt.Sprintf("processNewerPriorityLogsLocksResurrection: redis lock on coordinator('%s') failed, details: %v", notRestartedToken, err))
		return
	}
	if !ok {
		// Another process won the lock and will handle the resurrection.
		return
	}

	log.Info("onIdle: Redis restart detected (or token expired). Doing priority logs' locks resurrection...")

	// We won the lock. Proceed to resurrect locks for all priority logs to ensure isolation.
	if err := t.logger.resurrectPriorityLogLocks(ctx, t); err != nil {
		// Trigger a failover if a handler is registered; otherwise, just log path state.
		if t.HandleReplicationRelatedError != nil {
			t.HandleReplicationRelatedError(ctx, err, nil, true)
		}
	}
}

func (t *Transaction) processPriorityRollbackOnRestart(ctx context.Context) {
	// If this (standalone!) app restarted, attempt a one-time priority rollback sweep immediately.
	if t.l2Cache != nil && t.logger != nil && t.logger.PriorityLog().IsEnabled() {
		if t.onStartUp() {
			// On restart of standalone app, sweep all priority logs (ignore age) once.
			log.Info("onIdle: cache restarted or on startup, doing priority rollback check(sweep mode)...")
			ctxAll := context.WithValue(ctx, sop.ContextPriorityLogIgnoreAge, true)
			if _, err := t.logger.doPriorityRollbacks(ctxAll, t); err != nil {
				if t.HandleReplicationRelatedError != nil {
					t.HandleReplicationRelatedError(ctx, err, nil, true)
				}
			}
			// Reset the priority log found flag so interval will be at decent time (default).
			priorityLogFound = false
			lastPriorityOnIdleTime = sop.Now().UnixMilli()
		}
	}
}

func (t *Transaction) processScheduledPriorityRollback(ctx context.Context) {
	// Allow only one priority rollback processor.
	// Check every 5 minutes if there are any pending rollbacks that "aged" (5min or older).
	interval := priorityRollbackCheckIntervalSeconds
	// If there is known pending priority log, do more frequent checks.
	if priorityLogFound {
		interval = priorityRollbackQuickCheckIntervalSeconds
	}

	nextRunTime := sop.Now().Add(time.Duration(-interval) * time.Second).UnixMilli()
	if t.logger.PriorityLog().IsEnabled() && lastPriorityOnIdleTime < nextRunTime {
		runTime := func() bool {
			priorityLocker.Lock()
			defer priorityLocker.Unlock()
			if lastPriorityOnIdleTime < nextRunTime {
				lastPriorityOnIdleTime = sop.Now().UnixMilli()
				return true
			}
			return false
		}()
		if runTime {
			log.Info("onIdle: doing scheduled priority rollback check...")
			// Do the priority rollbacks of aged 5mins & older priority logs.
			if found, err := t.logger.doPriorityRollbacks(ctx, t); err != nil {
				// Trigger a failover if a handler is registered; otherwise, just log path state.
				if t.HandleReplicationRelatedError != nil {
					t.HandleReplicationRelatedError(ctx, err, nil, true)
				}
				priorityLogFound = found
			} else {
				priorityLogFound = found
			}
		}
	}
}

func (t *Transaction) processExpiredLogs(ctx context.Context) {
	// If it is known that there is nothing to clean up then do 4hr interval polling,
	// otherwise do shorter interval of 5 minutes, to allow faster cleanup.
	// Having "abandoned" commit is a very rare occurrence.
	interval := cleanupCheckIntervalMinutes
	if hourBeingProcessed != "" {
		interval = cleanupQuickCheckIntervalMinutes
	}
	nextRunTime := sop.Now().Add(time.Duration(-interval) * time.Minute).UnixMilli()
	if lastOnIdleRunTime < nextRunTime {
		runTime := func() bool {
			locker.Lock()
			defer locker.Unlock()
			if lastOnIdleRunTime < nextRunTime {
				lastOnIdleRunTime = sop.Now().UnixMilli()
				return true
			}
			return false
		}()
		if runTime {
			t.logger.processExpiredTransactionLogs(ctx, t)
		}
	}
}
