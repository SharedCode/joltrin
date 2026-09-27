// Package cache contains in-process MRU/L1 cache implementations and utilities used by SOP.
// It offers a generic Cache interface and concrete MRU- and L1-based caches.
package cache

import "github.com/sharedcode/joltrin/v5"

// Cache is a generic MRU cache interface used for in-memory caching scenarios.
// Implementations should maintain recency and support bulk operations.
type Cache[TK comparable, TV any] interface {
	// Clear removes all entries from the cache.
	Clear()
	// Set inserts or updates the given key/value pairs.
	Set(items []sop.KeyValuePair[TK, TV])
	// Get looks up the values for the given keys; missing keys yield zero values.
	Get(keys []TK) []TV
	// Delete removes the given keys from the cache, if present.
	Delete(keys []TK)
	// Count returns the number of items currently stored in the cache.
	Count() int
	// IsFull reports whether the cache has reached its maximum capacity.
	IsFull() bool
	// Evict removes least-recently-used entries until capacity constraints are satisfied.
	Evict()
}

type cacheEntry[TK, TV any] struct {
	data    TV
	dllNode *node[TK]
}

type cache[TK comparable, TV any] struct {
	lookup map[TK]*cacheEntry[TK, TV]
	mru    *mru[TK, TV]
}

// NewCache creates a new generic cache with MRU-based eviction.
func NewCache[TK comparable, TV any](minCapacity, maxCapacity int) Cache[TK, TV] {
	c := cache[TK, TV]{
		lookup: make(map[TK]*cacheEntry[TK, TV], maxCapacity),
	}
	c.mru = newMru(&c, minCapacity, maxCapacity)
	return &c
}

func (c *cache[TK, TV]) Clear() {
	c.lookup = make(map[TK]*cacheEntry[TK, TV], c.mru.maxCapacity)
	c.mru = newMru(c, c.mru.minCapacity, c.mru.maxCapacity)
}

func (c *cache[TK, TV]) Set(items []sop.KeyValuePair[TK, TV]) {
	for i := range items {
		if v, ok := c.lookup[items[i].Key]; ok {
			v.data = items[i].Value
			c.mru.remove(v.dllNode)
			v.dllNode = c.mru.add(items[i].Key)
			continue
		}
		n := c.mru.add(items[i].Key)
		c.lookup[items[i].Key] = &cacheEntry[TK, TV]{
			data:    items[i].Value,
			dllNode: n,
		}
	}
	c.Evict()
}

func (c *cache[TK, TV]) Get(keys []TK) []TV {
	r := make([]TV, len(keys))
	for i := range keys {
		if v, ok := c.lookup[keys[i]]; ok {
			c.mru.remove(v.dllNode)
			v.dllNode = c.mru.add(keys[i])
			r[i] = v.data
		}
	}
	return r
}

func (c *cache[TK, TV]) Delete(keys []TK) {
	for i := range keys {
		if v, ok := c.lookup[keys[i]]; ok {
			c.mru.remove(v.dllNode)
			v.dllNode = nil
			delete(c.lookup, keys[i])
		}
	}
}

// Count returns the number of items currently stored in this cache.
func (c *cache[TK, TV]) Count() int {
	return len(c.lookup)
}

func (c *cache[TK, TV]) IsFull() bool {
	return c.mru.isFull()
}

// Evict removes least-recently-used entries until the cache size is within capacity.
func (c *cache[TK, TV]) Evict() {
	c.mru.evict()
}
