package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// This test exercises GetCurrentValue path where ValueNeedsFetch flips and valueWasFetched gets set.
// We simulate by constructing Items with ValueNeedsFetch=true and toggling via ItemActionTracker.Get.
// For the in-memory setup, ItemActionTracker.Get is a no-op, so this test constructs a minimal btree
// with a custom ItemActionTracker to emulate fetch behavior.

type fakeIAT[TK Ordered, TV any] struct{}

func (fakeIAT[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (fakeIAT[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (fakeIAT[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error { return nil }

// Get will flip ValueNeedsFetch to false without changing the value to simulate a backend fetch.
func (fakeIAT[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error {
	if item.ValueNeedsFetch {
		item.ValueNeedsFetch = false
	}
	return nil
}

// Minimal NodeRepository for testing

type fakeNR[TK Ordered, TV any] struct{ n map[sop.UUID]*Node[TK, TV] }

func (f *fakeNR[TK, TV]) Add(node *Node[TK, TV])    { f.n[node.ID] = node }
func (f *fakeNR[TK, TV]) Update(node *Node[TK, TV]) { f.n[node.ID] = node }
func (f *fakeNR[TK, TV]) Remove(id sop.UUID)        { delete(f.n, id) }
func (f *fakeNR[TK, TV]) Fetched(id sop.UUID)       {}
func (f *fakeNR[TK, TV]) Get(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	return f.n[id], nil
}

func TestGetCurrentValue_FetchFlags(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}
	// Prepare a root node with one item needing fetch
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	val := "x"
	it := Item[int, string]{Key: 1, Value: &val, ID: sop.NewUUID(), ValueNeedsFetch: true}
	root.Slots[0] = it
	root.Count = 1
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)
	b.setCurrentItemID(root.ID, 0)

	v, err := b.GetCurrentValue(nil)
	if err != nil {
		t.Fatalf("GetCurrentValue err: %v", err)
	}
	if v != "x" {
		t.Fatalf("want x, got %s", v)
	}
	if root.Slots[0].ValueNeedsFetch {
		t.Fatalf("expected ValueNeedsFetch=false after GetCurrentValue")
	}
	if !root.Slots[0].valueWasFetched {
		t.Fatalf("expected valueWasFetched=true after fetch")
	}

	// Now call an internal that clears fetched value under certain store flags.
	b.unfetchCurrentValue()
	// Because IsValueDataInNodeSegment is true by default here, unfetchCurrentValue will no-op.
	// Flip flags to emulate separate value segment and active persistence.
	b.StoreInfo.IsValueDataInNodeSegment = false
	b.StoreInfo.IsValueDataActivelyPersisted = true
	b.unfetchCurrentValue()
	if root.Slots[0].Value != nil || !root.Slots[0].ValueNeedsFetch || root.Slots[0].valueWasFetched {
		t.Fatalf("expected value cleared and needs fetch again")
	}
}
