package cache

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

type countedL2Cache struct {
	sop.L2Cache
	getCount int64
	setCount int64
}

func (c *countedL2Cache) GetStruct(ctx context.Context, key string, target interface{}) (bool, error) {
	c.getCount++
	return c.L2Cache.GetStruct(ctx, key, target)
}

func (c *countedL2Cache) GetStructEx(ctx context.Context, key string, target interface{}, expiration time.Duration) (bool, error) {
	c.getCount++
	return c.L2Cache.GetStructEx(ctx, key, target, expiration)
}

func (c *countedL2Cache) SetStruct(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	c.setCount++
	return c.L2Cache.SetStruct(ctx, key, value, expiration)
}

func BenchmarkStandaloneCacheWorkload(b *testing.B) {
	cases := []struct {
		name           string
		workingSetSize int
		hotSetSize     int
		coldRatio      int
		l1Min          int
		l1Max          int
		l2Capacity     int
	}{
		{name: "working_4096_hot_256_cold_85_l1_64_128", workingSetSize: 4096, hotSetSize: 256, coldRatio: 85, l1Min: 64, l1Max: 128, l2Capacity: 1000},
		{name: "working_4096_hot_256_cold_85_l1_32_64", workingSetSize: 4096, hotSetSize: 256, coldRatio: 85, l1Min: 32, l1Max: 64, l2Capacity: 1000},
		{name: "working_8192_hot_512_cold_90_l1_64_128", workingSetSize: 8192, hotSetSize: 512, coldRatio: 90, l1Min: 64, l1Max: 128, l2Capacity: 1000},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			seed := int64(42)
			rng := rand.New(rand.NewSource(seed))
			var probeCount int64
			var l1Hits int64
			var l2Fallbacks int64
			var l2Gets int64
			var l2Sets int64

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				ctx := context.Background()
				l2Base := &L2InMemoryCache{data: newShardedMap(tc.l2Capacity), locks: newShardedMap(tc.l2Capacity), standalone: true}
				countedL2 := &countedL2Cache{L2Cache: l2Base}
				l1 := NewL1Cache(countedL2, tc.l1Min, tc.l1Max)
				l1.l2CacheNodes = countedL2

				nodes := make([]*btree.Node[string, string], 0, tc.workingSetSize)
				for j := 0; j < tc.workingSetSize; j++ {
					node := &btree.Node[string, string]{ID: sop.NewUUID(), Version: 1}
					node.Slots = []btree.Item[string, string]{{ID: sop.NewUUID(), Key: fmt.Sprintf("k-%d", j), Value: nil}}
					if err := l2Base.SetStruct(ctx, fmt.Sprintf("node:%s", node.ID.String()), node, 0); err != nil {
						b.Fatalf("failed to seed L2: %v", err)
					}
					nodes = append(nodes, node)
				}

				for j := 0; j < tc.hotSetSize; j++ {
					handle := sop.NewHandle(nodes[j].ID)
					handle.Version = nodes[j].Version
					target := &btree.Node[string, string]{}
					if _, err := l1.GetNode(ctx, handle, target, false, 0); err != nil {
						b.Fatalf("failed to warm hot set: %v", err)
					}
				}

				b.StartTimer()
				for j := 0; j < 256; j++ {
					var node *btree.Node[string, string]
					if rng.Intn(100) < tc.coldRatio {
						idx := tc.hotSetSize + rng.Intn(tc.workingSetSize-tc.hotSetSize)
						node = nodes[idx]
					} else {
						idx := rng.Intn(tc.hotSetSize)
						node = nodes[idx]
					}

					handle := sop.NewHandle(node.ID)
					handle.Version = node.Version
					target := &btree.Node[string, string]{}
					probeCount++

					if hit := l1.GetNodeFromMRU(handle, nil); hit != nil {
						l1Hits++
					} else {
						l2Fallbacks++
					}

					if _, err := l1.GetNode(ctx, handle, target, false, 0); err != nil {
						b.Fatalf("cache access failed: %v", err)
					}
				}
				l2Gets += countedL2.getCount
				l2Sets += countedL2.setCount
				b.StopTimer()
			}

			b.ReportMetric(float64(l1Hits)/float64(probeCount), "l1_hit_ratio")
			b.ReportMetric(float64(l2Fallbacks)/float64(probeCount), "l2_fallback_ratio")
			b.ReportMetric(float64(l2Gets)/float64(probeCount), "l2_gets_per_probe")
			b.ReportMetric(float64(l2Sets)/float64(probeCount), "l2_sets_per_probe")
		})
	}
}
