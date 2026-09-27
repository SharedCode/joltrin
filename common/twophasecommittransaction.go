package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
)

const phase1CommitMaxRetryCount = 30

// phase1LockAcquireMaxRetryCount bounds the cheap lock-check-and-sleep loop
// (Lock/IsLocked/DualLock), not the expensive commit-then-rollback loop
// phase1CommitMaxRetryCount guards. Each attempt here is just a lock call
// plus a 20-80ms sop.RandomSleep, so it needs far more iterations than the
// expensive path to cover a realistic contention window: at 30 retries this
// gives up in ~1s, nowhere near enough for two other transactions to finish
// their own multi-step conflict resolution first. 300 retries gives roughly
// 15s of real wall-clock room for that, still small next to the transaction's
// own maxTime ceiling, before failing with a clear, specific error.
const phase1LockAcquireMaxRetryCount = 300

type btreeBackend struct {
	btree          any
	nodeRepository *nodeRepositoryBackend
	// Following are function references because BTree is generic typed for Key & Value,
	// and these functions being references allow the backend to deal without requiring knowing data types.
	refetchAndMerge    func(ctx context.Context) error
	getStoreInfo       func() *sop.StoreInfo
	hasTrackedItems    func() bool
	checkTrackedItems  func(ctx context.Context) error
	lockTrackedItems   func(ctx context.Context, duration time.Duration) error
	unlockTrackedItems func(ctx context.Context) error

	// Manage tracked items' values in separate segments.
	commitTrackedItemsValues         func(ctx context.Context) error
	getForRollbackTrackedItemsValues func() *sop.BlobsPayload[sop.UUID]
	getObsoleteTrackedItemsValues    func() *sop.BlobsPayload[sop.UUID]

	// created is true if the store was created in this transaction.
	created bool
}

// Transaction implements the sop's TwoPhaseTransaction interface.
type Transaction struct {
	id sop.UUID
	// B-Tree instances, & their backend bits, managed within the transaction session.
	btreesBackend []btreeBackend
	// Needed by NodeRepository & ValueDataRepository for Node/Value data merging to the backend storage systems.
	blobStore       sop.BlobStore
	l1Cache         *cache.L1Cache
	l2Cache         sop.L2Cache
	StoreRepository sop.StoreRepository
	// VirtualIDRegistry manages the virtual IDs, a.k.a. "handle".
	registry sop.Registry
	// true if transaction allows upserts & deletes, false(read-only mode) otherwise.
	mode sop.TransactionMode
	// -1 = intial state, 0 = began, 1 = phase 1 commit started, 2 = phase 2 commit or rollback done.
	phaseDone int
	// committed is true if the transaction has been successfully committed.
	committed bool
	maxTime   time.Duration
	logger    *transactionLog

	// Handle replication related error.
	HandleReplicationRelatedError func(ctx context.Context, ioError error, rollbackError error, rollbackSucceeded bool)

	// Phase 1 commit generated objects required for phase 2 commit.
	updatedNodeHandles []sop.RegistryPayload[sop.Handle]
	removedNodeHandles []sop.RegistryPayload[sop.Handle]

	// Phase 1 commit generated objects required for "replication" in phase 2 commit.
	addedNodeHandles   []sop.RegistryPayload[sop.Handle]
	newRootNodeHandles []sop.RegistryPayload[sop.Handle]
	updatedStoresInfo  []sop.StoreInfo

	// Needed for Phase 2 commit for populating MRU cache.
	updatedNodes []sop.Tuple[*sop.StoreInfo, []any]
	addedNodes   []sop.Tuple[*sop.StoreInfo, []any]
	rootNodes    []sop.Tuple[*sop.StoreInfo, []any]

	// Used for transaction level locking.
	nodesKeys []*sop.LockKey

	// onCommitHooks is a list of callbacks to be executed after a successful commit.
	onCommitHooks []func(ctx context.Context) error
}

