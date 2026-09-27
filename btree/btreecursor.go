package btree

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
)

// Cursor is a Btree cursor, it allows iteration on an underlying Btree and behaves like it is the Btree
// though it is not. It only holds the current item reference "state".
type Cursor[TK Ordered, TV any] struct {
	*Btree[TK, TV]
	currentItemRef currentItemRef
	currentItem    *Item[TK, TV]
}

func NewCursor[TK Ordered, TV any](btree *Btree[TK, TV]) *Cursor[TK, TV] {
	return &Cursor[TK, TV]{
		Btree: btree,
	}
}

// Add adds a key/value.
func (b3 *Cursor[TK, TV]) Add(ctx context.Context, key TK, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Add(ctx, key, value)
}

// AddIfNotExist adds only when no duplicate key exists.
func (b3 *Cursor[TK, TV]) AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.AddIfNotExist(ctx, key, value)
}

// Upsert inserts or updates depending on existence.
func (b3 *Cursor[TK, TV]) Upsert(ctx context.Context, key TK, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Upsert(ctx, key, value)
}

// Update finds by key and updates value.
func (b3 *Cursor[TK, TV]) Update(ctx context.Context, key TK, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Update(ctx, key, value)
}

// UpdateKey finds by key and updates key.
func (b3 *Cursor[TK, TV]) UpdateKey(ctx context.Context, key TK) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.UpdateKey(ctx, key)
}

// Remove finds by key and deletes.
func (b3 *Cursor[TK, TV]) Remove(ctx context.Context, key TK) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Remove(ctx, key)
}

// Find positions the cursor on an exact/first match; requires begun transaction.
func (b3 *Cursor[TK, TV]) Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Find(ctx, key, firstItemWithKey)
}

// FindWithID positions the cursor on a match with specific ID; requires begun transaction.
func (b3 *Cursor[TK, TV]) FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.FindWithID(ctx, key, id)
}

// FindInDescendingOrder positions the cursor on a match for descending iteration; requires begun transaction.
func (b3 *Cursor[TK, TV]) FindInDescendingOrder(ctx context.Context, key TK) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.FindInDescendingOrder(ctx, key)
}

// First positions the cursor at the smallest key.
func (b3 *Cursor[TK, TV]) First(ctx context.Context) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.First(ctx)
}

// Last positions the cursor at the largest key.
func (b3 *Cursor[TK, TV]) Last(ctx context.Context) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()
	return b3.Btree.Last(ctx)
}

// UpdateCurrentValue updates the current item.
func (b3 *Cursor[TK, TV]) UpdateCurrentValue(ctx context.Context, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.UpdateCurrentValue(ctx, value)
}

// UpdateCurrentItem updates the current item.
func (b3 *Cursor[TK, TV]) UpdateCurrentItem(ctx context.Context, key TK, value TV) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.UpdateCurrentItem(ctx, key, value)
}

// UpdateCurrentKey updates the current item's key.
func (b3 *Cursor[TK, TV]) UpdateCurrentKey(ctx context.Context, key TK) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.UpdateCurrentKey(ctx, key)
}

// RemoveCurrentItem deletes the current item.
func (b3 *Cursor[TK, TV]) RemoveCurrentItem(ctx context.Context) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef

	// Nullify current pointers.
	b3.currentItem = nil
	b3.currentItemRef.nodeID = sop.NilUUID
	b3.currentItemRef.nodeItemIndex = 0

	return b3.Btree.RemoveCurrentItem(ctx)
}

// GetCurrentKey returns the current key/ID; returns zero value if no transaction.
func (b3 *Cursor[TK, TV]) GetCurrentKey() Item[TK, TV] {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.GetCurrentKey()
}

// GetCurrentValue returns the current value; requires begun transaction.
func (b3 *Cursor[TK, TV]) GetCurrentValue(ctx context.Context) (TV, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.GetCurrentValue(ctx)
}

// GetCurrentValueNoLock returns the current value without read lock hint.
func (b3 *Cursor[TK, TV]) GetCurrentValueNoLock(ctx context.Context) (TV, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.GetCurrentValueNoLock(ctx)
}

// RLockCurrentItem registers the current item for read lock.
func (b3 *Cursor[TK, TV]) RLockCurrentItem(ctx context.Context) error {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.RLockCurrentItem(ctx)
}

// GetCurrentItem returns the current item; requires begun transaction.
func (b3 *Cursor[TK, TV]) GetCurrentItem(ctx context.Context) (Item[TK, TV], error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.GetCurrentItem(ctx)
}

func (b3 *Cursor[TK, TV]) GetCurrentItemNoLock(ctx context.Context) (Item[TK, TV], error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef
	return b3.Btree.GetCurrentItemNoLock(ctx)
}

// Next advances the cursor forward; requires begun transaction.
func (b3 *Cursor[TK, TV]) Next(ctx context.Context) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef

	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()

	return b3.Btree.Next(ctx)
}

// Previous moves the cursor backward.
func (b3 *Cursor[TK, TV]) Previous(ctx context.Context) (bool, error) {
	b3.Btree.currentItem = b3.currentItem
	b3.Btree.currentItemRef = b3.currentItemRef

	defer func() {
		b3.currentItem = b3.Btree.currentItem
		b3.currentItemRef = b3.Btree.currentItemRef
	}()

	return b3.Btree.Previous(ctx)
}
