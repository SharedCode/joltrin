// Package btree provides the core B-Tree data structure and algorithms used by SOP.
// It defines generic node/item types and storage interfaces to support multiple backends.
//
// This B-tree can distribute items added on a given leaf sub-branch so it tends to fill nodes of that sub-branch.
// Instead of ~50% average fill (typical), each node can achieve higher average utilization, commonly 62%–75%.
// The logic operates within a given sub-branch to avoid broader performance impact and can be disabled.
//
// "Leaf sub-branch" refers to the outermost branch whose children have no further descendants.
// A "leaf" node is an edge node with no children.
package btree

import (
	"context"
	"fmt"
	log "log/slog"

	"github.com/sharedcode/joltrin"
)

// Btree manages items using a B-tree data structure and algorithm.
// TK (Key Type) can be any comparable type, including complex structs.
// Using a struct as TK allows the B-Tree to act as a "Covering Index",
// where metadata is stored directly in the key and available during traversal
// without fetching the Value (TV).
type Btree[TK Ordered, TV any] struct {
	StoreInfo          *sop.StoreInfo
	storeInterface     *StoreInterface[TK, TV]
	tempSlots          []Item[TK, TV]
	tempParent         Item[TK, TV]
	tempChildren       []sop.UUID
	tempParentChildren []sop.UUID
	currentItemRef     currentItemRef
	currentItem        *Item[TK, TV]
	distributeAction   distributeAction[TK, TV]
	promoteAction      promoteAction[TK, TV]
	comparer           ComparerFunc[TK]
	coercedComparer    func(x, y any) int
}

// currentItemRef contains node ID & item slot index position in the node.
// SOP B-tree has a "cursor" like feature to allow navigation & fetch of the items
// for most complicated querying scenario possible, or as needed by the business.
type currentItemRef struct {
	nodeID        sop.UUID
	nodeItemIndex int
}

func (c currentItemRef) getNodeItemIndex() int {
	return c.nodeItemIndex
}
func (c currentItemRef) getNodeID() sop.UUID {
	return c.nodeID
}

// inferSchemaOfFirst captures schema from the first item added to the B-tree.
// Uses reflection to infer types from the generic TK and TV parameters.
func (btree *Btree[TK, TV]) inferSchemaOfFirst(item *Item[TK, TV]) {
	if btree.StoreInfo.Count == 0 {
		result := sop.InferSchemaFromTypes(item.Key, item.Value)
		btree.StoreInfo.Schema = result.Schema
		btree.StoreInfo.KeyFields = result.KeyFields
		btree.StoreInfo.ValueFields = result.ValueFields
		btree.StoreInfo.NeedsMetaDataSave = true
	}
}

// distributeAction contains details to allow B-tree to balance item load across nodes.
// "distribute" function will use these details in order to distribute an item of a node
// to either the left side or right side nodes of the branch(relative to the sourceNode)
// that is known to have a vacant slot.
type distributeAction[TK Ordered, TV any] struct {
	sourceNode *Node[TK, TV]
	item       Item[TK, TV]
	// distributeToLeft is true if item needs to be distributed to the left side,
	// otherwise to the right side.
	distributeToLeft bool
}

// promoteAction similar to distributeAction, contains details to allow controller in B-tree
// to drive calls for Node promotion to a higher level branch without using recursion.
// Recursion can be more "taxing"(on edge case) as it accumulates items pushed to the stack.
type promoteAction[TK Ordered, TV any] struct {
	targetNode *Node[TK, TV]
	slotIndex  int
}

