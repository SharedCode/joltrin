package common

import (
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin"
)

// Tests were copied from inmemory package, refactored to work for in_red_ck.

func Test_HelloWorld(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	so := sop.ConfigureStore("inmymemory", false, 8, "", sop.SmallData, "")
	so.LeafLoadBalancing = true
	b3, _ := NewBtree[int, string](ctx, so, t1, nil)
	b3.Add(ctx, 5000, "I am the value with 5000 key.")

	b3.Add(ctx, 5001, "I am the value with 5001 key.")
	b3.Add(ctx, 5000, "I am also a value with 5000 key.")

	if ok, _ := b3.Find(ctx, 5000, true); !ok || b3.GetCurrentKey().Key != 5000 {
		t.Errorf("FindOne(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
	}
	if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5000 {
		t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
	}
	if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5001 {
		t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey().Key)
	}

	t1.Commit(ctx)
}

func Test_FunctionalityTests(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "inmymemory1",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, nil)

	const five001Value = "I am the value with 5001 key."

	// Check get on empty tree, returns false always as is empty.
	if ok, _ := b3.Find(ctx, 1, false); ok {
		t.Errorf("FindOne(1) failed, got true, want false.")
	}

	// Populate with some values.
	b3.Add(ctx, 5000, "I am the value with 5000 key.")
	b3.Add(ctx, 5001, five001Value)

	// Test AddIfNotExist method #1.
	if ok, _ := b3.AddIfNotExist(ctx, 5000, "foobar"); ok {
		t.Errorf("AddIfNotExist(5000, 'foobar') got success, want fail.")
	}

	b3.Add(ctx, 5000, "I am also a value with 5000 key.")

	// Test AddIfNotExist method #2.
	if ok, _ := b3.AddIfNotExist(ctx, 5000, "foobar"); ok {
		t.Errorf("AddIfNotExist(5000, 'foobar') got success, want fail.")
	}
	// Add more checks here as needed..

	// Check if B-Tree items are intact.
	if ok, _ := b3.Find(ctx, 5000, true); !ok || b3.GetCurrentKey().Key != 5000 {
		t.Errorf("FindOne(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
	}
	if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5000 {
		t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
	}
	if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5001 {
		t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey().Key)
	}

	// Test Next on EOF.
	if ok, _ := b3.Next(ctx); ok {
		t.Errorf("Next() on EOF failed, got = true, want = false")
	}

	// Test UpdateCurrentValue.
	b3.Find(ctx, 5000, true)
	newVal := "Updated with new Value."
	if ok, _ := b3.UpdateCurrentValue(ctx, newVal); !ok {
		t.Errorf("UpdateCurrentValue() failed, got = false, want = true")
	}
	if v, _ := b3.GetCurrentValue(ctx); v != newVal {
		t.Errorf("UpdateCurrentValue() failed, got = %s, want = %s", v, newVal)
	}

	if ok, _ := b3.Find(ctx, 5000, true); !ok {
		t.Errorf("UpdateCurrentValue(<k>) succeeded but FindOne(<k>, true) failed, got = false, want = true")
	}
	if v, _ := b3.GetCurrentValue(ctx); v != newVal {
		t.Errorf("UpdateCurrentValue(<k>) succeeded but FindOne(<k>, true) failed, got = %s, want = %s", v, newVal)
	}

	// Test RemoveCurrentItem
	b3.Find(ctx, 5000, true)
	if ok, _ := b3.RemoveCurrentItem(ctx); !ok {
		t.Errorf("RemoveCurrentItem() failed.")
	}
	b3.Find(ctx, 5000, true)
	if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5001 {
		t.Errorf("Next() after RemoveCurrentItem failed, expected item(5001) not found.")
	}
	if v, _ := b3.GetCurrentValue(ctx); v != five001Value {
		t.Errorf("Next() after RemoveCurrentItem failed, got = %s, want = %s.", v, five001Value)
	}

	// Test Next on EOF.
	if ok, _ := b3.Next(ctx); ok {
		t.Errorf("Next() on EOF failed, got = true, want = false")
	}

	t1.Commit(ctx)
}

