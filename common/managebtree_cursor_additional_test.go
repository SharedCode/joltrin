package common

import (
	"cmp"
	"testing"

	"github.com/sharedcode/joltrin"
)

// Covers CursorOnOpenedBtree and OpenBtreeCursor, previously untested (0%).

func TestCursorOnOpenedBtree_Preconditions(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	if _, err := CursorOnOpenedBtree[int, string](ctx, "any", nil); err == nil {
		t.Fatal("expected error for nil transaction")
	}

	notBegun, _ := newMockTransaction(t, sop.ForWriting, -1)
	if _, err := CursorOnOpenedBtree[int, string](ctx, "any", notBegun); err == nil {
		t.Fatal("expected error for a transaction that has not begun")
	}

	if _, err := CursorOnOpenedBtree[int, string](ctx, "", trans); err == nil {
		t.Fatal("expected error for empty store name")
	}

	if _, err := CursorOnOpenedBtree[int, string](ctx, "never-opened", trans); err == nil {
		t.Fatal("expected error when the store was never opened in this transaction")
	}
}

func TestCursorOnOpenedBtree_Success(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	b3, err := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "cursorOpened1",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	}, trans, cmp.Compare)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if ok, err := b3.Add(ctx, 1, "v"); !ok || err != nil {
		t.Fatalf("seed add failed: ok=%v err=%v", ok, err)
	}

	cur, err := CursorOnOpenedBtree[int, string](ctx, "cursorOpened1", trans)
	if err != nil {
		t.Fatalf("CursorOnOpenedBtree failed: %v", err)
	}
	if ok, err := cur.Find(ctx, 1, true); !ok || err != nil {
		t.Fatalf("cursor Find failed: ok=%v err=%v", ok, err)
	}
	if v, err := cur.GetCurrentValue(ctx); err != nil || v != "v" {
		t.Fatalf("cursor GetCurrentValue got %q err=%v", v, err)
	}
}

func TestOpenBtreeCursor_Preconditions(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	if _, err := OpenBtreeCursor[int, string](ctx, "any", nil, cmp.Compare); err == nil {
		t.Fatal("expected error for nil transaction")
	}

	notBegun, _ := newMockTransaction(t, sop.ForWriting, -1)
	if _, err := OpenBtreeCursor[int, string](ctx, "any", notBegun, cmp.Compare); err == nil {
		t.Fatal("expected error for a transaction that has not begun")
	}

	if _, err := OpenBtreeCursor[int, string](ctx, "", trans, cmp.Compare); err == nil {
		t.Fatal("expected error for empty store name")
	}

	if _, err := OpenBtreeCursor[int, string](ctx, "does-not-exist-anywhere", trans, cmp.Compare); err == nil {
		t.Fatal("expected error when the store doesn't exist in this transaction or the repository")
	}
}

func TestOpenBtreeCursor_AlreadyOpenInTransaction(t *testing.T) {
	trans, _ := newMockTransaction(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	b3, err := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "cursorOpened2",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	}, trans, cmp.Compare)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if ok, err := b3.Add(ctx, 2, "v2"); !ok || err != nil {
		t.Fatalf("seed add failed: ok=%v err=%v", ok, err)
	}

	cur, err := OpenBtreeCursor[int, string](ctx, "cursorOpened2", trans, cmp.Compare)
	if err != nil {
		t.Fatalf("OpenBtreeCursor (already-open path) failed: %v", err)
	}
	if ok, err := cur.Find(ctx, 2, true); !ok || err != nil {
		t.Fatalf("cursor Find failed: ok=%v err=%v", ok, err)
	}
}

func TestOpenBtreeCursor_FetchesFromStoreRepository(t *testing.T) {
	// Create the store in one transaction (NewBtree registers it into
	// StoreRepository synchronously, not just on commit).
	transA, _ := newMockTransaction(t, sop.ForWriting, -1)
	transA.Begin(ctx)
	b3, err := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "cursorOpened3",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	}, transA, cmp.Compare)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if ok, err := b3.Add(ctx, 3, "v3"); !ok || err != nil {
		t.Fatalf("seed add failed: ok=%v err=%v", ok, err)
	}

	// A fresh transaction has not opened "cursorOpened3" yet, so
	// OpenBtreeCursor must take the StoreRepository-fetch branch. transA
	// never committed, so transB sees the store definition but none of
	// transA's uncommitted node data - verify the returned cursor is a
	// working handle on this fresh transaction's own view of the store,
	// not that it somehow sees transA's in-flight writes.
	transB, _ := newMockTransaction(t, sop.ForWriting, -1)
	transB.Begin(ctx)
	cur, err := OpenBtreeCursor[int, string](ctx, "cursorOpened3", transB, cmp.Compare)
	if err != nil {
		t.Fatalf("OpenBtreeCursor (fetch-from-repository path) failed: %v", err)
	}
	if ok, err := cur.Add(ctx, 30, "v30"); !ok || err != nil {
		t.Fatalf("cursor Add on fetched store failed: ok=%v err=%v", ok, err)
	}
	if ok, err := cur.Find(ctx, 30, true); !ok || err != nil {
		t.Fatalf("cursor Find failed: ok=%v err=%v", ok, err)
	}
	if v, err := cur.GetCurrentValue(ctx); err != nil || v != "v30" {
		t.Fatalf("cursor GetCurrentValue got %q err=%v", v, err)
	}
}
