package btree

// Scenario file merged from: remove_current_item_test.go, remove_current_item_more_test.go, remove_current_item_error_test.go
// NOTE: Pure content merge; original files removed.

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// (from remove_current_item_test.go)
// No selection -> RemoveCurrentItem returns false, nil.
func TestRemoveCurrentItem_NoSelection(t *testing.T) {
	b, _ := newTestBtree[string]()
	b.setCurrentItemID(sop.NilUUID, 0)
	if ok, err := b.RemoveCurrentItem(nil); ok || err != nil {
		t.Fatalf("expected false,nil when no selection, got ok=%v err=%v", ok, err)
	}
}

// Current points to a node/slot that's nil -> returns false, nil.
func TestRemoveCurrentItem_SlotNil(t *testing.T) {
	b, fnr := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	// No slot value assigned
	n.Count = 1
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(n.ID, 0)
	if ok, err := b.RemoveCurrentItem(nil); ok || err != nil {
		t.Fatalf("expected false,nil when slot is nil, got ok=%v err=%v", ok, err)
	}
}

// Leaf deletion path: node has no children, fixVacatedSlot runs, selection cleared.
func TestRemoveCurrentItem_LeafDelete(t *testing.T) {
	b, fnr := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v := "x"
	n.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID()}
	n.Count = 1
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(n.ID, 0)

	ok, err := b.RemoveCurrentItem(nil)
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem leaf err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("expected selection cleared")
	}
	if b.StoreInfo.Count != 0 {
		t.Fatalf("expected store count=0, got %d", b.StoreInfo.Count)
	}
}

// Internal with children: exercise removeItemOnNodeWithNilChild early path returning true.
func TestRemoveCurrentItem_WithChildren_ShiftPath(t *testing.T) {
	b, fnr := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v := "v"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	n.Count = 2
	// Children with a nil on the right of index 1 to trigger the right-nil shift path
	n.ChildrenIDs = make([]sop.UUID, 3)
	n.ChildrenIDs[0] = sop.NewUUID()
	n.ChildrenIDs[1] = sop.NewUUID()
	n.ChildrenIDs[2] = sop.NilUUID
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	b.StoreInfo.Count = 2
	b.setCurrentItemID(n.ID, 1) // select key 20 at index 1

	ok, err := b.RemoveCurrentItem(nil)
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem with-children err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("expected selection cleared")
	}
	if b.StoreInfo.Count != 1 {
		t.Fatalf("expected store count=1, got %d", b.StoreInfo.Count)
	}
}

// Internal node with both children non-nil: RemoveCurrentItem should move to a leaf,
// replace the internal slot with the leaf item, then delete on the leaf via fixVacatedSlot.
func TestRemoveCurrentItem_Internal_MoveToLeafReplacement(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Root with two items and three non-nil children
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 50, Value: &vv, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 100, Value: &vv, ID: sop.NewUUID()}
	root.Count = 2
	root.ChildrenIDs = make([]sop.UUID, 3)
	// Create left and middle children (non-nil) so early remove path returns false
	left := newNode[int, string](b.getSlotLength())
	left.newID(root.ID)
	mid := newNode[int, string](b.getSlotLength())
	mid.newID(root.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	root.ChildrenIDs[0] = left.ID
	root.ChildrenIDs[1] = mid.ID
	root.ChildrenIDs[2] = right.ID
	// Left is a leaf with two items
	left.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	left.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	left.Count = 2

	fnr.Add(root)
	fnr.Add(left)
	fnr.Add(mid)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = root.ID
	// Total items across nodes (root has 2, left has 2). We only care about decremented count.
	b.StoreInfo.Count = 4

	// Select root index 0
	b.setCurrentItemID(root.ID, 0)
	ok, err := b.RemoveCurrentItem(nil)
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem internal->leaf path err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("expected selection cleared")
	}
	if b.StoreInfo.Count != 3 {
		t.Fatalf("expected store count decremented to 3, got %d", b.StoreInfo.Count)
	}
}

// (from remove_current_item_more_test.go)
func TestRemoveCurrentItem_WithChildren_LeftNilChild_Shortcut(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Build a node with two items and a nil left child at index 0
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v := "v"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	n.Count = 2
	n.ChildrenIDs = make([]sop.UUID, 3)
	// Left child (index 0) nil triggers removeItemOnNodeWithNilChild path
	n.ChildrenIDs[0] = sop.NilUUID
	n.ChildrenIDs[1] = sop.NewUUID()
	n.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	b.StoreInfo.Count = 2

	// Select index 0 and remove
	b.setCurrentItemID(n.ID, 0)
	ok, err := b.RemoveCurrentItem(context.TODO())
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("selection should be cleared")
	}
	if b.Count() != 1 {
		t.Fatalf("Store count should decrement to 1, got %d", b.Count())
	}
}

func TestRemoveCurrentItem_RootSingleItem_Delete(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(root.ID, 0)

	ok, err := b.RemoveCurrentItem(context.TODO())
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem root err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("selection should be cleared")
	}
	if b.Count() != 0 {
		t.Fatalf("store count should be 0, got %d", b.Count())
	}
	if root.Count != 0 {
		t.Fatalf("root not cleared")
	}
}

