package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/model"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/search"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Database extends the core sop.Database with AI capabilities.
type Database struct {
	config sop.DatabaseOptions
	cache  sop.L2Cache
}

// NewDatabase creates a new AI-enabled database manager.
func NewDatabase(config sop.DatabaseOptions) *Database {
	config, _ = database.ValidateOptions(config)
	var to sop.TransactionOptions
	config.CopyTo(&to)
	return &Database{
		config: config,
		cache:  sop.GetL2Cache(to),
	}
}

// NewCassandraDatabase creates a new AI-enabled database manager backed by Cassandra.
func NewCassandraDatabase(config sop.DatabaseOptions) *Database {
	config, _ = database.ValidateCassandraOptions(config)
	var to sop.TransactionOptions
	config.CopyTo(&to)
	return &Database{
		config: config,
		cache:  sop.GetL2Cache(to),
	}
}

// Cache returns the L2 cache used by the database.
func (db *Database) Cache() sop.L2Cache {
	return db.cache
}

// Locker returns a Lock facet fronting the L2 cache used in the database.
// Useful for implementing Optimistic locking.
func (db *Database) Locker() sop.Locker {
	return db.cache
}

// Options returns the database options.
func (db *Database) Options() sop.DatabaseOptions {
	return db.config
}

// StoragePath returns the base storage path.
func (db *Database) StoragePath() string {
	if len(db.config.StoresFolders) > 0 {
		return db.config.StoresFolders[0]
	}
	return ""
}

// ErasureConfig returns the configured erasure coding configuration.
func (db *Database) ErasureConfig() map[string]sop.ErasureCodingConfig {
	return db.config.ErasureConfig
}

// CacheType returns the configured cache type.
func (db *Database) CacheType() sop.L2CacheType {
	return db.config.CacheType
}

// StoresFolders returns the list of storage folders (for replication).
func (db *Database) StoresFolders() []string {
	return db.config.StoresFolders
}

// BeginTransaction starts a new transaction.
func (db *Database) BeginTransaction(ctx context.Context, mode sop.TransactionMode, maxTime ...time.Duration) (sop.Transaction, error) {
	tx, err := database.BeginTransaction(ctx, db.config, mode, maxTime...)
	return tx, err
}

// StoreExists checks if a B-Tree store with the given name exists.
// It starts a read-only transaction to perform the check.
func (db *Database) StoreExists(ctx context.Context, name string) (bool, error) {
	// Optimization: For filesystem-based databases, check for the store's existence directly
	// to avoid the overhead of starting a transaction.
	if (db.config.Type == sop.Standalone || db.config.Type == sop.Clustered) && len(db.config.StoresFolders) > 0 {
		storePath := filepath.Join(db.config.StoresFolders[0], name, fs.StoreInfoFilename)
		if _, err := os.Stat(storePath); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			// Log error but fall back to transaction-based check just in case
			log.Warn("Direct filesystem check for store existence failed", "error", err)
		} else {
			return false, nil
		}
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	trans, _ := tx.GetPhasedTransaction().(*common.Transaction)
	sr := trans.GetStoreRepository()

	stores, err := sr.Get(ctx, name)
	return len(stores) > 0, err
}

// Config returns the database configuration.
func (db *Database) Config() sop.DatabaseOptions {
	return db.config
}

// OpenModelStore opens a ModelStore for the specified name using the provided transaction.
func (db *Database) OpenModelStore(ctx context.Context, name string, t sop.Transaction) (ai.ModelStore, error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	return model.New(name, t), nil
}

// OpenBtree opens a general purpose B-Tree store with string keys and any values.
func (db *Database) OpenBtree(ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[string, any], error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	return database.OpenBtree[string, any](ctx, db.config, name, t, nil)
}

// NewBtree creates a new B-Tree store with string keys and any values.
func (db *Database) NewBtree(ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[string, any], error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	return database.NewBtree[string, any](ctx, db.config, name, t, nil)
}

