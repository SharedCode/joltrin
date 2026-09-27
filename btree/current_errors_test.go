package btree

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

type fakeNRGetErr[TK Ordered, TV any] struct{ fakeNR[TK, TV] }

func (f *fakeNRGetErr[TK, TV]) Get(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	return nil, errors.New("get error")
}

// Exercise GetCurrentItem/GetCurrentValue repo error branches.
func TestCurrent_Fetch_RepoError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRGetErr[int, string]{fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Set a non-nil current ref so getCurrentItem attempts a repo Get and errors
	b.setCurrentItemID(sop.NewUUID(), 0)
	if _, err := b.GetCurrentItem(context.Background()); err == nil {
		t.Fatalf("expected repo get error in GetCurrentItem")
	}
	if _, err := b.GetCurrentValue(context.Background()); err == nil {
		t.Fatalf("expected repo get error in GetCurrentValue")
	}
}
