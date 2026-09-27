package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// helper to construct a test btree with fake repos
func newTestBtree[T any]() (*Btree[int, T], *fakeNR[int, T]) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, T]{n: map[sop.UUID]*Node[int, T]{}}
	si := StoreInterface[int, T]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, T]{}}
	b, err := New[int, T](store, &si, nil)
	if err != nil {
		panic(err)
	}
	return b, fnr
}

func TestNew_ValidationErrors(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})

	// nil StoreInterface
	if b, err := New[int, string](store, nil, nil); err == nil || b != nil {
		t.Fatalf("expected error for nil store interface")
	}
	// nil NodeRepository
	si := &StoreInterface[int, string]{}
	if b, err := New[int, string](store, si, nil); err == nil || b != nil {
		t.Fatalf("expected error for nil node repo")
	}
	// nil ItemActionTracker
	si.NodeRepository = &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	if b, err := New[int, string](store, si, nil); err == nil || b != nil {
		t.Fatalf("expected error for nil item tracker")
	}
	// empty StoreInfo
	si2 := &StoreInterface[int, string]{
		NodeRepository:    &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}},
		ItemActionTracker: fakeIAT[int, string]{},
	}
	if b, err := New[int, string](&sop.StoreInfo{}, si2, nil); err == nil || b != nil {
		t.Fatalf("expected error for empty storeinfo")
	}
}

func TestCRUD_AndNavigation_Basic(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Add items
	if ok, _ := b.Add(nil, 2, "b"); !ok {
		t.Fatal("add 2")
	}
	if ok, _ := b.Add(nil, 1, "a"); !ok {
		t.Fatal("add 1")
	}
	if ok, _ := b.Add(nil, 3, "c"); !ok {
		t.Fatal("add 3")
	}

	if got := b.Count(); got != 3 {
		t.Fatalf("count %d", got)
	}

	// Find exact
	if ok, _ := b.Find(nil, 2, false); !ok {
		t.Fatal("find 2")
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 2 || *it.Value != "b" {
		t.Fatalf("bad current")
	}

	// Next and Previous
	if ok, _ := b.Next(nil); !ok {
		t.Fatal("next after 2")
	}
	if k := b.GetCurrentKey().Key; k != 3 {
		t.Fatalf("want 3 got %d", k)
	}
	if ok, _ := b.Previous(nil); !ok {
		t.Fatal("prev back to 2")
	}

	// Update current
	if ok, _ := b.UpdateCurrentValue(nil, "bb"); !ok {
		t.Fatal("update current")
	}
	if it, _ := b.GetCurrentItem(nil); *it.Value != "bb" {
		t.Fatalf("update not applied")
	}

	// Remove current
	if ok, _ := b.RemoveCurrentItem(nil); !ok {
		t.Fatal("remove current")
	}
	if got := b.Count(); got != 2 {
		t.Fatalf("count after remove %d", got)
	}

	// Boundaries
	b.setCurrentItemID(sop.NilUUID, 0)
	if ok, _ := b.Next(nil); ok {
		t.Fatalf("next with no selection should be false")
	}
	if ok, _ := b.Previous(nil); ok {
		t.Fatalf("prev with no selection should be false")
	}
}

func TestGetRootNode_CreateReuse(t *testing.T) {
	b, nr := newTestBtree[string]()
	// Initially empty, getRootNode should create a new one with RootNodeID set.
	root, err := b.getRootNode(nil)
	if err != nil || root == nil {
		t.Fatalf("getRootNode err=%v", err)
	}
	if b.StoreInfo.RootNodeID.IsNil() {
		t.Fatalf("expected RootNodeID set")
	}
	// Save in repo and then simulate all deleted by zeroing count; getRootNode should reuse existing root.
	nr.Add(root)
	b.StoreInfo.Count = 0
	root2, err := b.getRootNode(nil)
	if err != nil || root2 == nil {
		t.Fatalf("getRootNode second err=%v", err)
	}
	if root2.ID != root.ID {
		t.Fatalf("expected to retrieve same root")
	}
}

func TestUpdateCurrentItem(t *testing.T) {
	b, _ := newTestBtree[string]()
	b.Add(nil, 1, "a")
	b.Add(nil, 2, "b")

	// Position on 2
	b.Find(nil, 2, false)

	// Update both key and value
	// Note: Key update must not affect ordering.
	// Since 2 is > 1, updating 2 to 3 (if 3 doesn't exist and is > 1) might be allowed if it doesn't cross boundaries?
	// Wait, UpdateCurrentItem checks:
	// if btree.compare(item.Key, key) != 0 { return false, error }
	// So it ONLY allows updating key if it compares EQUAL.
	// This means it's for updating metadata within the key that doesn't affect the sort order (like ContentKey in vector store).
	// For int key, 2 == 2. So we can't change 2 to 3.

	// So let's try updating with same key but different value, just to test the method call.
	if ok, err := b.UpdateCurrentItem(nil, 2, "bb"); !ok || err != nil {
		t.Fatalf("UpdateCurrentItem failed: %v", err)
	}

	item, _ := b.GetCurrentItem(nil)
	if item.Key != 2 || *item.Value != "bb" {
		t.Fatalf("UpdateCurrentItem did not update value")
	}
}
