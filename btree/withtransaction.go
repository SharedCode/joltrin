package btree

import (
	"context"
	"errors"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
)

// btreeWithTransaction wraps a B-tree with a TwoPhaseCommitTransaction and enforces:
//   - A transaction must have begun before any operation.
//   - Write operations require a writer-mode transaction; otherwise the tx is rolled back.
//   - On any delegated operation error, the wrapper triggers Rollback to keep state consistent.
//
// It does not implement locking yet (see Lock). All methods simply delegate after precondition checks.
type btreeWithTransaction[TK Ordered, TV any] struct {
	// Inherit from Btree
	BtreeInterface[TK, TV]
	transaction sop.TwoPhaseCommitTransaction
}

// errTransHasNotBegunMsg is returned when an operation is attempted without a begun transaction.
// For read operations below, we proactively call Rollback to ensure no partial state lingers.
var errTransHasNotBegunMsg = errors.New("can't do operation on b-tree if transaction has not begun")

// NewBtreeWithTransaction wraps a B-tree with a transaction session, enforcing that
// operations are run only when a transaction has begun and in the correct mode.
func NewBtreeWithTransaction[TK Ordered, TV any](t sop.TwoPhaseCommitTransaction, btree BtreeInterface[TK, TV]) *btreeWithTransaction[TK, TV] {
	return &btreeWithTransaction[TK, TV]{
		transaction:    t,
		BtreeInterface: btree,
	}
}

// Write operations: the following methods require a writer-mode transaction.
// They all follow the same pattern:
//   1) Ensure transaction HasBegun and mode == ForWriting, else Rollback and return error
//   2) Delegate to the underlying BtreeInterface
//   3) If the delegate returns error, Rollback with that error

// Add adds a key/value; requires a begun writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) Add(ctx context.Context, key TK, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't add item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't add item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.Add(ctx, key, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree add failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree add failed: %w", err)
	}
	return r, nil
}

// AddIfNotExist adds only when no duplicate key exists; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't add item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't add item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.AddIfNotExist(ctx, key, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree add if not exist failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree add if not exist failed: %w", err)
	}
	return r, nil
}

// Upsert inserts or updates depending on existence; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) Upsert(ctx context.Context, key TK, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.Upsert(ctx, key, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree upsert failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree upsert failed: %w", err)
	}
	return r, nil
}

// Update finds by key and updates value; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) Update(ctx context.Context, key TK, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.Update(ctx, key, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree update failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree update failed: %w", err)
	}
	return r, nil
}

// UpdateKey finds by key and updates key; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) UpdateKey(ctx context.Context, key TK) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.UpdateKey(ctx, key)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree update key failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree update key failed: %w", err)
	}
	return r, nil
}

// UpdateCurrentValue updates the current item; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) UpdateCurrentValue(ctx context.Context, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.UpdateCurrentValue(ctx, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree update current value failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree update current value failed: %w", err)
	}
	return r, nil
}

// UpdateCurrentItem updates the current item; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) UpdateCurrentItem(ctx context.Context, key TK, value TV) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.UpdateCurrentItem(ctx, key, value)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree update current item failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree update current item failed: %w", err)
	}
	return r, nil
}

// UpdateCurrentKey updates the current item's key; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) UpdateCurrentKey(ctx context.Context, key TK) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.UpdateCurrentKey(ctx, key)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree update current key failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree update current key failed: %w", err)
	}
	return r, nil
}

// Remove finds by key and deletes; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) Remove(ctx context.Context, key TK) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't update item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't update item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.Remove(ctx, key)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree remove failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree remove failed: %w", err)
	}
	return r, nil
}

// RemoveCurrentItem deletes the current item; requires writer transaction.
func (b3 *btreeWithTransaction[TK, TV]) RemoveCurrentItem(ctx context.Context) (bool, error) {
	if !b3.transaction.HasBegun() {
		return false, errTransHasNotBegunMsg
	}
	if b3.transaction.GetMode() != sop.ForWriting {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("can't remove item, transaction is not for writing, rollback failed: %w", err)
		}
		return false, fmt.Errorf("can't remove item, transaction is not for writing")
	}
	r, err := b3.BtreeInterface.RemoveCurrentItem(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree remove current item failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree remove current item failed: %w", err)
	}
	return r, nil
}

