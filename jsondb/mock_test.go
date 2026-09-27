package jsondb

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

type mockBtree[TK btree.Ordered, TV any] struct {
	btree.BtreeInterface[TK, TV]
	addErr            error
	addOk             bool
	addIfNotExistErr  error
	addIfNotExistOk   bool
	updateErr         error
	updateOk          bool
	upsertErr         error
	upsertOk          bool
	removeErr         error
	removeOk          bool
	firstErr          error
	firstOk           bool
	nextErr           error
	nextOk            bool
	previousErr       error
	previousOk        bool
	getCurrentKey     btree.Item[TK, TV]
	getCurrentItem    btree.Item[TK, TV]
	getCurrentItemErr error
	findErr           error
	findOk            bool
	findWithIDErr     error
	findWithIDOk      bool
}

func (m *mockBtree[TK, TV]) Add(ctx context.Context, key TK, value TV) (bool, error) {
	return m.addOk, m.addErr
}
func (m *mockBtree[TK, TV]) AddIfNotExist(ctx context.Context, key TK, value TV) (bool, error) {
	return m.addIfNotExistOk, m.addIfNotExistErr
}
func (m *mockBtree[TK, TV]) Update(ctx context.Context, key TK, value TV) (bool, error) {
	return m.updateOk, m.updateErr
}
func (m *mockBtree[TK, TV]) Upsert(ctx context.Context, key TK, value TV) (bool, error) {
	return m.upsertOk, m.upsertErr
}
func (m *mockBtree[TK, TV]) Remove(ctx context.Context, key TK) (bool, error) {
	return m.removeOk, m.removeErr
}
func (m *mockBtree[TK, TV]) First(ctx context.Context) (bool, error) {
	return m.firstOk, m.firstErr
}
func (m *mockBtree[TK, TV]) Next(ctx context.Context) (bool, error) {
	return m.nextOk, m.nextErr
}
func (m *mockBtree[TK, TV]) Previous(ctx context.Context) (bool, error) {
	return m.previousOk, m.previousErr
}
func (m *mockBtree[TK, TV]) GetCurrentKey() btree.Item[TK, TV] {
	return m.getCurrentKey
}
func (m *mockBtree[TK, TV]) GetCurrentItem(ctx context.Context) (btree.Item[TK, TV], error) {
	return m.getCurrentItem, m.getCurrentItemErr
}
func (m *mockBtree[TK, TV]) Find(ctx context.Context, key TK, firstItemWithKey bool) (bool, error) {
	return m.findOk, m.findErr
}
func (m *mockBtree[TK, TV]) FindWithID(ctx context.Context, key TK, id sop.UUID) (bool, error) {
	return m.findWithIDOk, m.findWithIDErr
}
func (m *mockBtree[TK, TV]) GetStoreInfo() sop.StoreInfo {
	return sop.StoreInfo{}
}