// NewTwoPhaseCommitTransaction creates a new two-phase commit controller.
// commitMaxDuration limits commit duration; logging enables crash-safe recovery via a transaction log.
// Note: commitMaxDuration is the internal safety cap for commit and lock TTLs; the effective limit is min(ctx deadline, commitMaxDuration).
func NewTwoPhaseCommitTransaction(mode sop.TransactionMode, commitMaxDuration time.Duration,
	blobStore sop.BlobStore, storeRepository sop.StoreRepository, registry sop.Registry, l2Cache sop.L2Cache, transactionLog sop.TransactionLog) (*Transaction, error) {
	if l2Cache == nil {
		return nil, fmt.Errorf("l2Cache can't be nil")
	}
	// Transaction commit time defaults to 15 mins if negative or 0.
	if commitMaxDuration <= 0 {
		commitMaxDuration = time.Duration(15 * time.Minute)
	}
	// Maximum transaction commit time is 1 hour.
	if commitMaxDuration > time.Duration(1*time.Hour) {
		commitMaxDuration = time.Duration(1 * time.Hour)
	}
	l1c := cache.GetGlobalL1Cache(l2Cache)
	t := &Transaction{
		mode:            mode,
		maxTime:         commitMaxDuration,
		StoreRepository: storeRepository,
		registry:        registry,
		l2Cache:         l2Cache,
		l1Cache:         l1c,
		blobStore:       blobStore,
		logger:          newTransactionLogger(transactionLog, true),
		phaseDone:       -1,
		id:              sop.NewUUID(),
	}
	t.logger.transactionID = t.id
	return t, nil
}

func (t *Transaction) Begin(ctx context.Context) error {
	if t.HasBegun() {
		return fmt.Errorf("transaction is ongoing, 'can't begin again")
	}
	if t.phaseDone == 2 {
		return fmt.Errorf("transaction is done, 'create a new one")
	}
	t.phaseDone = 0

	// Service the cleanup of left hanging transactions.
	// We call this at the beginning to proactively resurrect locks or rollback stale transactions
	// from previous crashes, ensuring a cleaner state before this transaction starts its work.
	//
	// Note: A global coordinator ensures that for the entire cluster, only one process pings the
	// Redis 'notrestarted' flag and performs the actual lock resurrection service if indeed needed, maximizing efficiency.
	t.onIdle(ctx)
	return nil
}

// Close releases resources held by the transaction (e.g., registry file handles).
func (t *Transaction) Close() error {
	// Do registry cleanup, e.g. - close all opened files.
	if closeable, ok := t.registry.(io.Closer); ok {
		return closeable.Close()
	}
	return nil
}

// Phase1Commit performs the first phase of 2PC for writer transactions:
// - validates state, takes locks, refetches/merges on contention,
// - persists value blobs and prepares node mutations without finalizing registry updates.
func (t *Transaction) Phase1Commit(ctx context.Context) error {
	if !t.HasBegun() {
		return fmt.Errorf("no transaction to commit, call Begin to start a transaction")
	}
	if t.phaseDone == 2 {
		if t.committed {
			return nil
		}
		return fmt.Errorf("transaction is done, 'create a new one")
	}
	t.phaseDone = 1
	if t.mode == sop.NoCheck {
		return nil
	}
	if t.mode == sop.ForReading {
		return t.commitForReaderTransaction(ctx)
	}
	if err := t.phase1Commit(ctx); err != nil {
		t.phaseDone = 2
		rerr := t.rollback(ctx, true)

		// Allow replication handler to handle error related to replication, e.g. IO error.
		if t.HandleReplicationRelatedError != nil {
			t.HandleReplicationRelatedError(ctx, err, rerr, rerr == nil)
		}

		if rerr != nil {
			return fmt.Errorf("phase 1 commit failed, details: %w, rollback error: %v", err, rerr)
		}
		return fmt.Errorf("phase 1 commit failed, details: %w", err)
	}
	log.Debug("after phase1Commit call")
	return nil
}

