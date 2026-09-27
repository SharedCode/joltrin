package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

type panickingMRUCache[TK comparable, TV any] struct {
	Cache[TK, TV]
}

func (p *panickingMRUCache[TK, TV]) Set(items []sop.KeyValuePair[TK, TV]) {
	panic("simulated panic in Cache.Set")
}

func (p *panickingMRUCache[TK, TV]) Delete(keys []TK) {
	panic("simulated panic in Cache.Delete")
}

func (p *panickingMRUCache[TK, TV]) Clear() {
	panic("simulated panic in Cache.Clear")
}

// TestSynchronizedCache_SetPanicDeadlock confirms that a panic in Set leaves
// sc.locker permanently held if Unlock is not deferred.
func TestSynchronizedCache_SetPanicDeadlock(t *testing.T) {
	sc := &sync_cache[string, string]{
		locker: &sync.Mutex{},
		Cache:  &panickingMRUCache[string, string]{},
	}

	func() {
		defer func() {
			_ = recover()
		}()
		sc.Set([]sop.KeyValuePair[string, string]{{Key: "k", Value: "v"}})
	}()

	done := make(chan bool)
	go func() {
		// Attempting to acquire the lock must succeed if panic unlocked it.
		sc.locker.Lock()
		sc.locker.Unlock()
		done <- true
	}()

	select {
	case <-done:
		// Success, lock was released despite the panic.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("deadlock: sc.locker remained locked after panic in Set")
	}
}

// TestSynchronizedCache_DeletePanicDeadlock confirms Delete panic unlocks.
func TestSynchronizedCache_DeletePanicDeadlock(t *testing.T) {
	sc := &sync_cache[string, string]{
		locker: &sync.Mutex{},
		Cache:  &panickingMRUCache[string, string]{},
	}

	func() {
		defer func() {
			_ = recover()
		}()
		sc.Delete([]string{"k"})
	}()

	done := make(chan bool)
	go func() {
		sc.locker.Lock()
		sc.locker.Unlock()
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("deadlock: sc.locker remained locked after panic in Delete")
	}
}

// TestSynchronizedCache_ClearPanicDeadlock confirms Clear panic unlocks.
func TestSynchronizedCache_ClearPanicDeadlock(t *testing.T) {
	sc := &sync_cache[string, string]{
		locker: &sync.Mutex{},
		Cache:  &panickingMRUCache[string, string]{},
	}

	func() {
		defer func() {
			_ = recover()
		}()
		sc.Clear()
	}()

	done := make(chan bool)
	go func() {
		sc.locker.Lock()
		sc.locker.Unlock()
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("deadlock: sc.locker remained locked after panic in Clear")
	}
}