// New creates a new Btree instance. comparer can be provided to override default comparison for TK.
func New[TK Ordered, TV any](storeInfo *sop.StoreInfo, si *StoreInterface[TK, TV], comparer ComparerFunc[TK]) (*Btree[TK, TV], error) {
	// Return nil B-Tree to signify failure if there is not enough info to create an instance.
	if si == nil {
		return nil, fmt.Errorf("can't create a b-tree with nil StoreInterface parameter")
	}
	if si.NodeRepository == nil {
		return nil, fmt.Errorf("can't create a b-tree with nil si.NodeRepository parameter")
	}
	if si.ItemActionTracker == nil {
		return nil, fmt.Errorf("can't create a b-tree with nil si.ItemActionTracker parameter")
	}
	if storeInfo.IsEmpty() {
		return nil, fmt.Errorf("can't create a b-tree with empty StoreInfo parameter")
	}

	// Detect Key IsPrimitiveKey type accurately.
	var zero TK
	if any(zero) != nil {
		storeInfo.IsPrimitiveKey = IsPrimitive[TK]()
	}

	var b3 = Btree[TK, TV]{
		StoreInfo:          storeInfo,
		storeInterface:     si,
		tempSlots:          make([]Item[TK, TV], storeInfo.SlotLength+1),
		tempChildren:       make([]sop.UUID, storeInfo.SlotLength+2),
		tempParentChildren: make([]sop.UUID, 2),
		comparer:           comparer,
	}
	return &b3, nil
}

// Lock acquires any locks required by the underlying store for read or write access.
func (btree *Btree[TK, TV]) Lock(ctx context.Context, forWriting bool) error {
	return nil
}

// GetStoreInfo returns the StoreInfo describing this B-tree instance.
func (btree *Btree[TK, TV]) GetStoreInfo() sop.StoreInfo {
	return *btree.StoreInfo
}

// Count returns the number of items in the B-tree.
func (btree *Btree[TK, TV]) Count() int64 {
	return btree.StoreInfo.Count
}

// Add inserts a key/value pair into the tree.
func (btree *Btree[TK, TV]) Add(ctx context.Context, key TK, value TV) (bool, error) {
	var item = newItem(key, value)

	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	result, err := node.add(ctx, btree, item)
	if err != nil {
		return false, err
	}
	// Add failed with no reason, 'just return false.
	if !result {
		return false, nil
	}

	// Add to local cache for submit/resolution on Commit.
	if err := btree.storeInterface.ItemActionTracker.Add(ctx, item); err != nil {
		return false, err
	}

	// Service the node's requested action(s).
	if err := btree.distribute(ctx); err != nil {
		return false, err
	}
	btree.promote(ctx)

	// Capture schema on first item add using reflection for accurate type information.
	btree.inferSchemaOfFirst(item)

	// Increment store's item count.
	btree.StoreInfo.Count++

	return true, nil
}

// AddItem is used internally during refetch/merge in commit to add an already-constructed Item.
func (btree *Btree[TK, TV]) AddItem(ctx context.Context, item *Item[TK, TV]) (bool, error) {
	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	result, err := node.add(ctx, btree, item)
	if err != nil {
		return false, err
	}
	// Add failed with no reason, 'just return false.
	if !result {
		return false, nil
	}

	// Service the node's requested action(s).
	if err := btree.distribute(ctx); err != nil {
		return false, err
	}
	btree.promote(ctx)

	// Capture schema on first item add using reflection for accurate type information.
	btree.inferSchemaOfFirst(item)

	// Increment store's item count.
	btree.StoreInfo.Count++

	return true, nil
}

// compare function of the Btree delegates comparison to the right function either the explicit comparer
// or the implicit Key object comparer.
func (btree *Btree[TK, TV]) compare(a TK, b TK) int {
	if btree.comparer != nil {
		return btree.comparer(a, b)
	}
	if btree.coercedComparer == nil {
		btree.coercedComparer = CoerceComparer(a)
	}
	return btree.coercedComparer(a, b)
}

// Find searches for the key and positions the cursor to the first or exact match depending on firstItemWithKey.
func (btree *Btree[TK, TV]) Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error) {
	// return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 {
		return false, nil
	}
	// Return current Value if key is same as current Key.
	if btree.isCurrentItemSelected() {
		ci, err := btree.getCurrentItem(ctx)
		if err != nil {
			return false, err
		}
		if !firstItemWithKey && btree.compare(ci.Key, key) == 0 {
			return true, nil
		}
	}
	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	r, err := node.find(ctx, btree, key, firstItemWithKey)
	btree.getCurrentItem(ctx)
	return r, err
}

