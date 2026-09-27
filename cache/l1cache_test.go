package cache

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

func TestL1CacheSetNodeToMRU_IgnoresUnsupportedNodeValue(t *testing.T) {
	c := NewL1Cache(nil, 1, 2)
	ctx := context.Background()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SetNodeToMRU panicked on unsupported node value: %v", r)
		}
	}()

	c.SetNodeToMRU(ctx, sop.NewUUID(), struct{}{}, time.Minute)
	if got := c.Count(); got != 0 {
		t.Fatalf("expected no cache entry for unsupported node value, got %d", got)
	}
}

func TestL1CacheGetNodeFromMRU_DoesNotReturnMismatchedGenericNodeType(t *testing.T) {
	c := NewL1Cache(nil, 1, 2)
	ctx := context.Background()
	cachedNode := &btree.Node[any, any]{ID: sop.NewUUID(), Version: 1}
	c.SetNodeToMRU(ctx, cachedNode.ID, cachedNode, time.Minute)

	handle := sop.NewHandle(cachedNode.ID)
	handle.Version = cachedNode.Version
	target := &btree.Node[string, any]{}

	if got := c.GetNodeFromMRU(handle, target); got != nil {
		t.Fatalf("expected no cache materialization for mismatched generic node type, got %T", got)
	}
}

func TestL1CacheGetNodeFromMRU_ReturnsConstructedNodeForMatchingTargetType(t *testing.T) {
	c := NewL1Cache(nil, 1, 2)
	ctx := context.Background()
	cachedNode := &btree.Node[string, string]{ID: sop.NewUUID(), Version: 1}
	c.SetNodeToMRU(ctx, cachedNode.ID, cachedNode, time.Minute)

	handle := sop.NewHandle(cachedNode.ID)
	handle.Version = cachedNode.Version
	target := cachedNode

	if got := c.GetNodeFromMRU(handle, target); got != cachedNode {
		t.Fatalf("expected cached constructed node to be returned directly, got %T", got)
	}
}

func BenchmarkL1CacheGetNodeFromMRU(b *testing.B) {
	c := NewL1Cache(nil, 1, 2)
	ctx := context.Background()
	node := &btree.Node[string, string]{ID: sop.NewUUID(), Version: 1}
	c.SetNodeToMRU(ctx, node.ID, node, time.Minute)
	handle := sop.NewHandle(node.ID)
	handle.Version = node.Version
	target := &btree.Node[string, string]{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := c.GetNodeFromMRU(handle, target); got == nil {
			b.Fatal("expected cache hit")
		}
	}
}
