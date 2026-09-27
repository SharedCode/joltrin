package btree

import (
	"github.com/sharedcode/joltrin/v5"
	"testing"
)

// Covers getChildID branches (no children slice vs populated).
func TestGetChildID_Branches(t *testing.T) {
	b, _ := newTestBtree[string]()
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	if id := n.getChildID(0); id != sop.NilUUID {
		t.Fatalf("expected NilUUID when ChildrenIDs is nil")
	}
	cid := sop.NewUUID()
	n.ChildrenIDs = []sop.UUID{cid}
	if id := n.getChildID(0); id != cid {
		t.Fatalf("expected child id back")
	}
}

// Ensure getCurrentNode returns nil when the repo has no such node.
func TestGetCurrentNode_Nil(t *testing.T) {
	b, _ := newTestBtree[string]()
	ghost := sop.NewUUID()
	b.setCurrentItemID(ghost, 0)
	n, err := b.getCurrentNode(nil)
	if err != nil || n != nil {
		t.Fatalf("expected (nil, nil) when node not found; got n=%v err=%v", n, err)
	}
}

// Next/Previous guard when current slot is nil should return false without error.
func TestNextPrevious_WithNilSlot(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// No items in root; slot[0] is nil
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1 // pass the empty-guard
	b.setCurrentItemID(root.ID, 0)

	if ok, err := b.Next(nil); ok || err != nil {
		t.Fatalf("Next on nil slot should be (false,nil); got ok=%v err=%v", ok, err)
	}
	if ok, err := b.Previous(nil); ok || err != nil {
		t.Fatalf("Previous on nil slot should be (false,nil); got ok=%v err=%v", ok, err)
	}
}

// getChild returns nil for NilUUID child and the node for a real child id.
func TestGetChild_Branches(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	c := newNode[int, string](b.getSlotLength())
	c.newID(p.ID)
	fnr.Add(p)
	fnr.Add(c)
	p.ChildrenIDs = make([]sop.UUID, 2)
	// index 0 NilUUID -> nil child
	if ch, err := p.getChild(nil, b, 0); err != nil || ch != nil {
		t.Fatalf("expected nil child on NilUUID; got %v err=%v", ch, err)
	}
	p.ChildrenIDs[1] = c.ID
	if ch, err := p.getChild(nil, b, 1); err != nil || ch == nil || ch.ID != c.ID {
		t.Fatalf("expected real child on index 1")
	}
}

// copyArrayElements should early-return on nil args without panicking.
func TestCopyArrayElements_NilArgs(t *testing.T) {
	var dst []int
	src := []int{1, 2, 3}
	// nil destination
	copyArrayElements(dst, src, 2)
	// nil source
	dst2 := make([]int, 3)
	copyArrayElements(dst2, nil, 2)
}
