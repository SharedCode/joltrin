package cassandra

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestSortStores_WithDuplicates(t *testing.T) {
	// Scenario: Two stores with the same name.
	stores := []sop.StoreInfo{
		{Name: "foo", Count: 10},
		{Name: "bar", Count: 20},
		{Name: "foo", Count: 30}, // Duplicate name
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("sortStores panicked: %v", r)
		}
	}()

	sorted := sortStores(stores)

	// Check for zero values (which would happen if we allocated len(stores) but only filled unique count)
	for i, s := range sorted {
		if s.Name == "" {
			t.Errorf("Store at index %d has empty name. Sort logic likely failed to fill the slice.", i)
		}
	}
}