// Phase2Commit completes the commit:
// - applies registry updates (root changes, added/updated nodes),
// - populates caches, removes logs, and unlocks resources;
// on failure attempts priority rollback and surfaces the error.
func (t *Transaction) Phase2Commit(ctx context.Context) error {
	if !t.HasBegun() {
		return fmt.Errorf("no transaction to commit, call Begin to start a transaction")
	}
	if t.phaseDone == 0 {
		return fmt.Errorf("phase 1 commit has not been invoke yet")
	}
	if t.phaseDone == 2 {
		if t.committed {
			return nil
		}
		return fmt.Errorf("transaction is done, 'create a new one")
	}
	t.phaseDone = 2

	// Ensure resources are cleaned up or released.
	defer t.Close()
	if t.mode != sop.ForWriting {
		t.committed = true
		return nil
	}
	if err := t.phase2Commit(ctx); err != nil {
		if t.nodesKeysExist() {
			if p1Err := t.logger.priorityRollback(ctx, t.registry, t.GetID()); p1Err != nil {
				log.Warn(fmt.Sprintf("phase 2 commit priorityRollback failed, details: %v", p1Err))
				// Should generate a failover below.
				if se, ok := p1Err.(sop.Error); ok && se.Code == sop.RestoreRegistryFileSectorFailure {
					err = se
				}
			}
			t.unlockNodesKeys(ctx)
		} else {
			if err := t.logger.PriorityLog().Remove(ctx, t.GetID()); err != nil {
				log.Warn(fmt.Sprintf("phase 2 commit priority log remove failed, details: %v", err))
			}
		}

		rerr := t.rollback(ctx, true)

		// Allow replication handler to do failover if needed.
		if t.HandleReplicationRelatedError != nil {
			t.HandleReplicationRelatedError(ctx, err, rerr, rerr == nil)
		}

		if rerr != nil {
			return fmt.Errorf("phase 2 commit failed, details: %w, rollback error: %v", err, rerr)
		}
		return fmt.Errorf("phase 2 commit failed, details: %w", err)
	}
	t.committed = true
	return nil
}

// Rollback aborts the transaction and attempts to undo work recorded in the log.
// It also invokes replication error handlers to enable failover.
func (t *Transaction) Rollback(ctx context.Context, err error) error {
	if t.phaseDone == 2 {
		if t.committed {
			return fmt.Errorf("transaction is already committed, cannot rollback")
		}
		// Transaction is already done. Treat Rollback as idempotent.
		return nil
	}
	if !t.HasBegun() {
		return fmt.Errorf("no transaction to rollback, call Begin to start a transaction")
	}
	// Reset transaction status and mark done to end it without persisting any change.
	t.phaseDone = 2
	if rerr := t.rollback(ctx, true); rerr != nil {
		t.Close()

		// Allow replication handler to handle error related to replication, e.g. IO error.
		if t.HandleReplicationRelatedError != nil {
			t.HandleReplicationRelatedError(ctx, err, rerr, false)
		}

		return fmt.Errorf("rollback failed, details: %w", rerr)
	}
	t.Close()

	// Allow replication handler to handle error related to replication, e.g. IO error.
	if t.HandleReplicationRelatedError != nil {
		t.HandleReplicationRelatedError(ctx, err, nil, true)
	}

	return nil
}

// GetMode returns the transaction's mode (read-only, write, or no-check).
func (t *Transaction) GetMode() sop.TransactionMode {
	return t.mode
}

// Transaction has begun if it is has begun & not yet committed/rolled back.
func (t *Transaction) HasBegun() bool {
	return t.phaseDone >= 0 && t.phaseDone < 2
}

func (t *Transaction) GetStores(ctx context.Context) ([]string, error) {
	return t.StoreRepository.GetAll(ctx)
}

// Returns this transaction's StoreRepository.
func (t *Transaction) GetStoreRepository() sop.StoreRepository {
	return t.StoreRepository
}

func (t *Transaction) GetID() sop.UUID {
	return t.id
}

// CommitMaxDuration returns the configured maximum commit duration for this transaction.
// This is used as the internal cap and lock TTL; the effective runtime cap is min(ctx deadline, this duration).
func (t *Transaction) CommitMaxDuration() time.Duration { return t.maxTime }

// OnCommit registers a callback to be executed after a successful commit.
func (t *Transaction) OnCommit(callback func(ctx context.Context) error) {
	t.onCommitHooks = append(t.onCommitHooks, callback)
}

