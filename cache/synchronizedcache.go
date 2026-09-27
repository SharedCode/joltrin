package cache

import (
	"sync"

	"github.com/sharedcode/joltrin/v5"
)

// sync_cache wraps a Cache with a mutex to provide thread-safe operations.
type sync_cache[TK comparable, TV any] struct {
	// Inherit from Cache.
	Cache[TK, TV]
	locker *sync.Mutex
}

// NewSynchronizedCache returns a thread-safe Cache instance backed by an MRU cache.
func NewSynchronizedCache[TK comparable, TV any](minCapacity, maxCapacity int) Cache[TK, TV] {
	return &sync_cache[TK, TV]{
		locker: &sync.Mutex{},
		Cache:  NewCache[TK, TV](minCapacity, maxCapacity),
	}
}

func (sc *sync_cache[TK, TV]) Set(items []sop.KeyValuePair[TK, TV]) {
	sc.locker.Lock()
	defer sc.locker.Unlock()
	sc.Cache.Set(items)
}
func (sc *sync_cache[TK, TV]) Get(keys []TK) []TV {
	sc.locker.Lock()
	defer sc.locker.Unlock()
	return sc.Cache.Get(keys)
}

func (sc *sync_cache[TK, TV]) Delete(keys []TK) {
	sc.locker.Lock()
	defer sc.locker.Unlock()
	sc.Cache.Delete(keys)
}

func (sc *sync_cache[TK, TV]) Clear() {
	sc.locker.Lock()
	defer sc.locker.Unlock()
	sc.Cache.Clear()
}
