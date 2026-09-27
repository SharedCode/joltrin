// Package incfs provides SOP implementations that use Redis for caching and the filesystem
// for backend storage. It exposes package-level initialization helpers and B-Tree wrappers that
// enable filesystem replication.
package incfs

import (
	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/v5/inredck"
)

// Initialize assigns the configs & opens connections to different sub-systems used by this package.
// Example, connection to Cassandra, Redis.
func Initialize(cassandraConfig cas.Config, redisConfig redis.Options) error {
	return inredck.Initialize(cassandraConfig, redisConfig)
}

// IsInitialized returns true if components required were initialized, false otherwise.
func IsInitialized() bool {
	return inredck.IsInitialized()
}

// Shutdown closes all connections used in this package.
func Shutdown() {
	inredck.Shutdown()
}