// phase1Commit coordinates locking, conflict checks, value writes, and
// classifies node mutations, retrying when needed until success or timeout.
func (t *Transaction) phase1Commit(ctx context.Context) error {
	if !t.hasTrackedItems() {
		return nil
	}

	var preCommitTID sop.UUID
	if t.logger.committedState == addActivelyPersistedItem {
		// Keep the pre-commit TID as its logs need to be cleaned up seperately.
		preCommitTID = t.logger.transactionID
	}
	t.logger.transactionID = t.GetID()

	if err := t.logger.log(ctx, lockTrackedItems, nil); err != nil {
		return err
	}
	// Mark session modified items as locked in Redis. If lock or there is conflict, return it as error.
	if err := t.lockTrackedItems(ctx); err != nil {
		return err
	}

	var updatedNodes, removedNodes, addedNodes, fetchedNodes, rootNodes []sop.Tuple[*sop.StoreInfo, []any]
	var updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]

	// Classify modified Nodes into update, remove and add. Updated & removed nodes are processed differently,
	// has to do merging & conflict resolution. Add is simple upsert.
	updatedNodes, removedNodes, addedNodes, fetchedNodes, rootNodes = t.classifyModifiedNodes()

	// Extract lock keys from updated & removed nodes.
	t.mergeNodesKeys(ctx, updatedNodes, removedNodes)

	startTime := sop.Now()
	successful := false
	needsRefetchAndMerge := false
	retryCount := 0
	lockRetryCount := 0

	for !successful {

		var err error
		if err = t.timedOut(ctx, startTime); err != nil {
			return err
		}

		//* Start: Try to lock all updated & removed nodes before moving forward.
		ok, _, err := t.l2Cache.Lock(ctx, t.maxTime, t.nodesKeys)
		if err != nil {
			t.l2Cache.Unlock(ctx, t.nodesKeys)
			log.Error(fmt.Sprintf("Failed to lock nodes in cache: %v", err))
			return err
		}
		if !ok {
			// Unlock in case there are those that got locked.
			t.l2Cache.Unlock(ctx, t.nodesKeys)
			lockRetryCount++
			if lockRetryCount >= phase1LockAcquireMaxRetryCount {
				return fmt.Errorf("phase 1 commit failed to acquire node locks after %d retries", phase1LockAcquireMaxRetryCount)
			}
			sop.RandomSleep(ctx)
			needsRefetchAndMerge = true
			continue
		}

		if ok, err := t.l2Cache.IsLocked(ctx, t.nodesKeys); !ok || err != nil {
			if err != nil {
				log.Error(fmt.Sprintf("cache.IsLocked failed with error: %v", err))
				return err
			}
			log.Debug(fmt.Sprintf("cache.IsLocked didn't confirm nodesKeys are locked, tid: %v", t.GetID()))
			lockRetryCount++
			if lockRetryCount >= phase1LockAcquireMaxRetryCount {
				return fmt.Errorf("phase 1 commit failed to confirm node locks after %d retries", phase1LockAcquireMaxRetryCount)
			}
			sop.RandomSleep(ctx)
			continue
		}

		if needsRefetchAndMerge {
			log.Debug(fmt.Sprintf("before refetchAndMergeModifications, tid: %v", t.GetID()))
			if err := t.refetchAndMergeModifications(ctx); err != nil {
				return err
			}

			if err := t.logger.log(ctx, lockTrackedItems, nil); err != nil {
				return err
			}
			if err = t.lockTrackedItems(ctx); err != nil {
				return err
			}

			updatedNodes, removedNodes, addedNodes, fetchedNodes, rootNodes = t.classifyModifiedNodes()
			t.mergeNodesKeys(ctx, updatedNodes, removedNodes)
			needsRefetchAndMerge = false

			if ok, _, err := t.l2Cache.DualLock(ctx, t.maxTime, t.nodesKeys); !ok || err != nil {
				// Unlock in case there are those that got locked.
				t.l2Cache.Unlock(ctx, t.nodesKeys)
				lockRetryCount++
				if lockRetryCount >= phase1LockAcquireMaxRetryCount {
					return fmt.Errorf("phase 1 commit failed to acquire node locks (dual lock) after %d retries", phase1LockAcquireMaxRetryCount)
				}
				sop.RandomSleep(ctx)
				needsRefetchAndMerge = true
				continue
			}
		}
		//* End: Try to lock all updated & removed nodes before moving forward.

		if err := t.logger.log(ctx, commitTrackedItemsValues, toByteArray(t.getForRollbackTrackedItemsValues())); err != nil {
			return err
		}
		if err := t.commitTrackedItemsValues(ctx); err != nil {
			return err
		}

		// Remove the pre commit logs as not needed anymore from this point.
		if preCommitTID != sop.NilUUID {
			t.logger.TransactionLog.Remove(ctx, preCommitTID)
			preCommitTID = sop.NilUUID
		}

		successful = true

		// Commit new root nodes.
		bibs := convertToBlobRequestPayload(rootNodes)
		vids := convertToRegistryRequestPayload(rootNodes)

		if err := t.logger.log(ctx, commitNewRootNodes, toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
			First: vids, Second: bibs,
		})); err != nil {
			return err
		}
		if successful, t.newRootNodeHandles, err = t.btreesBackend[0].nodeRepository.commitNewRootNodes(ctx, rootNodes); err != nil {
			var se sop.Error
			if errors.As(err, &se) {
				if err := t.handleRegistrySectorLockTimeout(ctx, se); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		if successful {
			// Check for conflict on fetched items.
			if err := t.logger.log(ctx, areFetchedItemsIntact, nil); err != nil {
				return err
			}
			if successful, err = t.btreesBackend[0].nodeRepository.areFetchedItemsIntact(ctx, fetchedNodes); err != nil {
				return err
			}
		}
		if successful {
			// Commit updated nodes.
			if successful, updatedNodesHandles, err = t.btreesBackend[0].nodeRepository.commitUpdatedNodes(ctx, updatedNodes); err != nil {
				var se sop.Error
				if errors.As(err, &se) {
					if err := t.handleRegistrySectorLockTimeout(ctx, se); err != nil {
						return err
					}
				} else {
					return err
				}
			}
			// Log the inactive Blobs' IDs of newly written so we can just easily remove them when cleaning up "dead" transaction logs.
			if err := t.logger.log(ctx, commitUpdatedNodes, toByteArray(extractInactiveBlobsIDs(updatedNodesHandles))); err != nil {
				return err
			}
		}
		// Only do commit removed nodes if successful so far.
		if successful {
			// Commit removed nodes.
			if err := t.logger.log(ctx, commitRemovedNodes, toByteArray(convertToRegistryRequestPayload(removedNodes))); err != nil {
				return err
			}
			if successful, removedNodesHandles, err = t.btreesBackend[0].nodeRepository.commitRemovedNodes(ctx, removedNodes); err != nil {
				return err
			}
		}

		// Only do commit added nodes if successful so far.
		if successful {
			// Commit added nodes.
			if err := t.logger.log(ctx, commitAddedNodes, toByteArray(sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{
				First:  convertToRegistryRequestPayload(addedNodes),
				Second: convertToBlobRequestPayload(addedNodes),
			})); err != nil {
				return err
			}
			if t.addedNodeHandles, err = t.btreesBackend[0].nodeRepository.commitAddedNodes(ctx, addedNodes); err != nil {
				var se sop.Error
				if errors.As(err, &se) {
					if err := t.handleRegistrySectorLockTimeout(ctx, se); err != nil {
						return err
					}
					successful = false
				} else {
					return err
				}
			}
		}

		if !successful {
			retryCount++
			if retryCount >= phase1CommitMaxRetryCount {
				return fmt.Errorf("phase 1 commit exceeded retry limit of %d after rollback", phase1CommitMaxRetryCount)
			}

			// Rollback partial changes.
			if rerr := t.rollback(ctx, false); rerr != nil {
				return fmt.Errorf("phase 1 commit failed, then rollback errored with: %w", rerr)
			}

			log.Warn("commit failed, refetch, remerge & another commit try will occur after randomSleep")

			needsRefetchAndMerge = true
			sop.RandomSleep(ctx)
		}
	}

	log.Debug(fmt.Sprintf("phase 1 commit loop done, tid: %v", t.GetID()))

	// Commit stores update(CountDelta apply).
	var err error
	if err := t.logger.log(ctx, commitStoreInfo, toByteArray(t.getRollbackStoresInfo())); err != nil {
		return err
	}
	if t.updatedStoresInfo, err = t.commitStores(ctx); err != nil {
		return err
	}

	// Mark that store info commit succeeded, so it can get rolled back if rollback occurs.
	if err := t.logger.log(ctx, beforeFinalize, nil); err != nil {
		return err
	}

	if len(updatedNodesHandles) > 0 || len(removedNodesHandles) > 0 {
		// Log the updated nodes & removed nodes handles for use in their rollback in File System Registry implementation.
		// Cassandra tlogger will ignore this as it has its own "all or nothing" feature handled inside Cassandra cluster.
		if err := t.logger.PriorityLog().Add(ctx, t.GetID(), toByteArray(append(updatedNodesHandles, removedNodesHandles...))); err != nil {
			return err
		}
	}

	// Prepare to switch to active "state" the (inactive) updated Nodes, in phase2Commit.
	uh, err := t.btreesBackend[0].nodeRepository.activateInactiveNodes(updatedNodesHandles)
	if err != nil {
		return err
	}

	// Prepare to update upsert time of removed nodes to signal that they are finalized, in phase2Commit.
	rh, err := t.btreesBackend[0].nodeRepository.touchNodes(removedNodesHandles)
	if err != nil {
		return err
	}

	// In case race condition exists, we remove it here by checking our tracked items' lock integrity.
	if err := t.checkTrackedItems(ctx); err != nil {
		return err
	}

	// Ensure that we still hold the locks on the nodes' keys before finalizing the commit.
	if ok, err := t.nodesKeysNilOrLocked(ctx); !ok || err != nil {
		if ok, _, _ := t.l2Cache.DualLock(ctx, t.maxTime, t.nodesKeys); !ok {
			log.Warn(fmt.Sprintf("Transaction maxTime(%v), details: %v", t.maxTime, err))
			return err
		}
	}

	log.Debug(fmt.Sprintf("phase 1 commit ends, tid: %v", t.GetID()))

	// Populate the phase 2 commit required objects.
	t.updatedNodeHandles = uh
	t.removedNodeHandles = rh

	t.addedNodes = addedNodes
	t.rootNodes = rootNodes
	t.updatedNodes = updatedNodes

	return nil
}

