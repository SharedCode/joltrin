package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestRemoveItemOnNodeWithNilChild_Shifts(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	mkNode := func() *Node[int, string] {
		n := newNode[int, string](b.getSlotLength())
		n.newID(sop.NilUUID)
		v := "v"
		vv := v
		n.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
		n.Slots[1] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
		n.Slots[2] = Item[int, string]{Key: 30, Value: &vv, ID: sop.NewUUID()}
		n.Count = 3
		n.ChildrenIDs = make([]sop.UUID, 4)
		n.ChildrenIDs[0] = sop.NewUUID()
		n.ChildrenIDs[1] = sop.NewUUID()
		n.ChildrenIDs[2] = sop.NewUUID()
		n.ChildrenIDs[3] = sop.NewUUID()
		fnr.Add(n)
		b.StoreInfo.RootNodeID = n.ID
		return n
	}

	// Case A: left child nil at index -> shift from index, expect middle slot replaced by former right value
	nA := mkNode()
	nA.ChildrenIDs[1] = sop.NilUUID // left side nil for slot index=1
	if ok, err := nA.removeItemOnNodeWithNilChild(nil, b, 1); !ok || err != nil {
		t.Fatalf("removeItemOnNodeWithNilChild A failed: %v", err)
	}
	if nA.Count != 2 || nA.Slots[1].ID.IsNil() || nA.Slots[1].Key != 30 {
		t.Fatalf("left-nil shift did not move items correctly")
	}

	// Case B: right child nil at index -> different shift path
	nB := mkNode()
	nB.ChildrenIDs[2] = sop.NilUUID // right side nil for slot index=1 (index+1)
	if ok, err := nB.removeItemOnNodeWithNilChild(nil, b, 1); !ok || err != nil {
		t.Fatalf("removeItemOnNodeWithNilChild B failed: %v", err)
	}
	if nB.Count != 2 || nB.Slots[1].ID.IsNil() || nB.Slots[1].Key != 30 {
		t.Fatalf("right-nil shift did not move items correctly")
	}
}

// Cover removeItemOnNodeWithNilChild root-collapse path
func TestRemoveItemOnNodeWithNilChild_RootCollapse(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// Root with one item
	v := "r"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	root.Count = 1
	root.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Set right child nil and left child populated
	child := newNode[int, string](b.getSlotLength())
	child.newID(root.ID)
	v1 := "c1"
	v2 := "c2"
	child.Slots[0] = Item[int, string]{Key: 5, Value: &v1, ID: sop.NewUUID()}
	child.Slots[1] = Item[int, string]{Key: 15, Value: &v2, ID: sop.NewUUID()}
	child.Count = 2
	root.ChildrenIDs[0] = child.ID
	root.ChildrenIDs[1] = sop.NilUUID
	fnr.Add(root)
	fnr.Add(child)
	b.StoreInfo.RootNodeID = root.ID

	if ok, err := root.removeItemOnNodeWithNilChild(nil, b, 0); !ok || err != nil {
		t.Fatalf("root collapse remove failed: %v", err)
	}
	if root.Count != 2 || root.Slots[0].Key != 5 || root.Slots[1].Key != 15 {
		t.Fatalf("root did not adopt child contents as expected")
	}
	if root.ChildrenIDs != nil {
		t.Fatalf("expected root.ChildrenIDs=nil after merging leaf child")
	}
	if _, ok := fnr.n[child.ID]; ok {
		t.Fatalf("child node should be removed from repo after collapse")
	}
}

// Cover removeItemOnNodeWithNilChild unlink path
func TestRemoveItemOnNodeWithNilChild_Unlink(t *testing.T) {
	b, fnr := newTestBtree[string]()
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	fnr.Add(parent)
	b.StoreInfo.RootNodeID = parent.ID

	n := newNode[int, string](b.getSlotLength())
	n.newID(parent.ID)
	v := "x"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	n.Count = 1
	n.ChildrenIDs = make([]sop.UUID, 2)
	n.ChildrenIDs[0] = sop.NilUUID
	n.ChildrenIDs[1] = sop.NilUUID
	// Wire as parent's first child
	parent.ChildrenIDs[0] = n.ID
	fnr.Add(n)

	if ok, err := n.removeItemOnNodeWithNilChild(nil, b, 0); !ok || err != nil {
		t.Fatalf("unlink path remove failed: %v", err)
	}
	if _, ok := fnr.n[n.ID]; ok {
		t.Fatalf("expected empty node to be removed")
	}
	if parent.ChildrenIDs != nil {
		if len(parent.ChildrenIDs) == 0 || parent.ChildrenIDs[0] != sop.NilUUID {
			t.Fatalf("expected parent child to be NilUUID or ChildrenIDs nil")
		}
	}
}

