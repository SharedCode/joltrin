package main

/*
#include <stdio.h> // C.longlong
#include <stdlib.h> // For free
*/
import "C"
import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	log "log/slog"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/ai"
	database "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
	sopdb "github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func init() {
	sop.ConfigureLogging()
}

var contextLookup map[int64]context.Context = make(map[int64]context.Context)
var contextLookupLocker sync.Mutex
var contextLastID int64

// Context related API to allow Python code to be able to get access to the context objects and thus,
// allow things like cancellation if needed.

//export createContext
func createContext() C.longlong {
	ctx := context.Background()
	contextLookupLocker.Lock()
	defer contextLookupLocker.Unlock()
	contextLastID++
	id := contextLastID

	contextLookup[id] = ctx
	return C.longlong(id)
}

//export cancelContext
func cancelContext(ctxID C.longlong) {
	id := int64(ctxID)
	var c context.CancelFunc
	func() {
		contextLookupLocker.Lock()
		defer contextLookupLocker.Unlock()
		if ctx, ok := contextLookup[id]; ok {
			_, c = context.WithCancel(ctx)
		}
		delete(contextLookup, id)
	}()

	// Call the cancel function for the ctx context.
	if c != nil {
		c()
	}
}

//export removeContext
func removeContext(ctxID C.longlong) {
	id := int64(ctxID)
	contextLookupLocker.Lock()
	defer contextLookupLocker.Unlock()
	delete(contextLookup, id)
}

// Private get context for use internally here.
func getContext(ctxID C.longlong) context.Context {
	contextLookupLocker.Lock()
	defer contextLookupLocker.Unlock()
	return contextLookup[int64(ctxID)]
}

//export contextError
func contextError(ctxID C.longlong) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString("context not found")
	}
	if ctx.Err() == nil {
		return nil
	}
	return C.CString(ctx.Err().Error())
}

// Redis global connection management related.
//
//export openRedisConnection
func openRedisConnection(uri *C.char) *C.char {
	_, err := redis.OpenConnectionWithURL(C.GoString(uri))
	if err != nil {
		errMsg := fmt.Sprintf("error encountered opening Redis connection, details: %v", err)
		log.Warn(errMsg)

		// Remember to deallocate errInfo.message!
		return C.CString(errMsg)
	}
	return nil
}

//export closeRedisConnection
func closeRedisConnection() *C.char {
	err := redis.CloseConnection()
	if err != nil {
		errMsg := fmt.Sprintf("error encountered closing Redis connection, details: %v", err)
		log.Warn(errMsg)

		// Remember to deallocate errMsg!
		return C.CString(errMsg)
	}
	return nil
}

// Cassandra global connection management related.