func Test_ComplexDataMgmtCases(t *testing.T) {
	max := 100000
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)
	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "inmymemory2",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, nil)

	// Simple IsUnique check.
	if !b3.IsUnique() {
		t.Errorf("b3.IsUnique() got false, want true.")
	}

	// The Complex Data Mgmt Test cases.
	tests := []struct {
		name       string
		startRange int
		endRange   int
		action     int
		wantFound  int
	}{
		{
			name:       "Populate",
			startRange: 0,
			endRange:   max,
			action:     1, // add
		},
		{
			name:       "Find 1",
			startRange: 0,
			endRange:   max,
			action:     2, // find
		},
		{
			name:       "Remove 1",
			startRange: 450,
			endRange:   542,
			action:     3, // remove
		},
		{
			name:       "Remove 2",
			startRange: 543,
			endRange:   600,
			action:     3, // remove
		},
		{
			name:       "Find with missing items 1",
			startRange: 445,
			endRange:   607,
			action:     4, // FindOne + track not found items
			wantFound:  12,
		},
		{
			name:       "Re add deleted items",
			startRange: 450,
			endRange:   600,
			action:     1,
		},
		{
			name:       "Find All 1",
			startRange: 0,
			endRange:   max,
			action:     2,
		},
		{
			name:       "Remove 3",
			startRange: 60000,
			endRange:   90000,
			action:     3,
		},
		{
			name:       "Remove 4",
			startRange: 91000,
			endRange:   99000,
			action:     3,
		},
		{
			name:       "Find All 1",
			startRange: 0,
			endRange:   max,
			action:     4,
			wantFound:  61999,
		},
		{
			name:       "Re add 2",
			startRange: 60000,
			endRange:   90000,
			action:     1,
		},
		{
			name:       "Re add 3",
			startRange: 91000,
			endRange:   99000,
			action:     1,
		},
		{
			name:       "Remove all",
			startRange: 0,
			endRange:   max,
			action:     3,
		},
		{
			name:       "Re add all",
			startRange: 0,
			endRange:   max,
			action:     1,
		},
		{
			name:       "Range Query all",
			startRange: 0,
			endRange:   max,
			action:     5, // FindOne + MoveNext()
			wantFound:  max + 1,
		},
	}

	var k int
	for _, test := range tests {
		t.Logf("Test %s started.", test.name)
		if test.action == 4 {
			itemsFoundCount := 0
			for i := test.startRange; i <= test.endRange; i++ {
				k = i
				if ok, _ := b3.Find(ctx, k, true); ok {
					itemsFoundCount++
				}
			}
			if itemsFoundCount != test.wantFound {
				t.Errorf("got %d items, want %d", itemsFoundCount, test.wantFound)
				t.Logf("Test %s ended.", test.name)
				t.FailNow()
			}
			continue
		}
		if test.action == 5 {
			itemsFoundCount := 0
			k = test.startRange
			if ok, _ := b3.Find(ctx, k, true); ok {
				itemsFoundCount++
			}
			for i := test.startRange + 1; i <= test.endRange; i++ {
				k = i
				if ok, _ := b3.Next(ctx); ok {
					if b3.GetCurrentKey().Key == k {
						itemsFoundCount++
						continue
					}
					t.Errorf("got %d key, want %d key", b3.GetCurrentKey().Key, k)
				}
			}
			if itemsFoundCount != test.wantFound {
				t.Errorf("got %d items, want %d", itemsFoundCount, test.wantFound)
				t.Logf("Test %s ended.", test.name)
				t.FailNow()
			}
			continue
		}
		for i := test.startRange; i <= test.endRange; i++ {
			k = i
			v := fmt.Sprintf("bar%d", i)

			switch test.action {
			case 1:
				if ok, _ := b3.Add(ctx, k, v); !ok {
					t.Errorf("Failed Add item with key %d.\n", k)
				}
			case 2:
				if ok, _ := b3.Find(ctx, k, true); !ok {
					t.Errorf("Failed FindOne item with key %d.\n", k)
				}
			case 3:
				if ok, _ := b3.Remove(ctx, k); !ok {
					t.Errorf("Failed Remove item with key %d.\n", k)
				}
			}
		}
		t.Logf("Test %s ended.", test.name)
	}

	t1.Commit(ctx)

	t1, _ = newMockTransaction(t, sop.ForReading, -1)
	t1.Begin(ctx)
	b3, _ = OpenBtree[int, string](ctx, "inmymemory2", t1, nil)

	// Find those items populated in previous transaction.
	for _, test := range tests {
		for i := test.startRange; i <= test.endRange; i++ {
			k = i

			switch test.action {
			case 2:
				if ok, _ := b3.Find(ctx, k, true); !ok {
					t.Errorf("Failed FindOne item with key %d.\n", k)
				}
			}
		}
	}

	if err := t1.Commit(ctx); err != nil {
		t.Error(err)
	}
}

