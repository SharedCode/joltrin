// Package inredck provides SOP implementations that use Redis for caching
// and Cassandra for backend data storage. It offers package-level initialization
// helpers (Initialize, IsInitialized, Shutdown), transaction constructors, and
// B-tree convenience wrappers for Cassandra-backed persistence.
package inredck

import (
	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/adapters/redis"
)

// Initialize assigns configs and opens connections for this package (Cassandra, Redis).
func Initialize(cassandraConfig cas.Config, redisConfig redis.Options) error {
	if _, err := cas.OpenConnection(cassandraConfig); err != nil {
		return err
	}
	if _, err := redis.OpenConnection(redisConfig); err != nil {
		return err
	}
	return nil
}

// IsInitialized reports whether required components have been initialized.
func IsInitialized() bool {
	return cas.IsConnectionInstantiated() && redis.IsConnectionInstantiated()
}

// Shutdown closes all connections used by this package.
func Shutdown() {
	cas.CloseConnection()
	redis.CloseConnection()
}