// FindInDescendingOrder is analogous to Find but is useful when doing search item and
// retrieval will be in descending order. Use Previous to navigate backwards.
func (btree *Btree[TK, TV]) FindInDescendingOrder(ctx context.Context, key TK) (bool, error) {
	// return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 {
		return false, nil
	}
	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	r, err := node.findInDescendingOrder(ctx, btree, key)
	btree.getCurrentItem(ctx)
	return r, err
}

// FindWithID searches for the key and then walks duplicates until the specified ID is matched.
func (btree *Btree[TK, TV]) FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error) {
	if ok, err := btree.Find(ctx, key, true); ok && err == nil {
		for {
			if item, err := btree.getCurrentItem(ctx); err != nil {
				return false, err
			} else if id == item.ID {
				return true, nil
			}
			if ok, err := btree.Next(ctx); !ok || err != nil {
				return false, err
			}
		}
	} else {
		return ok, err
	}
}

// GetCurrentKey returns the current item's key and ID.
func (btree *Btree[TK, TV]) GetCurrentKey() Item[TK, TV] {
	var item Item[TK, TV]
	if btree.currentItem == nil {
		return item
	}
	return Item[TK, TV]{
		Key: btree.currentItem.Key,
		ID:  btree.currentItem.ID,
	}
}

// GetCurrentValue returns the current item's value, fetching it if necessary.
func (btree *Btree[TK, TV]) GetCurrentValue(ctx context.Context) (TV, error) {
	var zero TV
	if item, err := btree.getCurrentItem(ctx); err != nil || item == nil {
		return zero, err
	} else {
		// Register to local cache the "item get" for submit/resolution on Commit.
		vnf := item.ValueNeedsFetch
		if btree.storeInterface == nil {
			return zero, fmt.Errorf("storeInterface is nil")
		}
		if btree.storeInterface.ItemActionTracker == nil {
			return zero, fmt.Errorf("ItemActionTracker is nil")
		}
		if err := btree.storeInterface.ItemActionTracker.Get(ctx, item); err != nil {
			return zero, err
		}
		btree.storeInterface.NodeRepository.Fetched(btree.currentItemRef.nodeID)
		if vnf && !item.ValueNeedsFetch && item.Value != nil {
			item.valueWasFetched = true
		}
		if item.Value == nil {
			return zero, nil
		}
		return *item.Value, nil
	}
}

// GetCurrentValueNoLock returns the current item's value without registering a read lock hint.
// This is useful when the caller manages locking explicitly (e.g., SQL layer with explicit lock management).
func (btree *Btree[TK, TV]) GetCurrentValueNoLock(ctx context.Context) (TV, error) {
	var zero TV
	if item, err := btree.getCurrentItem(ctx); err != nil || item == nil {
		return zero, err
	} else {
		//vnf := item.ValueNeedsFetch

		// Don't mark the node & item as fetched to avoid read lock hint on Commit,
		// since this method is for scenarios where caller manages rlocks explicitly.
		// btree.storeInterface.NodeRepository.Fetched(btree.currentItemRef.nodeID)
		// btree.storeInterface.ItemActionTracker.Get(ctx, item)

		if item.ValueNeedsFetch && item.Value != nil {
			item.valueWasFetched = true
			item.ValueNeedsFetch = false
		}
		if item.Value == nil {
			return zero, nil
		}
		return *item.Value, nil
	}
}