// Cover distributeItemOnNodeWithNilChild: ensure it creates a child at the first nil slot.
func TestDistributeItemOnNodeWithNilChild(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	// Two items and three children slots with a nil in the middle
	v := "v"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	n.Count = 2
	n.ChildrenIDs = make([]sop.UUID, 3)
	n.ChildrenIDs[0] = sop.NewUUID()
	n.ChildrenIDs[1] = sop.NilUUID // first nil slot
	n.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(n)

	extraV := "x"
	extra := &Item[int, string]{Key: 15, Value: &extraV, ID: sop.NewUUID()}
	if ok := n.distributeItemOnNodeWithNilChild(b, extra); !ok {
		t.Fatalf("distributeItemOnNodeWithNilChild returned false")
	}
	if n.ChildrenIDs[1].IsNil() {
		t.Fatalf("child not created at nil slot")
	}
}

// Cover addItemOnNodeWithNilChild success path: create child and insert item into it.
func TestAddItemOnNodeWithNilChild(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	n.Count = 0
	n.ChildrenIDs = make([]sop.UUID, 1)
	n.ChildrenIDs[0] = sop.NilUUID
	fnr.Add(n)

	val := "z"
	item := &Item[int, string]{Key: 5, Value: &val, ID: sop.NewUUID()}
	if ok, err := n.addItemOnNodeWithNilChild(b, item, 0); !ok || err != nil {
		t.Fatalf("addItemOnNodeWithNilChild failed: %v", err)
	}
	if n.ChildrenIDs[0].IsNil() {
		t.Fatalf("child was not created")
	}
	// Verify child received the item
	child, _ := n.getChild(nil, b, 0)
	if child == nil || child.Count != 1 || child.Slots[0] != *item {
		t.Fatalf("child item not inserted as expected")
	}
}

// Cover btree.removeNode early return when node ID is nil.
func TestRemoveNode_EarlyReturn(t *testing.T) {
	b, _ := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength()) // ID is Nil by default
	// Should not panic and should early return.
	b.removeNode(n)
}

// Cover nodeHasNilChild cases: no children, has a nil child, and no nil children.
func TestNodeHasNilChild_Cases(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	b, _ := New[int, string](store, &StoreInterface[int, string]{NodeRepository: &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, ItemActionTracker: fakeIAT[int, string]{}}, nil)
	n := newNode[int, string](b.getSlotLength())
	if n.nodeHasNilChild() {
		t.Fatalf("no children should be false")
	}
	n.ChildrenIDs = make([]sop.UUID, 2)
	n.Count = 1
	n.ChildrenIDs[0] = sop.NewUUID()
	n.ChildrenIDs[1] = sop.NilUUID
	if !n.nodeHasNilChild() {
		t.Fatalf("expected true with a nil child")
	}
	n.ChildrenIDs[1] = sop.NewUUID()
	if n.nodeHasNilChild() {
		t.Fatalf("expected false when no nil children")
	}
}

