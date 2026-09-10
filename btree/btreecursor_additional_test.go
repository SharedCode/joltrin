package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin"
)

// Covers Cursor methods that delegate to the underlying Btree but weren't
// exercised anywhere yet: AddIfNotExist, Upsert, Update, UpdateKey, Remove,
// FindWithID, FindInDescendingOrder, UpdateCurrentItem, UpdateCurrentKey,
// GetCurrentItem, and GetCurrentItemNoLock.

func TestCursor_AddIfNotExist(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)

	if ok, err := c.AddIfNotExist(ctx, 1, "first"); !ok || err != nil {
		t.Fatalf("expected first add to succeed, got ok=%v err=%v", ok, err)
	}
	if ok, err := c.AddIfNotExist(ctx, 1, "second"); ok || err != nil {
		t.Fatalf("expected duplicate add to no-op with ok=false, got ok=%v err=%v", ok, err)
	}

	if ok, _ := c.Find(ctx, 1, true); !ok {
		t.Fatal("Find(1) failed")
	}
	if v, err := c.GetCurrentValue(ctx); err != nil || v != "first" {
		t.Errorf("expected the original value to survive the rejected duplicate add, got %q err=%v", v, err)
	}
}

func TestCursor_Upsert(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)

	if ok, err := c.Upsert(ctx, 1, "inserted"); !ok || err != nil {
		t.Fatalf("expected insert-path upsert to succeed, got ok=%v err=%v", ok, err)
	}
	if ok, err := c.Upsert(ctx, 1, "updated"); !ok || err != nil {
		t.Fatalf("expected update-path upsert to succeed, got ok=%v err=%v", ok, err)
	}

	if ok, _ := c.Find(ctx, 1, true); !ok {
		t.Fatal("Find(1) failed")
	}
	if v, err := c.GetCurrentValue(ctx); err != nil || v != "updated" {
		t.Errorf("expected upsert to overwrite the value, got %q err=%v", v, err)
	}
}

func TestCursor_UpdateByKey(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	c.Add(ctx, 1, "before")

	if ok, err := c.Update(ctx, 1, "after"); !ok || err != nil {
		t.Fatalf("Update failed: ok=%v err=%v", ok, err)
	}

	if ok, _ := c.Find(ctx, 1, true); !ok {
		t.Fatal("Find(1) failed")
	}
	if v, err := c.GetCurrentValue(ctx); err != nil || v != "after" {
		t.Errorf("expected updated value, got %q err=%v", v, err)
	}
}

// newTestKeyStructBtree builds a btree keyed on KeyStruct, comparing only
// ID: it lets UpdateKey/UpdateCurrentKey/UpdateCurrentItem change the
// non-comparer Metadata field, which a plain int key (the comparer value
// itself) can never allow - see key_update_test.go.
func newTestKeyStructBtree(t *testing.T) *Btree[KeyStruct, string] {
	t.Helper()
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	si := StoreInterface[KeyStruct, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[KeyStruct, string]{}}
	b, err := New[KeyStruct, string](store, &si, compareKeyStruct)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return b
}

func TestCursor_UpdateKey(t *testing.T) {
	b := newTestKeyStructBtree(t)
	ctx := context.Background()
	c := NewCursor(b)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	c.Add(ctx, key, "v")

	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := c.UpdateKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateKey failed: ok=%v err=%v", ok, err)
	}

	if ok, _ := c.Find(ctx, key, false); !ok {
		t.Fatal("Find by ID failed")
	}
	if got := c.GetCurrentKey().Key; got.Metadata != "updated" {
		t.Errorf("expected metadata 'updated', got %q", got.Metadata)
	}
}

func TestCursor_Remove(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	c.Add(ctx, 1, "v")

	if ok, err := c.Remove(ctx, 1); !ok || err != nil {
		t.Fatalf("Remove failed: ok=%v err=%v", ok, err)
	}
	if ok, _ := c.Find(ctx, 1, true); ok {
		t.Fatal("expected key 1 to be gone after Remove")
	}
}