type CassandraConfig struct {
	ClusterHosts      []string `json:"cluster_hosts"`
	Keyspace          string   `json:"keyspace"`
	Consistency       int      `json:"consistency"`
	ConnectionTimeout int      `json:"connection_timeout"`
	ReplicationClause string   `json:"replication_clause"`
	Authenticator     struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"authenticator"`
}

//export openCassandraConnection
func openCassandraConnection(payload *C.char) *C.char {
	jsonPayload := C.GoString(payload)
	var cfg CassandraConfig
	if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &cfg); err != nil {
		return C.CString(fmt.Sprintf("invalid options: %v", err))
	}

	casConfig := cas.Config{
		ClusterHosts:      cfg.ClusterHosts,
		Keyspace:          cfg.Keyspace,
		Consistency:       gocql.Consistency(cfg.Consistency),
		ConnectionTimeout: time.Duration(cfg.ConnectionTimeout) * time.Millisecond,
		ReplicationClause: cfg.ReplicationClause,
	}
	if cfg.Authenticator.Username != "" {
		casConfig.Authenticator = gocql.PasswordAuthenticator{
			Username: cfg.Authenticator.Username,
			Password: cfg.Authenticator.Password,
		}
	}

	_, err := cas.OpenConnection(casConfig)
	if err != nil {
		errMsg := fmt.Sprintf("error encountered opening Cassandra connection, details: %v", err)
		log.Warn(errMsg)
		return C.CString(errMsg)
	}
	return nil
}

//export closeCassandraConnection
func closeCassandraConnection() *C.char {
	cas.CloseConnection()
	return nil
}

// Logging related.

//export manageLogging
func manageLogging(level C.int, logPath *C.char) *C.char {
	var l log.Level
	switch int(level) {
	case 0:
		l = log.LevelDebug
	case 1:
		l = log.LevelInfo
	case 2:
		l = log.LevelWarn
	case 3:
		l = log.LevelError
	default:
		l = log.LevelInfo
	}

	var w io.Writer = os.Stderr
	if logPath != nil {
		p := C.GoString(logPath)
		if p != "" {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return C.CString(fmt.Sprintf("failed to open log file: %v", err))
			}
			w = f
		}
	}

	opts := &log.HandlerOptions{
		Level: l,
	}
	logger := log.New(log.NewTextHandler(w, opts))
	log.SetDefault(logger)
	return nil
}

// Transaction management related.

// Transaction lookup table is comprised of the transaction & its related B-trees.
var transRegistry = newTransactionRegistry()

// Unified Database Lookup
var dbRegistry = newRegistry[*database.Database]()

const (
	TransactionActionUnknown = iota
	NewTransaction
	Begin
	Commit
	Rollback
)

func extractTransaction(ps string) (*transactionItem, *C.char) {
	uuid, err := sop.ParseUUID(ps)
	if err != nil {
		errMsg := fmt.Sprintf("error parsing UUID, details: %v", err)
		return nil, C.CString(errMsg)
	}

	item, ok := transRegistry.GetItem(uuid)

	if !ok {
		errMsg := fmt.Sprintf("UUID %v not found", uuid.String())
		return nil, C.CString(errMsg)
	}
	return item, nil
}

//export manageTransaction
func manageTransaction(ctxID C.longlong, action C.int, payload *C.char) *C.char {
	ps := C.GoString(payload)

	var ctx context.Context
	if int64(ctxID) > 0 {
		ctx = getContext(ctxID)
		if ctx == nil {
			return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
		}
	}
	switch int(action) {
	case NewTransaction:
		return C.CString("NewTransaction is deprecated. Please use manageDatabase with BeginTransaction action.")

	case Begin:
		return C.CString("Begin is deprecated. Transaction is already begun when created via manageDatabase.")
	case Commit:
		t, err := extractTransaction(ps)
		if err != nil {
			return err
		}

		log.Debug("before Transaction.Commit call")

		if err := t.Transaction.Commit(ctx); err != nil {
			errMsg := fmt.Sprintf("transaction %v Commit failed, details: %v", t.Transaction.GetID().String(), err)
			transRegistry.Remove(t.Transaction.GetID())
			return C.CString(errMsg)
		}

		transRegistry.Remove(t.Transaction.GetID())

	case Rollback:
		t, err := extractTransaction(ps)
		if err != nil {
			return err
		}

		if err := t.Transaction.Rollback(ctx); err != nil {
			errMsg := fmt.Sprintf("transaction %v Rollback failed, details: %v", t.Transaction.GetID().String(), err)
			transRegistry.Remove(t.Transaction.GetID())
			return C.CString(errMsg)
		}

		transRegistry.Remove(t.Transaction.GetID())

	default:
		errMsg := fmt.Sprintf("unsupported action %d", int(action))
		return C.CString(errMsg)
	}
	return nil
}

// Database management related.

type DatabaseAction int

const (
	DatabaseActionUnknown = iota
	NewDatabase
	BeginTransaction
	NewBtree
	OpenBtree
	OpenModelStore
	OpenVectorStore
	OpenSearch
	RemoveBtree
	RemoveModelStore
	RemoveVectorStore
	RemoveSearch
	SetupDatabase
	GetDatabaseOptions
)

//export manageDatabase
func manageDatabase(ctxID C.longlong, action C.int, targetID *C.char, payload *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	targetIDStr := C.GoString(targetID)
	jsonPayload := C.GoString(payload)

	switch int(action) {
	case NewDatabase:
		var opts sop.DatabaseOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid options: %v", err))
		}

		// If StoresFolders is provided, try to load options from the first folder
		// and merge them with the provided options.
		if len(opts.StoresFolders) > 0 {
			loadedOpts, err := sopdb.GetOptions(ctx, opts.StoresFolders[0])
			if err == nil {
				// Merge loaded options into opts
				// We prioritize loaded options for StoresFolders and ErasureConfig
				// but keep other options from the provided opts (like CacheType, RedisConfig, etc.)
				opts.StoresFolders = loadedOpts.StoresFolders
				opts.ErasureConfig = loadedOpts.ErasureConfig
			}
			// If loading fails, we assume it's a new database or options are not persisted yet,
			// so we use the provided opts as is.
		}

		var db *database.Database
		if opts.IsCassandraHybrid() {
			db = database.NewCassandraDatabase(opts)
		} else {
			db = database.NewDatabase(opts)
		}

		id := dbRegistry.Add(db)
		return C.CString(id.String())

	case BeginTransaction:
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		mode := sop.ForWriting
		var maxTime time.Duration
		var opts sop.TransactionOptions
		if jsonPayload != "" {
			if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err == nil {
				mode = opts.Mode
				// Adjust MaxTime from minutes to Duration.
				maxTime = opts.MaxTime * time.Minute
			} else {
				var m int
				if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &m); err == nil {
					mode = sop.TransactionMode(m)
				}
			}
		}

		tx, err := sopdb.BeginTransaction(ctx, db.Config(), mode, maxTime)
		if err != nil {
			return C.CString(err.Error())
		}

		id := transRegistry.Add(tx)
		return C.CString(id.String())

	case SetupDatabase:
		var opts sop.DatabaseOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid options: %v", err))
		}
		updatedOpts, err := sopdb.Setup(ctx, opts)
		if err != nil {
			return C.CString(err.Error())
		}
		if b, err := encoding.DefaultMarshaler.Marshal(updatedOpts); err == nil {
			return C.CString(string(b))
		}
		return nil

	case GetDatabaseOptions:
		// Payload is the store path string
		if jsonPayload == "" {
			return C.CString("store path is required")
		}
		opts, err := sopdb.GetOptions(ctx, jsonPayload)
		if err != nil {
			return C.CString(err.Error())
		}
		if b, err := encoding.DefaultMarshaler.Marshal(opts); err == nil {
			return C.CString(string(b))
		}
		return nil

	case NewBtree:
		var b3o BtreeOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &b3o); err != nil {
			return C.CString(fmt.Sprintf("error Unmarshal BtreeOptions, details: %v", err))
		}
		log.Info(fmt.Sprintf("BtreeOptions: %v", b3o))
		log.Info(fmt.Sprintf("NewBtree IndexSpec: %s", b3o.IndexSpecification))

		// Validate Database exists
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		item, ok := transRegistry.GetItem(sop.UUID(b3o.TransactionID))
		if !ok {
			return C.CString(fmt.Sprintf("can't find Transaction %v", b3o.TransactionID.String()))
		}
		so := convertTo(&b3o)

		if b3o.IsPrimitiveKey {
			log.Debug(fmt.Sprintf("NewBtree %s, primitiveKey: %v", b3o.Name, b3o.IsPrimitiveKey))
			b3, err := jsondb.NewJsonBtree[any, any](ctx, db.Config(), *so, item.Transaction, nil)
			if err != nil {
				return C.CString(fmt.Sprintf("error creating Btree, details: %v", err))
			}
			b3id, _ := transRegistry.AddBtree(sop.UUID(b3o.TransactionID), b3)
			return C.CString(b3id.String())
		} else {
			log.Debug(fmt.Sprintf("NewBtree %s, primitiveKey: %v", b3o.Name, b3o.IsPrimitiveKey))
			b3, err := jsondb.NewJsonBtreeMapKey(ctx, db.Config(), *so, item.Transaction, b3o.IndexSpecification)
			if err != nil {
				return C.CString(fmt.Sprintf("error creating Btree, details: %v", err))
			}
			b3id, _ := transRegistry.AddBtree(sop.UUID(b3o.TransactionID), b3)
			return C.CString(b3id.String())
		}

	case OpenBtree:
		var b3o BtreeOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &b3o); err != nil {
			return C.CString(fmt.Sprintf("error Unmarshal BtreeOptions, details: %v", err))
		}
		log.Debug(fmt.Sprintf("BtreeOptions: %v", b3o))

		// Validate Database exists
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		item, ok := transRegistry.GetItem(sop.UUID(b3o.TransactionID))
		if !ok {
			return C.CString(fmt.Sprintf("can't find Transaction %v", b3o.TransactionID.String()))
		}
		so := convertTo(&b3o)

		// Get StoreInfo from backend DB and determine if key is primitive or not.
		intf := item.Transaction.(interface{})
		t2 := intf.(*sop.SinglePhaseTransaction).SopPhaseCommitTransaction
		intf = t2
		t := intf.(*common.Transaction)

		sr := t.StoreRepository
		si, err := sr.Get(ctx, so.Name)
		isPrimitiveKey := false
		if err == nil && len(si) > 0 {
			isPrimitiveKey = si[0].IsPrimitiveKey
		} else if err == nil && len(si) == 0 {
			return C.CString(fmt.Sprintf("error opening Btree (%s), store not found", so.Name))
		}

		if isPrimitiveKey {
			b3, err := jsondb.OpenJsonBtree[any, any](ctx, db.Config(), so.Name, item.Transaction, nil)
			if err != nil {
				return C.CString(fmt.Sprintf("error opening Btree (%s), details: %v", so.Name, err))
			}
			ce := b3.GetStoreInfo().MapKeyIndexSpecification
			if ce != "" {
				errMsg := fmt.Sprintf("error opening for 'Primitive Type' Btree (%s), CELexpression %s is restricted for class type Key", so.Name, ce)
				log.Warn(errMsg)
				return C.CString(errMsg)
			}
			b3id, _ := transRegistry.AddBtree(sop.UUID(b3o.TransactionID), b3)
			return C.CString(b3id.String())
		} else {
			b3, err := jsondb.OpenJsonBtreeMapKey(ctx, db.Config(), so.Name, item.Transaction)
			if err != nil {
				return C.CString(fmt.Sprintf("error opening Btree (%s), details: %v", so.Name, err))
			}
			b3id, _ := transRegistry.AddBtree(sop.UUID(b3o.TransactionID), b3)
			return C.CString(b3id.String())
		}

	case OpenModelStore:
		var opts ModelStoreOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid options: %v", err))
		}

		// targetID is the DB UUID
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		transUUID, err := sop.ParseUUID(opts.TransactionID)
		if err != nil {
			return C.CString("Invalid transaction UUID")
		}

		item, ok := transRegistry.GetItem(transUUID)
		if !ok {
			return C.CString("Transaction not found")
		}

		if opts.Name == "" && opts.Path != "" {
			opts.Name = opts.Path
		}

		store, err := db.OpenModelStore(ctx, opts.Name, item.Transaction)
		if err != nil {
			return C.CString(err.Error())
		}

		id, err := transRegistry.AddBtree(transUUID, store)
		if err != nil {
			return C.CString(err.Error())
		}
		if id.IsNil() {
			return C.CString("Transaction not found during registration")
		}

		return C.CString(id.String())

	case OpenVectorStore:
		var opts VectorStoreTransportOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid options: %v", err))
		}
		log.Debug(fmt.Sprintf("OpenVectorStore StoragePath='%s' Payload='%s'", opts.StoragePath, jsonPayload))

		// targetID is the DB UUID
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		transUUID, err := sop.ParseUUID(opts.TransactionID)
		if err != nil {
			return C.CString("Invalid transaction UUID")
		}

		item, ok := transRegistry.GetItem(transUUID)
		if !ok {
			return C.CString("Transaction not found")
		}

		cfg := vector.Config{
			UsageMode:   ai.UsageMode(opts.Config.UsageMode),
			ContentSize: sop.ValueDataSize(opts.Config.ContentSize),
		}
		if opts.StoragePath != "" {
			cfg.TransactionOptions.StoresFolders = []string{opts.StoragePath}
		}

		store, err := db.OpenVectorStore(ctx, opts.Name, item.Transaction, cfg)
		if err != nil {
			return C.CString(err.Error())
		}

		id, err := transRegistry.AddBtree(transUUID, store)
		if err != nil {
			return C.CString(err.Error())
		}
		if id.IsNil() {
			return C.CString("Transaction not found during registration")
		}

		return C.CString(id.String())

	case OpenSearch:
		var opts SearchOptions
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid options: %v", err))
		}

		// targetID is the DB UUID
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		transUUID, err := sop.ParseUUID(opts.TransactionID)
		if err != nil {
			return C.CString("Invalid transaction UUID")
		}

		item, ok := transRegistry.GetItem(transUUID)
		if !ok {
			return C.CString("Transaction not found")
		}

		store, err := db.OpenSearch(ctx, opts.Name, item.Transaction)
		if err != nil {
			return C.CString(err.Error())
		}

		id, err := transRegistry.AddBtree(transUUID, store)
		if err != nil {
			return C.CString(err.Error())
		}
		if id.IsNil() {
			return C.CString("Transaction not found during registration")
		}

		return C.CString(id.String())

	case RemoveBtree:
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		btreeName := jsonPayload
		if err := sopdb.RemoveBtree(ctx, db.Config(), btreeName); err != nil {
			return C.CString(err.Error())
		}
		return nil

	case RemoveModelStore:
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		name := jsonPayload
		if err := db.RemoveModelStore(ctx, name); err != nil {
			return C.CString(err.Error())
		}
		return nil

	case RemoveVectorStore:
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		name := jsonPayload
		if err := db.RemoveVectorStore(ctx, name); err != nil {
			return C.CString(err.Error())
		}
		return nil

	case RemoveSearch:
		targetUUID, err := sop.ParseUUID(targetIDStr)
		if err != nil {
			return C.CString(fmt.Sprintf("invalid database UUID: %v", err))
		}
		db, ok := dbRegistry.Get(targetUUID)
		if !ok {
			return C.CString("Database not found")
		}

		name := jsonPayload
		if err := db.RemoveSearch(ctx, name); err != nil {
			return C.CString(err.Error())
		}
		return nil
	}
	return nil
}

// Some B-tree related artifacts.

// BtreeOptions is used to package the Btree StoreInfo.
type BtreeOptions struct {
	Name                         string               `json:"name" minLength:"1" maxLength:"128"`
	SlotLength                   int                  `json:"slot_length" min:"2" max:"20000"`
	IsUnique                     bool                 `json:"is_unique"`
	Description                  string               `json:"description" maxLength:"1000"`
	IsValueDataInNodeSegment     bool                 `json:"is_value_data_in_node_segment"`
	IsValueDataActivelyPersisted bool                 `json:"is_value_data_actively_persisted"`
	IsValueDataGloballyCached    bool                 `json:"is_value_data_globally_cached"`
	LeafLoadBalancing            bool                 `json:"leaf_load_balancing"`
	CacheConfig                  sop.StoreCacheConfig `json:"cache_config"`

	IndexSpecification string    `json:"index_specification"`
	CELexpression      string    `json:"cel_expression"`
	TransactionID      uuid.UUID `json:"transaction_id"`
	IsPrimitiveKey     bool      `json:"is_primitive_key"`
}

// Extract from StoreInfo. StoreInfo has annotations useful for JSON serialization, thus, it can get used
// packaging from compatible client's JSON, could be written on other languages.
func convertTo(si *BtreeOptions) *sop.StoreOptions {
	so := sop.StoreOptions{}
	so.Name = si.Name
	so.SlotLength = si.SlotLength
	so.IsUnique = si.IsUnique
	so.IsValueDataActivelyPersisted = si.IsValueDataActivelyPersisted
	so.IsValueDataGloballyCached = si.IsValueDataGloballyCached
	so.IsValueDataInNodeSegment = si.IsValueDataInNodeSegment
	so.LeafLoadBalancing = si.LeafLoadBalancing
	so.Description = si.Description
	so.CacheConfig = &si.CacheConfig
	// Adjust the Durations from "minute" unit into proper time.Duration values.
	so.CacheConfig.NodeCacheDuration = so.CacheConfig.NodeCacheDuration * time.Minute
	so.CacheConfig.RegistryCacheDuration = so.CacheConfig.RegistryCacheDuration * time.Minute
	so.CacheConfig.StoreInfoCacheDuration = so.CacheConfig.StoreInfoCacheDuration * time.Minute
	so.CacheConfig.ValueDataCacheDuration = so.CacheConfig.ValueDataCacheDuration * time.Minute
	// IndexSpecification carries either the MapKey IndexSpecification JSON or a CEL expression.
	// It must populate MapKeyIndexSpecification; CELexpression is handled by jsondb.NewJsonBtreeMapKey
	// when it detects the string is a CEL expression.
	so.MapKeyIndexSpecification = si.IndexSpecification
	so.CELexpression = si.CELexpression
	so.IsPrimitiveKey = si.IsPrimitiveKey
	return &so
}
func (bo *BtreeOptions) extract(si *sop.StoreInfo) {
	bo.Name = si.Name
	bo.SlotLength = si.SlotLength
	bo.IsUnique = si.IsUnique
	bo.IsValueDataActivelyPersisted = si.IsValueDataActivelyPersisted
	bo.IsValueDataGloballyCached = si.IsValueDataGloballyCached
	bo.IsValueDataInNodeSegment = si.IsValueDataInNodeSegment
	bo.LeafLoadBalancing = si.LeafLoadBalancing
	bo.Description = si.Description
	bo.CacheConfig = si.CacheConfig
	bo.CELexpression = si.CELexpression
	bo.IndexSpecification = si.MapKeyIndexSpecification
	bo.IsPrimitiveKey = si.IsPrimitiveKey
	// Restore back to "minute" unit.
	bo.CacheConfig.NodeCacheDuration = si.CacheConfig.NodeCacheDuration / time.Minute
	bo.CacheConfig.RegistryCacheDuration = si.CacheConfig.RegistryCacheDuration / time.Minute
	bo.CacheConfig.StoreInfoCacheDuration = si.CacheConfig.StoreInfoCacheDuration / time.Minute
	bo.CacheConfig.ValueDataCacheDuration = si.CacheConfig.ValueDataCacheDuration / time.Minute
}

//export freeString
func freeString(cString *C.char) {
	if cString != nil {
		C.free(unsafe.Pointer(cString))
	}
}

func main() {
	// main function is required for building a shared library, but can be empty
}
