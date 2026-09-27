package cache

import "github.com/sharedcode/joltrin/v5"

// l1_mru manages MRU ordering and eviction for the L1Cache.
type l1_mru struct {
	minCapacity int
	maxCapacity int
	dll         *doublyLinkedList[sop.UUID]
	l1Cache     *L1Cache
}

func newL1Mru(l1c *L1Cache, minCapacity, maxCapacity int) *l1_mru {
	return &l1_mru{
		l1Cache:     l1c,
		minCapacity: minCapacity,
		maxCapacity: maxCapacity,
		dll:         newDoublyLinkedList[sop.UUID](),
	}
}

// add inserts the id at the head of the MRU list and returns its node handle.
func (m *l1_mru) add(id sop.UUID) *node[sop.UUID] {
	return m.dll.addToHead(id)
}

// remove unchains the node from the MRU list.
func (m *l1_mru) remove(n *node[sop.UUID]) {
	m.dll.delete(n)
}

// evict removes entries from the tail while the cache exceeds its capacity, updating the L1 index.
func (m *l1_mru) evict() {
	if !m.isFull() {
		return
	}
	itemsToRemove := m.maxCapacity - m.minCapacity
	for range itemsToRemove {
		if id, ok := m.dll.deleteFromTail(); ok {
			if v, found := m.l1Cache.lookup[id]; found {
				v.nodeData = nil
				v.dllNode = nil
				delete(m.l1Cache.lookup, id)
			}
		} else {
			break
		}
	}
}

// isFull reports whether the L1 cache has reached its maximum capacity.
func (m *l1_mru) isFull() bool {
	return m.dll.count() >= m.maxCapacity
}
