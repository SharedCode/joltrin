// Package cache contains the L1 (MRU) cache implementation and L2 integration for B-tree nodes and handles.
package cache

import (
	"context"
	"fmt"
	log "log/slog"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/encoding"
)

type l1CacheEntry struct {
	nodeVersion int32
	nodeData    any
	dllNode     *node[sop.UUID]
}

type cloneableNode interface {
	CloneMetaData() btree.MetaDataType
}

func cloneCacheNodeValue(node any) any {
	if node == nil {
		return nil
	}
	if cloneable, ok := node.(cloneableNode); ok {
		return cloneable.CloneMetaData()
	}
	return node
}

type cacheMaterializer interface {
	CopyTo(dst any) bool
}

func materializeCacheValue(node any, nodeTarget any) any {
	if node == nil || nodeTarget == nil {
		return nil
	}
	if data, ok := node.([]byte); ok {
		if err := encoding.BlobMarshaler.Unmarshal(data, nodeTarget); err != nil {
			return nil
		}
		return nodeTarget
	}
	if nodeTarget == node {
		return node
	}
	if materializer, ok := node.(cacheMaterializer); ok && materializer.CopyTo(nodeTarget) {
		return nodeTarget
	}
	if cloned := cloneCacheNodeValue(node); cloned != nil {
		if materializer, ok := cloned.(cacheMaterializer); ok && materializer.CopyTo(nodeTarget) {
			return nodeTarget
		}
	}
	return nil
}

// L1Cache is an in-memory MRU cache for B-tree nodes and handle objects.
// It optionally integrates with an L2 cache (e.g., Redis) for cross-process sharing and TTL.
type L1Cache struct {
	lookup       map[sop.UUID]*l1CacheEntry
	mru          *l1_mru
	l2CacheNodes sop.L2Cache
	locker       sync.Mutex
	Handles      Cache[sop.UUID, sop.Handle]
}

// DefaultMinCapacity is the default minimum number of entries to retain before evictions are considered.
// Clustered deployments benefit from a larger L1 footprint because L2 (Redis) access has network latency.
var DefaultMinCapacity = 256

// DefaultMaxCapacity is the default hard limit of entries allowed in the L1 cache.
var DefaultMaxCapacity = 512

// DefaultStandaloneMinCapacity is the L1 cache footprint for standalone/in-process usage.
// Standalone uses a smaller L1 because L2 is also in-memory in the same process (deserialization only, no network).
var DefaultStandaloneMinCapacity = 32

// DefaultStandaloneMaxCapacity is the L1 cache ceiling for standalone/in-process usage.
var DefaultStandaloneMaxCapacity = 64

// globalL1CacheRegistry is the singleton L1 cache instance used by GetGlobalCache and NewGlobalCache.
var globalL1CacheRegistry = make(map[sop.L2CacheType]*L1Cache)
var globalL1Locker sync.RWMutex

// GetGlobalL1Cache returns the global L1 cache singleton, creating one on first use
// with the provided L2 cache and capacities appropriate for the deployment mode.
func GetGlobalL1Cache(l2c sop.L2Cache) *L1Cache {
	if l2c == nil {
		return nil
	}

	gc := func() *L1Cache {
		globalL1Locker.RLock()
		defer globalL1Locker.RUnlock()
		return globalL1CacheRegistry[l2c.GetType()]
	}()
	if gc != nil {
		return gc
	}

	globalL1Locker.Lock()
	defer globalL1Locker.Unlock()

	gc = globalL1CacheRegistry[l2c.GetType()]
	if gc != nil {
		return gc
	}

	// Create and register new Global L1Cache for the given Cache Type.
	minCapacity, maxCapacity := defaultL1Capacities(l2c)
	gc = NewL1Cache(l2c, minCapacity, maxCapacity)
	gc.l2CacheNodes = l2c
	globalL1CacheRegistry[l2c.GetType()] = gc
	return gc
}

// NewL1Cache constructs a new L1Cache with the given L2 cache and capacity bounds.
func NewL1Cache(l2c sop.L2Cache, minCapacity, maxCapacity int) *L1Cache {
	l1c := &L1Cache{
		lookup:       make(map[sop.UUID]*l1CacheEntry, maxCapacity),
		l2CacheNodes: l2c,
		Handles:      NewSynchronizedCache[sop.UUID, sop.Handle](minCapacity, maxCapacity),
	}
	l1c.mru = newL1Mru(l1c, minCapacity, maxCapacity)
	return l1c
}

func defaultL1Capacities(l2c sop.L2Cache) (int, int) {
	if l2c != nil {
		if inMemory, ok := l2c.(*L2InMemoryCache); ok && inMemory.standalone {
			// Standalone: both L1 and L2 are in-memory in same process, smaller L1 is optimal
			return DefaultStandaloneMinCapacity, DefaultStandaloneMaxCapacity
		}
	}
	// Clustered: L2 is remote Redis, larger L1 reduces expensive network calls
	return DefaultMinCapacity, DefaultMaxCapacity
}

// SetNode caches the provided node in the L1 MRU and also in the L2 cache with the given duration.
func (c *L1Cache) SetNode(ctx context.Context, nodeID sop.UUID, node any, nodeCacheDuration time.Duration) {
	c.SetNodeToMRU(ctx, nodeID, node, nodeCacheDuration)
	if c.l2CacheNodes == nil {
		return
	}
	if err := c.l2CacheNodes.SetStruct(ctx, FormatNodeKey(nodeID.String()), node, nodeCacheDuration); err != nil {
		log.Warn(fmt.Sprintf("failed to cache in Redis node with ID: %v, details: %v", nodeID.String(), err))
	}
}