// OpenBtreeCursor opens a cursor wrapper for a given Btree. It opens it if it is not yet.
func (db *Database) OpenBtreeCursor(ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[string, any], error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	return database.OpenBtreeCursor[string, any](ctx, db.config, name, t, nil)
}

// OpenVectorStore opens a vector store with map[string]any payload.
func (db *Database) OpenVectorStore(ctx context.Context, name string, t sop.Transaction, cfg vector.Config) (ai.VectorStore[map[string]any], error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	// Populate replication config if available and not set in cfg
	// We access the core database's configuration via the getters we added.
	if len(cfg.TransactionOptions.StoresFolders) == 0 && len(db.StoresFolders()) > 0 {
		cfg.TransactionOptions.StoresFolders = db.StoresFolders()
	}
	if len(cfg.TransactionOptions.ErasureConfig) == 0 && len(db.ErasureConfig()) > 0 {
		cfg.TransactionOptions.ErasureConfig = db.ErasureConfig()
	}
	if cfg.TransactionOptions.CacheType == 0 {
		cfg.TransactionOptions.CacheType = db.CacheType()
	}
	if cfg.Cache == nil {
		cfg.Cache = db.Cache()
	}
	return vector.Open[map[string]any](ctx, t, name, cfg)
}

// OpenSearch opens a text search index for the specified name using the provided transaction.
func (db *Database) OpenSearch(ctx context.Context, name string, t sop.Transaction) (*search.Index, error) {
	if db.StoragePath() != "" {
		if err := os.MkdirAll(db.StoragePath(), 0755); err != nil {
			return nil, err
		}
	}
	return search.NewIndex(ctx, db.config, t, name)
}

// RemoveModelStore removes the model store and its underlying B-Tree.
func (db *Database) RemoveModelStore(ctx context.Context, name string) error {
	return database.RemoveBtree(ctx, db.config, fmt.Sprintf("%s_models", name))
}

// RemoveKnowledgeBase removes the Knowledge Base and its underlying B-Trees.
func (db *Database) RemoveKnowledgeBase(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), "/data") {
		name = name[:len(name)-len("/data")]
	}
	if name == "" {
		return fmt.Errorf("knowledge base name is required")
	}

	trans, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return err
	}

	stores, err := trans.GetStores(ctx)
	_ = trans.Rollback(ctx)
	if err != nil {
		return err
	}

	suggestion := ""
	for _, store := range stores {
		baseName, _, hasSlash := strings.Cut(store, "/")
		if !hasSlash {
			continue
		}
		if baseName == name {
			suggestion = ""
			break
		}
		if suggestion == "" && strings.EqualFold(baseName, name) {
			suggestion = baseName
		}
	}

	prefix := name + "/"
	var lastErr error
	removedAny := false
	for _, store := range stores {
		if !strings.HasPrefix(store, prefix) {
			continue
		}
		if err := database.RemoveBtree(ctx, db.config, store); err != nil {
			lastErr = err
			continue
		}
		removedAny = true
	}

	if removedAny {
		return nil
	}
	if lastErr == nil {
		if suggestion != "" {
			return fmt.Errorf("knowledge base '%s' not found: names are case-sensitive; did you mean '%s'?", name, suggestion)
		}
		return fmt.Errorf("knowledge base '%s' not found", name)
	}
	return lastErr
}