// RLockCurrentItem registers the current item for read lock (version check) on Commit without returning the value.
// This is useful when you need to lock an item for consistency checking but don't need to read its value.
func (btree *Btree[TK, TV]) RLockCurrentItem(ctx context.Context) error {
	if item, err := btree.getCurrentItem(ctx); err != nil || item == nil {
		if err == nil && item == nil {
			err = fmt.Errorf("no current item selected to lock")
		}
		return err
	} else {
		// Register to local cache the "item get" for submit/resolution on Commit.
		if btree.storeInterface == nil {
			return fmt.Errorf("storeInterface is nil")
		}
		if btree.storeInterface.ItemActionTracker == nil {
			return fmt.Errorf("ItemActionTracker is nil")
		}
		vnf := item.ValueNeedsFetch
		// Mark item as fetched for version conflict detection.
		if err := btree.storeInterface.ItemActionTracker.Get(ctx, item); err != nil {
			return err
		}
		// Mark node as fetched for version conflict detection.
		btree.storeInterface.NodeRepository.Fetched(btree.currentItemRef.nodeID)
		if vnf && !item.ValueNeedsFetch && item.Value != nil {
			item.valueWasFetched = true
		}
	}
	return nil
}

// GetCurrentItem returns the current item including key and value, fetching value if necessary.
func (btree *Btree[TK, TV]) GetCurrentItem(ctx context.Context) (Item[TK, TV], error) {
	var zero Item[TK, TV]
	if item, err := btree.getCurrentItem(ctx); err != nil || item == nil {
		return zero, err
	} else {
		vnf := item.ValueNeedsFetch
		if err := btree.storeInterface.ItemActionTracker.Get(ctx, item); err != nil {
			return zero, err
		}
		btree.storeInterface.NodeRepository.Fetched(btree.currentItemRef.nodeID)
		if vnf && !item.ValueNeedsFetch && item.Value != nil {
			item.valueWasFetched = true
		}
		return *item, nil
	}
}

// GetCurrentItemNoLock returns the current item without read lock hint.
// Use this when managing locks explicitly (e.g., in SQL layer).
func (btree *Btree[TK, TV]) GetCurrentItemNoLock(ctx context.Context) (Item[TK, TV], error) {
	var zero Item[TK, TV]
	if item, err := btree.getCurrentItem(ctx); err != nil || item == nil {
		return zero, err
	} else {
		//vnf := item.ValueNeedsFetch

		// Don't mark the node & item as fetched to avoid read lock hint on Commit,
		// since this method is for scenarios where caller manages rlocks explicitly.
		// btree.storeInterface.NodeRepository.Fetched(btree.currentItemRef.nodeID)
		// btree.storeInterface.ItemActionTracker.Get(ctx, item)

		if item.ValueNeedsFetch && item.Value != nil {
			item.valueWasFetched = true
			item.ValueNeedsFetch = false
		}
		if item.Value == nil {
			return zero, nil
		}
		return *item, nil
	}
}

// AddIfNotExist inserts the item only when a duplicate key does not exist (temporarily enabling uniqueness).
func (btree *Btree[TK, TV]) AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error) {
	u := btree.StoreInfo.IsUnique
	btree.StoreInfo.IsUnique = true
	ok, err := btree.Add(ctx, key, value)
	btree.StoreInfo.IsUnique = u
	return ok, err
}

// First positions the cursor at the first item according to key ordering.
func (btree *Btree[TK, TV]) First(ctx context.Context) (bool, error) {
	// Return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 {
		return false, nil
	}
	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	r, err := node.moveToFirst(ctx, btree)
	btree.getCurrentItem(ctx)
	return r, err
}

// Last positions the cursor at the last item according to key ordering.
func (btree *Btree[TK, TV]) Last(ctx context.Context) (bool, error) {
	// Return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 {
		return false, nil
	}
	node, err := btree.getRootNode(ctx)
	if err != nil {
		return false, err
	}
	r, err := node.moveToLast(ctx, btree)
	btree.getCurrentItem(ctx)
	return r, err
}

// Next advances the cursor to the next item.
func (btree *Btree[TK, TV]) Next(ctx context.Context) (bool, error) {
	// Return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 || !btree.isCurrentItemSelected() {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	r, err := node.moveToNext(ctx, btree)
	btree.getCurrentItem(ctx)
	return r, err
}

