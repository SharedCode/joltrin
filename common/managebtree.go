// Package common contains shared transaction and B-tree management helpers used by SOP.
package common

import (
	"context"
	"fmt"
	log "log/slog"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// OpenBtree will open an existing B-Tree instance & prepare it for use in a transaction.
// Requires an active transaction. Returns an error if the store does not exist.
func OpenBtree[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if t == nil {
		return nil, fmt.Errorf("transaction 't' parameter can't be nil")
	}
	if !t.HasBegun() {
		return nil, fmt.Errorf("transaction 't' parameter has not started")
	}
	if name == "" {
		return nil, fmt.Errorf("b-tree name can't be empty string")
	}

	var t2 any = t.GetPhasedTransaction()
	trans := t2.(*Transaction)

	// Check if store is already open in this transaction; if so, return the existing handle.
	for i := range trans.btreesBackend {
		backend := &trans.btreesBackend[i]
		if backend.getStoreInfo != nil {
			info := backend.getStoreInfo()
			if info != nil && info.Name == name {
				if b3, ok := backend.btree.(*btree.Btree[TK, TV]); ok {
					return btree.NewBtreeWithTransaction(trans, b3), nil
				}
				return nil, fmt.Errorf("b-tree '%s' is already open with a different key/value type", name)
			}
		}
	}

	stores, err := trans.StoreRepository.Get(ctx, name)
	if len(stores) == 0 || stores[0].IsEmpty() || err != nil {
		if err == nil {
			if rbErr := trans.Rollback(ctx, nil); rbErr != nil {
				return nil, fmt.Errorf("b-tree '%s' does not exist, please use NewBtree to create an instance of it, rollback failed: %w", name, rbErr)
			}
			return nil, fmt.Errorf("b-tree '%s' does not exist, please use NewBtree to create an instance of it", name)
		}
		if rbErr := trans.Rollback(ctx, err); rbErr != nil {
			return nil, fmt.Errorf("Get store failed: %w, rollback failed: %v", err, rbErr)
		}
		return nil, err
	}
	return newBtreeWithTransaction[TK, TV](ctx, &stores[0], trans, comparer, false)
}

func CursorOnOpenedBtree[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[TK, TV], error) {
	if t == nil {
		return nil, fmt.Errorf("transaction 't' parameter can't be nil")
	}
	if !t.HasBegun() {
		return nil, fmt.Errorf("transaction 't' parameter has not started")
	}
	if name == "" {
		return nil, fmt.Errorf("b-tree name can't be empty string")
	}

	var t2 any = t.GetPhasedTransaction()
	trans := t2.(*Transaction)

	// Check if store is already open in this transaction, if so, return a Cursor to it.
	for _, backend := range trans.btreesBackend {
		if backend.getStoreInfo != nil {
			info := backend.getStoreInfo()
			if info.Name == name {
				if b3, ok := backend.btree.(*btree.Btree[TK, TV]); ok {
					return btree.NewBtreeWithTransaction(trans, btree.NewCursor(b3)), nil
				}
				return nil, fmt.Errorf("b-tree '%s' is already open with different key/value types", name)
			}
		}
	}
	return nil, fmt.Errorf("can't wrap a cursor on an unopened b-tree '%s'", name)
}

func OpenBtreeCursor[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if t == nil {
		return nil, fmt.Errorf("transaction 't' parameter can't be nil")
	}
	if !t.HasBegun() {
		return nil, fmt.Errorf("transaction 't' parameter has not started")
	}
	if name == "" {
		return nil, fmt.Errorf("b-tree name can't be empty string")
	}

	var t2 any = t.GetPhasedTransaction()
	trans := t2.(*Transaction)

	// Check if store is already open in this transaction, if so, return a Cursor to it.
	for _, backend := range trans.btreesBackend {
		if backend.getStoreInfo != nil {
			info := backend.getStoreInfo()
			if info.Name == name {
				if b3, ok := backend.btree.(*btree.Btree[TK, TV]); ok {
					return btree.NewBtreeWithTransaction(trans, btree.NewCursor(b3)), nil
				}
				return nil, fmt.Errorf("b-tree '%s' is already open with different key/value types", name)
			}
		}
	}

	stores, err := trans.StoreRepository.Get(ctx, name)
	if len(stores) == 0 || stores[0].IsEmpty() || err != nil {
		if err == nil {
			if rbErr := trans.Rollback(ctx, nil); rbErr != nil {
				return nil, fmt.Errorf("b-tree '%s' does not exist, please use NewBtree to create an instance of it, rollback failed: %w", name, rbErr)
			}
			return nil, fmt.Errorf("b-tree '%s' does not exist, please use NewBtree to create an instance of it", name)
		}
		if rbErr := trans.Rollback(ctx, err); rbErr != nil {
			return nil, fmt.Errorf("Get store failed: %w, rollback failed: %v", err, rbErr)
		}
		return nil, err
	}
	b3, err := newBtree[TK, TV](ctx, &stores[0], trans, comparer, false)
	if err != nil {
		return nil, err
	}
	return btree.NewBtreeWithTransaction(trans, btree.NewCursor(b3)), nil
}

// NewBtree will create a new B-Tree instance with data persisted to backend storage upon commit.
// If the store exists, it opens it and validates compatibility with the provided options.
// The creation of the store is fully transactional; if the transaction rolls back, the store will not be created.
// When creating a new store, a root node ID is preassigned for commit-time merging.
func NewBtree[TK btree.Ordered, TV any](ctx context.Context, si sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if t == nil {
		return nil, fmt.Errorf("transaction 't' parameter can't be nil")
	}
	if !t.HasBegun() {
		return nil, fmt.Errorf("transaction 't' parameter has not started")
	}
	if si.Name == "" {
		return nil, fmt.Errorf("b-tree name can't be empty string")
	}

	var t2 any = t.GetPhasedTransaction()
	trans := t2.(*Transaction)

	var stores []sop.StoreInfo
	var err error
	if si.CacheConfig != nil {
		stores, err = trans.StoreRepository.GetWithTTL(ctx, si.CacheConfig.IsStoreInfoCacheTTL, si.CacheConfig.StoreInfoCacheDuration, si.Name)
	} else {
		stores, err = trans.StoreRepository.Get(ctx, si.Name)
	}
	if err != nil {
		if rbErr := trans.Rollback(ctx, err); rbErr != nil {
			return nil, fmt.Errorf("GetWithTTL failed: %w, rollback failed: %v", err, rbErr)
		}
		return nil, err
	}
	ns := sop.NewStoreInfo(si)

	if len(stores) == 0 || stores[0].IsEmpty() {
		// Add to store repository if store not found.
		if ns.RootNodeID.IsNil() {
			// Pre-assign root node ID so B-Trees can merge newly created root nodes on commit.
			ns.RootNodeID = sop.NewUUID()
			ns.Timestamp = sop.Now().UnixMilli()
		}
		if err := trans.logger.log(ctx, createStore, toByteArray(ns.Name)); err != nil {
			if rbErr := trans.Rollback(ctx, err); rbErr != nil {
				return nil, fmt.Errorf("log createStore failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}
		if err := trans.StoreRepository.Add(ctx, *ns); err != nil {
			// Cleanup the store if there was anything added in backend.
			trans.StoreRepository.Remove(ctx, ns.Name)
			if rbErr := trans.Rollback(ctx, err); rbErr != nil {
				return nil, fmt.Errorf("StoreRepository Add failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}
		return newBtreeWithTransaction[TK, TV](ctx, ns, trans, comparer, true)
	}
	// Check if store retrieved is empty or of non-compatible specification.
	if !ns.IsCompatible(stores[0]) {
		if rbErr := trans.Rollback(ctx, nil); rbErr != nil {
			return nil, fmt.Errorf("b-tree '%s' exists & has different configuration, please use OpenBtree to open & create an instance of it, rollback failed: %w", si.Name, rbErr)
		}
		// Recommend to use the OpenBtree function to open it.
		return nil, fmt.Errorf("b-tree '%s' exists & has different configuration, please use OpenBtree to open & create an instance of it", si.Name)
	}
	ns = &stores[0]

	for i := range trans.btreesBackend {
		backend := &trans.btreesBackend[i]
		if backend.getStoreInfo != nil {
			info := backend.getStoreInfo()
			if info != nil && info.Name == si.Name {
				if b3, ok := backend.btree.(*btree.Btree[TK, TV]); ok {
					return btree.NewBtreeWithTransaction(trans, b3), nil
				}
				return nil, fmt.Errorf("b-tree '%s' is already open with a different key/value type", si.Name)
			}
		}
	}

	return newBtreeWithTransaction[TK, TV](ctx, ns, trans, comparer, false)
}

func newBtreeWithTransaction[TK btree.Ordered, TV any](ctx context.Context, s *sop.StoreInfo, trans *Transaction, comparer btree.ComparerFunc[TK], created bool) (btree.BtreeInterface[TK, TV], error) {
	b3, err := newBtree[TK, TV](ctx, s, trans, comparer, created)
	if err != nil {
		return nil, err
	}
	return btree.NewBtreeWithTransaction(trans, b3), nil
}

func newBtree[TK btree.Ordered, TV any](ctx context.Context, s *sop.StoreInfo, trans *Transaction, comparer btree.ComparerFunc[TK], created bool) (*btree.Btree[TK, TV], error) {
	si := StoreInterface[TK, TV]{}

	// Assign the item action tracker frontend and backend bits.
	iat := newItemActionTracker[TK, TV](s, trans.l2Cache, trans.blobStore, trans.logger)
	si.ItemActionTracker = iat

	// Assign the node repository frontend and backend bits.
	nrw := newNodeRepository[TK, TV](trans, s)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend

	// Wire up the B-tree & the backend bits required by the transaction.
	b3, err := btree.New(s, &si.StoreInterface, comparer)
	if err != nil {
		if rbErr := trans.Rollback(ctx, err); rbErr != nil {
			return nil, fmt.Errorf("btree.New failed: %w, rollback failed: %v", err, rbErr)
		}
		return nil, err
	}

	// B-Tree backend processing(of commit & rollback) required objects.
	b3b := btreeBackend{
		btree: b3,
		// Node blob repository.
		nodeRepository: nrw.nodeRepositoryBackend,
		// Needed for auto-merging of Node contents.
		refetchAndMerge: refetchAndMergeClosure(&si, b3, trans.StoreRepository),
		// Needed when applying the "delta" to the Store Count field.
		getStoreInfo: func() *sop.StoreInfo { return b3.StoreInfo },

		// Needed for tracked items' lock & "value data" in separate segments management.
		commitTrackedItemsValues:         iat.commitTrackedItemsValues,
		getForRollbackTrackedItemsValues: iat.getForRollbackTrackedItemsValues,
		getObsoleteTrackedItemsValues:    iat.getObsoleteTrackedItemsValues,

		hasTrackedItems:    iat.hasTrackedItems,
		checkTrackedItems:  iat.checkTrackedItems,
		lockTrackedItems:   iat.lock,
		unlockTrackedItems: iat.unlock,
		created:            created,
	}
	trans.btreesBackend = append(trans.btreesBackend, b3b)

	return b3, nil
}

// Use tracked Items to refetch their Nodes(using B-Tree) and merge the changes in, if there is no conflict.
// This closure resets caches, reloads store info, and replays actions recorded by the item tracker,
// validating versions to prevent lost updates.
func refetchAndMergeClosure[TK btree.Ordered, TV any](si *StoreInterface[TK, TV], b3 *btree.Btree[TK, TV], sr sop.StoreRepository) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		b3ModifiedItems := si.ItemActionTracker.(*itemActionTracker[TK, TV]).items
		// Clear the backend "cache" so we can force B-Tree to re-fetch from Redis(or BlobStore).
		si.ItemActionTracker.(*itemActionTracker[TK, TV]).items = make(map[sop.UUID]cacheItem[TK, TV])
		si.backendNodeRepository.localCache = make(map[sop.UUID]cachedNode)
		si.backendNodeRepository.readNodesCache.Clear()
		// Reset StoreInfo of B-Tree in prep to replay the "actions".
		storeInfo, err := sr.GetWithTTL(ctx, b3.StoreInfo.CacheConfig.IsStoreInfoCacheTTL, b3.StoreInfo.CacheConfig.StoreInfoCacheDuration, b3.StoreInfo.Name)
		if err != nil {
			return err
		}
		if len(storeInfo) == 0 {
			return fmt.Errorf("store %s not found (maybe deleted by rollback?)", b3.StoreInfo.Name)
		}

		// Reset the internal variables with value from backend Store DB.
		b3.StoreInfo.Count = storeInfo[0].Count
		si.backendNodeRepository.count = storeInfo[0].Count
		b3.StoreInfo.RootNodeID = storeInfo[0].RootNodeID

		for uuid, ci := range b3ModifiedItems {
			log.Debug(fmt.Sprintf("inside refetchAndMergeClosure, tid: %v", si.backendNodeRepository.transaction.GetID()))
			if ci.Action == addAction {
				log.Debug(fmt.Sprintf("Merging add item: %v", ci.item.Key))
				if ok, err := b3.AddItem(ctx, ci.item); !ok || err != nil {
					if err != nil {
						return err
					}
					return fmt.Errorf("refetchAndMergeModifications failed to merge add item with key %v", ci.item.Key)
				}
				if !b3.StoreInfo.IsValueDataInNodeSegment {
					ci.persisted = true
					si.ItemActionTracker.(*itemActionTracker[TK, TV]).items[ci.item.ID] = ci
				}
				continue
			}
			if ok, err := b3.FindWithID(ctx, ci.item.Key, uuid); !ok || err != nil {
				if err != nil {
					return err
				}
				return fmt.Errorf("refetchAndMergeModifications failed to find item with key %v", ci.item.Key)
			}

			// Check if the item read from backend has been updated since the time we read it.
			item, err := b3.GetCurrentItem(ctx)
			if err != nil || item.Version != ci.versionInDB {
				if err != nil {
					return err
				}
				return fmt.Errorf("refetchAndMergeModifications detected a newer version of item with key %v", ci.item.Key)
			}

			if ci.Action == getAction {
				// GetCurrentItem call above already "marked" the "get" (or fetch) done.
				continue
			}
			if ci.Action == removeAction {
				if ok, err := b3.RemoveCurrentItem(ctx); !ok || err != nil {
					if err != nil {
						return err
					}
					return fmt.Errorf("refetchAndMergeModifications failed to merge remove item with key %v", ci.item.Key)
				}
				continue
			}
			if ci.Action == updateAction {
				if b3.StoreInfo.IsValueDataInNodeSegment {
					if ok, err := b3.UpdateCurrentItemWithItem(ctx, ci.item); !ok || err != nil {
						if err != nil {
							return err
						}
						return fmt.Errorf("refetchAndMergeModifications failed to merge update item with key %v", ci.item.Key)
					}
					continue
				}
				// Correctly track the registry IDs of data segment blobs for deletion.
				si.ItemActionTracker.(*itemActionTracker[TK, TV]).forDeletionItems = append(
					si.ItemActionTracker.(*itemActionTracker[TK, TV]).forDeletionItems, item.ID)
				delete(si.ItemActionTracker.(*itemActionTracker[TK, TV]).items, item.ID)
				ci.persisted = true
				si.ItemActionTracker.(*itemActionTracker[TK, TV]).items[ci.item.ID] = ci

				// Ensure Btree will do everything else needed to update current Item, except merge change(above).
				if ok, err := b3.UpdateCurrentItemWithItem(ctx, ci.item); !ok || err != nil {
					if err != nil {
						return err
					}
					return fmt.Errorf("refetchAndMergeModifications failed to merge update item with key %v", ci.item.Key)
				}
			}
		}
		return nil
	}
}