// phase2Commit finalizes the commit process and does cleanup afterwards.
func (t *Transaction) phase2Commit(ctx context.Context) error {

	f := t.getToBeObsoleteEntries()
	s := t.getObsoleteTrackedItemsValues()
	var pl sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]
	if len(f.First) > 0 || len(f.Second) > 0 || len(s) > 0 {
		pl = sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]{
			First:  f,
			Second: s,
		}
	}
	// Log the "finalizeCommit" step & parameters, useful for rollback.
	if err := t.logger.log(ctx, finalizeCommit, toByteArray(pl)); err != nil {
		t.unlockNodesKeys(ctx)
		return err
	}

	// Replicate to passive target paths.
	tr := sop.NewTaskRunner(ctx, -1)

	if len(t.updatedNodeHandles) > 0 || len(t.removedNodeHandles) > 0 {
		// The last step to consider a completed commit.
		if err := t.registry.UpdateNoLocks(ctx, true, append(t.updatedNodeHandles, t.removedNodeHandles...)); err != nil {
			return err
		}
		tr.Go(func() error {
			// Also, remove the special priority log file as it is no longer needed.
			if err := t.logger.PriorityLog().Remove(tr.GetContext(), t.GetID()); err != nil {
				log.Warn(fmt.Sprintf("removing priority log for tid %v failed, details: %v", t.GetID(), err))
			}
			return nil
		})
	}

	tr.Go(func() error {
		if err := t.registry.Replicate(tr.GetContext(), t.newRootNodeHandles, t.addedNodeHandles, t.updatedNodeHandles, t.removedNodeHandles); err != nil {
			log.Warn(fmt.Sprintf("registry.Replicate failed but will not fail commit(phase 2 succeeded), details: %v", err))
		}
		return nil
	})
	tr.Go(func() error {
		if err := t.StoreRepository.Replicate(tr.GetContext(), t.updatedStoresInfo); err != nil {
			log.Warn(fmt.Sprintf("storeRepository.Replicate failed but will not fail commit(phase 2 succeeded), details: %v", err))
		}
		return nil
	})
	tr.Go(func() error {
		if err := t.logger.PriorityLog().LogCommitChanges(tr.GetContext(), t.updatedStoresInfo, t.newRootNodeHandles, t.addedNodeHandles, t.updatedNodeHandles, t.removedNodeHandles); err != nil {
			log.Warn(fmt.Sprintf("logger.LogCommitChanges failed but will not fail commit(phase 2 succeeded), details: %v", err))
		}
		return nil
	})
	t.populateMru(ctx)

	// Wait before proceeding to let replication to complete.
	tr.Wait()

	// Let other transactions get a lock on these updated & removed nodes' keys we've locked.
	t.unlockNodesKeys(ctx)

	// Unlock the items in Redis since technically, "commit" is done.
	if err := t.unlockTrackedItems(ctx); err != nil {
		// Just log as warning any error as at this point, commit is already finalized.
		// Any partial changes before failure in unlock tracked items will just expire in Redis.
		log.Warn(err.Error())
	}

	// Cleanup transaction logs & obsolete entries.
	t.cleanup(ctx)

	log.Debug(fmt.Sprintf("phase 2 commit ends, tid: %v", t.GetID()))

	for _, hook := range t.onCommitHooks {
		if err := hook(ctx); err != nil {
			log.Warn(fmt.Sprintf("onCommit hook failed, details: %v", err))
		}
	}

	return nil
}