// Previous moves the cursor to the previous item.
func (btree *Btree[TK, TV]) Previous(ctx context.Context) (bool, error) {
	// Return default value & no error if B-tree is empty.
	if btree.StoreInfo.Count == 0 || !btree.isCurrentItemSelected() {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	r, err := node.moveToPrevious(ctx, btree)
	btree.getCurrentItem(ctx)
	return r, err
}

// Update finds the item with matching key and calls UpdateCurrentItem to update it.
func (btree *Btree[TK, TV]) Update(ctx context.Context, key TK, newValue TV) (bool, error) {
	ok, err := btree.Find(ctx, key, false)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return btree.UpdateCurrentItem(ctx, key, newValue)
}

// UpdateKey finds the item with matching key and updates its key.
func (btree *Btree[TK, TV]) UpdateKey(ctx context.Context, key TK) (bool, error) {
	ok, err := btree.Find(ctx, key, false)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return btree.UpdateCurrentKey(ctx, key)
}

// UpdateCurrentItem updates the current item with the incoming key & value.
func (btree *Btree[TK, TV]) UpdateCurrentItem(ctx context.Context, key TK, value TV) (bool, error) {
	if btree.currentItemRef.getNodeID() == sop.NilUUID {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}

	item := node.Slots[btree.currentItemRef.getNodeItemIndex()]

	if btree.compare(item.Key, key) != 0 {
		// This Key change instance may affect ordering, so we prevent it.
		return false,
			fmt.Errorf("updating key(%v) with fields involved in comparer may affect ordering, thus is not allowed", btree.currentItem.Key)
	}

	item.Key = key
	item.Value = &value
	// Register to local cache the "item update" for submit/resolution on Commit.
	if err := btree.storeInterface.ItemActionTracker.Update(ctx, &item); err != nil {
		return false, err
	}
	node.Slots[btree.currentItemRef.getNodeItemIndex()] = item
	// Let the NodeRepository (& TransactionManager take care of backend storage upsert, etc...)
	btree.saveNode(node)
	return true, nil
}

// UpdateCurrentValue updates the value of the current item.
func (btree *Btree[TK, TV]) UpdateCurrentValue(ctx context.Context, newValue TV) (bool, error) {
	if btree.currentItemRef.getNodeID() == sop.NilUUID {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	item := node.Slots[btree.currentItemRef.getNodeItemIndex()]
	item.Value = &newValue
	// Register to local cache the "item update" for submit/resolution on Commit.
	if err := btree.storeInterface.ItemActionTracker.Update(ctx, &item); err != nil {
		return false, err
	}
	node.Slots[btree.currentItemRef.getNodeItemIndex()] = item
	// Let the NodeRepository (& TransactionManager take care of backend storage upsert, etc...)
	btree.saveNode(node)
	return true, nil
}

// UpdateCurrentKey updates the current item with the provided key.
func (btree *Btree[TK, TV]) UpdateCurrentKey(ctx context.Context, key TK) (bool, error) {
	if btree.currentItemRef.getNodeID() == sop.NilUUID {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	item := node.Slots[btree.currentItemRef.getNodeItemIndex()]

	if btree.compare(item.Key, key) != 0 {
		// This Key change instance may affect ordering, so we prevent it.
		return false,
			fmt.Errorf("updating key(%v) with fields involved in comparer may affect ordering, thus is not allowed", btree.currentItem.Key)
	}

	// Merge incoming key into current item
	item.Key = key

	// Register to local cache the "item update" for submit/resolution on Commit.
	if err := btree.storeInterface.ItemActionTracker.Update(ctx, &item); err != nil {
		return false, err
	}
	node.Slots[btree.currentItemRef.getNodeItemIndex()] = item
	// Let the NodeRepository (& TransactionManager take care of backend storage upsert, etc...)
	btree.saveNode(node)
	return true, nil
}

// UpdateCurrentNodeItem is used internally during refetch/merge in commit to replace the current item.
func (btree *Btree[TK, TV]) UpdateCurrentItemWithItem(ctx context.Context, item *Item[TK, TV]) (bool, error) {
	if btree.currentItemRef.getNodeID() == sop.NilUUID {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	btree.unfetchCurrentValue()
	btree.currentItem = item
	node.Slots[btree.currentItemRef.getNodeItemIndex()] = *item

	// Register to local cache the "item update" for submit/resolution on Commit.
	if err := btree.storeInterface.ItemActionTracker.Update(ctx, item); err != nil {
		return false, err
	}

	// Let the NodeRepository (& TransactionManager take care of backend storage upsert, etc...)
	btree.saveNode(node)
	return true, nil
}

// Upsert adds the item if it does not exist or updates it when it does.
func (btree *Btree[TK, TV]) Upsert(ctx context.Context, key TK, value TV) (bool, error) {
	if ok, err := btree.AddIfNotExist(ctx, key, value); !ok || err != nil {
		if err != nil {
			return false, err
		}
		// It means item with key already exists, update it.
		return btree.Update(ctx, key, value)
	}
	return true, nil
}

// Remove deletes the item matching the given key.
func (btree *Btree[TK, TV]) Remove(ctx context.Context, key TK) (bool, error) {
	ok, err := btree.Find(ctx, key, false)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return btree.RemoveCurrentItem(ctx)
}

// RemoveCurrentItem deletes the item at the current cursor position.
func (btree *Btree[TK, TV]) RemoveCurrentItem(ctx context.Context) (bool, error) {
	if btree.currentItemRef.getNodeID() == sop.NilUUID {
		return false, nil
	}
	node, err := btree.getNode(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return false, err
	}
	if node == nil || btree.currentItemRef.getNodeItemIndex() >= node.Count {
		return false, nil
	}
	if node.Slots[btree.currentItemRef.getNodeItemIndex()].ID.IsNil() {
		return false, nil
	}
	// Check if there are children nodes.
	if node.hasChildren() {
		index := btree.currentItemRef.getNodeItemIndex()
		deletedItem := node.Slots[index]
		if ok, err := node.removeItemOnNodeWithNilChild(ctx, btree, index); ok || err != nil {
			if ok {
				// Register to local cache the "item remove" for submit/resolution on Commit.
				if err := btree.storeInterface.ItemActionTracker.Remove(ctx, &deletedItem); err != nil {
					return false, err
				}
				// Make the current item pointer point to null since we just deleted the current item.
				btree.setCurrentItemID(sop.NilUUID, 0)
				btree.StoreInfo.Count--
			}
			return ok, err
		}
		// Below code allows for deletion to happen in the leaf(a.k.a. outermost) node's slots.
		// MoveNext method will position the Current Item ref to point to a leaf node.
		if ok, err := node.moveToNext(ctx, btree); !ok || err != nil {
			return false, err
		}
		currentNode, err := btree.getCurrentNode(ctx)
		if err != nil || currentNode == nil {
			return false, nil
		}
		// Replace the requested item for delete with the next item found on leaf node,
		// so we can delete that instead & make it happen on the leaf.
		// Deletion on leaf nodes is easier to repair/fix respective leaf branch.
		node.Slots[index] = currentNode.Slots[btree.currentItemRef.getNodeItemIndex()]
		btree.saveNode(node)
		deletedItem = currentNode.Slots[btree.currentItemRef.getNodeItemIndex()]
		if ok, err := currentNode.removeItemOnNodeWithNilChild(ctx, btree, btree.currentItemRef.getNodeItemIndex()); ok || err != nil {
			if ok {
				// Register to local cache the "item remove" for submit/resolution on Commit.
				if err := btree.storeInterface.ItemActionTracker.Remove(ctx, &deletedItem); err != nil {
					return false, err
				}
				// Make the current item pointer point to null since we just deleted the current item.
				btree.setCurrentItemID(sop.NilUUID, 0)
				btree.StoreInfo.Count--
			}
			return ok, err
		}
		node = currentNode
	}
	err = node.fixVacatedSlot(ctx, btree)
	if err != nil {
		return false, err
	}
	// Make the current item pointer point to null since we just deleted the current item.
	btree.setCurrentItemID(sop.NilUUID, 0)
	// Not needed in in-memory (V1) version.
	btree.StoreInfo.Count--

	return true, nil
}

// IsValueDataInNodeSegment reports whether Values are stored in the node segment with Keys.
func (btree *Btree[TK, TV]) IsValueDataInNodeSegment() bool {
	return btree.StoreInfo.IsValueDataInNodeSegment
}

// IsUnique reports whether the B-tree is configured to store unique keys.
func (btree *Btree[TK, TV]) IsUnique() bool {
	return btree.StoreInfo.IsUnique
}

// Internal helpers below.
// unfetchCurrentValue resets the current item's Value to an unfetched state to conserve memory when applicable.
func (btree *Btree[TK, TV]) unfetchCurrentValue() {
	if !btree.StoreInfo.IsValueDataInNodeSegment && btree.StoreInfo.IsValueDataActivelyPersisted &&
		!btree.StoreInfo.IsValueDataGloballyCached && btree.currentItem != nil &&
		btree.currentItem.Value != nil && btree.currentItem.valueWasFetched {
		btree.currentItem.Value = nil
		btree.currentItem.ValueNeedsFetch = true
		btree.currentItem.valueWasFetched = false
	}
}

// getCurrentItem returns the current item pointer or nil when no selection is active.
func (btree *Btree[TK, TV]) getCurrentItem(ctx context.Context) (*Item[TK, TV], error) {
	if btree.currentItemRef.nodeID.IsNil() {
		btree.currentItem = nil
		return nil, nil
	}
	if btree.currentItem != nil {
		return btree.currentItem, nil
	}
	n, err := btree.storeInterface.NodeRepository.Get(ctx, btree.currentItemRef.getNodeID())
	if err != nil {
		return nil, err
	}
	btree.currentItem = &n.Slots[btree.currentItemRef.getNodeItemIndex()]
	return btree.currentItem, nil
}

// getCurrentNode returns the Node of the current cursor selection.
func (btree *Btree[TK, TV]) getCurrentNode(ctx context.Context) (*Node[TK, TV], error) {
	n, err := btree.getNode(ctx, btree.currentItemRef.nodeID)
	if n == nil {
		return nil, err
	}
	return n, nil
}

// getRootNode returns the root node creating it on demand when the store is empty.
func (btree *Btree[TK, TV]) getRootNode(ctx context.Context) (*Node[TK, TV], error) {
	// If Store items were all deleted(Count = 0) then just fetch the root node.
	if !btree.StoreInfo.RootNodeID.IsNil() && btree.StoreInfo.Count == 0 {
		root, _ := btree.getNode(ctx, btree.StoreInfo.RootNodeID)
		if root != nil {
			return root, nil
		}
	}
	// Create a new root node as store is empty and has no root node yet.
	if btree.StoreInfo.RootNodeID.IsNil() || btree.StoreInfo.Count == 0 {
		var root = newNode[TK, TV](btree.getSlotLength())
		if btree.StoreInfo.RootNodeID.IsNil() {
			root.newID(sop.NilUUID)
			btree.StoreInfo.RootNodeID = root.ID
			return root, nil
		}
		root.ID = btree.StoreInfo.RootNodeID
		return root, nil
	}
	// Fetch the root node from blob store.
	root, err := btree.getNode(ctx, btree.StoreInfo.RootNodeID)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("can't retrieve root node w/ logical ID '%v'", btree.StoreInfo.RootNodeID)
	}
	return root, nil
}

// getNode fetches a node by ID from the repository.
func (btree *Btree[TK, TV]) getNode(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	n, e := btree.storeInterface.NodeRepository.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if n != nil {
		if cap(n.Slots) < btree.getSlotLength() {
			newSlots := make([]Item[TK, TV], btree.getSlotLength())
			copy(newSlots, n.Slots)
			n.Slots = newSlots
		} else {
			n.Slots = n.Slots[:btree.getSlotLength()]
		}
		if n.ChildrenIDs != nil {
			if cap(n.ChildrenIDs) < btree.getSlotLength()+1 {
				newChildren := make([]sop.UUID, btree.getSlotLength()+1)
				copy(newChildren, n.ChildrenIDs)
				n.ChildrenIDs = newChildren
			} else {
				n.ChildrenIDs = n.ChildrenIDs[:btree.getSlotLength()+1]
			}
		}
	}
	return n, nil
}

// setCurrentItemID updates the cursor to point to the specific node and slot index.
func (btree *Btree[TK, TV]) setCurrentItemID(nodeID sop.UUID, itemIndex int) {
	btree.unfetchCurrentValue()
	btree.currentItem = nil
	if btree.currentItemRef.nodeID == nodeID && btree.currentItemRef.getNodeItemIndex() == itemIndex {
		return
	}
	btree.currentItemRef.nodeID = nodeID
	btree.currentItemRef.nodeItemIndex = itemIndex
}

func (btree *Btree[TK, TV]) isUnique() bool {
	return btree.StoreInfo.IsUnique
}

// getSlotLength returns the node's item capacity, clamped to
// [2, sop.MaxSlotLength]. StoreInfo normally goes through sop.NewStoreInfo,
// which already enforces this range, but a StoreInfo loaded straight from
// persisted storage bypasses that and could otherwise carry an unbounded
// or corrupted value straight into a node allocation.
func (btree *Btree[TK, TV]) getSlotLength() int {
	n := btree.StoreInfo.SlotLength
	if n < 2 {
		return 2
	}
	if n > sop.MaxSlotLength {
		return sop.MaxSlotLength
	}
	return n
}

func (btree *Btree[TK, TV]) isCurrentItemSelected() bool {
	return btree.currentItemRef.getNodeID() != sop.NilUUID && btree.currentItemRef.getNodeItemIndex() >= 0
}

// distribute moves an item from a full node to a sibling with a vacant slot (controller pattern, avoids recursion).
func (btree *Btree[TK, TV]) distribute(ctx context.Context) error {
	for btree.distributeAction.sourceNode != nil {
		log.Debug(fmt.Sprintf("distribute item with key(%v) of node ID(%v) to left(%v)",
			btree.distributeAction.item.Key, btree.distributeAction.sourceNode.ID, btree.distributeAction.distributeToLeft))
		n := btree.distributeAction.sourceNode
		btree.distributeAction.sourceNode = nil
		item := btree.distributeAction.item
		btree.distributeAction.item = Item[TK, TV]{}

		// Node DistributeLeft or XxRight contains actual logic of "item distribution".
		if btree.distributeAction.distributeToLeft {
			if err := n.distributeToLeft(ctx, btree, &item); err != nil {
				return err
			}
		} else {
			if err := n.distributeToRight(ctx, btree, &item); err != nil {
				return err
			}
		}
	}
	return nil
}

// promote promotes a node to a higher-level branch when necessary (controller pattern, avoids recursion).
func (btree *Btree[TK, TV]) promote(ctx context.Context) {
	for btree.promoteAction.targetNode != nil {
		log.Debug(fmt.Sprintf("promote will promote a node with ID %v", btree.promoteAction.targetNode.ID))
		n := btree.promoteAction.targetNode
		i := btree.promoteAction.slotIndex
		btree.promoteAction.targetNode = nil
		btree.promoteAction.slotIndex = 0
		// Node's promote method contains actual logic to promote a (new parent outcome of
		// splitting a full node) node to higher up.
		n.promote(ctx, btree, i)
	}
}

// saveNode persists the node via the repository, assigning a new ID if needed.
func (btree *Btree[TK, TV]) saveNode(node *Node[TK, TV]) {
	if node.ID.IsNil() {
		node.ID = sop.NewUUID()
		btree.storeInterface.NodeRepository.Add(node)
		return
	}
	btree.storeInterface.NodeRepository.Update(node)
}

// removeNode removes the node from the backend repository when it has a valid ID.
func (btree *Btree[TK, TV]) removeNode(node *Node[TK, TV]) {
	if node.ID.IsNil() {
		return
	}
	btree.storeInterface.NodeRepository.Remove(node.ID)
}
