package inmemory

import (
	"fmt"
	"testing"
)

func TestHelloWorld(t *testing.T) {
	fmt.Printf("Btree hello world.\n")
	b3 := NewBtree[int, string](false)

	b3.Add(5000, "I am the value with 5000 key.")
	b3.Add(5001, "I am the value with 5001 key.")
	b3.Add(5000, "I am also a value with 5000 key.")

	if b3.Count() != 3 {
		t.Errorf("Count() failed, got = %d, want = 3.", b3.Count())
	}

	if !b3.Find(5000, true) || b3.GetCurrentKey() != 5000 {
		t.Errorf("Find(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())

	if !b3.Next() || b3.GetCurrentKey() != 5000 {
		t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())

	if !b3.Next() || b3.GetCurrentKey() != 5001 {
		t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())
	fmt.Printf("Btree hello world ended.\n\n")
}

func TestFunctionalityTests(t *testing.T) {
	fmt.Printf("Btree functionality tests.\n")
	b3 := NewBtree[int, string](false)

	const five001Value = "I am the value with 5001 key."

	// Check get on empty tree, returns false always as is empty.
	if b3.Find(1, false) {
		t.Errorf("Find(1) failed, got true, want false.")
	}

	// Test Upsert.
	b3.Upsert(1234, "I am the value with 1234 key.")
	b3.Upsert(567, "I am the value with 567 key.")
	if !b3.Find(1234, false) {
		t.Errorf("Upsert(1234) failed, Find(1234) got false, want true.")
	}
	if !b3.Find(567, false) {
		t.Errorf("Upsert(567) failed, Find(567) got false, want true.")
	}
	b3.Upsert(1234, "I am coolio.")
	b3.Upsert(567, "I am hottie.")
	if !b3.Find(1234, false) {
		t.Errorf("Upsert(1234) #2 failed, Find(1234) got false, want true.")
	}
	if b3.GetCurrentValue() != "I am coolio." {
		t.Errorf("Upsert(1234) #2 failed, GetCurrentValue got %s, want 'I am coolio.'", b3.GetCurrentValue())
	}
	if !b3.Find(567, false) {
		t.Errorf("Upsert(567) failed, Find(567) got false, want true.")
	}
	if b3.GetCurrentValue() != "I am hottie." {
		t.Errorf("Upsert(567) #2 failed, GetCurrentValue got %s, want 'I am hottie.'", b3.GetCurrentValue())
	}
	// Cleanup after the Upsert tests.
	b3.Remove(1234)
	b3.Remove(567)

	// Populate with some values.
	b3.Add(5000, "I am the value with 5000 key.")
	b3.Add(5001, five001Value)

	// Test AddIfNotExist method #1.
	if b3.AddIfNotExist(5000, "foobar") {
		t.Errorf("AddIfNotExist(5000, 'foobar') got success, want fail.")
	}

	b3.Add(5000, "I am also a value with 5000 key.")

	// Test AddIfNotExist method #2.
	if b3.AddIfNotExist(5000, "foobar") {
		t.Errorf("AddIfNotExist(5000, 'foobar') got success, want fail.")
	}
	// Add more checks here as needed..

	// Check if B-Tree items are intact.
	if !b3.Find(5000, true) || b3.GetCurrentKey() != 5000 {
		t.Errorf("Find(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	if !b3.Next() || b3.GetCurrentKey() != 5000 {
		t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	if !b3.Next() || b3.GetCurrentKey() != 5001 {
		t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey())
	}

	// Test Next on EOF.
	if b3.Next() {
		t.Errorf("Next() on EOF failed, got = true, want = false")
	}

	// Test UpdateCurrentValue.
	b3.Find(5000, true)
	newVal := "Updated with new Value."
	if !b3.UpdateCurrentValue(newVal) || b3.GetCurrentValue() != newVal {
		t.Errorf("UpdateCurrentValue() failed, got = %s, want = %s", b3.GetCurrentValue(), newVal)
	}

	if !b3.Find(5000, true) || b3.GetCurrentValue() != newVal {
		t.Errorf("UpdateCurrentValue(<k>) succeeded but Find(<k>, true) failed, got = %s, want = %s", b3.GetCurrentValue(), newVal)
	}

	// Test RemoveCurrentItem
	b3.Find(5000, true)
	if !b3.RemoveCurrentItem() {
		t.Errorf("RemoveCurrentItem() failed.")
	}
	b3.Find(5000, true)
	if !b3.Next() || b3.GetCurrentKey() != 5001 {
		t.Errorf("Next() after RemoveCurrentItem failed, expected item(5001) not found.")
	}
	if b3.GetCurrentValue() != five001Value {
		t.Errorf("Next() after RemoveCurrentItem failed, got = %s, want = %s.", b3.GetCurrentValue(), five001Value)
	}

	// Test Next on EOF.
	if b3.Next() {
		t.Errorf("Next() on EOF failed, got = true, want = false")
	}

	fmt.Printf("Btree functionality tests ended.\n\n")
}

func TestEdgeCases(t *testing.T) {
	b3 := NewBtree[int, string](false)

	k := b3.GetCurrentKey()
	if k != 0 {
		t.Errorf("GetCurrentKey on empty Btree failed, got %d, want 0.", k)
	}
	v := b3.GetCurrentValue()
	if v != "" {
		t.Errorf("GetCurrentValue on empty Btree failed, got %s, want ''.", v)
	}
	// Add other edge cases unit test(s) here.
}

func TestComplexDataMgmtCases(t *testing.T) {
	max := 100000
	fmt.Printf("Btree complex data mgmt tests started(%d items).\n", max)
	b3 := NewBtree[int, string](true)

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
				if b3.Find(k, true) {
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
			if b3.Find(k, true) {
				itemsFoundCount++
			}
			for i := test.startRange + 1; i <= test.endRange; i++ {
				k = i
				if b3.Next() {
					if b3.GetCurrentKey() == k {
						itemsFoundCount++
						continue
					}
					t.Errorf("got %d key, want %d key", b3.GetCurrentKey(), k)
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
				if !b3.Add(k, v) {
					t.Errorf("Failed Add item with key %d.\n", k)
				}
			case 2:
				if !b3.Find(k, true) {
					t.Errorf("Failed FindOne item with key %d.\n", k)
				}
			case 3:
				if !b3.Remove(k) {
					t.Errorf("Failed Remove item with key %d.\n", k)
				}
			}
		}
		t.Logf("Test %s ended.", test.name)
	}
	fmt.Printf("Btree complex data mgmt tests ended.\n\n")
}

func TestSimpleDataMgmtCases(t *testing.T) {
	max := 100000
	fmt.Printf("Btree simple data mgmt tests started(%d items).\n", max)
	b3 := NewBtree[string, string](false)

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
				if !b3.Add(k, v) {
					t.Errorf("Failed Add item with key %s.\n", k)
				}
			case 2:
				if !b3.Find(k, true) {
					t.Errorf("Failed FindOne item with key %s.\n", k)
				}
			case 3:
				if !b3.Remove(k) {
					t.Errorf("Failed Delete item with key %s.\n", k)
				}
			}
		}
		t.Logf("Test %s ended.", test.name)
	}
	fmt.Printf("Btree simple data mgmt tests ended.\n\n")
}