func TestCursor_FindWithID(t *testing.T) {
	b, _ := newNonUniqueBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	c.Add(ctx, 5, "a")
	c.Add(ctx, 5, "b")

	// Capture the ID of the second duplicate via the underlying Btree's cursor state.
	if ok, err := b.Find(ctx, 5, true); err != nil || !ok {
		t.Fatalf("find: %v ok=%v", err, ok)
	}
	if ok, err := b.Next(ctx); err != nil || !ok {
		t.Fatalf("next: %v ok=%v", err, ok)
	}
	second, _ := b.GetCurrentItem(ctx)

	if ok, err := c.FindWithID(ctx, 5, second.ID); err != nil || !ok {
		t.Fatalf("Cursor.FindWithID should locate the specific duplicate: ok=%v err=%v", ok, err)
	}
	if got := c.GetCurrentKey(); got.ID != second.ID {
		t.Errorf("expected cursor positioned on ID %v, got %v", second.ID, got.ID)
	}

	if ok, err := c.FindWithID(ctx, 5, sop.NewUUID()); err != nil || ok {
		t.Fatalf("expected FindWithID to return (false,nil) for an unknown ID, got ok=%v err=%v", ok, err)
	}
}

func TestCursor_FindInDescendingOrder(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	for i := 1; i <= 5; i++ {
		c.Add(ctx, i, "v")
	}

	if ok, err := c.FindInDescendingOrder(ctx, 3); err != nil || !ok {
		t.Fatalf("FindInDescendingOrder(3) failed: ok=%v err=%v", ok, err)
	}
	if k := c.GetCurrentKey().Key; k != 3 {
		t.Errorf("expected cursor at 3, got %d", k)
	}
	// Previous still walks toward the next smaller distinct key (see
	// TestFindInDescendingOrder_StringKeys: it only reverses duplicate
	// insertion order, not distinct-key traversal direction).
	if ok, err := c.Previous(ctx); err != nil || !ok {
		t.Fatalf("Previous after descending find failed: ok=%v err=%v", ok, err)
	}
	if k := c.GetCurrentKey().Key; k != 2 {
		t.Errorf("expected Previous to move to 2, got %d", k)
	}
}

func TestCursor_UpdateCurrentItem(t *testing.T) {
	b := newTestKeyStructBtree(t)
	ctx := context.Background()
	c := NewCursor(b)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	c.Add(ctx, key, "v")
	c.Find(ctx, key, false)

	newKey := KeyStruct{ID: 1, Metadata: "updated_item"}
	if ok, err := c.UpdateCurrentItem(ctx, newKey, "v2"); !ok || err != nil {
		t.Fatalf("UpdateCurrentItem failed: ok=%v err=%v", ok, err)
	}
	item, err := c.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem failed: %v", err)
	}
	if item.Key.Metadata != "updated_item" || item.Value == nil || *item.Value != "v2" {
		t.Errorf("unexpected item after UpdateCurrentItem: %+v", item)
	}
}

func TestCursor_UpdateCurrentKey(t *testing.T) {
	b := newTestKeyStructBtree(t)
	ctx := context.Background()
	c := NewCursor(b)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	c.Add(ctx, key, "v")
	c.Find(ctx, key, false)

	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := c.UpdateCurrentKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: ok=%v err=%v", ok, err)
	}
	if got := c.GetCurrentKey().Key; got.Metadata != "updated" {
		t.Errorf("expected metadata 'updated', got %q", got.Metadata)
	}
}

func TestCursor_GetCurrentItem(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	c.Add(ctx, 1, "v")
	c.First(ctx)

	item, err := c.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem failed: %v", err)
	}
	if item.Key != 1 || item.Value == nil || *item.Value != "v" {
		t.Errorf("unexpected item: %+v", item)
	}
}

func TestCursor_GetCurrentItemNoLock(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)
	c.Add(ctx, 1, "v")
	c.First(ctx)

	item, err := c.GetCurrentItemNoLock(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItemNoLock failed: %v", err)
	}
	if item.Key != 1 || item.Value == nil || *item.Value != "v" {
		t.Errorf("unexpected item: %+v", item)
	}
}