// Read-only operations: the following methods only require a begun transaction (any mode).
// On failure or when a transaction has not begun, we call Rollback to terminate the session.

// Find positions the cursor on an exact/first match; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.Find(ctx, key, firstItemWithKey)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree find failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree find failed: %w", err)
	}
	return r, nil
}

// FindWithID positions the cursor on a match with specific ID; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.FindWithID(ctx, key, id)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree find with id failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree find with id failed: %w", err)
	}
	return r, nil
}

// GetCurrentKey returns the current key/ID; returns zero value if no transaction.
func (b3 *btreeWithTransaction[TK, TV]) GetCurrentKey() Item[TK, TV] {
	var item Item[TK, TV]
	if !b3.transaction.HasBegun() {
		return item
	}
	return b3.BtreeInterface.GetCurrentKey()
}

// GetCurrentValue returns the current value; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) GetCurrentValue(ctx context.Context) (TV, error) {
	var zero TV
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return zero, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return zero, errTransHasNotBegunMsg
	}
	v, err := b3.BtreeInterface.GetCurrentValue(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return v, fmt.Errorf("btree get current value failed: %w, rollback failed: %v", err, rbErr)
		}
		return v, fmt.Errorf("btree get current value failed: %w", err)
	}
	return v, nil
}

// GetCurrentValueNoLock returns the current item's value without giving hint for read lock on Commit.
func (b3 *btreeWithTransaction[TK, TV]) GetCurrentValueNoLock(ctx context.Context) (TV, error) {
	var zero TV
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return zero, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return zero, errTransHasNotBegunMsg
	}
	v, err := b3.BtreeInterface.GetCurrentValueNoLock(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return v, fmt.Errorf("btree get current value nolock failed: %w, rollback failed: %v", err, rbErr)
		}
		return v, fmt.Errorf("btree get current value nolock failed: %w", err)
	}
	return v, nil
}

// RLockCurrentItem registers the current item for read lock; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) RLockCurrentItem(ctx context.Context) error {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return errTransHasNotBegunMsg
	}
	err := b3.BtreeInterface.RLockCurrentItem(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return fmt.Errorf("btree rlock current item failed: %w, rollback failed: %v", err, rbErr)
		}
		return fmt.Errorf("btree rlock current item failed: %w", err)
	}
	return nil
}

// GetCurrentItem returns the current item; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) GetCurrentItem(ctx context.Context) (Item[TK, TV], error) {
	var zero Item[TK, TV]
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return zero, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return zero, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.GetCurrentItem(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree get current item failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree get current item failed: %w", err)
	}
	return r, nil
}

// GetCurrentItemNoLock returns the current item without read lock hint; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) GetCurrentItemNoLock(ctx context.Context) (Item[TK, TV], error) {
	var zero Item[TK, TV]
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return zero, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return zero, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.GetCurrentItemNoLock(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree get current item nolock failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree get current item nolock failed: %w", err)
	}
	return r, nil
}

// First positions the cursor at the smallest key; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) First(ctx context.Context) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.First(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree first failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree first failed: %w", err)
	}
	return r, nil
}

// Last positions the cursor at the largest key; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) Last(ctx context.Context) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.Last(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree last failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree last failed: %w", err)
	}
	return r, nil
}

// Next advances the cursor forward; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) Next(ctx context.Context) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.Next(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree next failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree next failed: %w", err)
	}
	return r, nil
}

// Previous moves the cursor backward; requires begun transaction.
func (b3 *btreeWithTransaction[TK, TV]) Previous(ctx context.Context) (bool, error) {
	if !b3.transaction.HasBegun() {
		if err := b3.transaction.Rollback(ctx, nil); err != nil {
			return false, fmt.Errorf("%v, rollback failed: %w", errTransHasNotBegunMsg, err)
		}
		return false, errTransHasNotBegunMsg
	}
	r, err := b3.BtreeInterface.Previous(ctx)
	if err != nil {
		if rbErr := b3.transaction.Rollback(ctx, err); rbErr != nil {
			return r, fmt.Errorf("btree previous failed: %w, rollback failed: %v", err, rbErr)
		}
		return r, fmt.Errorf("btree previous failed: %w", err)
	}
	return r, nil
}
