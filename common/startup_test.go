package common

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func init() {
	// Register dummy factories to allow SetCacheFactory to work
	sop.RegisterL2CacheFactory(sop.NoCache, func(sop.TransactionOptions) sop.L2Cache { return nil })
	sop.RegisterL2CacheFactory(sop.Redis, func(sop.TransactionOptions) sop.L2Cache { return nil })
}

func TestOnStartUp_InMemory(t *testing.T) {
	// Save state
	originalFlag := onStartUpFlag
	originalCache := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.InMemory})
	defer func() {
		onStartUpFlag = originalFlag
		if originalCache == nil {
			sop.RegisterL2CacheFactory(sop.InMemory, nil)
		}
	}()

	// Setup
	onStartUpFlag = true
	sop.RegisterL2CacheFactory(sop.InMemory, func(sop.TransactionOptions) sop.L2Cache { return cache.NewL2InMemoryCache() })

	trans := &Transaction{l2Cache: cache.NewL2InMemoryCache()}

	// First call should return true
	if !trans.onStartUp() {
		t.Error("Expected onStartUp() to return true on first call for InMemory, got false")
	}

	// Second call should return false (run once)
	if trans.onStartUp() {
		t.Error("Expected onStartUp() to return false on second call for InMemory, got true")
	}
}

type mockNoCache struct {
	sop.L2Cache
}

func (m mockNoCache) GetType() sop.L2CacheType { return sop.NoCache }

func TestOnStartUp_NoCache(t *testing.T) {
	// Save state
	originalFlag := onStartUpFlag
	defer func() {
		onStartUpFlag = originalFlag
	}()

	// Setup
	onStartUpFlag = true

	trans := &Transaction{l2Cache: mockNoCache{L2Cache: mocks.NewMockClient()}}

	// Should return false
	if trans.onStartUp() {
		t.Error("Expected onStartUp() to return false for NoCache, got true")
	}
}

func TestOnStartUp_Redis(t *testing.T) {
	// Save state
	originalFlag := onStartUpFlag
	defer func() {
		onStartUpFlag = originalFlag
	}()

	// Setup
	onStartUpFlag = true

	trans := &Transaction{l2Cache: mocks.NewMockClient()}

	// Should return false
	if trans.onStartUp() {
		t.Error("Expected onStartUp() to return false for Redis, got true")
	}
}
