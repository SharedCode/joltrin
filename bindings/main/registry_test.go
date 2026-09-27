package main

import (
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestRegistry_Add_Get(t *testing.T) {
	reg := newRegistry[string]()
	item := "test_item"

	id := reg.Add(item)
	if id.IsNil() {
		t.Error("Expected valid UUID, got nil")
	}

	retrieved, ok := reg.Get(id)
	if !ok {
		t.Error("Expected item to be found")
	}
	if retrieved != item {
		t.Errorf("Expected %s, got %s", item, retrieved)
	}
}

func TestRegistry_Set(t *testing.T) {
	reg := newRegistry[int]()
	id := sop.NewUUID()
	item := 42

	reg.Set(id, item)

	retrieved, ok := reg.Get(id)
	if !ok {
		t.Error("Expected item to be found")
	}
	if retrieved != item {
		t.Errorf("Expected %d, got %d", item, retrieved)
	}
}

func TestRegistry_Remove(t *testing.T) {
	reg := newRegistry[string]()
	item := "to_be_removed"
	id := reg.Add(item)

	reg.Remove(id)

	_, ok := reg.Get(id)
	if ok {
		t.Error("Expected item to be removed")
	}
}

func TestRegistry_Concurrency(t *testing.T) {
	reg := newRegistry[int]()
	var wg sync.WaitGroup
	count := 100

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			reg.Add(val)
		}(i)
	}

	wg.Wait()

	// Since we can't easily count without exposing internal map,
	// we just ensure no panic occurred and basic operations still work.
	id := reg.Add(999)
	val, ok := reg.Get(id)
	if !ok || val != 999 {
		t.Error("Registry failed after concurrent access")
	}
}
