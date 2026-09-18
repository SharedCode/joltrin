package database

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/cache"
	"github.com/sharedcode/joltrin/common"
	"github.com/sharedcode/joltrin/fs"
	"github.com/sharedcode/joltrin/incfs"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

// DatabaseOptions holds the configuration for the database.
// Deprecated: Use sop.DatabaseOptions instead.
type DatabaseOptions = sop.DatabaseOptions

var databaseOptionsLookup = make(map[string]*DatabaseOptions)

const databaseOptionsFilename = "dboptions.json"

var locker = sync.Mutex{}

func setOptionToLookup(filename string, dbOptions *DatabaseOptions) {
	locker.Lock()
	defer locker.Unlock()
	databaseOptionsLookup[filename] = dbOptions
}
func getOptionFromLookup(filename string) *DatabaseOptions {
	locker.Lock()
	defer locker.Unlock()
	if v, ok := databaseOptionsLookup[filename]; ok {
		return v
	}
	return nil
}
func removeOptionFromLookup(filename string) {
	locker.Lock()
	defer locker.Unlock()
	delete(databaseOptionsLookup, filename)
}
func isOptionInLookup(filename string) bool {
	opts := getOptionFromLookup(filename)
	return opts != nil && !opts.IsEmpty()
}

// Setup persists the database options to the stores folders.
// This is a one-time setup operation for the database.
// It ensures the options are saved to all StoresFolders.
func Setup(ctx context.Context, opts sop.DatabaseOptions) (DatabaseOptions, error) {
	if len(opts.StoresFolders) == 0 {
		return opts, fmt.Errorf("StoresFolders must be provided")
	}

	// Allow to interpret some basic fields.
	opts, _ = ValidateOptions(opts)
	// Convert to absolute paths
	for i, folder := range opts.StoresFolders {
		absPath, err := filepath.Abs(folder)
		if err != nil {
			return opts, err
		}
		opts.StoresFolders[i] = absPath
	}
	if opts.ErasureConfig != nil {
		for k, v := range opts.ErasureConfig {
			for i, folder := range v.BaseFolderPathsAcrossDrives {
				absPath, err := filepath.Abs(folder)
				if err != nil {
					return opts, err
				}
				v.BaseFolderPathsAcrossDrives[i] = absPath
			}
			opts.ErasureConfig[k] = v
		}
	}

	folder := opts.StoresFolders[0]
	fileName := filepath.Join(folder, databaseOptionsFilename)

	if isOptionInLookup(fileName) {
		// Check if file physically exists to detect stale cache (e.g. manual deletion)
		if _, err := os.Stat(fileName); os.IsNotExist(err) {
			removeOptionFromLookup(fileName)
		} else {
			return DatabaseOptions{}, fmt.Errorf("database %s already setup", fileName)
		}
	}

	// Check if exists in the first folder
	if _, err := os.Stat(fileName); err == nil {
		// Read
		b, err := os.ReadFile(fileName)
		if err != nil {
			return opts, err
		}
		var loadedOpts sop.DatabaseOptions
		if err := json.Unmarshal(b, &loadedOpts); err != nil {
			return opts, err
		}
		setOptionToLookup(fileName, &loadedOpts)
		return loadedOpts, nil
	}

	// Use provided options
	ba, err := json.Marshal(opts)
	if err != nil {
		return opts, err
	}

	// Persist to all folders
	for _, folder := range opts.StoresFolders {
		if err := os.MkdirAll(folder, 0755); err != nil {
			return opts, err
		}
		fname := filepath.Join(folder, databaseOptionsFilename)
		if err := os.WriteFile(fname, ba, 0644); err != nil {
			return opts, err
		}

		if opts.RegistryHashModValue > 0 {
			fname = filepath.Join(folder, fs.RegistryHashModValueFilename)
			if _, err := os.Stat(fname); os.IsNotExist(err) {
				if err := os.WriteFile(fname, []byte(fmt.Sprintf("%d", opts.RegistryHashModValue)), 0644); err != nil {
					return opts, err
				}
			}
		}
	}

	setOptionToLookup(fileName, &opts)
	return opts, nil
}

// IsDatabasePath returns true (hasDBOptions), true (hasRegHashMod) if folder path has necessary ingredients of a SOP DB.
func IsDatabasePath(path string) (bool, bool) {
	var hasDBOptions bool
	var hasRegHashMod bool
	if _, err := os.Stat(filepath.Join(path, databaseOptionsFilename)); err == nil {
		hasDBOptions = true
	}
	if _, err := os.Stat(filepath.Join(path, fs.RegistryHashModValueFilename)); err == nil {
		hasRegHashMod = true
	}
	return hasDBOptions, hasRegHashMod
}