func (t *Transaction) populateMru(ctx context.Context) {
	// Sync up the cache layers.
	t.updateVersionThenPopulateMru(ctx, t.addedNodeHandles, t.addedNodes)
	t.updateVersionThenPopulateMru(ctx, t.updatedNodeHandles, t.updatedNodes)
	t.updateVersionThenPopulateMru(ctx, t.newRootNodeHandles, t.rootNodes)
}

func (t *Transaction) updateVersionThenPopulateMru(ctx context.Context, handles []sop.RegistryPayload[sop.Handle], nodes []sop.Tuple[*sop.StoreInfo, []any]) {
	for i := range nodes {
		for ii := range nodes[i].Second {
			target := nodes[i].Second[ii]
			target.(btree.MetaDataType).SetVersion(handles[i].IDs[ii].Version)
			t.l1Cache.SetNodeToMRU(ctx, handles[i].IDs[ii].GetActiveID(), target, nodes[i].First.CacheConfig.NodeCacheDuration)
		}
	}
}

func (t *Transaction) handleRegistrySectorLockTimeout(ctx context.Context, err sop.Error) error {
	const (
		lockKey = "DTrollbk"
	)

	lk := t.l2Cache.CreateLockKeys([]string{lockKey})
	if ok, _, _ := t.l2Cache.DualLock(ctx, defaultLockDuration, lk); ok {
		defer t.l2Cache.Unlock(ctx, lk)
		ud, ok := err.UserData.(*sop.LockKey)
		if !ok {
			return err
		}
		// In this case, LockID is the transaction ID that holds the lock.
		tid := ud.LockID
		if err2 := t.logger.priorityRollback(ctx, t.registry, tid); err2 != nil {
			log.Info(fmt.Sprintf("error priorityRollback on tid %v, details: %v", err.UserData, err2))
			return err
		}

		log.Info(fmt.Sprintf("priorityRollback on tid %v, success", err.UserData))
		ud.IsLockOwner = true
		t.l2Cache.Unlock(ctx, []*sop.LockKey{ud})
		return nil
	}

	return err
}
