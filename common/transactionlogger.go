package common

import (
	"context"
	"fmt"
	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

type commitFunction int

// Transaction commit functions.
const (
	unknown = iota
	createStore
	lockTrackedItems
	commitTrackedItemsValues
	commitNewRootNodes
	areFetchedItemsIntact
	commitUpdatedNodes
	commitRemovedNodes
	commitAddedNodes
	commitStoreInfo
	beforeFinalize
	finalizeCommit
	deleteObsoleteEntries
	deleteTrackedItemsValues

	// Pre commit functions.
	addActivelyPersistedItem    = 99
	updateActivelyPersistedItem = addActivelyPersistedItem
)

// transactionLog wraps a TransactionLog backend with state to support phased commit logging
// and rollback. It records which phase has been committed and can reconstruct partial state
// for recovery using GetOne/GetOneOfHour.
type transactionLog struct {
	sop.TransactionLog
	committedState commitFunction
	logging        bool
	transactionID  sop.UUID
}

const (
	defaultLockDuration = 5 * time.Minute
	// coordinatorLockName is the global coordinator lock used to serialize
	// priority rollbacks across the cluster.
	coordinatorLockName = "Prbs"
	// resurrectLogLocksCoordinatorLockName is the global coordinator lock used to serialize
	// priority logs' lock resurrection across the cluster.
	resurrectLogLocksCoordinatorLockName = "Prbl"
)

// Instantiate a transaction logger.
func newTransactionLogger(logger sop.TransactionLog, logging bool) *transactionLog {
	return &transactionLog{
		TransactionLog: logger,
		logging:        logging,
		transactionID:  logger.NewUUID(),
	}
}

// Log the about to be committed function state.
func (tl *transactionLog) log(ctx context.Context, f commitFunction, payload []byte) error {
	tl.committedState = f
	if !tl.logging || f == unknown {
		return nil
	}

	return tl.TransactionLog.Add(ctx, tl.transactionID, int(f), payload)
}

// removes logs saved to backend. During commit completion, logs need to be cleared.
func (tl *transactionLog) removeLogs(ctx context.Context) error {
	if !tl.logging {
		return nil
	}
	err := tl.TransactionLog.Remove(ctx, tl.transactionID)
	return err
}

var hourBeingProcessed string

// processExpiredTransactionLogs iterates through transactions grouped by hour and
// triggers rollback for those with leftover logs. It keeps processing a specific hour
// to exhaustion before moving to the next to reduce churn.
func (tl *transactionLog) processExpiredTransactionLogs(ctx context.Context, t *Transaction) error {
	var tid sop.UUID
	var hr string
	var committedFunctionLogs []sop.KeyValuePair[int, []byte]
	var err error
	if hourBeingProcessed == "" {
		tid, hr, committedFunctionLogs, err = tl.TransactionLog.GetOne(ctx)
		if err != nil {
			return err
		}
		hourBeingProcessed = hr
	} else {
		tid, committedFunctionLogs, err = tl.TransactionLog.GetOneOfHour(ctx, hourBeingProcessed)
		if err != nil {
			return err
		}
	}
	if tid.IsNil() {
		hourBeingProcessed = ""
		return nil
	}
	if tid == tl.transactionID {
		return nil
	}
	return tl.rollback(ctx, t, tid, committedFunctionLogs)
}

// resurrectPriorityLogLocks iterates all priority logs and re-acquires locks for their handles.
// This ensures that if Redis lost locks (e.g. restart), they are restored so that
// "self-heal" or standard rollback can function correctly.
func (tl *transactionLog) resurrectPriorityLogLocks(ctx context.Context, t *Transaction) error {
	lk := t.l2Cache.CreateLockKeys([]string{t.l2Cache.FormatLockKey(resurrectLogLocksCoordinatorLockName)})
	const maxDuration = 5 * time.Minute

	ok1, _, _ := t.l2Cache.DualLock(ctx, maxDuration, lk)
	if !ok1 {
		if err := tl.waitForCoordinatorRelease(ctx, t, resurrectLogLocksCoordinatorLockName, maxDuration); err != nil {
			return err
		}
		return nil
	}
	defer t.l2Cache.Unlock(ctx, lk)

	return tl.PriorityLog().ProcessNewer(ctx, func(tid sop.UUID, handles []sop.RegistryPayload[sop.Handle]) error {
		// Attempt to re-acquire locks.
		// If the transaction is alive and holds them, this is re-entrant/no-op.
		// If Redis restarted, this restores the lock.
		lks, err := tl.acquireLocks(ctx, t, tid, handles)
		if err != nil {
			// Log warning but continue processing other logs.
			log.Warn(fmt.Sprintf("resurrectPriorityLogLocks: failed to acquire locks for %v: %v", tid, err))
			return nil
		}

		reqIDs := sop.ExtractLogicalIDs(handles)
		if cuhAndrh, err := t.registry.Get(ctx, reqIDs); err != nil {
			log.Info(fmt.Sprintf("error reading (partly expected) current registry sector values for transaction %s, err details: %v", tid.String(), err))
		} else {
			for i := range handles {
				for ii := range handles[i].IDs {
					if !(handles[i].IDs[ii].Version == cuhAndrh[i].IDs[ii].Version || handles[i].IDs[ii].Version+1 == cuhAndrh[i].IDs[ii].Version) {
						t.l2Cache.Unlock(ctx, lks)
						// Version in Registry had gone past the value we can repair, 'just trigger a failover.
						return sop.Error{
							Code:     sop.RestoreRegistryFileSectorFailure,
							Err:      fmt.Errorf("version in Registry had gone past the value we can repair, 'just trigger a failover"),
							UserData: tid,
						}
					}
				}
			}
		}
		return nil
	})
}

func (tl *transactionLog) doPriorityRollbacks(ctx context.Context, t *Transaction) (bool, error) {
	lk := t.l2Cache.CreateLockKeys([]string{t.l2Cache.FormatLockKey(coordinatorLockName)})
	const maxDuration = 5 * time.Minute
	// When restart-triggered, ctx may contain the flag to ignore age. In that mode we
	// will process multiple batches; if not the winner, we pause and poll until the
	// coordinator lock is released.
	ignoreAge := false
	if v := ctx.Value(sop.ContextPriorityLogIgnoreAge); v != nil {
		if b, ok := v.(bool); ok && b {
			ignoreAge = true
		}
	}

	ok1, _, _ := t.l2Cache.DualLock(ctx, maxDuration, lk)
	if !ok1 {
		if ignoreAge {
			if err := tl.waitForCoordinatorRelease(ctx, t, coordinatorLockName, maxDuration); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	defer t.l2Cache.Unlock(ctx, lk)

	consumed := false
	start := sop.Now()
	for {
		v, err := tl.PriorityLog().GetBatch(ctx, 20)
		if len(v) == 0 || err != nil {
			return consumed, err
		}
		for i := range v {
			tid := v[i].Key
			uhAndrh := v[i].Value

			var lks []*sop.LockKey
			var err error
			// Acquire locks may involve attempt to override existing locks that may be for the dead transactions.
			if lks, err = tl.acquireLocks(ctx, t, tid, uhAndrh); err != nil {
				if se, ok := err.(sop.Error); ok && se.Code == sop.RestoreRegistryFileSectorFailure {
					// Allow failover event to occur, return the error.
					return false, se
				}
				log.Warn(fmt.Sprintf("unable to acquire locks for transaction %s, skip priority log rollback: %v", tid.String(), err))
				continue
			}

			reqIDs := sop.ExtractLogicalIDs(uhAndrh)
			if cuhAndrh, err := t.registry.Get(ctx, reqIDs); err != nil {
				log.Info(fmt.Sprintf("error reading (partly expected) current registry sector values for transaction %s, err details: %v", tid.String(), err))
			} else {
				for i := range uhAndrh {
					for ii := range uhAndrh[i].IDs {
						if !(uhAndrh[i].IDs[ii].Version == cuhAndrh[i].IDs[ii].Version || uhAndrh[i].IDs[ii].Version+1 == cuhAndrh[i].IDs[ii].Version) {
							t.l2Cache.Unlock(ctx, lks)
							// Version in Registry had gone past the value we can repair, 'just trigger a failover.
							return false, sop.Error{
								Code:     sop.RestoreRegistryFileSectorFailure,
								Err:      fmt.Errorf("version in Registry had gone past the value we can repair, 'just trigger a failover"),
								UserData: tid,
							}
						}
					}
				}
			}

			if err := t.registry.UpdateNoLocks(ctx, false, uhAndrh); err != nil {
				t.l2Cache.Unlock(ctx, lks)
				// When Registry is known to be corrupted, we can raise a failover event.
				return false, sop.Error{
					Code:     sop.RestoreRegistryFileSectorFailure,
					Err:      err,
					UserData: tid,
				}
			}

			if err := t.l2Cache.Unlock(ctx, lks); err != nil {
				log.Warn(fmt.Sprintf("error releasing locks for transaction %s, but priority log got rolled back", tid.String()))
			} else {
				log.Info(fmt.Sprintf("restoring a priority log for transaction %s occurred", tid.String()))
			}

			// Remove the priority log file as we succeeded in registry file sector restore.
			if err := tl.PriorityLog().Remove(ctx, tid); err != nil {
				return false, err
			}

			// Loop through & consume entire batch or until timeout if busy.
			if err := sop.TimedOut(ctx, "doPriorityRollbacks", start, maxDuration); err != nil {
				return true, err
			}
		}
		consumed = true
		if !ignoreAge {
			break // single batch in normal mode
		}
	}
	return true, nil
}

// waitForCoordinatorRelease blocks until the global coordinator lock is not held by others
// or until the max wait duration elapses. Logs a status at most once every ~2 seconds.
func (tl *transactionLog) waitForCoordinatorRelease(ctx context.Context, t *Transaction, lockName string, maxWait time.Duration) error {
	names := []string{t.l2Cache.FormatLockKey(lockName)}
	start := sop.Now()
	lastLog := time.Time{}
	for {
		heldByOthers, _ := t.l2Cache.IsLockedByOthers(ctx, names)
		if !heldByOthers {
			return nil
		}
		if lastLog.IsZero() || time.Since(lastLog) >= 2*time.Second {
			log.Info("waiting for coordinator lock to be released", "lock", lockName)
			lastLog = time.Now()
		}
		if err := sop.TimedOut(ctx, "waitPrbs", start, maxWait); err != nil {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// acquireLocks creates per-ID lock keys, sorts by UUID to avoid deadlocks, and attempts to
// acquire locks with TTL. If a dead transaction owns the locks, it attempts to take over by
// verifying lock owner IDs and setting LockID accordingly.
func (tl *transactionLog) acquireLocks(ctx context.Context, t *Transaction, tid sop.UUID, storesHandles []sop.RegistryPayload[sop.Handle]) ([]*sop.LockKey, error) {
	logicalIDs := sop.ExtractLogicalIDs(storesHandles)
	lookupByUUID := inmemory.NewBtree[sop.UUID, *sop.LockKey](true)

	for _, lids := range logicalIDs {
		for _, id := range lids.IDs {
			lookupByUUID.Add(id, t.l2Cache.CreateLockKeys([]string{id.String()})[0])
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

	if ok, ownerTID, err := t.l2Cache.DualLock(ctx, defaultLockDuration, keys); ok {
		return keys, nil
	} else if ownerTID.IsNil() {
		t.l2Cache.Unlock(ctx, keys)
		return keys, err
	} else {
		if ownerTID.Compare(tid) != 0 {
			t.l2Cache.Unlock(ctx, keys)
			return keys, sop.Error{
				Code: sop.RestoreRegistryFileSectorFailure,
				Err:  fmt.Errorf("key(s) is locked by another transaction %s, 'can't acquire lock to restore registry", ownerTID.String()),
			}
		}
		// Attempt to acquire ownership of the keys' locks from the dead transaction.

		// Clone to a new set so we can still unlock w/ the untainted copy.
		keysCopy := make([]*sop.LockKey, len(keys))
		for i := range keys {
			copy := *keys[i]
			keysCopy[i] = &copy
		}
		for i := range keys {
			if ok, tid2, err := t.l2Cache.GetEx(ctx, keys[i].Key, defaultLockDuration); ok {
				if tid.String() == tid2 {
					// Acquire the LockID of the dead transaction.
					keys[i].LockID = tid
					keys[i].IsLockOwner = true
				} else {
					// Unlock any key that got locked by lock call above, if there is any.
					t.l2Cache.Unlock(ctx, keysCopy)
					return keys, sop.Error{
						Code: sop.RestoreRegistryFileSectorFailure,
						Err:  fmt.Errorf("key(s) %s is locked by another transaction %s, 'can't acquire lock to restore registry", keys[i].Key, tid2),
					}
				}
			} else if err != nil {
				// Unlock any key that got locked by lock call above, if there is any.
				t.l2Cache.Unlock(ctx, keysCopy)
				return keys, err
			} else {
				// Key not found, so it is free. We must lock it.
				if ok, owner, err := t.l2Cache.DualLock(ctx, defaultLockDuration, []*sop.LockKey{keys[i]}); !ok {
					// Failed to lock.
					t.l2Cache.Unlock(ctx, keysCopy)
					if err != nil {
						return keys, err
					}
					return keys, sop.Error{
						Code: sop.RestoreRegistryFileSectorFailure,
						Err:  fmt.Errorf("key(s) %s could not be locked (owner: %s), 'can't acquire lock to restore registry", keys[i].Key, owner),
					}
				}
				// Update keysCopy to reflect that we now own this lock, so Unlock(keysCopy) will release it if we fail later.
				keysCopy[i].IsLockOwner = true
			}
		}
		// At this point, it is as good as we have achieved full lock on the set of keys, because they acquired lock ownership.
		return keys, nil
	}
}

// priorityRollback restores a single transaction's priority log into the registry and removes it.
func (tl *transactionLog) priorityRollback(ctx context.Context, registry sop.Registry, tid sop.UUID) error {
	if !tl.PriorityLog().IsEnabled() {
		return nil
	}
	if uhAndrh, err := tl.PriorityLog().Get(ctx, tid); err != nil {
		return err
	} else {
		// Nothing to restore; treat as no-op if no logs are present.
		if uhAndrh == nil {
			return tl.PriorityLog().Remove(ctx, tid)
		}
		// If registry is not available, avoid panic and treat as no-op for safety.
		if registry == nil {
			return nil
		}
		if err := registry.UpdateNoLocks(ctx, false, uhAndrh); err != nil {
			// When Registry is known to be corrupted, we can raise a failover event.
			return sop.Error{
				Code:     sop.RestoreRegistryFileSectorFailure,
				Err:      err,
				UserData: tid,
			}
		}
		return tl.PriorityLog().Remove(ctx, tid)
	}
}

// rollback walks committed function logs in reverse order and invokes targeted recovery actions
// for each phase, stopping early when finalizeCommit marks completion, and finally removing logs.
func (tl *transactionLog) rollback(ctx context.Context, t *Transaction, tid sop.UUID, committedFunctionLogs []sop.KeyValuePair[int, []byte]) error {
	if len(committedFunctionLogs) == 0 {
		if !tid.IsNil() {
			return tl.TransactionLog.Remove(ctx, tid)
		}
		return nil
	}

	var lastErr error
	lastCommittedFunctionLog := committedFunctionLogs[len(committedFunctionLogs)-1].Key
	for i := len(committedFunctionLogs) - 1; i >= 0; i-- {
		// Process pre commit log functions.
		if committedFunctionLogs[i].Key == addActivelyPersistedItem && committedFunctionLogs[i].Value != nil {
			itemsForDelete := toStruct[sop.BlobsPayload[sop.UUID]](committedFunctionLogs[i].Value)
			if err := t.blobStore.Remove(ctx, []sop.BlobsPayload[sop.UUID]{itemsForDelete}); err != nil {
				lastErr = err
			}
			continue
		}

		// Process commit log functions.
		if committedFunctionLogs[i].Key == createStore {
			if committedFunctionLogs[i].Value != nil {
				storeName := toStruct[string](committedFunctionLogs[i].Value)
				if err := t.StoreRepository.Remove(ctx, storeName); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == finalizeCommit {
			if committedFunctionLogs[i].Value == nil {
				if lastCommittedFunctionLog >= deleteObsoleteEntries {
					if err := tl.TransactionLog.Remove(ctx, tid); err != nil {
						lastErr = err
					}
					return lastErr
				}
				continue
			}
			v := toStruct[sop.Tuple[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]], []sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]]](committedFunctionLogs[i].Value)
			if lastCommittedFunctionLog == deleteTrackedItemsValues {
				if err := t.deleteTrackedItemsValues(ctx, v.Second); err != nil {
					lastErr = err
				}
			}
			if lastCommittedFunctionLog >= deleteObsoleteEntries {
				if err := t.deleteObsoleteEntries(ctx, v.First.First, v.First.Second); err != nil {
					lastErr = err
				}
				if err := tl.TransactionLog.Remove(ctx, tid); err != nil {
					lastErr = err
				}
				return lastErr
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitStoreInfo {
			if lastCommittedFunctionLog > commitStoreInfo && committedFunctionLogs[i].Value != nil {
				sis := toStruct[[]sop.StoreInfo](committedFunctionLogs[i].Value)
				if _, err := t.StoreRepository.Update(ctx, sis); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitAddedNodes {
			if lastCommittedFunctionLog > commitAddedNodes && committedFunctionLogs[i].Value != nil {
				bv := toStruct[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]](committedFunctionLogs[i].Value)
				if err := t.btreesBackend[0].nodeRepository.rollbackAddedNodes(ctx, bv); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitRemovedNodes {
			if lastCommittedFunctionLog > commitRemovedNodes && committedFunctionLogs[i].Value != nil {
				vids := toStruct[[]sop.RegistryPayload[sop.UUID]](committedFunctionLogs[i].Value)
				if err := t.btreesBackend[0].nodeRepository.rollbackRemovedNodes(ctx, false, vids); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitUpdatedNodes {
			if lastCommittedFunctionLog >= commitUpdatedNodes && committedFunctionLogs[i].Value != nil {
				blobsIDs := toStruct[[]sop.BlobsPayload[sop.UUID]](committedFunctionLogs[i].Value)
				log.Info(fmt.Sprintf("about to remove unused Nodes: %v", blobsIDs))
				// In Updated Nodes, removal of left hanging temp Nodes is the task. No need to do anything else as the main data flow,
				// transaction is able to clean up the Handle and kick out the unfinalized InactiveID that refers to the temp Node.
				if err := t.btreesBackend[0].nodeRepository.removeNodes(ctx, blobsIDs); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitNewRootNodes {
			if lastCommittedFunctionLog > commitNewRootNodes && committedFunctionLogs[i].Value != nil {
				bv := toStruct[sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]](committedFunctionLogs[i].Value)
				if err := t.btreesBackend[0].nodeRepository.rollbackNewRootNodes(ctx, bv); err != nil {
					lastErr = err
				}
			}
			continue
		}
		if committedFunctionLogs[i].Key == commitTrackedItemsValues {
			if lastCommittedFunctionLog >= commitTrackedItemsValues && committedFunctionLogs[i].Value != nil {
				ifd := toStruct[[]sop.Tuple[bool, sop.BlobsPayload[sop.UUID]]](committedFunctionLogs[i].Value)
				if err := t.deleteTrackedItemsValues(ctx, ifd); err != nil {
					lastErr = err
				}
			}
			continue
		}
	}

	if err := tl.TransactionLog.Remove(ctx, tid); err != nil {
		lastErr = err
	}

	return lastErr
}

func toStruct[T any](obj []byte) T {
	var t T
	if obj == nil {
		return t
	}
	encoding.DefaultMarshaler.Unmarshal(obj, &t)
	return t
}

func toByteArray(obj any) []byte {
	ba, _ := encoding.DefaultMarshaler.Marshal(obj)
	return ba
}
