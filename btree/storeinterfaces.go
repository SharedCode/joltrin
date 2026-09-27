package btree

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
)

// BtreeInterface defines the public API of the Btree.
type BtreeInterface[TK Ordered, TV any] interface {
	// Add adds an item to the B-tree and does not check for duplicates.
	Add(ctx context.Context, key TK, value TV) (bool, error)

	// AddIfNotExist adds an item if there is no item matching the key yet.
	// Otherwise it does nothing and returns false.
	// This is useful when adding an item without creating a duplicate entry.
	AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error)

	// Update finds the item with key and calls UpdateCurrentValue to update it.
	Update(ctx context.Context, key TK, value TV) (bool, error)

	// UpdateKey finds the item with key and updates its Key to the incoming key argument.
	UpdateKey(ctx context.Context, key TK) (bool, error)

	// UpdateCurrentKey updates the Key of the current item but only allows if key does not affect ordering.
	UpdateCurrentKey(ctx context.Context, key TK) (bool, error)
	// UpdateCurrentValue updates the Value of the current item.
	UpdateCurrentValue(ctx context.Context, newValue TV) (bool, error)

	// UpdateCurrentItem updates the current item with the incoming key & value.
	// This is a convenience method that combines UpdateCurrentKey and UpdateCurrentValue but
	// only allows if key does not affect ordering.
	UpdateCurrentItem(ctx context.Context, key TK, value TV) (bool, error)

	// Upsert adds the item if it does not exist or updates it if it does.
	Upsert(ctx context.Context, key TK, value TV) (bool, error)

	// Remove finds the item with a given key then removes that item.
	Remove(ctx context.Context, key TK) (bool, error)
	// RemoveCurrentItem removes the current key/value pair from the store.
	RemoveCurrentItem(ctx context.Context) (bool, error)

	// Find searches the B-tree for an item with a given key. Returns true if found,
	// otherwise false. firstItemWithKey is useful when there are items with the same key:
	// true positions the cursor to the first item with the given key according to ordering.
	// Use GetCurrentKey/GetCurrentValue to retrieve the current item's details.
	Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error)
	// FindWithID is synonymous to Find but allows code to supply the Item's ID to identify it.
	// This is useful for B-tree configurations that allow duplicate keys (IsUnique = false),
	// as it provides a way to differentiate duplicates via the unique ID (sop.UUID).
	FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error)

	// FindInDescendingOrder is analogous to Find but is useful when doing search item and
	// retrieval will be in descending order. Use Previous to navigate backwards.
	FindInDescendingOrder(ctx context.Context, key TK) (bool, error)

	// GetCurrentKey returns the current item's key (and Item ID). If the B-tree allows duplicates,
	// having the Item ID available allows finding that item conveniently (see FindWithID).
	GetCurrentKey() Item[TK, TV]
	// GetCurrentValue returns the current item's value with read lock hint for transaction commit.
	GetCurrentValue(ctx context.Context) (TV, error)
	// GetCurrentValueNoLock returns the current item's value without read lock hint.
	// Use this when managing locks explicitly (e.g., in SQL layer).
	GetCurrentValueNoLock(ctx context.Context) (TV, error)
	// GetCurrentItem returns the current item.
	GetCurrentItem(ctx context.Context) (Item[TK, TV], error)
	// GetCurrentItemNoLock returns the current item without read lock hint.
	// Use this when managing locks explicitly (e.g., in SQL layer).
	GetCurrentItemNoLock(ctx context.Context) (Item[TK, TV], error)

	// RLockCurrentItem registers the current item for read lock (version check) on Commit.
	// Use this when you need to lock an item without fetching its value.
	RLockCurrentItem(ctx context.Context) error

	// First positions the cursor to the first item as per key ordering.
	// Use GetCurrentKey/GetCurrentValue to retrieve the current item.
	First(ctx context.Context) (bool, error)
	// Last positions the cursor to the last item as per key ordering.
	// Use GetCurrentKey/GetCurrentValue to retrieve the current item.
	Last(ctx context.Context) (bool, error)
	// Next positions the cursor to the next item as per key ordering.
	// Use GetCurrentKey/GetCurrentValue to retrieve the current item.
	Next(ctx context.Context) (bool, error)
	// Previous positions the cursor to the previous item as per key ordering.
	// Use GetCurrentKey/GetCurrentValue to retrieve the current item.
	Previous(ctx context.Context) (bool, error)

	// IsUnique returns true if the B-tree is configured to store items with unique keys.
	// If you only need a uniqueness check when adding an item, use AddIfNotExist instead.
	IsUnique() bool

	// Count returns the number of items in this B-tree.
	Count() int64

	// GetStoreInfo returns StoreInfo which contains the details about this B-tree.
	GetStoreInfo() sop.StoreInfo

	// // Lock the B-tree for writing (if param is true) or for reading (if param is false).
	// // Upon transaction commit, lock on any B-trees are automatically released, why there is no "Unlock" function.
	// Lock(ctx context.Context, forWriting bool) error
}

// NodeRepository specifies the node repository used by the B-tree.
type NodeRepository[TK Ordered, TV any] interface {
	// Add caches the add action for submit on transaction commit.
	Add(node *Node[TK, TV])
	// Get fetches from backend (or cache if present) and returns the Node with the given nodeID.
	Get(ctx context.Context, nodeID sop.UUID) (*Node[TK, TV], error)
	// Fetched marks the Node with nodeID as fetched so it will be checked for version conflict during commit.
	Fetched(nodeID sop.UUID)
	// Update caches the update action for resolution on transaction commit.
	Update(node *Node[TK, TV])
	// Remove caches the remove action for resolution on transaction commit.
	Remove(nodeID sop.UUID)
}

// ItemActionTracker specifies the CRUD action methods that can be done to manage Items.
// These action methods can be implemented to allow the backend to resolve and submit
// these changes to the backend storage during transaction commit.
type ItemActionTracker[TK Ordered, TV any] interface {
	// Add caches the add action for submit on transaction commit.
	Add(ctx context.Context, item *Item[TK, TV]) error
	// Get caches the get action to be resolved on transaction commit, comparing version
	// with the backend copy and erroring if another transaction modified/deleted the item.
	Get(ctx context.Context, item *Item[TK, TV]) error
	// Update caches the update action for submit on transaction commit.
	Update(ctx context.Context, item *Item[TK, TV]) error
	// Remove caches the remove action for submit on transaction commit.
	Remove(ctx context.Context, item *Item[TK, TV]) error
}

// StoreInterface bundles the repositories the B-tree uses to manage/access its data/objects from a backend.
type StoreInterface[TK Ordered, TV any] struct {
	// NodeRepository is used to manage/access B-tree nodes.
	NodeRepository NodeRepository[TK, TV]
	// ItemActionTracker tracks management actions to Items which are resolved and submitted
	// to the backend during transaction commit.
	ItemActionTracker ItemActionTracker[TK, TV]
}
