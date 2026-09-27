package main

import (
	"sync"

	"github.com/sharedcode/joltrin/v5"
)

// registry is a thread-safe generic registry for storing objects by UUID.
type registry[T any] struct {
	lookup map[sop.UUID]T
	mu     sync.Mutex
}

// newRegistry creates a new instance of registry.
func newRegistry[T any]() *registry[T] {
	return &registry[T]{
		lookup: make(map[sop.UUID]T),
	}
}

// Add registers an item and returns its new UUID.
func (r *registry[T]) Add(item T) sop.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := sop.NewUUID()
	r.lookup[id] = item
	return id
}

// Set registers an item with a specific UUID.
func (r *registry[T]) Set(id sop.UUID, item T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookup[id] = item
}

// Get retrieves an item by UUID.
func (r *registry[T]) Get(id sop.UUID) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.lookup[id]
	return item, ok
}

// Remove removes an item by UUID.
func (r *registry[T]) Remove(id sop.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lookup, id)
}
