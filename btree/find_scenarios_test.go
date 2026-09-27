package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Cover find() stopping on left-nil child (nearest neighbor positioning)
func TestFind_StopsOnNilLeftChild_PositionsNearest(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Insert a few items; tree may remain a single node but logic still applies
	for _, k := range []int{10, 20, 30} {
		ok, err := b.Add(nil, k, "v")
		if err != nil || !ok {
			t.Fatalf("add %d", k)
		}
	}
	// Search for key < 10 to hit nearest-neighbor logic
	ok, err := b.Find(nil, 5, true)
	if err != nil || ok {
		t.Fatalf("expected false,nil for not-found with nearest positioning, got ok=%v err=%v", ok, err)
	}
	// After not found, current should be the next-greater (10)
	if it, _ := b.GetCurrentItem(nil); it.Key != 10 {
		t.Fatalf("expected current=10, got %d", it.Key)
	}
}

// Create a parent with left subtree duplicates and ensure Find(key, true) descends to the left-most duplicate.
func TestFind_Duplicates_FirstItemWithKey_Leftmost(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with one separator key 5 and left child containing two 5s.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	pv := "p"
	parent.Slots[0] = Item[int, string]{Key: 5, Value: &pv, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	l1 := "a"
	l2 := "b"
	left.Slots[0] = Item[int, string]{Key: 5, Value: &l1, ID: sop.NewUUID()}
	left.Slots[1] = Item[int, string]{Key: 5, Value: &l2, ID: sop.NewUUID()}
	left.Count = 2

	// right child (unused but present)
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 3

	ok, err := b.Find(context.Background(), 5, true)
	if err != nil || !ok {
		t.Fatalf("Find failed: ok=%v err=%v", ok, err)
	}
	// Expect left-most duplicate (left.Slots[0])
	if it, _ := b.GetCurrentItem(context.Background()); it.ID != left.Slots[0].ID {
		t.Fatalf("expected left-most duplicate selected; got key=%v id=%v", it.Key, it.ID)
	}
}

// When key is found at parent but left child at index is NilUUID, Find should break and select parent's slot.
func TestFind_FoundWithNilLeftChild_BreaksAtParent(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	v := "v"
	parent.Slots[0] = Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)
	// Force nil left child at index 0
	parent.ChildrenIDs[0] = sop.NilUUID
	fnr.Add(parent)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 1

	ok, err := b.Find(context.Background(), 10, true)
	if err != nil || !ok {
		t.Fatalf("Find failed: ok=%v err=%v", ok, err)
	}
	if it := b.GetCurrentKey(); it.ID != parent.Slots[0].ID {
		t.Fatalf("expected to remain on parent slot; got id=%v", it.ID)
	}
}

// Exercise distributeToLeft rotation path when node is full and has a left sibling, ensuring parent slot updates
// and a distributeAction is prepared for the left sibling.
func TestDistributeToLeft_Rotation_Full(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with two children; current full, left has room
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, 3)

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	// left not full (room at end)
	lv := "l"
	left.Slots[0] = Item[int, string]{Key: 1, Value: &lv, ID: sop.NewUUID()}
	left.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	// Make current node full with ascending keys
	for i := 0; i < b.getSlotLength(); i++ {
		v := "c"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: 10 + i, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	// separators [slot0, slot1]
	s0 := "s0"
	s1 := "s1"
	parent.Slots[0] = Item[int, string]{Key: 20, Value: &s0, ID: sop.NewUUID()}
	parent.Slots[1] = Item[int, string]{Key: 30, Value: &s1, ID: sop.NewUUID()}

	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = parent.ID

	item := &Item[int, string]{Key: 99, Value: &[]string{"x"}[0], ID: sop.NewUUID()}
	if err := cur.distributeToLeft(context.Background(), b, item); err != nil {
		t.Fatalf("distributeToLeft rotation err: %v", err)
	}
	// Parent slot 0 should now equal previous cur.Slots[0]
	if parent.Slots[0].Key != 10 {
		t.Fatalf("parent slot0 not updated from cur.Slots[0]; got %v", parent.Slots[0].Key)
	}
	// Controller prepared: next distribution should be to left sibling
	if b.distributeAction.sourceNode == nil || b.distributeAction.sourceNode.ID != left.ID || !b.distributeAction.distributeToLeft {
		t.Fatalf("expected distribution scheduled to left sibling")
	}
}

// getChildren should return slice with nils for NilUUID entries and actual node pointers for others.
func TestGetChildren_MixedIDs(t *testing.T) {
	b, fnr := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	n.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Assign mixed IDs
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(n.ID)
	fnr.Add(c0)
	n.ChildrenIDs[0] = c0.ID
	// Leave [1] NilUUID
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(n.ID)
	fnr.Add(c2)
	n.ChildrenIDs[2] = c2.ID

	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID

	children, err := n.getChildren(context.Background(), b)
	if err != nil {
		t.Fatalf("getChildren err: %v", err)
	}
	if children[0] == nil || children[1] != nil || children[2] == nil {
		t.Fatalf("unexpected children slice: %+v", children)
	}
}

// If key is greater than the parent's slot and the right child is NilUUID,
// Find should break and select the parent's slot as the nearest neighbor (returning ok=false).
func TestFind_NotFound_NearestNeighborOnParentRightNilChild(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	pv := "p"
	parent.Slots[0] = Item[int, string]{Key: 10, Value: &pv, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)
	// Left child exists; right child is nil to trigger short-circuit.
	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	lv := "l"
	left.Slots[0] = Item[int, string]{Key: 5, Value: &lv, ID: sop.NewUUID()}
	left.Count = 1
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = sop.NilUUID

	fnr.Add(parent)
	fnr.Add(left)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 2

	ok, err := b.Find(context.Background(), 11, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for not found with nearest neighbor")
	}
	// Nearest neighbor should be parent slot 0 (key=10)
	if it, _ := b.GetCurrentItem(context.Background()); it.Key != 10 {
		t.Fatalf("expected current key=10; got %v", it.Key)
	}
}

// If getChild fails during Find traversal, the error should be propagated.
func TestFind_GetChildError_Propagates(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	pv := "p"
	parent.Slots[0] = Item[int, string]{Key: 10, Value: &pv, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)
	// Left child ID will error when fetched.
	bad := sop.NewUUID()
	parent.ChildrenIDs[0] = bad
	repo.errs[bad] = true

	repo.Add(parent)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 1

	// Search for a key less than 10 to navigate left (index=0 -> getChild error)
	if ok, err := b.Find(context.Background(), 5, false); err == nil || ok {
		t.Fatalf("expected error from getChild, got ok=%v err=%v", ok, err)
	}
}
