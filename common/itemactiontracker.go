package common

import (
	"context"
	"fmt"
	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/encoding"
)

type actionType int

const (
	defaultAction = iota
	getAction
	addAction
	updateAction
	removeAction
)

type lockRecord struct {
	LockID sop.UUID
	Action actionType
}
type cacheItem[TK btree.Ordered, TV any] struct {
	lockRecord
	item *btree.Item[TK, TV]
	// Version of the item as read from DB.
	versionInDB int32
	isLockOwner bool
	persisted   bool
}

type itemActionTracker[TK btree.Ordered, TV any] struct {
	storeInfo        *sop.StoreInfo
	items            map[sop.UUID]cacheItem[TK, TV]
	forDeletionItems []sop.UUID
	cache            sop.L2Cache
	blobStore        sop.BlobStore
	tlogger          *transactionLog
}

// Creates a new Item Action Tracker instance with frontend and backend interface/methods.
func newItemActionTracker[TK btree.Ordered, TV any](storeInfo *sop.StoreInfo, redisCache sop.L2Cache, blobStore sop.BlobStore, tl *transactionLog) *itemActionTracker[TK, TV] {
	return &itemActionTracker[TK, TV]{
		storeInfo: storeInfo,
		items:     make(map[sop.UUID]cacheItem[TK, TV], 10),
		cache:     redisCache,
		blobStore: blobStore,
		tlogger:   tl,
	}
}

// Sample use-case logic table:
// Current		Action		Outcome
// _			Add			ForAdd
// _			Get			Get(fetch from blobStore)
// _			Update		ForUpdate
// _			Remove		ForRemove
// ForAdd		Get			ForAdd
// ForAdd		Update		ForAdd
// ForAdd		Remove		_
// ForRemove 	Remove		ForRemove
// ForRemove 	Get			ForRemove
// ForUpdate	Remove		ForRemove
// Get			Get			Get
// Get			Remove		ForRemove
// Get			Update		ForUpdate

func (t *itemActionTracker[TK, TV]) Get(ctx context.Context, item *btree.Item[TK, TV]) error {
	val, ok := t.items[item.ID]
	if !ok || val.item == nil || val.item.ValueNeedsFetch {
		if item.Value == nil && item.ValueNeedsFetch {
			var v TV
			if t.storeInfo.IsValueDataGloballyCached {
				var err error
				var found bool
				if t.storeInfo.CacheConfig.IsValueDataCacheTTL {
					found, err = t.cache.GetStructEx(ctx, formatItemKey(item.ID.String()), &v, t.storeInfo.CacheConfig.ValueDataCacheDuration)
				} else {
					found, err = t.cache.GetStruct(ctx, formatItemKey(item.ID.String()), &v)
				}
				if err != nil {
					log.Warn(err.Error())
				}
				if !found || err != nil {
					// If item not found in Redis or an error fetching it, fetch from Blob store.
					var ba []byte
					if ba, err = t.blobStore.GetOne(ctx, t.storeInfo.BlobTable, item.ID); err != nil {
						return err
					}
					err = encoding.Unmarshal[TV](ba, &v)
					if err != nil {
						return err
					}

					// Just log Redis error since it is just secondary.
					if err := t.cache.SetStruct(ctx, formatItemKey(item.ID.String()), &v, t.storeInfo.CacheConfig.ValueDataCacheDuration); err != nil {
						log.Warn(err.Error())
					}
				}
			} else {
				var ba []byte
				var err error
				if ba, err = t.blobStore.GetOne(ctx, t.storeInfo.BlobTable, item.ID); err != nil {
					return err
				}
				err = encoding.Unmarshal[TV](ba, &v)
				if err != nil {
					return err
				}
			}
			item.Value = &v
			item.ValueNeedsFetch = false
			if ok {
				return nil
			}
		}
		t.items[item.ID] = cacheItem[TK, TV]{
			lockRecord: lockRecord{
				LockID: sop.NewUUID(),
				Action: getAction,
			},
			item:        item,
			versionInDB: item.Version,
		}
	}
	return nil
}

