package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Covers removeItemOnNodeWithNilChild branch where Count becomes 0 on root with a non-nil single child,
// causing the root to merge the child's contents (copy slots/children) and remove the child from repo.
func TestRemoveItemOnNodeWithNilChild_RootMergesChild(t *testing.T) {
	b, fnr := newTestBtree[string]()

	// Root with one item and two child pointers where the right child is nil to trigger the right-nil path.
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID

	// Single item to delete at index 0, Count=1 so Count-- => 0 hits the special branch
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1

	// Child on the left, nil on the right at index 1
	child := newNode[int, string](b.getSlotLength())
	child.newID(root.ID)
	// Give child some contents that will be merged into root
	child.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	child.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	child.Count = 2

	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = child.ID
	root.ChildrenIDs[1] = sop.NilUUID

	fnr.Add(root)
	fnr.Add(child)

	// Select current at index 0 for deletion
	b.setCurrentItemID(root.ID, 0)
	ok, err := root.removeItemOnNodeWithNilChild(nil, b, 0)
	if err != nil || !ok {
		t.Fatalf("removeItemOnNodeWithNilChild root-merge err=%v ok=%v", err, ok)
	}
	// Root should now contain child's items
	if root.Count != 2 || root.Slots[0].ID.IsNil() || root.Slots[1].ID.IsNil() {
		t.Fatalf("root did not merge child contents correctly")
	}
	// Children should be nilified since child had no children
	if root.ChildrenIDs != nil {
		t.Fatalf("expected root.ChildrenIDs nil after merge, got %v", root.ChildrenIDs)
	}
	// Child should be removed from repo
	if _, exists := fnr.n[child.ID]; exists {
		t.Fatalf("child node should be removed from repository")
	}
}

// Covers removeItemOnNodeWithNilChild branch where Count becomes 0 and first child is nil,
// causing unlink() path (non-root), pruning from parent and removing the node.
func TestRemoveItemOnNodeWithNilChild_UnlinkWhenAllChildrenNil(t *testing.T) {
	b, fnr := newTestBtree[string]()

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = parent.ID

	// Child node with one item and children slice of len 2 all nil to trigger unlink path after delete
	child := newNode[int, string](b.getSlotLength())
	child.newID(parent.ID)
	v := "v"
	vv := v
	child.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	child.Count = 1
	child.ChildrenIDs = make([]sop.UUID, 2) // both zero => NilUUID

	// Wire parent -> child
	parent.ChildrenIDs = make([]sop.UUID, 1)
	parent.ChildrenIDs[0] = child.ID

	fnr.Add(parent)
	fnr.Add(child)

	b.setCurrentItemID(child.ID, 0)
	ok, err := child.removeItemOnNodeWithNilChild(nil, b, 0)
	if err != nil || !ok {
		t.Fatalf("removeItemOnNodeWithNilChild unlink err=%v ok=%v", err, ok)
	}
	// Child should be removed from repo
	if _, exists := fnr.n[child.ID]; exists {
		t.Fatalf("child node should have been removed")
	}
	// Parent should prune its children slice to nil (all NilUUID)
	if parent.ChildrenIDs != nil {
		t.Fatalf("expected parent.ChildrenIDs nil after pruning, got %v", parent.ChildrenIDs)
	}
}

// Covers promoteSingleChildAsParentChild error branch when parent cannot be fetched.
func TestPromoteSingleChildAsParentChild_ParentMissing_Error(t *testing.T) {
	b, fnr := newTestBtree[string]()

	// Node references a non-existent parent ID
	node := newNode[int, string](b.getSlotLength())
	node.newID(sop.NewUUID())
	node.ParentID = sop.NewUUID() // not added to repo
	node.ChildrenIDs = make([]sop.UUID, 1)
	c := newNode[int, string](b.getSlotLength())
	c.newID(node.ID)
	node.ChildrenIDs[0] = c.ID

	fnr.Add(node)
	fnr.Add(c)

	if ok, err := node.promoteSingleChildAsParentChild(nil, b); err == nil || ok {
		t.Fatalf("expected error due to missing parent, got ok=%v err=%v", ok, err)
	}
}