func TestRemoveCurrentItem_NonRoot_UnlinkEarly(t *testing.T) {
	b, fnr := newTestBtree[string]()
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	// No ChildrenIDs on parent -> hasChildren() false triggers early return in unlink
	child := newNode[int, string](b.getSlotLength())
	child.newID(parent.ID)
	v := "v"
	vv := v
	child.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	child.Count = 1
	fnr.Add(parent)
	fnr.Add(child)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(child.ID, 0)

	ok, err := b.RemoveCurrentItem(context.TODO())
	if err != nil || !ok {
		t.Fatalf("RemoveCurrentItem unlink err=%v ok=%v", err, ok)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("selection should be cleared")
	}
	if b.Count() != 0 {
		t.Fatalf("store count should be 0, got %d", b.Count())
	}
}

// (from remove_current_item_error_test.go)
// Trigger the RemoveCurrentItem branch where removeItemOnNodeWithNilChild returns false
// (both adjacent children non-nil) and moveToNext returns an error from the repository.
func TestRemoveCurrentItem_MoveToNext_Error(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Root with one item and two non-nil children so early nil-child delete path is skipped.
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1
	left := newNode[int, string](b.getSlotLength())
	left.newID(root.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = left.ID
	root.ChildrenIDs[1] = right.ID

	// Add to repo and force an error only when fetching the right child during moveToNext.
	fnr.Add(root)
	fnr.Add(left)
	fnr.Add(right)
	fnr.errs[right.ID] = true

	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(root.ID, 0)

	ok, err := b.RemoveCurrentItem(context.TODO())
	if err == nil || ok {
		t.Fatalf("expected RemoveCurrentItem to return error via moveToNext, got ok=%v err=%v", ok, err)
	}
}

// Cover RemoveCurrentItem when node has children and removal occurs on leaf path with IAT error
func TestRemoveCurrentItem_LeafPath_IATError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatRemoveErr2[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Build a small tree with root and a left child so that root has children
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1

	leaf := newNode[int, string](b.getSlotLength())
	leaf.newID(root.ID)
	// place a greater key in leaf so moveToNext() will step into leaf when deleting root item
	leaf.Slots[0] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	leaf.Count = 1
	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = sop.NilUUID
	root.ChildrenIDs[1] = leaf.ID

	fnr.Add(root)
	fnr.Add(leaf)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 2

	// Select the root item and remove current; this triggers the move-to-leaf replacement path
	b.setCurrentItemID(root.ID, 0)
	if ok, err := b.RemoveCurrentItem(nil); err == nil || ok {
		t.Fatalf("expected error from IAT.Remove during leaf delete path, got ok=%v err=%v", ok, err)
	}
}

// Cover RemoveCurrentItem when node has children and removeItemOnNodeWithNilChild returns ok=true (unlink path),
// ensuring ItemActionTracker.Remove is called and count decremented.
func TestRemoveCurrentItem_NodeWithChildren_ImmediateUnlink_Success(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Parent root
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = parent.ID

	// Child node to be unlinked after deletion
	child := newNode[int, string](b.getSlotLength())
	child.newID(parent.ID)
	v := "v"
	vv := v
	child.Slots[0] = Item[int, string]{Key: 5, Value: &vv, ID: sop.NewUUID()}
	child.Count = 1
	// Children slice exists but all nil -> hasChildren true, triggers unlink path when Count becomes 0
	child.ChildrenIDs = make([]sop.UUID, 2)

	parent.ChildrenIDs = make([]sop.UUID, 1)
	parent.ChildrenIDs[0] = child.ID

	fnr.Add(parent)
	fnr.Add(child)
	b.StoreInfo.Count = 1

	// Select child's item and remove
	b.setCurrentItemID(child.ID, 0)
	if ok, err := b.RemoveCurrentItem(nil); err != nil || !ok {
		t.Fatalf("expected successful removal with unlink, got ok=%v err=%v", ok, err)
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("current selection should be cleared after removal")
	}
	if b.Count() != 0 {
		t.Fatalf("store count should decrement to 0, got %d", b.Count())
	}
}

// Covers RemoveCurrentItem path where starting at an internal node with children,
// removeItemOnNodeWithNilChild returns false, so the code replaces the target with
// the next leaf item and then deletes on the leaf via unlink path (ok=true).
func TestRemoveCurrentItem_InternalReplaceThenLeafUnlink_Success(t *testing.T) {
	b, fnr := newTestBtree[string]()

	// Internal node (root) with one separator
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	// Separator key 10
	sep := Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	root.Slots[0] = sep
	root.Count = 1

	// Left child: single item < 10
	left := newNode[int, string](b.getSlotLength())
	left.newID(root.ID)
	leftVal := Item[int, string]{Key: 5, Value: &vv, ID: sop.NewUUID()}
	left.Slots[0] = leftVal
	left.Count = 1

	// Right child (leaf): single item > 10; give it children slice to allow unlink path
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	rightVal := Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	right.Slots[0] = rightVal
	right.Count = 1
	right.ChildrenIDs = make([]sop.UUID, 2) // both nil -> hasChildren true to exercise unlink path

	// Wire root children (both non-nil, so initial removeItemOnNodeWithNilChild on root returns false)
	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = left.ID
	root.ChildrenIDs[1] = right.ID

	fnr.Add(root)
	fnr.Add(left)
	fnr.Add(right)

	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 3 // sep is not counted separately in store; simulate 2 items
	b.StoreInfo.Count = 2

	// Select the separator at root and remove current; should replace with right leaf item and
	// then unlink-remove the right leaf's item (ok=true path), decrementing count.
	b.setCurrentItemID(root.ID, 0)
	if ok, err := b.RemoveCurrentItem(nil); err != nil || !ok {
		t.Fatalf("expected successful removal via leaf unlink, ok=%v err=%v", ok, err)
	}
	if b.Count() != 1 {
		t.Fatalf("after removal, count should be 1, got %d", b.Count())
	}
}
