// Package cassandra contains code for integration or inter-operation with SOP's Cassandra DB.
// This package manages contents in tables like Registry, StoreRepository, and Transaction Log.
package cassandra

import (
	"context"
	"fmt"
	log "log/slog"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
)

// registry implements sop.Registry using Cassandra as the source of truth and
// coordinates an in-process L1 cache with an external L2 cache (Redis).
type registry struct {
	connection *Connection
	l2Cache    sop.L2Cache
	l1Cache    *cache.L1Cache
}

// updateAllOrNothingOfHandleSetLockTimeout is the TTL used for cache-based conflict checks during updates.
const updateAllOrNothingOfHandleSetLockTimeout = time.Duration(10 * time.Minute)

// NewRegistry returns a Cassandra-backed implementation of sop.Registry.
func NewRegistry(customConnection *Connection, l2Cache sop.L2Cache) sop.Registry {
	return &registry{
		connection: customConnection,
		l2Cache:    l2Cache,
		l1Cache:    cache.GetGlobalL1Cache(l2Cache),
	}
}

func (v *registry) getConnection() (*Connection, error) {
	if v.connection != nil {
		return v.connection, nil
	}
	return GetGlobalConnection()
}

// Add inserts new handle records into Cassandra and updates L1/L2 caches best-effort.
func (v *registry) Add(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	conn, err := v.getConnection()
	if err != nil {
		return err
	}
	for _, sh := range storesHandles {
		insertStatement := fmt.Sprintf("INSERT INTO %s.%s (lid, is_idb, p_ida, p_idb, ver, wip_ts, is_del) VALUES(?,?,?,?,?,?,?);",
			conn.Config.Keyspace, sh.RegistryTable)
		for _, h := range sh.IDs {

			qry := conn.Session.Query(insertStatement, gocql.UUID(h.LogicalID), h.IsActiveIDB, gocql.UUID(h.PhysicalIDA),
				gocql.UUID(h.PhysicalIDB), h.Version, h.WorkInProgressTimestamp, h.IsDeleted).WithContext(ctx)
			if conn.Config.ConsistencyBook.RegistryAdd > gocql.Any {
				qry.Consistency(conn.Config.ConsistencyBook.RegistryAdd)
			}

			// Add a new store record.
			if err := qry.Exec(); err != nil {
				return fmt.Errorf("cassandra registry add failed: %w", err)
			}
			// Update in-process cache.
			v.l1Cache.Handles.Set(convertToKvp([]sop.Handle{h}))
			// Tolerate Redis cache failure.
			if err := v.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
				log.Warn("Registry Add (redis setstruct) failed", "error", err)
			}
		}
	}
	return nil
}

// Update updates per-handle records with per-key logical locks to reduce conflicts. On success, caches are synced.
func (v *registry) Update(ctx context.Context, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	conn, err := v.getConnection()
	if err != nil {
		return err
	}

	for _, sh := range storesHandles {
		updateStatement := fmt.Sprintf("UPDATE %s.%s SET is_idb = ?, p_ida = ?, p_idb = ?, ver = ?, wip_ts = ?, is_del = ? WHERE lid = ?;",
			conn.Config.Keyspace, sh.RegistryTable)
		// Fail on 1st encountered error. It is non-critical operation, SOP can "heal" those got left.
		for _, h := range sh.IDs {
			// Update registry record.
			lk := v.l2Cache.CreateLockKeys([]string{h.LogicalID.String()})
			if ok, _, err := v.l2Cache.DualLock(ctx, updateAllOrNothingOfHandleSetLockTimeout, lk); !ok || err != nil {
				if err == nil {
					err = fmt.Errorf("lock failed, key %v is already locked by another", lk[0].Key)
				}
				return fmt.Errorf("cassandra registry update lock failed: %w", err)
			}

			qry := conn.Session.Query(updateStatement, h.IsActiveIDB, gocql.UUID(h.PhysicalIDA), gocql.UUID(h.PhysicalIDB),
				h.Version, h.WorkInProgressTimestamp, h.IsDeleted, gocql.UUID(h.LogicalID)).WithContext(ctx)
			if conn.Config.ConsistencyBook.RegistryUpdate > gocql.Any {
				qry.Consistency(conn.Config.ConsistencyBook.RegistryUpdate)
			}

			// Update registry record.
			if err := qry.Exec(); err != nil {
				v.l1Cache.Handles.Delete([]sop.UUID{h.LogicalID})
				v.l2Cache.Delete(ctx, []string{h.LogicalID.String()})
				// Unlock the object Keys before return.
				v.l2Cache.Unlock(ctx, lk)
				return fmt.Errorf("cassandra registry update failed: %w", err)
			}

			// Update Redis to sync it since storage update succeeded.
			if err := v.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
				log.Warn("Registry Update (redis setstruct) failed", "error", err)
			}

			// Unlock the object Keys.
			if err := v.l2Cache.Unlock(ctx, lk); err != nil {
				return fmt.Errorf("cassandra registry update unlock failed: %w", err)
			}
		}
		v.l1Cache.Handles.Set(convertToKvp(sh.IDs))
	}
	return nil
}

