package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestFindInDescendingOrder_StringKeys(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNR[string, string]{n: map[sop.UUID]*Node[string, string]{}}
	si := StoreInterface[string, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[string, string]{}}
	b, _ := New[string, string](store, &si, nil)

	ctx := context.Background()

	// Add items: "A", "A", "A", "B", "C"
	// We want to distinguish them to verify order. Values: "v1", "v2", "v3", "v4", "v5"

	items := []struct {
		k string
		v string
	}{
		{"A", "val1"},
		{"A", "val2"},
		{"A", "val3"},
		{"B", "val4"},
		{"C", "val5"},
	}

	for _, it := range items {
		b.Add(ctx, it.k, it.v)
	}

	// FindInDescendingOrder("A") should land on the LAST "A" (which is "val1" because duplicates are stored in reverse insertion order).
	ok, err := b.FindInDescendingOrder(ctx, "A")
	if err != nil {
		t.Fatalf("FindInDescendingOrder failed: %v", err)
	}
	if !ok {
		t.Fatal("FindInDescendingOrder returned false")
	}

	// Verify we are at "A", "val1"
	k := b.GetCurrentKey()
	v, _ := b.GetCurrentValue(ctx)

	if k.Key != "A" || v != "val1" {
		t.Errorf("Expected A/val1, got %v/%v", k.Key, v)
	}

	// Previous -> "A", "val2"
	ok, _ = b.Previous(ctx)
	if !ok {
		t.Fatal("Previous failed")
	}
	k = b.GetCurrentKey()
	v, _ = b.GetCurrentValue(ctx)
	if k.Key != "A" || v != "val2" {
		t.Errorf("Expected A/val2, got %v/%v", k.Key, v)
	}

	// Previous -> "A", "val3"
	ok, _ = b.Previous(ctx)
	if !ok {
		t.Fatal("Previous failed")
	}
	k = b.GetCurrentKey()
	v, _ = b.GetCurrentValue(ctx)
	if k.Key != "A" || v != "val3" {
		t.Errorf("Expected A/val3, got %v/%v", k.Key, v)
	}

	// Previous -> False (Start of tree)
	ok, _ = b.Previous(ctx)
	if ok {
		t.Fatal("Previous expected false, got true")
	}
}
