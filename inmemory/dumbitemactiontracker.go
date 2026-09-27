package inmemory

import (
	"context"

	"github.com/sharedcode/joltrin/v5/btree"
)

type mt[TK btree.Ordered, TV any] struct{}

func newDumbItemActionTracker[TK btree.Ordered, TV any]() btree.ItemActionTracker[TK, TV] {
	return &mt[TK, TV]{}
}

// in-memory SOP does not track item actions, thus, do nothing for its ItemActionTracker.

func (iat mt[TK, TV]) Add(ctx context.Context, item *btree.Item[TK, TV]) error {
	return nil
}
func (iat mt[TK, TV]) Get(ctx context.Context, item *btree.Item[TK, TV]) error {
	return nil
}
func (iat mt[TK, TV]) Update(ctx context.Context, item *btree.Item[TK, TV]) error {
	return nil
}
func (iat mt[TK, TV]) Remove(ctx context.Context, item *btree.Item[TK, TV]) error {
	return nil
}