func (t *itemActionTracker[TK, TV]) Add(ctx context.Context, item *btree.Item[TK, TV]) error {
	cachedItem := cacheItem[TK, TV]{
		lockRecord: lockRecord{
			LockID: sop.NewUUID(),
			Action: addAction,
		},
		item:        item,
		versionInDB: item.Version,
	}
	t.items[item.ID] = cachedItem
	// Update upsert time, now that we have kept its DB value intact, for use in conflict resolution.
	item.Version++

	if t.storeInfo.IsValueDataActivelyPersisted {
		// Actively persist the item.
		itemsForAdd := sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{
			BlobTable: t.storeInfo.BlobTable,
			Blobs:     make([]sop.KeyValuePair[sop.UUID, []byte], 0, 1),
		}
		iv := item.Value
		itemForAdd, err := t.manage(item.ID, cachedItem)
		if err != nil {
			return err
		}
		if itemForAdd != nil {
			itemsForAdd.Blobs = append(itemsForAdd.Blobs, *itemForAdd)
		}
		if len(itemsForAdd.Blobs) > 0 {
			// Log so on crash it can get cleaned up.
			if err := t.tlogger.log(ctx, addActivelyPersistedItem, extractRequestPayloadIDs(&itemsForAdd)); err != nil {
				return err
			}
			if err := t.blobStore.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{itemsForAdd}); err != nil {
				return err
			}
			if t.storeInfo.IsValueDataGloballyCached {
				if err := t.cache.SetStruct(ctx, formatItemKey(itemForAdd.Key.String()), iv, t.storeInfo.CacheConfig.ValueDataCacheDuration); err != nil {
					log.Warn(err.Error())
				}
			}
		}
	}

	return nil
}

func (t *itemActionTracker[TK, TV]) Update(ctx context.Context, item *btree.Item[TK, TV]) error {
	v, ok := t.items[item.ID]

	activelyPersist := func(v cacheItem[TK, TV]) error {
		if t.storeInfo.IsValueDataActivelyPersisted {
			// Actively persist the item.
			itemsForAdd := sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{
				BlobTable: t.storeInfo.BlobTable,
				Blobs:     make([]sop.KeyValuePair[sop.UUID, []byte], 0, 1),
			}
			iv := v.item.Value
			itemForAdd, err := t.manage(item.ID, v)
			if err != nil {
				return err
			}
			if itemForAdd != nil {
				itemsForAdd.Blobs = append(itemsForAdd.Blobs, *itemForAdd)
			}
			if len(itemsForAdd.Blobs) > 0 {
				// Log so on crash it can get cleaned up.
				if err := t.tlogger.log(ctx, updateActivelyPersistedItem, extractRequestPayloadIDs(&itemsForAdd)); err != nil {
					return err
				}
				if err := t.blobStore.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{itemsForAdd}); err != nil {
					return err
				}
				if t.storeInfo.IsValueDataGloballyCached {
					if err := t.cache.SetStruct(ctx, formatItemKey(itemForAdd.Key.String()), iv, t.storeInfo.CacheConfig.ValueDataCacheDuration); err != nil {
						log.Warn(err.Error())
					}
				}
			}
		}
		return nil
	}

	if ok {
		if v.Action == addAction {
			return activelyPersist(v)
		}
		v.lockRecord.Action = updateAction
		v.item = item
		t.items[item.ID] = v
		if item.Version == v.versionInDB {
			item.Version++
		}
		return activelyPersist(v)
	}

	v = cacheItem[TK, TV]{
		lockRecord: lockRecord{
			LockID: sop.NewUUID(),
			Action: updateAction,
		},
		item:        item,
		versionInDB: item.Version,
	}
	t.items[item.ID] = v
	// Up the version # since item got updated.
	item.Version++
	return activelyPersist(v)
}

func (t *itemActionTracker[TK, TV]) Remove(ctx context.Context, item *btree.Item[TK, TV]) error {
	if t.storeInfo.IsValueDataActivelyPersisted {
		t.forDeletionItems = append(t.forDeletionItems, item.ID)
		item.ValueNeedsFetch = false
		return nil
	}

	if v, ok := t.items[item.ID]; ok && v.Action == addAction {
		delete(t.items, item.ID)
		return nil
	}
	t.items[item.ID] = cacheItem[TK, TV]{
		lockRecord: lockRecord{
			LockID: sop.NewUUID(),
			Action: removeAction,
		},
		item:        item,
		versionInDB: item.Version,
	}
	return nil
}

func extractRequestPayloadIDs(payload *sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) []byte {
	var r sop.BlobsPayload[sop.UUID]
	r.BlobTable = payload.BlobTable
	r.Blobs = make([]sop.UUID, len(payload.Blobs))
	for i := range payload.Blobs {
		r.Blobs[i] = payload.Blobs[i].Key
	}
	return toByteArray(r)
}

func (t *itemActionTracker[TK, TV]) hasTrackedItems() bool {
	return len(t.items) > 0
}

