package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Ensure Add propagates error from distribute() (e.g., sibling fetch failure) and does not increment Count.
func TestAdd_DistributeError_Propagates(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Build a parent with current(full) and right(has room) to trigger distribute-to-right.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	// Make current node full
	for i := 0; i < b.getSlotLength(); i++ {
		v := "v"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: 10 + i, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	// Right has vacancy
	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = cur.ID
	parent.ChildrenIDs[1] = right.ID
	// separator key
	kv := "s"
	parent.Slots[0] = Item[int, string]{Key: 99, Value: &kv, ID: sop.NewUUID()}

	fnr.Add(parent)
	fnr.Add(cur)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 1

	// Make right sibling fetch fail during distributeToRight
	fnr.errs[right.ID] = true

	if ok, err := b.Add(context.Background(), 5, "x"); err == nil || ok {
		t.Fatalf("expected Add to propagate distribute error, ok=%v err=%v", ok, err)
	}
	if b.Count() != 1 {
		t.Fatalf("count should remain unchanged on error, got %d", b.Count())
	}
}

// moveToPrevious: when starting node has children but the targeted child cannot be fetched (repo returns nil),
// it should set selection to nil and return false.
func TestMoveToPrevious_ChildMissing_ReturnsFalse(t *testing.T) {
	b, fnr := newTestBtree[string]()

	// Root with one item and two children positions
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	// Left child ID not present in repo (simulates missing)
	missingLeftID := sop.NewUUID()
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	root.ChildrenIDs = []sop.UUID{missingLeftID, right.ID}
	fnr.Add(root)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = root.ID
	// Select current at root slot 0
	b.setCurrentItemID(root.ID, 0)

	ok, err := root.moveToPrevious(context.Background(), b)
	if err != nil || ok {
		t.Fatalf("expected (false,nil) when child missing; ok=%v err=%v", ok, err)
	}
	if !b.GetCurrentKey().ID.IsNil() {
		t.Fatalf("expected current selection cleared")
	}
}

// moveToFirst: when a child ID cannot be fetched (repo returns nil), it should stop and select the last valid node seen.
func TestMoveToFirst_ChildFetchNil_StopsAtParent(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	v := "p"
	p.Slots[0] = Item[int, string]{Key: 5, Value: &v, ID: sop.NewUUID()}
	p.Count = 1
	// First child points to unknown ID (not in repo)
	p.ChildrenIDs = make([]sop.UUID, 2)
	p.ChildrenIDs[0] = sop.NewUUID()
	fnr.Add(p)
	b.StoreInfo.RootNodeID = p.ID

	if ok, err := p.moveToFirst(context.Background(), b); err != nil || !ok {
		t.Fatalf("moveToFirst err=%v ok=%v", err, ok)
	}
	if it, _ := b.GetCurrentItem(context.Background()); it.Key != 5 {
		t.Fatalf("expected parent key selected, got %v", it.Key)
	}
}

// Directly exercise promoteSingleChildAsParentChild: rewires parent to adopt the only child and removes the node.
func TestPromoteSingleChildAsParentChild_WiresAndRemoves(t *testing.T) {
	b, fnr := newTestBtree[string]()

	// Parent with two children slots; middle separator count=1
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)
	// Node under parent with a single child at index 0
	mid := newNode[int, string](b.getSlotLength())
	mid.newID(parent.ID)
	mid.ChildrenIDs = make([]sop.UUID, 1)
	child := newNode[int, string](b.getSlotLength())
	child.newID(mid.ID)
	mid.ChildrenIDs[0] = child.ID

	// Wire into repo
	parent.ChildrenIDs[0] = mid.ID
	fnr.Add(parent)
	fnr.Add(mid)
	fnr.Add(child)

	// Act
	ok, err := mid.promoteSingleChildAsParentChild(context.Background(), b)
	if err != nil || !ok {
		t.Fatalf("promoteSingleChildAsParentChild failed: ok=%v err=%v", ok, err)
	}
	// Parent should now adopt child at the same index
	if parent.ChildrenIDs[0] != child.ID {
		t.Fatalf("parent did not adopt child; got %v want %v", parent.ChildrenIDs[0], child.ID)
	}
	// Child's parent should be updated
	if child.ParentID != parent.ID {
		t.Fatalf("child.ParentID not updated")
	}
	// Mid should be removed from repo
	if _, exists := fnr.n[mid.ID]; exists {
		t.Fatalf("mid node should be removed from repository")
	}
}
