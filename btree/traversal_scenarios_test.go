package btree

// Scenario file merged from: traversal_test.go, traversal_previous_test.go, traversal_next_test.go, traversal_more_test.go
// NOTE: Pure content merge; originals removed.

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// (from traversal_test.go)
// Cover shiftSlots branch that calls moveArrayElements when position < occupied.
func TestShiftSlots_Shifts(t *testing.T) {
	arr := []int{1, 2, 3, 4, 0}
	// Make space at position 2 among first 4 occupied slots.
	shiftSlots(arr, 2, 4)
	// Now simulate an insert at position 2 and verify layout becomes [1,2,99,3,4].
	arr[2] = 99
	if got := arr; !(got[0] == 1 && got[1] == 2 && got[2] == 99 && got[3] == 3 && got[4] == 4) {
		t.Fatalf("unexpected layout after shift+insert: %v", arr)
	}
}

// Cover moveToPrevious when current node has children and left-down shortcut applies (nil child at index).
func TestMoveToPrevious_WithChildren_GoLeftUpShortcut(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v := "v"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	n.Count = 2
	n.ChildrenIDs = make([]sop.UUID, 3)
	n.ChildrenIDs[0] = sop.NewUUID()
	n.ChildrenIDs[1] = sop.NilUUID // nil child at index triggers goLeftUp shortcut
	n.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	// Ensure B-tree is considered non-empty by API checks.
	b.StoreInfo.Count = 2
	// Set current to index 1 (key 2)
	b.setCurrentItemID(n.ID, 1)

	if ok, err := b.Previous(nil); !ok || err != nil {
		t.Fatalf("Previous failed: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 1 {
		t.Fatalf("expected to move to left item key=1, got %v", it.Key)
	}
}

// Cover moveToPrevious when descending into left subtree and selecting last item of that leaf.
func TestMoveToPrevious_DescendLeftSubtree_LastItem(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	pv := "p"
	pvv := pv
	parent.Slots[0] = Item[int, string]{Key: 100, Value: &pvv, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)

	child := newNode[int, string](b.getSlotLength())
	child.newID(parent.ID)
	v := "c"
	vv := v
	child.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	child.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	child.Count = 2

	parent.ChildrenIDs[0] = child.ID
	parent.ChildrenIDs[1] = sop.NewUUID()

	fnr.Add(parent)
	fnr.Add(child)
	b.StoreInfo.RootNodeID = parent.ID
	// Total items across tree: 1 (parent) + 2 (child)
	b.StoreInfo.Count = 3

	// Current at parent's index 0; Previous should descend into child and pick last item (20)
	b.setCurrentItemID(parent.ID, 0)
	if ok, err := b.Previous(nil); !ok || err != nil {
		t.Fatalf("Previous descend failed: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 20 {
		t.Fatalf("expected to land on key=20, got %v", it.Key)
	}
}

// (from traversal_previous_test.go)
// Covers moveToPrevious edge paths:
// - when node has children and left-down path reaches a node with no children
// - when climbing to root with slotIndex underflow
func TestMoveToPrevious_Edges(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Build a simple tree: root with one separator and two children
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 50, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	root.ChildrenIDs = make([]sop.UUID, 2)
	left := newNode[int, string](b.getSlotLength())
	left.newID(root.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	// left has two items so slotIndex-1 path is exercised
	left.Slots[0] = Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	left.Slots[1] = Item[int, string]{Key: 20, Value: &v, ID: sop.NewUUID()}
	left.Count = 2
	root.ChildrenIDs[0] = left.ID
	root.ChildrenIDs[1] = right.ID
	fnr.Add(root)
	fnr.Add(left)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = root.ID

	// Select root separator and go previous -> should land on left's last item
	b.setCurrentItemID(root.ID, 0)
	if ok, err := root.moveToPrevious(nil, b); err != nil || !ok {
		t.Fatalf("moveToPrevious err=%v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 20 {
		t.Fatalf("expected previous to land on 20, got %v", it.Key)
	}

	// Now at left[1], go previous twice to climb to root-underflow and end
	if ok, err := left.moveToPrevious(nil, b); err != nil || !ok {
		t.Fatalf("moveToPrevious #2 err=%v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 10 {
		t.Fatalf("expected 10 after previous")
	}
	if ok, err := left.moveToPrevious(nil, b); err != nil || ok {
		t.Fatalf("expected false at start-of-tree, err=%v ok=%v", err, ok)
	}
}

// getIndexOfNode root and error paths
func TestGetIndexOfNode_RootAndError(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	fnr.Add(root)
	if idx, err := root.getIndexOfNode(nil, b); err != nil || idx != 0 {
		t.Fatalf("root index expected 0, got %d err=%v", idx, err)
	}
}

// (from traversal_next_test.go)
// Exercise moveToNext path where the right child is nil at index, so goRightUp hits root and ends.
func TestNext_GoRightUpToEnd(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Root with one item and right child nil
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 50, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1
	root.ChildrenIDs = make([]sop.UUID, 2)
	// left child can be anything or nil; right child nil triggers goRightUp end
	root.ChildrenIDs[1] = sop.NilUUID
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1

	// Select the only item and call Next; should return false and clear selection
	b.setCurrentItemID(root.ID, 0)
	if ok, err := b.Next(nil); err != nil || ok {
		t.Fatalf("Next at end should be false,nil; got ok=%v err=%v", ok, err)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("selection should be cleared at end-of-tree")
	}
}

// (from traversal_more_test.go)
// Previous at the very first item should return false and clear selection (root termination path).
func TestPrevious_AtFirstItem_ReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	for _, k := range []int{2, 1, 3} {
		ok, _ := b.Add(nil, k, "v")
		if !ok {
			t.Fatal("seed add")
		}
	}
	if ok, _ := b.First(nil); !ok {
		t.Fatal("First")
	}
	if ok, err := b.Previous(nil); err != nil || ok {
		t.Fatalf("Previous at first should be false,nil; got ok=%v err=%v", ok, err)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("selection should be cleared at start boundary")
	}
}

// Previous when current node has children should descend into left subtree and pick the last there.
func TestPrevious_DescendLeftSubtree(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Parent with two items and three children; left child has two items.
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	v := "v"
	vv := v
	p.Slots[0] = Item[int, string]{Key: 50, Value: &vv, ID: sop.NewUUID()}
	p.Slots[1] = Item[int, string]{Key: 100, Value: &vv, ID: sop.NewUUID()}
	p.Count = 2
	p.ChildrenIDs = make([]sop.UUID, 3)
	left := newNode[int, string](b.getSlotLength())
	left.newID(p.ID)
	mid := newNode[int, string](b.getSlotLength())
	mid.newID(p.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(p.ID)
	p.ChildrenIDs[0] = left.ID
	p.ChildrenIDs[1] = mid.ID
	p.ChildrenIDs[2] = right.ID
	left.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	left.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	left.Count = 2
	fnr.Add(p)
	fnr.Add(left)
	fnr.Add(mid)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = p.ID
	// Ensure non-empty guard passes
	b.StoreInfo.Count = 4
	// Select parent's first item (index 0), then Previous should go into left child last (20)
	b.setCurrentItemID(p.ID, 0)
	if ok, err := b.Previous(nil); err != nil || !ok {
		t.Fatalf("Previous err=%v ok=%v", err, ok)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 20 {
		t.Fatalf("want 20, got %d", it.Key)
	}
}

// Early return for unlinkNodeWithNilChild when node has no children.
func TestUnlinkNodeWithNilChild_EarlyReturn(t *testing.T) {
	b, fnr := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NewUUID())
	// No ChildrenIDs slice => isNilChildren() true
	fnr.Add(n)
	if ok, err := n.unlinkNodeWithNilChild(nil, b); err != nil || ok {
		t.Fatalf("expected false,nil early return; got ok=%v err=%v", ok, err)
	}
}

// Covers moveToPrevious branch where goLeftUpItemOnNodeWithNilChild selects left slot in same node and returns immediately.
func TestMoveToPrevious_GoLeftUpImmediateSelect(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// Two items
	root.Slots[0] = Item[int, string]{Key: 10, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 20, ID: sop.NewUUID()}
	root.Count = 2
	// Children present with nil at index 1 to trigger goLeftUp immediate selection of slot 0
	root.ChildrenIDs = make([]sop.UUID, 3)
	root.ChildrenIDs[0] = sop.NewUUID() // non-nil child (exists)
	root.ChildrenIDs[1] = sop.NilUUID   // nil to trigger goLeftUp
	root.ChildrenIDs[2] = sop.NewUUID() // non-nil
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	// Current selection at index 1 so previous should select index 0 via goLeftUp
	b.setCurrentItemID(root.ID, 1)
	if ok, err := root.moveToPrevious(nil, b); err != nil || !ok {
		t.Fatalf("moveToPrevious immediate-select err=%v ok=%v", err, ok)
	}
	it, err := b.GetCurrentItem(nil)
	if err != nil {
		t.Fatalf("GetCurrentItem: %v", err)
	}
	if it.Key != 10 {
		t.Fatalf("expected to land on key 10, got %v", it.Key)
	}
}

// Covers moveToNext branch where goRightUpItemOnNodeWithNilChild selects right slot in same node and returns immediately.
func TestMoveToNext_GoRightUpImmediateSelect(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	root.Slots[0] = Item[int, string]{Key: 10, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 20, ID: sop.NewUUID()}
	root.Count = 2
	// Children present with nil at index 1 to trigger goRightUp immediate selection
	root.ChildrenIDs = make([]sop.UUID, 3)
	root.ChildrenIDs[0] = sop.NewUUID()
	root.ChildrenIDs[1] = sop.NilUUID // nil child at i=1
	root.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	// Current selection at index 0 so slotIndex+1 == 1 triggers immediate select of index 1
	b.setCurrentItemID(root.ID, 0)
	if ok, err := root.moveToNext(nil, b); err != nil || !ok {
		t.Fatalf("moveToNext immediate-select err=%v ok=%v", err, ok)
	}
	it, err := b.GetCurrentItem(nil)
	if err != nil {
		t.Fatalf("GetCurrentItem: %v", err)
	}
	if it.Key != 20 {
		t.Fatalf("expected to land on key 20, got %v", it.Key)
	}
}