func Test_SimpleDataMgmtCases(t *testing.T) {
	max := 100000
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	so := sop.ConfigureStore("inmymemory3", false, 8, "", sop.SmallData, "")
	so.LeafLoadBalancing = true
	b3, _ := NewBtree[string, string](ctx, so, t1, nil)

	tests := []struct {
		name       string
		startRange int
		endRange   int
		action     int
	}{
		{
			name:       "Populate",
			startRange: 0,
			endRange:   max,
			action:     1, // add
		},
		{
			name:       "Find 1",
			startRange: 0,
			endRange:   max,
			action:     2, // find
		},
		{
			name:       "Remove 1",
			startRange: 450,
			endRange:   800,
			action:     3, // remove
		},
		{
			name:       "Re add deleted items",
			startRange: 450,
			endRange:   800,
			action:     1,
		},
		{
			name:       "Find All 1",
			startRange: 0,
			endRange:   max,
			action:     2,
		},
		{
			name:       "Remove 2",
			startRange: 5000,
			endRange:   10000,
			action:     3,
		},
		{
			name:       "Re add deleted items 2",
			startRange: 5000,
			endRange:   10000,
			action:     1,
		},
		{
			name:       "Find All 2",
			startRange: 0,
			endRange:   max,
			action:     2, // find
		},
	}

	for _, test := range tests {
		t.Logf("Test %s started.", test.name)
		for i := test.startRange; i < test.endRange; i++ {
			k := fmt.Sprintf("foo%d", i)
			v := fmt.Sprintf("bar%d", i)

			switch test.action {
			case 1:
				if ok, _ := b3.Add(ctx, k, v); !ok {
					t.Errorf("Failed Add item with key %s.\n", k)
				}
			case 2:
				if ok, _ := b3.Find(ctx, k, true); !ok {
					t.Errorf("Failed FindOne item with key %s.\n", k)
				}
			case 3:
				if ok, _ := b3.Remove(ctx, k); !ok {
					t.Errorf("Failed Delete item with key %s.\n", k)
				}
			}
		}
		t.Logf("Test %s ended.", test.name)
	}
	t1.Commit(ctx)

	t1, _ = newMockTransaction(t, sop.ForReading, -1)
	t1.Begin(ctx)
	b3, _ = OpenBtree[string, string](ctx, "inmymemory3", t1, nil)

	for _, test := range tests {
		t.Logf("Test %s started.", test.name)
		for i := test.startRange; i < test.endRange; i++ {
			k := fmt.Sprintf("foo%d", i)

			switch test.action {
			case 2:
				if ok, _ := b3.Find(ctx, k, true); !ok {
					t.Errorf("Failed FindOne item with key %s.\n", k)
				}
			}
		}
		t.Logf("Test %s ended.", test.name)
	}

	if err := t1.Commit(ctx); err != nil {
		t.Error(err)
	}
}
