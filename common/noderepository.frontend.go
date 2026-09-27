package common

import (
	"context"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// Frontend facing Node Repository. Implements the NodeRepository interface CRUD methods.
// The frontend adapts generic btree.Node types to backend interfaces and stages fetched
// nodes into the transaction-local cache when Fetched is called.

type nodeRepositoryFrontEnd[TK btree.Ordered, TV any] struct {
	*nodeRepositoryBackend
}

// Add will upsert node to the map.
func (nr *nodeRepositoryFrontEnd[TK, TV]) Add(n *btree.Node[TK, TV]) {
	nr.add(n.ID, n)
}

// Update will upsert node to the map.
func (nr *nodeRepositoryFrontEnd[TK, TV]) Update(n *btree.Node[TK, TV]) {
	nr.update(n.ID, n)
}

// DefaultInitialSlotsCapacity is the optimal initial capacity for node Slots.
// This balances memory efficiency (95%+ savings vs pre-allocating SlotLength) with
// minimal resize overhead (0-2 resizes for typical node sizes via Go's doubling strategy).
const DefaultInitialSlotsCapacity = 256

// Get will retrieve a node with nodeID from the map.
func (nr *nodeRepositoryFrontEnd[TK, TV]) Get(ctx context.Context, nodeID sop.UUID) (*btree.Node[TK, TV], error) {
	target := btree.Node[TK, TV]{
		Slots: make([]btree.Item[TK, TV], 0, DefaultInitialSlotsCapacity),
	}
	n, err := nr.get(ctx, nodeID, &target)
	if n == nil {
		return nil, err
	}
	return n.(*btree.Node[TK, TV]), err
}

// Fetched marks a node as fetched so it becomes tracked by the transaction (moved from MRU to local cache).
func (nr *nodeRepositoryFrontEnd[TK, TV]) Fetched(nodeID sop.UUID) {
	n := nr.readNodesCache.Get([]sop.UUID{nodeID})
	if n[0] != nil {
		nr.localCache[nodeID] = cachedNode{
			action: getAction,
			node:   n[0],
		}
		// Remove now from MRU since node got migrated to local cache and is now "tracked".
		nr.readNodesCache.Delete([]sop.UUID{nodeID})
	}
}

// Remove will remove a node with nodeID from the map.
func (nr *nodeRepositoryFrontEnd[TK, TV]) Remove(nodeID sop.UUID) {
	nr.remove(nodeID)
}
