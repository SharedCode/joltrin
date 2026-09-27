package btree

// Consolidated from: distribute_error_test.go, distribute_left_nilchild_test.go, distribution_test.go
import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// NOTE: Original distribute_* test files have been removed. This file intentionally contains
// all combined tests. Ensure no duplicate symbol definitions remain elsewhere.

// fakeNRSelectiveErr wraps an in-memory node map and returns an error for IDs in errs.
type fakeNRSelectiveErr[TK Ordered, TV any] struct {
	n    map[sop.UUID]*Node[TK, TV]
	errs map[sop.UUID]bool
}

func (f *fakeNRSelectiveErr[TK, TV]) Add(node *Node[TK, TV])    { f.n[node.ID] = node }
func (f *fakeNRSelectiveErr[TK, TV]) Update(node *Node[TK, TV]) { f.n[node.ID] = node }
func (f *fakeNRSelectiveErr[TK, TV]) Remove(id sop.UUID)        { delete(f.n, id) }
func (f *fakeNRSelectiveErr[TK, TV]) Fetched(id sop.UUID)       {}
func (f *fakeNRSelectiveErr[TK, TV]) Get(_ context.Context, id sop.UUID) (*Node[TK, TV], error) {
	if f.errs[id] {
		return nil, fmt.Errorf("forced get error for %v", id)
	}
	return f.n[id], nil
}

func TestDistributeToRight_ReturnsParentError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parentID := sop.NewUUID()
	fnr.errs[parentID] = true

	node := newNode[int, string](b.getSlotLength())
	node.ID = sop.NewUUID()
	node.ParentID = parentID
	v := "v"
	vv := v
	for i := 0; i < b.getSlotLength(); i++ {
		node.Slots[i] = Item[int, string]{Key: 100 + i, Value: &vv, ID: sop.NewUUID()}
	}
	node.Count = b.getSlotLength()

	err := node.distributeToRight(nil, b, &Item[int, string]{Key: 999, Value: &vv, ID: sop.NewUUID()})
	if err == nil {
		t.Fatalf("expected error from getParent to be returned")
	}
}

func TestDistributeToRight_ReturnsRightSiblingError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1

	node := newNode[int, string](b.getSlotLength())
	node.newID(parent.ID)
	v := "v"
	vv := v
	for i := 0; i < b.getSlotLength(); i++ {
		node.Slots[i] = Item[int, string]{Key: 100 + i, Value: &vv, ID: sop.NewUUID()}
	}
	node.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)

	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = node.ID
	parent.ChildrenIDs[1] = right.ID

	fnr.Add(parent)
	fnr.Add(node)
	fnr.errs[right.ID] = true

	err := node.distributeToRight(nil, b, &Item[int, string]{Key: 999, Value: &vv, ID: sop.NewUUID()})
	if err == nil {
		t.Fatalf("expected error from getRightSibling to be returned")
	}
}

func TestDistribute_Controller_ReturnsError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	v := "v"
	vv := v
	for i := 0; i < b.getSlotLength(); i++ {
		left.Slots[i] = Item[int, string]{Key: 10 + i, Value: &vv, ID: sop.NewUUID()}
	}
	left.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)

	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(right)

	b.distributeAction.sourceNode = left
	b.distributeAction.item = Item[int, string]{Key: 999, Value: &vv, ID: sop.NewUUID()}
	b.distributeAction.distributeToLeft = false

	fnr.errs[right.ID] = true

	if err := b.distribute(nil); err == nil {
		t.Fatalf("expected controller distribute to return error from distributeToRight")
	}
}

func TestDistributeToLeft_NilChildShortCircuit(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	for i := 0; i < b.getSlotLength(); i++ {
		v := "a"
		vv := v
		n.Slots[i] = Item[int, string]{Key: i + 1, Value: &vv, ID: sop.NewUUID()}
	}
	n.Count = b.getSlotLength()
	n.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	b.StoreInfo.RootNodeID = n.ID
	fnr.Add(n)

	item := &Item[int, string]{Key: 0, Value: &[]string{"z"}[0], ID: sop.NewUUID()}
	if err := n.distributeToLeft(nil, b, item); err != nil {
		t.Fatalf("distributeToLeft err: %v", err)
	}

	attached := false
	for _, cid := range n.ChildrenIDs {
		if !cid.IsNil() {
			attached = true
			break
		}
	}
	if !attached {
		t.Fatalf("expected a new child to be attached via nil-child path")
	}
}

func TestDistributeToLeft_RotationAndAppend(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	sepVal := "m"
	parent.Slots[0] = Item[int, string]{Key: 100, Value: &sepVal, ID: sop.NewUUID()}
	parent.Count = 1

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	for i, k := range []int{10, 20, 30} {
		v := "l"
		vv := v
		left.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	left.Count = 3

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	for i, k := range []int{110, 120, 130, 140} {
		v := "r"
		vv := v
		right.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	right.Count = 4

	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(right)

	extraV := "x"
	extra := &Item[int, string]{Key: 105, Value: &extraV, ID: sop.NewUUID()}
	if err := right.distributeToLeft(nil, b, extra); err != nil {
		t.Fatalf("distributeToLeft err: %v", err)
	}

	if err := b.distribute(nil); err != nil {
		t.Fatalf("controller distribute err: %v", err)
	}

	if left.Count != 4 {
		t.Fatalf("left count want 4 got %d", left.Count)
	}
	if parent.Slots[0].ID.IsNil() {
		t.Fatalf("parent separator not set")
	}
}

func TestDistribute_Rotations_DoNotPanic(t *testing.T) {
	b, _, _ := buildSmallTree()
	for _, k := range []int{15, 16, 17, 18, 19, 21, 22, 23, 24} {
		v := "y"
		vv := v
		if ok, err := b.Add(nil, k, vv); err != nil || !ok {
			t.Fatalf("add %d err=%v", k, err)
		}
	}
	if ok, _ := b.First(nil); !ok {
		t.Fatalf("First failed")
	}
	prev := b.GetCurrentKey().Key
	count := 1
	for {
		ok, _ := b.Next(nil)
		if !ok {
			break
		}
		k := b.GetCurrentKey().Key
		if k < prev {
			t.Fatalf("order violated: %d < %d", k, prev)
		}
		prev = k
		count++
		if count > 2000 {
			t.Fatalf("loop guard")
		}
	}
}