// UpdateNoLocks updates records without acquiring per-key locks. When allOrNothing is true, a logged batch is used.
// In all cases, L1/L2 caches are refreshed on success, but Redis errors are tolerated.
func (v *registry) UpdateNoLocks(ctx context.Context, allOrNothing bool, storesHandles []sop.RegistryPayload[sop.Handle]) error {
	conn, err := v.getConnection()
	if err != nil {
		return err
	}

	if allOrNothing {
		// Do the actual batch logged transaction update in Cassandra.
		batch := conn.Session.NewBatch(gocql.LoggedBatch).WithContext(ctx)
		if conn.Config.ConsistencyBook.RegistryUpdate > gocql.Any {
			batch.SetConsistency(conn.Config.ConsistencyBook.RegistryUpdate)
		}

		for _, sh := range storesHandles {
			updateStatement := fmt.Sprintf("UPDATE %s.%s SET is_idb = ?, p_ida = ?, p_idb = ?, ver = ?, wip_ts = ?, is_del = ? WHERE lid = ?;",
				conn.Config.Keyspace, sh.RegistryTable)
			for _, h := range sh.IDs {
				// Enqueue update registry record cmd.
				batch.Query(updateStatement, h.IsActiveIDB, gocql.UUID(h.PhysicalIDA), gocql.UUID(h.PhysicalIDB),
					h.Version, h.WorkInProgressTimestamp, h.IsDeleted, gocql.UUID(h.LogicalID))
			}
		}

		// Execute the batch query, all or nothing.
		if err := conn.Session.ExecuteBatch(batch); err != nil {
			// Failed update all, thus, return err to cause rollback.
			return fmt.Errorf("cassandra registry update no locks (batch) failed: %w", err)
		}

		// Update redis cache.
		for _, sh := range storesHandles {
			for _, h := range sh.IDs {
				if err := v.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
					log.Warn("Registry Update (redis setstruct) failed", "error", err)
				}
				v.l1Cache.Handles.Set(convertToKvp([]sop.Handle{h}))
			}
		}
	} else {
		for _, sh := range storesHandles {
			updateStatement := fmt.Sprintf("UPDATE %s.%s SET is_idb = ?, p_ida = ?, p_idb = ?, ver = ?, wip_ts = ?, is_del = ? WHERE lid = ?;",
				conn.Config.Keyspace, sh.RegistryTable)
			// Fail on 1st encountered error. It is non-critical operation, SOP can "heal" those got left.
			for _, h := range sh.IDs {
				qry := conn.Session.Query(updateStatement, h.IsActiveIDB, gocql.UUID(h.PhysicalIDA), gocql.UUID(h.PhysicalIDB),
					h.Version, h.WorkInProgressTimestamp, h.IsDeleted, gocql.UUID(h.LogicalID)).WithContext(ctx)
				if conn.Config.ConsistencyBook.RegistryUpdate > gocql.Any {
					qry.Consistency(conn.Config.ConsistencyBook.RegistryUpdate)
				}

				// Update registry record.
				if err := qry.Exec(); err != nil {
					return fmt.Errorf("cassandra registry update no locks failed: %w", err)
				}

				if err := v.l2Cache.SetStruct(ctx, h.LogicalID.String(), &h, sh.CacheDuration); err != nil {
					log.Warn("Registry Update (redis setstruct) failed", "error", err)
				}
				v.l1Cache.Handles.Set(convertToKvp([]sop.Handle{h}))
			}
		}
	}

	return nil
}

