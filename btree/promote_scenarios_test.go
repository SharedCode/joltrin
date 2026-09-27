package btree

// Scenario file merged from: promote_test.go, promote_single_child_error_test.go
// NOTE: Pure content merge; originals removed.

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// (from promote_test.go)
// Promote a single child of an internal node to the parent, covering promoteSingleChildAsParentChild.
func TestPromoteSingleChildAsParentChild(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Create parent -> node -> child
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)

	node := newNode[int, string](b.getSlotLength())
	node.newID(parent.ID)
	node.ParentID = parent.ID
	node.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)

	child := newNode[int, string](b.getSlotLength())
	child.newID(node.ID)
	child.ParentID = node.ID

	// Wire
	parent.ChildrenIDs[0] = node.ID
	node.ChildrenIDs[0] = child.ID
	b.StoreInfo.RootNodeID = parent.ID
	fnr.Add(parent)
	fnr.Add(node)
	fnr.Add(child)

	if ok, err := node.promoteSingleChildAsParentChild(nil, b); !ok || err != nil {
		t.Fatalf("promoteSingleChildAsParentChild failed: %v", err)
	}
	// Parent should now reference child; child's parent should be parent
	if parent.ChildrenIDs[0] != child.ID {
		t.Fatalf("parent did not adopt child")
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parent not rewired")
	}
	// Node should be removed from repo
	if _, ok := fnr.n[node.ID]; ok {
		t.Fatalf("intermediate node was not removed")
	}
}

// Cover node.promote for two scenarios:
// 1) Root node is full and splits into left/right children (root split path).
// 2) Non-root full node splits and triggers a promotion propagation to the parent.
func TestPromote_RootSplit_And_PropagateToParent(t *testing.T) {
	b, fnr := newTestBtree[string]()
	slot := b.getSlotLength()

	// ---------- Root split path ----------
	root := newNode[int, string](slot)
	root.newID(sop.NilUUID)
	root.ChildrenIDs = make([]sop.UUID, slot+1) // must exist so clear() keeps length for assignments
	// Fill 4 items (slot length) to make it full
	for i, k := range []int{10, 20, 30, 40} {
		v := "r"
		vv := v
		root.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	root.Count = slot
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Prepare temp parent to be inserted around the middle; index 2 is fine (after 2nd slot)
	pv := "p"
	temp := &Item[int, string]{Key: 25, Value: &pv, ID: sop.NewUUID()}
	b.tempParent = *temp
	// tempParentChildren are not essential for root split verification but set for completeness
	b.tempParentChildren[0] = sop.NewUUID()
	b.tempParentChildren[1] = sop.NewUUID()

	if err := root.promote(nil, b, 2); err != nil {
		t.Fatalf("root promote split err: %v", err)
	}
	// After root split: root has 1 item (the middle), and two children IDs
	if root.Count != 1 || root.Slots[0].ID.IsNil() || root.Slots[0].Key != 25 {
		t.Fatalf("root not restructured as expected; count=%d key=%v", root.Count, func() any {
			if root.Slots[0].ID.IsNil() {
				return nil
			}
			return root.Slots[0].Key
		}())
	}
	if root.ChildrenIDs[0].IsNil() || root.ChildrenIDs[1].IsNil() {
		t.Fatalf("root children not linked after split")
	}

	// ---------- Non-root split with propagation ----------
	parent := newNode[int, string](slot)
	parent.newID(sop.NilUUID)
	parent.ChildrenIDs = make([]sop.UUID, slot+1)
	// One item in parent initially, so it has space for insertion during propagate
	pv2 := "p2"
	parent.Slots[0] = Item[int, string]{Key: 100, Value: &pv2, ID: sop.NewUUID()}
	parent.Count = 1
	fnr.Add(parent)
	b.StoreInfo.RootNodeID = parent.ID

	child := newNode[int, string](slot)
	child.newID(parent.ID)
	child.ChildrenIDs = make([]sop.UUID, slot+1)
	// Fill child to be full
	for i, k := range []int{110, 120, 130, 140} {
		v := "c"
		vv := v
		child.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	child.Count = slot
	parent.ChildrenIDs[0] = child.ID
	fnr.Add(child)

	// Prepare temp parent to be inserted on split at index 2
	pv3 := "pp"
	b.tempParent = Item[int, string]{Key: 135, Value: &pv3, ID: sop.NewUUID()}
	clear(b.tempChildren)
	if err := child.promote(nil, b, 2); err != nil {
		t.Fatalf("child promote split err: %v", err)
	}
	// Should have scheduled a promotion on the parent
	if b.promoteAction.targetNode == nil || b.promoteAction.targetNode.ID != parent.ID {
		t.Fatalf("expected promoteAction to target parent")
	}
	if b.promoteAction.slotIndex != 0 {
		t.Fatalf("expected slotIndex=0 for child at position 0; got %d", b.promoteAction.slotIndex)
	}
	// Apply controller to finish promotion into parent
	b.promote(nil)
	if parent.Count != 2 {
		t.Fatalf("parent did not receive promoted separator; count=%d", parent.Count)
	}
	if parent.ChildrenIDs[0] != child.ID || parent.ChildrenIDs[1].IsNil() {
		t.Fatalf("parent children not wired after propagate")
	}
}

// (from promote_single_child_error_test.go)
// Force promoteSingleChildAsParentChild to fail when parent can't be fetched (nil parent).
func TestPromoteSingleChildAsParentChild_Error_NoParent(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Node claims a non-nil parent ID but repo lacks that parent
	n := newNode[int, string](b.getSlotLength())
	n.ID = sop.NewUUID()
	n.ParentID = sop.NewUUID()
	// Single child setup
	n.ChildrenIDs = make([]sop.UUID, 1)
	child := newNode[int, string](b.getSlotLength())
	child.newID(n.ID)
	n.ChildrenIDs[0] = child.ID
	fnr.Add(n)
	fnr.Add(child)

	if ok, err := n.promoteSingleChildAsParentChild(context.TODO(), b); ok || err == nil {
		t.Fatalf("expected error when parent is missing in repo, got ok=%v err=%v", ok, err)
	}
}