// checkTrackedItems for conflict so we can remove "race condition" caused issue.
// Returns nil if there are no tracked items or no conflict, otherwise returns an error.
func (t *itemActionTracker[TK, TV]) checkTrackedItems(ctx context.Context) error {
	var lastErr error
	var keys []string
	var targets []any
	var uuids []sop.UUID

	for uuid, cachedItem := range t.items {
		if cachedItem.Action == addAction {
			continue
		}
		uuids = append(uuids, uuid)
		keys = append(keys, t.cache.FormatLockKey(uuid.String()))
		targets = append(targets, &lockRecord{})
	}

	if len(keys) == 0 {
		return nil
	}

	found, err := t.cache.GetStructs(ctx, keys, targets, 0)
	if err != nil {
		return err
	}

	for i, uuid := range uuids {
		cachedItem := t.items[uuid]
		if !found[i] {
			cachedItem.isLockOwner = false
			t.items[uuid] = cachedItem
			continue
		}
		readItem := *(targets[i].(*lockRecord))
		// Item found in Redis.
		if readItem.LockID == cachedItem.LockID {
			cachedItem.isLockOwner = true
			t.items[uuid] = cachedItem
			continue
		}
		// Lock compatibility check.
		if readItem.Action == getAction && cachedItem.Action == getAction {
			continue
		}
		cachedItem.isLockOwner = false
		t.items[uuid] = cachedItem
		lastErr = fmt.Errorf("lock(item: %v) call detected conflict", uuid.String())
	}
	return lastErr
}

// lock the tracked items in Redis in preparation to finalize the transaction commit.
// This should work in combination of optimistic locking.
func (t *itemActionTracker[TK, TV]) lock(ctx context.Context, duration time.Duration) error {
	var keys []string
	var uuids []sop.UUID
	for uuid, cachedItem := range t.items {
		if cachedItem.Action == addAction {
			continue
		}
		keys = append(keys, t.cache.FormatLockKey(uuid.String()))
		uuids = append(uuids, uuid)
	}
	if len(keys) == 0 {
		return nil
	}
	targets := make([]any, len(keys))
	for i := range targets {
		targets[i] = &lockRecord{}
	}
	found, err := t.cache.GetStructs(ctx, keys, targets, 0)
	if err != nil {
		return err
	}

	var verifyKeys []string
	var verifyUUIDs []sop.UUID
	var verifyTargets []any
	var setKeys []string
	var setValues []any

	for i, uuid := range uuids {
		cachedItem := t.items[uuid]
		if found[i] {
			readItem := *(targets[i].(*lockRecord))
			if readItem.LockID == cachedItem.LockID {
				continue
			}
			if readItem.Action == getAction && cachedItem.Action == getAction {
				continue
			}
			return fmt.Errorf("lock(item: %v) call detected conflict", uuid.String())
		}
		// Item does not exist, upsert it.
		setKeys = append(setKeys, keys[i])
		rec := cachedItem.lockRecord
		setValues = append(setValues, &rec)

		verifyKeys = append(verifyKeys, keys[i])
		verifyUUIDs = append(verifyUUIDs, uuid)
		verifyTargets = append(verifyTargets, &lockRecord{})
	}

	if len(setKeys) > 0 {
		if err := t.cache.SetStructs(ctx, setKeys, setValues, duration); err != nil {
			return err
		}
	}

	if len(verifyKeys) == 0 {
		return nil
	}

	// Use a 2nd "get" to ensure we "won" the lock attempt & fail if not.
	foundVerify, err := t.cache.GetStructs(ctx, verifyKeys, verifyTargets, 0)
	if err != nil {
		return err
	}

	for i, uuid := range verifyUUIDs {
		cachedItem := t.items[uuid]
		readItem := *(verifyTargets[i].(*lockRecord))
		if !foundVerify[i] {
			return fmt.Errorf("lock(item: %v) call can't attain a lock in Redis", uuid.String())
		}
		if readItem.LockID != cachedItem.LockID {
			if readItem.Action == getAction && cachedItem.Action == getAction {
				continue
			}
			if readItem.LockID.IsNil() {
				return fmt.Errorf("lock(item: %v) call can't attain a lock in Redis", uuid.String())
			}
			return fmt.Errorf("lock(item: %v) call detected conflict", uuid.String())
		}
		// We got the item locked, ensure we can unlock it.
		cachedItem.isLockOwner = true
		t.items[uuid] = cachedItem
	}
	return nil
}

// unlock will attempt to unlock or delete all tracked items from redis.
func (t *itemActionTracker[TK, TV]) unlock(ctx context.Context) error {
	keys := make([]string, 0, len(t.items))
	for uuid, cachedItem := range t.items {
		if cachedItem.Action == addAction {
			continue
		}
		if !cachedItem.isLockOwner {
			continue
		}
		keys = append(keys, t.cache.FormatLockKey(uuid.String()))
	}
	if len(keys) == 0 {
		return nil
	}
	if _, err := t.cache.Delete(ctx, keys); err != nil {
		return err
	}
	return nil
}