// Get fetches handles by ID, checking Redis first (optionally extending TTL) and falling back to Cassandra on misses.
func (v *registry) Get(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) ([]sop.RegistryPayload[sop.Handle], error) {
	conn, err := v.getConnection()
	if err != nil {
		return nil, err
	}

	storesHandles := make([]sop.RegistryPayload[sop.Handle], 0, len(storesLids))
	for _, storeLids := range storesLids {
		handles := make([]sop.Handle, 0, len(storeLids.IDs))
		paramQ := make([]string, 0, len(storeLids.IDs))
		lidsAsIntfs := make([]interface{}, 0, len(storeLids.IDs))
		for i := range storeLids.IDs {
			h := sop.Handle{}
			var err error
			var found bool
			if storeLids.IsCacheTTL {
				found, err = v.l2Cache.GetStructEx(ctx, storeLids.IDs[i].String(), &h, storeLids.CacheDuration)
			} else {
				found, err = v.l2Cache.GetStruct(ctx, storeLids.IDs[i].String(), &h)
			}
			if err != nil {
				log.Warn("Registry Get (redis getstruct) failed", "error", err)
			}
			if !found || err != nil {
				paramQ = append(paramQ, "?")
				lidsAsIntfs = append(lidsAsIntfs, interface{}(gocql.UUID(storeLids.IDs[i])))
				continue
			}
			handles = append(handles, h)
		}

		if len(paramQ) == 0 {
			storesHandles = append(storesHandles, sop.RegistryPayload[sop.Handle]{
				RegistryTable: storeLids.RegistryTable,
				BlobTable:     storeLids.BlobTable,
				CacheDuration: storeLids.CacheDuration,
				IsCacheTTL:    storeLids.IsCacheTTL,
				IDs:           handles,
			})
			continue
		}
		selectStatement := fmt.Sprintf("SELECT lid, is_idb, p_ida, p_idb, ver, wip_ts, is_del FROM %s.%s WHERE lid in (%v);",
			conn.Config.Keyspace, storeLids.RegistryTable, strings.Join(paramQ, ", "))

		qry := conn.Session.Query(selectStatement, lidsAsIntfs...).WithContext(ctx)
		if conn.Config.ConsistencyBook.RegistryGet > gocql.Any {
			qry.Consistency(conn.Config.ConsistencyBook.RegistryGet)
		}

		iter := qry.Iter()
		handle := sop.Handle{}
		var lid, ida, idb gocql.UUID
		for iter.Scan(&lid, &handle.IsActiveIDB, &ida, &idb, &handle.Version, &handle.WorkInProgressTimestamp, &handle.IsDeleted) {
			handle.LogicalID = sop.UUID(lid)
			handle.PhysicalIDA = sop.UUID(ida)
			handle.PhysicalIDB = sop.UUID(idb)
			handles = append(handles, handle)

			if err := v.l2Cache.SetStruct(ctx, handle.LogicalID.String(), &handle, storeLids.CacheDuration); err != nil {
				log.Warn("Registry Set (redis setstruct) failed", "error", err)
			}
			handle = sop.Handle{}
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("cassandra registry get failed: %w", err)
		}
		storesHandles = append(storesHandles, sop.RegistryPayload[sop.Handle]{
			RegistryTable: storeLids.RegistryTable,
			BlobTable:     storeLids.BlobTable,
			CacheDuration: storeLids.CacheDuration,
			IsCacheTTL:    storeLids.IsCacheTTL,
			IDs:           handles,
		})
	}
	return storesHandles, nil
}

// Remove deletes handle records from Cassandra and evicts affected entries from caches.
func (v *registry) Remove(ctx context.Context, storesLids []sop.RegistryPayload[sop.UUID]) error {
	conn, err := v.getConnection()
	if err != nil {
		return err
	}

	for _, storeLids := range storesLids {
		paramQ := make([]string, len(storeLids.IDs))
		lidsAsIntfs := make([]interface{}, len(storeLids.IDs))
		for i := range storeLids.IDs {
			paramQ[i] = "?"
			lidsAsIntfs[i] = interface{}(gocql.UUID(storeLids.IDs[i]))
		}
		deleteStatement := fmt.Sprintf("DELETE FROM %s.%s WHERE lid in (%v);",
			conn.Config.Keyspace, storeLids.RegistryTable, strings.Join(paramQ, ", "))

		// Flush out the failing records from cache.
		deleteFromCache := func(storeLids sop.RegistryPayload[sop.UUID]) {
			for _, id := range storeLids.IDs {
				if _, err := v.l2Cache.Delete(ctx, []string{id.String()}); err != nil {
					log.Warn("Registry Delete (redis delete) failed", "error", err)
				}
			}
		}

		qry := conn.Session.Query(deleteStatement, lidsAsIntfs...).WithContext(ctx)
		if conn.Config.ConsistencyBook.RegistryRemove > gocql.Any {
			qry.Consistency(conn.Config.ConsistencyBook.RegistryRemove)
		}

		v.l1Cache.Handles.Delete(storeLids.IDs)
		if err := qry.Exec(); err != nil {
			deleteFromCache(storeLids)
			return fmt.Errorf("cassandra registry remove failed: %w", err)
		}
		deleteFromCache(storeLids)
	}
	return nil
}

// Replicate is a no-op because Cassandra provides its own replication.
func (v *registry) Replicate(ctx context.Context, newRootNodeHandles, addedNodeHandles, updatedNodeHandles, removedNodeHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}

// convertToKvp converts handles to KVP slices for cache writes.
func convertToKvp(handles []sop.Handle) []sop.KeyValuePair[sop.UUID, sop.Handle] {
	items := make([]sop.KeyValuePair[sop.UUID, sop.Handle], len(handles))
	for i := range handles {
		items[i] = sop.KeyValuePair[sop.UUID, sop.Handle]{Key: handles[i].LogicalID, Value: handles[i]}
	}
	return items
}