// RemoveVectorStore removes the vector store and its underlying B-Trees.
func (db *Database) RemoveVectorStore(ctx context.Context, name string) error {
	suffixes := []string{
		"/sys_config",
		"/tmp_vecs",
		"/data",
	}

	var lastErr error
	succeeded := false

	for _, suffix := range suffixes {
		if err := database.RemoveBtree(ctx, db.config, fmt.Sprintf("%s%s", name, suffix)); err != nil {
			lastErr = err
		}
	}

	// Also try to remove versioned tables.
	// We use an internal transaction to peek at the current version.
	trans, err := db.BeginTransaction(ctx, sop.ForReading)
	if err == nil {
		vs, err := db.OpenVectorStore(ctx, name, trans, vector.Config{})
		if err == nil {
			version, _ := vs.Version(ctx)

			f := func(versionSuffix string) int {
				i := 0
				if err := database.RemoveBtree(ctx, db.config, fmt.Sprintf("%s%s%s", name, "/lku", versionSuffix)); err == nil {
					i++
				} else {
					lastErr = err
				}
				if err := database.RemoveBtree(ctx, db.config, fmt.Sprintf("%s%s%s", name, "/centroids", versionSuffix)); err == nil {
					i++
				} else {
					lastErr = err
				}
				if err := database.RemoveBtree(ctx, db.config, fmt.Sprintf("%s%s%s", name, "/vecs", versionSuffix)); err == nil {
					i++
				} else {
					lastErr = err
				}

				log.Debug(fmt.Sprintf("version suffix %s, success count: %d", versionSuffix, i))

				return i
			}

			// Delete the unversioned current set.
			if f("") == 3 {
				succeeded = true
			}

			// Remove the current version vector components.
			versionSuffix := fmt.Sprintf("_%d", version)
			if f(versionSuffix) == 3 {
				succeeded = true
			}

			// Remove the previous version vector components, if there is.
			if version > 0 {
				versionSuffix = fmt.Sprintf("_%d", version-1)
				if f(versionSuffix) == 3 {
					succeeded = true
				}
			}

			// Remove the next version vector components, if there is.
			versionSuffix = fmt.Sprintf("_%d", version+1)
			if f(versionSuffix) == 3 {
				succeeded = true
			}
		}
		// We only read, so rollback is fine/preferred to release locks immediately.
		_ = trans.Rollback(ctx)
	}

	// We know we deleted a set of VectorStore component files, return nil to denote success.
	if succeeded {
		lastErr = nil
	}

	return lastErr
}

// RemoveSearch removes the search index and its underlying B-Trees.
func (db *Database) RemoveSearch(ctx context.Context, name string) error {
	var lastErr error
	remove := func(n string) {
		if err := database.RemoveBtree(ctx, db.config, n); err != nil {
			lastErr = err
		}
	}

	suffixes := []string{
		"_postings",
		"_term_stats",
		"_doc_stats",
		"_global",
	}

	for _, suffix := range suffixes {
		remove(fmt.Sprintf("%s%s", name, suffix))
	}
	return lastErr
}

// GetStores returns the standard stores, excluding system and vector databases.
func (db *Database) GetStores(ctx context.Context) ([]string, error) {
	return db.getStores(ctx, false)
}

// GetDomains returns only the vector databases (domains).
func (db *Database) GetDomains(ctx context.Context) ([]string, error) {
	return db.getStores(ctx, true)
}

func (db *Database) getStores(ctx context.Context, vector bool) ([]string, error) {
	trans, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	defer trans.Rollback(ctx)

	// Since trans in main.go actually supports GetStores, let's use type assertion
	if getStoresTx, ok := trans.(interface {
		GetStores(context.Context) ([]string, error)
	}); ok {
		allStores, err := getStoresTx.GetStores(ctx)
		if err != nil {
			return nil, err
		}

		var stores []string
		kbLookup := make(map[string]byte)
		for _, store := range allStores {
			if !vector {
				// non-vector
				if !strings.Contains(store, "/") {
					stores = append(stores, store)
				}
			} else {
				// vector or text search
				if strings.Contains(store, "/") {
					parts := strings.Split(store, "/")
					if _, exists := kbLookup[parts[0]]; !exists {
						stores = append(stores, parts[0])
						kbLookup[parts[0]] = 1
					}
				}
			}
		}
		if len(stores) > 0 {
			return stores, nil
		}
		return make([]string, 0), nil
	}

	return nil, fmt.Errorf("underlying transaction does not support GetStores")
}