// Covers promoteSingleChildAsParentChild success branch where the parent exists and the only child is promoted,
// updating the parent's child pointer, saving both, and removing the intermediate node.
func TestPromoteSingleChildAsParentChild_Success(t *testing.T) {
	b, fnr := newTestBtree[string]()

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = parent.ID
	parent.ChildrenIDs = make([]sop.UUID, 1)

	node := newNode[int, string](b.getSlotLength())
	node.newID(parent.ID)
	parent.ChildrenIDs[0] = node.ID

	child := newNode[int, string](b.getSlotLength())
	child.newID(node.ID)
	node.ChildrenIDs = make([]sop.UUID, 1)
	node.ChildrenIDs[0] = child.ID

	fnr.Add(parent)
	fnr.Add(node)
	fnr.Add(child)

	ok, err := node.promoteSingleChildAsParentChild(nil, b)
	if err != nil || !ok {
		t.Fatalf("promoteSingleChildAsParentChild success err=%v ok=%v", err, ok)
	}
	if parent.ChildrenIDs[0] != child.ID {
		t.Fatalf("parent should now reference child directly")
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parent must be updated to parent")
	}
	if _, exists := fnr.n[node.ID]; exists {
		t.Fatalf("intermediate node should be removed")
	}
}

// Covers goRightUpItemOnNodeWithNilChild when ascending reaches root and returns end-of-tree.
func TestGoRightUpItemOnNodeWithNilChild_RootEnd(t *testing.T) {
	b, fnr := newTestBtree[string]()

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// Two items so Count=2; pass index=2 to simulate no right item, with nil right child
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	root.Count = 2
	root.ChildrenIDs = make([]sop.UUID, 3)
	root.ChildrenIDs[2] = sop.NilUUID

	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	ok, err := root.goRightUpItemOnNodeWithNilChild(nil, b, 2)
	if err != nil {
		t.Fatalf("goRightUp err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false at end-of-tree")
	}
	// Current selection should be cleared
	if b.isCurrentItemSelected() {
		t.Fatalf("expected current selection cleared (nil)")
	}
}

// Covers goRightUpItemOnNodeWithNilChild happy path where the right slot exists in the same node.
func TestGoRightUpItemOnNodeWithNilChild_SelectRightInSameNode(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	root.Count = 2
	root.ChildrenIDs = make([]sop.UUID, 3)
	// Nil child at index 1 to trigger goRightUp path
	root.ChildrenIDs[1] = sop.NilUUID
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	ok, err := root.goRightUpItemOnNodeWithNilChild(nil, b, 1)
	if err != nil || !ok {
		t.Fatalf("goRightUp select err=%v ok=%v", err, ok)
	}
	it, _ := b.GetCurrentItem(nil)
	if got := it.Key; got != 2 {
		t.Fatalf("expected to select key 2, got %v", got)
	}
}

// Covers goLeftUpItemOnNodeWithNilChild when index=0 (i becomes -1) and at root -> end-of-tree.
func TestGoLeftUpItemOnNodeWithNilChild_RootEnd(t *testing.T) {
	b, fnr := newTestBtree[string]()

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1
	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = sop.NilUUID // ensure left child nil

	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	ok, err := root.goLeftUpItemOnNodeWithNilChild(nil, b, 0)
	if err != nil {
		t.Fatalf("goLeftUp err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false at beginning-of-tree")
	}
	if b.isCurrentItemSelected() {
		t.Fatalf("expected current selection cleared (nil)")
	}
}

// Covers goLeftUpItemOnNodeWithNilChild happy path where there is a left slot in the same node.
func TestGoLeftUpItemOnNodeWithNilChild_SelectLeftInSameNode(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	root.Count = 2
	root.ChildrenIDs = make([]sop.UUID, 3)
	root.ChildrenIDs[1] = sop.NilUUID // ensure we go left within node
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	ok, err := root.goLeftUpItemOnNodeWithNilChild(nil, b, 1)
	if err != nil || !ok {
		t.Fatalf("goLeftUp select err=%v ok=%v", err, ok)
	}
	it, _ := b.GetCurrentItem(nil)
	if got := it.Key; got != 1 {
		t.Fatalf("expected to select key 1, got %v", got)
	}
}

// Covers distributeItemOnNodeWithNilChild returning false when there are no children
// and when all positions up to Count are non-nil (no vacancy).
func TestDistributeItemOnNodeWithNilChild_NoChildOrNoVacancy(t *testing.T) {
	b, _ := newTestBtree[string]()
	item := &Item[int, string]{Key: 1}

	// Case 1: node has no children slice
	n1 := newNode[int, string](b.getSlotLength())
	if ok := n1.distributeItemOnNodeWithNilChild(b, item); ok {
		t.Fatalf("expected false when node has no children slice")
	}

	// Case 2: node has children but none are NilUUID up to Count
	n2 := newNode[int, string](b.getSlotLength())
	n2.Count = 2
	n2.ChildrenIDs = make([]sop.UUID, 3)
	n2.ChildrenIDs[0] = sop.NewUUID()
	n2.ChildrenIDs[1] = sop.NewUUID()
	n2.ChildrenIDs[2] = sop.NewUUID() // ensure 0..Count are all non-nil so no vacancy
	if ok := n2.distributeItemOnNodeWithNilChild(b, item); ok {
		t.Fatalf("expected false when no nil child up to Count")
	}
}

