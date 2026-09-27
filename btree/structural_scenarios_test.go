package btree

// Consolidated from: compare_more_test.go, vacancy_checks_test.go, unlink_test.go
import (
	"github.com/sharedcode/joltrin/v5"
	"testing"
)

type customCmp int

func (customCmp) Compare(other interface{}) int { return -1 }

func TestBtreeCompare_CustomComparer(t *testing.T) {
	b, _ := newTestBtree[string]()
	b.comparer = func(a, b int) int {
		if a < b {
			return -1
		} else if a > b {
			return 1
		} else {
			return 0
		}
	}
	if got := b.compare(1, 2); got != -1 {
		t.Fatalf("custom comparer not used, got %d", got)
	}
}

func TestIsThereVacantSlotInLeft_Cases(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NewUUID())
	n.ChildrenIDs = make([]sop.UUID, 2)
	n.ChildrenIDs[0] = sop.NilUUID
	fnr.Add(n)
	if ok, err := n.isThereVacantSlotInLeft(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true due to nil child, err=%v", err)
	}

	n2 := newNode[int, string](b.getSlotLength())
	n2.newID(sop.NewUUID())
	n2.ChildrenIDs = []sop.UUID{sop.NewUUID()}
	fnr.Add(n2)
	unb := false
	if ok, err := n2.isThereVacantSlotInLeft(nil, b, &unb); err != nil || ok || !unb {
		t.Fatalf("expected unbalanced=false return, got ok=%v unb=%v err=%v", ok, unb, err)
	}

	n3 := newNode[int, string](b.getSlotLength())
	n3.newID(sop.NewUUID())
	fnr.Add(n3)
	if ok, err := n3.isThereVacantSlotInLeft(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true for not-full leaf, err=%v", err)
	}

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, 3)
	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	full := newNode[int, string](b.getSlotLength())
	full.newID(parent.ID)
	full.Count = b.getSlotLength()
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = full.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(full)
	if ok, err := full.isThereVacantSlotInLeft(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true by scanning to left sibling, err=%v", err)
	}
}

func TestIsThereVacantSlotInRight_Cases(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NewUUID())
	n.ChildrenIDs = make([]sop.UUID, 2)
	n.ChildrenIDs[1] = sop.NilUUID
	fnr.Add(n)
	if ok, err := n.isThereVacantSlotInRight(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true due to nil child, err=%v", err)
	}

	n2 := newNode[int, string](b.getSlotLength())
	n2.newID(sop.NewUUID())
	n2.ChildrenIDs = []sop.UUID{sop.NewUUID()}
	fnr.Add(n2)
	unb := false
	if ok, err := n2.isThereVacantSlotInRight(nil, b, &unb); err != nil || ok || !unb {
		t.Fatalf("expected unbalanced=false return, got ok=%v unb=%v err=%v", ok, unb, err)
	}

	n3 := newNode[int, string](b.getSlotLength())
	n3.newID(sop.NewUUID())
	fnr.Add(n3)
	if ok, err := n3.isThereVacantSlotInRight(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true for not-full leaf, err=%v", err)
	}

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, 3)
	full := newNode[int, string](b.getSlotLength())
	full.newID(parent.ID)
	full.Count = b.getSlotLength()
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	parent.ChildrenIDs[1] = full.ID
	parent.ChildrenIDs[2] = right.ID
	fnr.Add(parent)
	fnr.Add(full)
	fnr.Add(right)
	if ok, err := full.isThereVacantSlotInRight(nil, b, new(bool)); err != nil || !ok {
		t.Fatalf("expected true by scanning to right sibling, err=%v", err)
	}
}

func TestUnlinkNodeWithNilChild_Promote(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.ChildrenIDs = make([]sop.UUID, 2)
	n := newNode[int, string](b.getSlotLength())
	n.newID(p.ID)
	n.ParentID = p.ID
	n.ChildrenIDs = make([]sop.UUID, 1)
	c := newNode[int, string](b.getSlotLength())
	c.newID(n.ID)
	c.ParentID = n.ID
	n.ChildrenIDs[0] = c.ID
	p.ChildrenIDs[0] = n.ID
	fnr.Add(p)
	fnr.Add(n)
	fnr.Add(c)
	b.StoreInfo.RootNodeID = p.ID
	if ok, err := n.unlinkNodeWithNilChild(nil, b); !ok || err != nil {
		t.Fatalf("unlinkNodeWithNilChild failed: %v", err)
	}
	if p.ChildrenIDs[0] != c.ID {
		t.Fatalf("parent did not adopt child")
	}
	if _, exists := fnr.n[n.ID]; exists {
		t.Fatalf("intermediate node not removed")
	}
	if gotP := c.ParentID; gotP != p.ID {
		t.Fatalf("child parent not rewired")
	}
}