// GetOptions reads the database options from the specified folder.
func GetOptions(ctx context.Context, folderPath string) (sop.DatabaseOptions, error) {
	fileName := filepath.Join(folderPath, databaseOptionsFilename)

	fio := fs.NewFileIO()

	// If already loaded in memory, & file exists, just return that copy.
	dbOpts := getOptionFromLookup(fileName)
	if dbOpts != nil && !dbOpts.IsEmpty() && fio.Exists(ctx, fileName) {
		return *dbOpts, nil
	}

	ba, err := os.ReadFile(fileName)
	if err != nil {
		return sop.DatabaseOptions{}, err
	}
	var opts sop.DatabaseOptions
	if err := json.Unmarshal(ba, &opts); err != nil {
		return sop.DatabaseOptions{}, err
	}
	setOptionToLookup(fileName, &opts)
	return opts, nil
}

// ValidateOptions validates and prepares the database options.
// It infers CacheType if not set.
func ValidateOptions(config sop.DatabaseOptions) (sop.DatabaseOptions, error) {
	// If CacheType is not set, infer it from Type.
	if config.CacheType == sop.NoCache {
		if config.Type == sop.Clustered {
			config.CacheType = sop.Redis
		} else {
			config.CacheType = sop.InMemory
		}
	}

	if config.CacheType == sop.Redis {
		// Check if Redis adapter is registered.
		c := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
		if c == nil {
			// Auto register Redis L2 Cache.
			sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
		}
	} else if config.CacheType == sop.InMemory {
		c := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.InMemory})
		if c == nil {
			// Auto register InMemory L2 Cache.
			sop.RegisterL2CacheFactory(sop.InMemory, func(options sop.TransactionOptions) sop.L2Cache {
				return cache.NewL2InMemoryCache()
			})
		}
	}

	return config, nil
}

// ValidateCassandraOptions validates and prepares the database options for Cassandra.
func ValidateCassandraOptions(config sop.DatabaseOptions) (sop.DatabaseOptions, error) {
	if config.Keyspace == "" {
		return config, fmt.Errorf("cassandra mode requires Keyspace to be set")
	}
	c := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
	if c == nil {
		// Auto register Redis L2 Cache.
		sop.RegisterL2CacheFactory(sop.Redis, redis.NewClient)
		c = sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
		if c == nil {
			return config, fmt.Errorf("cassandra mode requires the Redis adapter; initialize the connection using redis.OpenConnection first")
		}
	}
	config.CacheType = sop.Redis
	return config, nil
}

// BeginTransaction starts a new transaction.
func BeginTransaction(ctx context.Context, config sop.DatabaseOptions, mode sop.TransactionMode, maxTime ...time.Duration) (sop.Transaction, error) {
	var err error
	config, err = ValidateOptions(config)
	if err != nil {
		return nil, err
	}

	var mt time.Duration
	if len(maxTime) > 0 {
		mt = maxTime[0]
	} else {
		mt = 15 * time.Minute
	}

	var opts sop.TransactionOptions
	// Merge DatabaseOptions into TransactionOptions
	config.CopyTo(&opts)
	log.Debug(fmt.Sprintf("BeginTransaction: StoresFolders=%v, ErasureConfigLen=%d, IsReplicated=%v", opts.StoresFolders, len(opts.ErasureConfig), opts.IsReplicated()))
	opts.Mode = mode
	opts.MaxTime = mt

	var t sop.Transaction

	if opts.IsReplicated() {
		if opts.IsCassandraHybrid() {
			t, err = incfs.NewTransactionWithReplication(ctx, opts)
		} else {
			t, err = infs.NewTransactionWithReplication(ctx, opts)
		}
	} else {
		if config.IsCassandraHybrid() {
			t, err = incfs.NewTransaction(ctx, opts)
		} else {
			t, err = infs.NewTransaction(ctx, opts)
		}
	}
	if err != nil {
		return nil, err
	}

	if err := t.Begin(ctx); err != nil {
		return nil, err
	}

	return t, nil
}

// OpenBtree opens a general purpose B-Tree store.
// This allows the Database to manage standard Key-Value stores alongside AI stores.
func OpenBtree[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if config.IsReplicated() {
		if config.IsCassandraHybrid() {
			return incfs.OpenBtree[TK, TV](ctx, name, t, comparer)
		} else {
			return infs.OpenBtreeWithReplication[TK, TV](ctx, name, t, comparer)
		}
	} else {
		if config.IsCassandraHybrid() {
			return incfs.OpenBtree[TK, TV](ctx, name, t, comparer)
		} else {
			return infs.OpenBtree[TK, TV](ctx, name, t, comparer)
		}
	}
}

// OpenBtreeCursor opens a cursor wrapper for a given Btree. It opens it if it is not yet.
func OpenBtreeCursor[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	return common.OpenBtreeCursor[TK, TV](ctx, name, t, comparer)
}

// CursorOnOpenedBtree opens a cursor wrapper for a given opened Btree.
func CursorOnOpenedBtree[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction) (btree.BtreeInterface[TK, TV], error) {
	return common.CursorOnOpenedBtree[TK, TV](ctx, name, t)
}