// Covers distributeItemOnNodeWithNilChild success branch where a NilUUID child exists within 0..Count.
func TestDistributeItemOnNodeWithNilChild_Success(t *testing.T) {
	b, fnr := newTestBtree[string]()
	item := &Item[int, string]{Key: 42, Value: new(string), ID: sop.NewUUID()}

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	n.Count = 1
	n.ChildrenIDs = make([]sop.UUID, 2)
	// index 0 is NilUUID -> should be used
	fnr.Add(n)

	if ok := n.distributeItemOnNodeWithNilChild(b, item); !ok {
		t.Fatalf("expected success distributing to nil child")
	}
	if n.ChildrenIDs[0].IsNil() {
		t.Fatalf("expected child ID to be set")
	}
	// Ensure new child exists in repo and contains the item
	child := fnr.n[n.ChildrenIDs[0]]
	if child == nil || child.Count != 1 || child.Slots[0].ID.IsNil() || child.Slots[0].Key != 42 {
		t.Fatalf("distributed child not created or item not set")
	}
}

// Covers removeItemOnNodeWithNilChild branch (non-root) where Count becomes 0 and node has a single non-nil child,
// leading to promoteSingleChildAsParentChild path.
func TestRemoveItemOnNodeWithNilChild_PromoteSingleChildNonRoot(t *testing.T) {
	b, fnr := newTestBtree[string]()
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = parent.ID
	parent.ChildrenIDs = make([]sop.UUID, 1)

	node := newNode[int, string](b.getSlotLength())
	node.newID(parent.ID)
	parent.ChildrenIDs[0] = node.ID
	// One item to delete, Count will become 0
	v := "v"
	vv := v
	node.Slots[0] = Item[int, string]{Key: 7, Value: &vv, ID: sop.NewUUID()}
	node.Count = 1
	// Single non-nil child
	child := newNode[int, string](b.getSlotLength())
	child.newID(node.ID)
	node.ChildrenIDs = make([]sop.UUID, 2)
	node.ChildrenIDs[0] = child.ID
	node.ChildrenIDs[1] = sop.NilUUID

	fnr.Add(parent)
	fnr.Add(node)
	fnr.Add(child)

	b.setCurrentItemID(node.ID, 0)
	ok, err := node.removeItemOnNodeWithNilChild(nil, b, 0)
	if err != nil || !ok {
		t.Fatalf("removeItemOnNodeWithNilChild promote err=%v ok=%v", err, ok)
	}
	if parent.ChildrenIDs[0] != child.ID {
		t.Fatalf("parent should now point to child after promotion")
	}
	if _, exists := fnr.n[node.ID]; exists {
		t.Fatalf("promoted-from node should be removed")
	}
}

// Covers removeItemOnNodeWithNilChild on root with child that itself has children: after merge, root should adopt children.
func TestRemoveItemOnNodeWithNilChild_RootMergesChild_WithChildren(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1

	child := newNode[int, string](b.getSlotLength())
	child.newID(root.ID)
	// child has one slot and a non-nil child under it
	child.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	child.Count = 1
	gc := newNode[int, string](b.getSlotLength())
	gc.newID(child.ID)
	child.ChildrenIDs = make([]sop.UUID, 2)
	child.ChildrenIDs[0] = sop.NewUUID() // ensure hasChildren true
	// Wire root children: left child is child, right is nil
	root.ChildrenIDs = make([]sop.UUID, 2)
	root.ChildrenIDs[0] = child.ID
	root.ChildrenIDs[1] = sop.NilUUID

	fnr.Add(root)
	fnr.Add(child)
	fnr.Add(gc)

	b.setCurrentItemID(root.ID, 0)
	ok, err := root.removeItemOnNodeWithNilChild(nil, b, 0)
	if err != nil || !ok {
		t.Fatalf("root merge w/ children err=%v ok=%v", err, ok)
	}
	if root.ChildrenIDs == nil || root.ChildrenIDs[0].IsNil() {
		t.Fatalf("root should adopt child's children after merge")
	}
}
