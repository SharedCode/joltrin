package common

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

type b3WithInducedErrors[TK btree.Ordered, TV any] struct {
	induceErrorOnMethod int
	t                   *testing.T
}

func newBTreeWithInducedErrors[TK btree.Ordered, TV any](t *testing.T) *b3WithInducedErrors[TK, TV] {
	t.Helper()
	return &b3WithInducedErrors[TK, TV]{t: t}
}

func (b3 b3WithInducedErrors[TK, TV]) Count() int64 {
	return 0
}

func (b3 b3WithInducedErrors[TK, TV]) Add(ctx context.Context, key TK, value TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 1 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 2 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) Update(ctx context.Context, key TK, value TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 3 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) UpdateCurrentValue(ctx context.Context, newValue TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 4 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) UpdateCurrentKey(ctx context.Context, newKey TK) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 16 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) UpdateKey(ctx context.Context, key TK) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 18 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) UpdateCurrentItem(ctx context.Context, key TK, value TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 17 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) Remove(ctx context.Context, key TK) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 5 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) RemoveCurrentItem(ctx context.Context) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 6 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 7 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 8 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) GetCurrentKey() btree.Item[TK, TV] {
	b3.t.Helper()
	var zero btree.Item[TK, TV]
	return zero
}
func (b3 b3WithInducedErrors[TK, TV]) GetCurrentValue(ctx context.Context) (TV, error) {
	b3.t.Helper()
	var zero TV
	if b3.induceErrorOnMethod == 9 {
		return zero, fmt.Errorf("foobar")
	}
	return zero, nil
}

func (b3 b3WithInducedErrors[TK, TV]) GetCurrentItem(ctx context.Context) (btree.Item[TK, TV], error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 10 {
		return btree.Item[TK, TV]{}, fmt.Errorf("foobar")
	}
	return btree.Item[TK, TV]{}, nil
}

func (b3 b3WithInducedErrors[TK, TV]) GetCurrentValueNoLock(ctx context.Context) (TV, error) {
	b3.t.Helper()
	var zero TV
	if b3.induceErrorOnMethod == 9 {
		return zero, fmt.Errorf("foobar")
	}
	return zero, nil
}

func (b3 b3WithInducedErrors[TK, TV]) GetCurrentItemNoLock(ctx context.Context) (btree.Item[TK, TV], error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 10 {
		return btree.Item[TK, TV]{}, fmt.Errorf("foobar")
	}
	return btree.Item[TK, TV]{}, nil
}

func (b3 b3WithInducedErrors[TK, TV]) RLockCurrentItem(ctx context.Context) error {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 19 {
		return fmt.Errorf("foobar")
	}
	return nil
}

func (b3 b3WithInducedErrors[TK, TV]) First(ctx context.Context) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 11 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) Last(ctx context.Context) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 12 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}
func (b3 b3WithInducedErrors[TK, TV]) Next(ctx context.Context) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 13 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) Previous(ctx context.Context) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 14 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) Upsert(ctx context.Context, key TK, value TV) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 15 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) FindInDescendingOrder(ctx context.Context, key TK) (bool, error) {
	b3.t.Helper()
	if b3.induceErrorOnMethod == 16 {
		return false, fmt.Errorf("foobar")
	}
	return true, nil
}

func (b3 b3WithInducedErrors[TK, TV]) GetStoreInfo() sop.StoreInfo {
	return sop.StoreInfo{}
}

func (b3 b3WithInducedErrors[TK, TV]) IsValueDataInNodeSegment() bool { return true }
func (b3 b3WithInducedErrors[TK, TV]) IsUnique() bool                 { return true }