// NewBtree will open an existing one or create a new general purpose B-Tree store.
func NewBtree[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction, comparer btree.ComparerFunc[TK], options ...sop.StoreOptions) (btree.BtreeInterface[TK, TV], error) {
	var opts sop.StoreOptions
	if len(options) > 0 {
		opts = options[0]
		opts.Name = name
		// Reset IsPrimitiveKey to true if TK is primitive.
		// This is needed because IsPrimitiveKey is false by default in StoreOptions.
		if !opts.IsPrimitiveKey {
			opts.IsPrimitiveKey = btree.IsPrimitive[TK]()
		}
	} else {
		opts = sop.ConfigureStore(name, true, btree.DefaultSlotLength, "General purpose B-Tree created via Database", sop.SmallData, "")
		opts.IsPrimitiveKey = btree.IsPrimitive[TK]()
	}

	// If BlobStoreBaseFolderPath is not set, use the first store folder from the database configuration.
	if opts.BlobStoreBaseFolderPath == "" && len(config.StoresFolders) > 0 {
		opts.BlobStoreBaseFolderPath = config.StoresFolders[0]
	}

	if config.IsReplicated() {
		if config.IsCassandraHybrid() {
			return incfs.NewBtreeWithReplication[TK, TV](ctx, opts, t, comparer)
		} else {
			return infs.NewBtreeWithReplication[TK, TV](ctx, opts, t, comparer)
		}
	} else {
		if config.IsCassandraHybrid() {
			return incfs.NewBtree[TK, TV](ctx, opts, t, comparer)
		} else {
			return infs.NewBtree[TK, TV](ctx, opts, t, comparer)
		}
	}
}

// RemoveBtree removes a B-Tree store from the database.
// This is a destructive operation and cannot be undone.
func RemoveBtree(ctx context.Context, config sop.DatabaseOptions, name string) error {
	config, _ = ValidateOptions(config)
	if config.IsCassandraHybrid() {
		return incfs.RemoveBtree(ctx, name, config.CacheType)
	}
	return infs.RemoveBtree(ctx, name, config.StoresFolders, config.ErasureConfig, config.CacheType, config.RedisConfig)
}

// RemoveBtrees removes all B-Trees (stores) in the database.
// This is a destructive operation and cannot be undone.
// This function ensures a clean removal of all stores and their metadata (e.g. Redis keys).
func RemoveBtrees(ctx context.Context, config sop.DatabaseOptions) error {
	t, err := BeginTransaction(ctx, config, sop.ForReading)
	if err != nil {
		return err
	}

	stores, err := t.GetStores(ctx)
	// Always rollback since we only needed to read the store list.
	_ = t.Rollback(ctx)

	if err != nil {
		return err
	}

	for _, name := range stores {
		if err := RemoveBtree(ctx, config, name); err != nil {
			return fmt.Errorf("failed to remove btree %s: %w", name, err)
		}
	}
	return nil
}

// Remove deletes the database(Btrees, options and reghashmod file) from the folder.
func Remove(ctx context.Context, dbPath string) error {
	opts, err := GetOptions(ctx, dbPath)
	if err != nil {
		return err
	}
	err = RemoveBtrees(ctx, opts)
	if err != nil {
		return err
	}

	// Collect all folders to remove: the primary dbPath + any replicated folders.
	foldersToRemove := make(map[string]struct{})
	foldersToRemove[dbPath] = struct{}{}
	for _, f := range opts.StoresFolders {
		foldersToRemove[f] = struct{}{}
	}

	cwd, _ := os.Getwd()

	for folder := range foldersToRemove {
		// Remove from Options lookup.
		filename := filepath.Join(folder, databaseOptionsFilename)
		removeOptionFromLookup(filename)

		// Also avoid deleting the current working directory if it happens to be the DB path.
		absPath, _ := filepath.Abs(folder)
		if absPath == cwd {
			// Instead of entirely skipping, we must clean up SOP's specific metadata files.
			// This addresses the issue where `storelist.txt` is not deleted or recreated
			// during workspace initialization overrides.
			os.RemoveAll(filepath.Join(folder, "storelist.txt"))
			os.RemoveAll(filepath.Join(folder, databaseOptionsFilename))
			os.RemoveAll(filepath.Join(folder, "replstat.txt")) // fs.replicationStatusFilename

			log.Warn("Skipping os.RemoveAll because it is the current working directory. Removed SOP metadata only.", "path", logsafe.V(folder))
		} else {
			if err := os.RemoveAll(folder); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReinstateFailedDrives asks the replication tracker to reinstate failed passive targets.
// This API is only applicable for infs backend.
func ReinstateFailedDrives(ctx context.Context, config sop.DatabaseOptions) error {
	config, _ = ValidateOptions(config)
	if config.IsCassandraHybrid() {
		return fmt.Errorf("ReinstateFailedDrives only apply for infs, a pure File System based backend")
	}
	return infs.ReinstateFailedDrives(ctx, config.StoresFolders, config.CacheType)
}