// Cover goLeftUpItemOnNodeWithNilChild basic paths
func TestGoLeftUpItemOnNodeWithNilChild(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	// Two items
	v := "v"
	vv := v
	n.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	n.Count = 2
	// Children with a nil at index 1
	n.ChildrenIDs = make([]sop.UUID, 3)
	n.ChildrenIDs[0] = sop.NewUUID() // non-nil
	n.ChildrenIDs[1] = sop.NilUUID   // target nil
	n.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID

	// Case 1: index=1 -> i becomes 0 and returns left item
	if ok, err := n.goLeftUpItemOnNodeWithNilChild(nil, b, 1); err != nil || !ok {
		t.Fatalf("goLeftUp #1 err=%v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 1 {
		t.Fatalf("expected current=1, got %d", it.Key)
	}

	// Case 2: index=0 -> i becomes -1 and since root, returns false
	if ok, err := n.goLeftUpItemOnNodeWithNilChild(nil, b, 0); err != nil {
		t.Fatalf("goLeftUp #2 err=%v", err)
	} else if ok {
		t.Fatalf("expected false at BOF")
	}
}

// Climb to parent and select parent's left item when child has nil at index 0.
func TestGoLeftUpItemOnNodeWithNilChild_ClimbParent(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with one item and two children; we target the right child
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	v := "p"
	p.Slots[0] = Item[int, string]{Key: 100, Value: &v, ID: sop.NewUUID()}
	p.Count = 1
	p.ChildrenIDs = make([]sop.UUID, 2)

	left := newNode[int, string](b.getSlotLength())
	left.newID(p.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(p.ID)
	right.ParentID = p.ID
	// right child has nil at index 0
	right.ChildrenIDs = make([]sop.UUID, 1)
	right.ChildrenIDs[0] = sop.NilUUID
	p.ChildrenIDs[0] = left.ID
	p.ChildrenIDs[1] = right.ID

	fnr.Add(p)
	fnr.Add(left)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = p.ID

	if ok, err := right.goLeftUpItemOnNodeWithNilChild(nil, b, 0); !ok || err != nil {
		t.Fatalf("goLeftUp climb failed: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 100 {
		t.Fatalf("expected parent left item 100, got %d", it.Key)
	}
}

// Cover goRightUpItemOnNodeWithNilChild for three paths: immediate right-select, root-EOF, and climb-to-parent.
func TestGoRightUpItemOnNodeWithNilChild_Paths(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Case 1: immediate selection on right slot within same node
	n1 := newNode[int, string](b.getSlotLength())
	n1.newID(sop.NilUUID)
	v := "v"
	vv := v
	n1.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	n1.Slots[1] = Item[int, string]{Key: 2, Value: &vv, ID: sop.NewUUID()}
	n1.Count = 2
	n1.ChildrenIDs = make([]sop.UUID, 3)
	n1.ChildrenIDs[0] = sop.NewUUID()
	n1.ChildrenIDs[1] = sop.NilUUID // target nil
	n1.ChildrenIDs[2] = sop.NewUUID()
	fnr.Add(n1)
	b.StoreInfo.RootNodeID = n1.ID
	if ok, err := n1.goRightUpItemOnNodeWithNilChild(nil, b, 1); !ok || err != nil {
		t.Fatalf("goRightUp immediate failed: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 2 {
		t.Fatalf("expected current=2")
	}

	// Case 2: at root end -> false
	n2 := newNode[int, string](b.getSlotLength())
	n2.newID(sop.NilUUID)
	n2.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	n2.Count = 1
	n2.ChildrenIDs = make([]sop.UUID, 2)
	n2.ChildrenIDs[0] = sop.NewUUID()
	n2.ChildrenIDs[1] = sop.NilUUID
	fnr.Add(n2)
	b.StoreInfo.RootNodeID = n2.ID
	if ok, err := n2.goRightUpItemOnNodeWithNilChild(nil, b, 1); err != nil {
		t.Fatalf("goRightUp root EOF err: %v", err)
	} else if ok {
		t.Fatalf("expected false at EOF")
	}

	// Case 3: climb to parent and select parent's item
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Slots[0] = Item[int, string]{Key: 100, Value: &vv, ID: sop.NewUUID()}
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, 2)

	n3 := newNode[int, string](b.getSlotLength())
	n3.newID(parent.ID)
	n3.ParentID = parent.ID
	// Child has no items; we need a nil child at index equal to Count (0)
	n3.ChildrenIDs = make([]sop.UUID, 1)
	n3.ChildrenIDs[0] = sop.NilUUID

	parent.ChildrenIDs[0] = n3.ID
	fnr.Add(parent)
	fnr.Add(n3)
	b.StoreInfo.RootNodeID = parent.ID

	if ok, err := n3.goRightUpItemOnNodeWithNilChild(nil, b, 0); !ok || err != nil {
		t.Fatalf("goRightUp climb failed: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); it.Key != 100 {
		t.Fatalf("expected current=100 from parent")
	}
}

// Cover early-return branches of goRightUpItemOnNodeWithNilChild/goLeftUpItemOnNodeWithNilChild
// when the target child is non-nil: they should return (false, nil) without changing selection.
func TestGoRightLeftUp_WithNonNilChild_ReturnsFalse(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// One slot and two children positions
	root.Slots[0] = Item[int, string]{Key: 10, ID: sop.NewUUID()}
	root.Count = 1
	// Prepare non-nil child at index 0 for left-up path and at index 1 for right-up path
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(root.ID)
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(root.ID)
	root.ChildrenIDs = []sop.UUID{c0.ID, c1.ID}
	fnr.Add(root)
	fnr.Add(c0)
	fnr.Add(c1)
	b.StoreInfo.RootNodeID = root.ID

	// Set a current selection distinct from any branch outcome to detect changes
	b.setCurrentItemID(root.ID, 0)

	if ok, err := root.goRightUpItemOnNodeWithNilChild(nil, b, 1); err != nil || ok {
		t.Fatalf("goRightUp with non-nil child should return false,nil, got ok=%v err=%v", ok, err)
	}
	// left-up with non-nil child at index 0
	if ok, err := root.goLeftUpItemOnNodeWithNilChild(nil, b, 0); err != nil || ok {
		t.Fatalf("goLeftUp with non-nil child should return false,nil, got ok=%v err=%v", ok, err)
	}
}

// Verify goRightUp/LeftUp propagate error from getParent when child is nil and node is not root.
func TestGoRightLeftUp_NilChild_ParentGetError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &repoErrOnIDs[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parentID := sop.NewUUID()
	repo.errs[parentID] = true

	n := newNode[int, string](b.getSlotLength())
	n.ID = sop.NewUUID()
	n.ParentID = parentID
	// one slot, but ensure no right slot at index, forcing parent lookup
	n.Count = 1
	n.ChildrenIDs = make([]sop.UUID, 2)
	// make child nil at both positions to trigger the helpers
	n.ChildrenIDs[0] = sop.NilUUID
	n.ChildrenIDs[1] = sop.NilUUID
	repo.Add(n)

	if ok, err := n.goRightUpItemOnNodeWithNilChild(nil, b, 1); err == nil || ok {
		t.Fatalf("expected error from goRightUpItemOnNodeWithNilChild via parent.Get, ok=%v err=%v", ok, err)
	}
	if ok, err := n.goLeftUpItemOnNodeWithNilChild(nil, b, 0); err == nil || ok {
		t.Fatalf("expected error from goLeftUpItemOnNodeWithNilChild via parent.Get, ok=%v err=%v", ok, err)
	}
}