// SetNodeToMRU caches the provided node only in the L1 MRU without touching the L2 cache.
func (c *L1Cache) SetNodeToMRU(ctx context.Context, nodeID sop.UUID, node any, nodeCacheDuration time.Duration) {
	meta, ok := node.(btree.MetaDataType)
	if !ok {
		return
	}
	nv := meta.GetVersion()
	cachedNode := cloneCacheNodeValue(node)
	c.locker.Lock()
	defer c.locker.Unlock()
	if v, ok := c.lookup[nodeID]; ok {
		v.nodeData = cachedNode
		v.nodeVersion = nv
		c.mru.remove(v.dllNode)
		v.dllNode = c.mru.add(nodeID)
		return
	}
	// Add to MRU cache.
	n := c.mru.add(nodeID)
	c.lookup[nodeID] = &l1CacheEntry{
		nodeData:    cachedNode,
		nodeVersion: nv,
		dllNode:     n,
	}
	// Evict LRU items if MRU is full.
	c.mru.evict()
}

func (c *L1Cache) getEntryForHandleLocked(handle sop.Handle) (*l1CacheEntry, bool) {
	nodeID := handle.GetActiveID()
	if v, ok := c.lookup[nodeID]; ok && v.nodeVersion == handle.Version {
		c.mru.remove(v.dllNode)
		v.dllNode = c.mru.add(nodeID)
		return v, true
	}
	return nil, false
}

// GetNodeFromMRU returns the node from L1 if the cached version matches the handle version; otherwise it returns nil.
func (c *L1Cache) GetNodeFromMRU(handle sop.Handle, nodeTarget any) any {
	entry, hit := func() (*l1CacheEntry, bool) {
		c.locker.Lock()
		defer c.locker.Unlock()
		return c.getEntryForHandleLocked(handle)
	}()
	if !hit {
		return nil
	}
	nodeData := entry.nodeData
	if nodeData == nil {
		return nodeTarget
	}
	return materializeCacheValue(nodeData, nodeTarget)
}

// GetNode loads the node by handle either from L1 (if fresh) or from L2, honoring TTL semantics,
// and refreshes the entry in L1 before returning it.
func (c *L1Cache) GetNode(ctx context.Context, handle sop.Handle, nodeTarget any, isNodeCacheTTL bool, nodeCacheTTLDuration time.Duration) (any, error) {
	nodeID := handle.GetActiveID()

	entry, hit := func() (*l1CacheEntry, bool) {
		c.locker.Lock()
		defer c.locker.Unlock()
		return c.getEntryForHandleLocked(handle)
	}()
	if hit {
		nodeData := entry.nodeData
		if nodeData == nil {
			return nodeTarget, nil
		}
		materialized := materializeCacheValue(nodeData, nodeTarget)
		if materialized == nil {
			return nil, nil
		}
		if materialized == nodeTarget {
			return nodeTarget, nil
		}
		return materialized, nil
	}

	if c.l2CacheNodes == nil {
		return nil, nil
	}

	// Get node from L2 cache.
	if isNodeCacheTTL {
		if found, err := c.l2CacheNodes.GetStructEx(ctx, FormatNodeKey(nodeID.String()), nodeTarget, nodeCacheTTLDuration); !found || err != nil {
			return nil, err
		}
	} else {
		if found, err := c.l2CacheNodes.GetStruct(ctx, FormatNodeKey(nodeID.String()), nodeTarget); !found || err != nil {
			return nil, err
		}
	}
	// Found in L2, put in MRU.
	c.SetNodeToMRU(ctx, nodeID, nodeTarget, nodeCacheTTLDuration)

	return nodeTarget, nil
}

// DeleteNodes removes the given node IDs from both the L1 MRU and the L2 cache.
// It returns true if any entries were removed and the last error encountered when deleting from L2.
func (c *L1Cache) DeleteNodes(ctx context.Context, nodesIDs []sop.UUID) (bool, error) {
	var result bool
	var lastErr error

	// Delete from MRU if it is there.
	func() {
		c.locker.Lock()
		defer c.locker.Unlock()
		for _, nodeID := range nodesIDs {
			if v, ok := c.lookup[nodeID]; ok {
				c.mru.remove(v.dllNode)
				v.nodeData = nil
				v.dllNode = nil
				delete(c.lookup, nodeID)
				result = true
			}
		}
	}()

	if c.l2CacheNodes == nil {
		return result, nil
	}

	// Delete from L2 cache if it is there.
	for _, nodeID := range nodesIDs {
		if found, err := c.l2CacheNodes.Delete(ctx, []string{FormatNodeKey(nodeID.String())}); !found || err != nil {
			if err != nil {
				log.Debug(err.Error())
				lastErr = err

			}
		} else {
			result = true
		}
	}
	return result, lastErr
}

// Count returns the number of entries currently stored in the L1 cache.
func (c *L1Cache) Count() int {
	c.locker.Lock()
	defer c.locker.Unlock()
	return len(c.lookup)
}

// IsFull reports whether the L1 cache has reached its maximum capacity.
func (c *L1Cache) IsFull() bool {
	c.locker.Lock()
	defer c.locker.Unlock()
	return c.mru.isFull()
}

// Evict removes least-recently-used entries until the cache is within capacity.
func (c *L1Cache) Evict() {
	c.locker.Lock()
	defer c.locker.Unlock()
	c.mru.evict()
}

// FormatNodeKey prefixes the key with 'N' to form the cache key for a node.
func FormatNodeKey(k string) string {
	return fmt.Sprintf("N%s", k)
}
