package cache

// import (
// 	"context"
// 	"fmt"
// 	log "log/slog"
// 	"testing"
// 	"time"

// 	"github.com/sharedcode/joltrin/v5"
// 	"github.com/sharedcode/joltrin/v5/btree"
// 	"github.com/sharedcode/joltrin/v5/redis"
// )

// // Redis config.
// var redisConfig = redis.Options{
// 	Address:  "localhost:6379",
// 	Password: "", // no password set
// 	DB:       0,  // use default DB
// }

// func init() {
// 	if _, err := redis.OpenConnection(redisConfig); err != nil {
// 		log.Error(err.Error())
// 	}
// }

// var uuid, _ = sop.ParseUUID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// func TestBasicUse(t *testing.T) {
// 	ctx := context.Background()
// 	cache := GetGlobalCache()
// 	if err := cache.SetHandles(ctx, []sop.RegistryPayload[sop.Handle]{
// 		{
// 			RegistryTable: "foo",
// 			IDs: []sop.Handle{
// 				{
// 					LogicalID: uuid,
// 				},
// 			},
// 		},
// 	}, ); err != nil {
// 		t.Error(err)
// 		t.FailNow()
// 	}
// 	var hs []sop.RegistryPayload[sop.Handle]
// 	var err error
// 	if hs, err = cache.GetHandles(ctx, []sop.RegistryPayload[sop.UUID]{
// 		{
// 			IDs: []sop.UUID{
// 				uuid,
// 			},
// 		},
// 	}); err != nil {
// 		t.Error(err)
// 		t.FailNow()
// 	}
// 	if len(hs) == 0 || hs[0].IDs[0].LogicalID != uuid {
// 		t.Error(fmt.Errorf("got %v, expected %v", hs[0].IDs[0].LogicalID, uuid))
// 		t.FailNow()
// 	}

// 	if err = cache.SetNode(ctx, uuid, sop.NilUUID, btree.Node[int, string]{
// 		ID: uuid,
// 		Version: 23,
// 	}, time.Duration(5 * time.Second)); err != nil {
// 		t.Error(err)
// 		t.FailNow()
// 	}

// 	var targetNode btree.Node[int, string]
// 	var n any
// 	if _, n, err = cache.GetNode(ctx, sop.RegistryPayload[sop.UUID]{
// 		IDs: []sop.UUID{
// 			uuid,
// 		},
// 		CacheDuration: 5*time.Second,
// 	}, true, time.Duration(5*time.Second), &targetNode); err != nil {
// 		t.Error(err)
// 		t.FailNow()
// 	}

// 	if n == nil {
// 		t.Error(fmt.Errorf("did not get Node with UUID %v", uuid))
// 		t.FailNow()
// 	}
// 	if n2, ok := n.(btree.Node[int, string]); ok {
// 		if n2.ID != uuid {
// 			t.Error(fmt.Errorf("got %v, expected %v", n2.ID, uuid))
// 			t.FailNow()
// 		}
// 	}
// }

// func TestMruPruning(t *testing.T) {
// 	ctx := context.Background()
// 	cache := GetGlobalCache()

// 	firstTen := make([]sop.UUID, 10)
// 	var lastUUID sop.UUID

// 	for i := range DefaultMaxCapacity {
// 		uuid := sop.NewUUID()
// 		lastUUID = uuid
// 		if i < len(firstTen) {
// 			firstTen[i] = uuid
// 		}
// 		if err := cache.SetHandles(ctx, []sop.RegistryPayload[sop.Handle]{
// 			{
// 				RegistryTable: fmt.Sprintf("foo%v", i),
// 				IDs: []sop.Handle{
// 					{
// 						LogicalID: uuid,
// 					},
// 				},
// 			},
// 		}, ); err != nil {
// 			t.Error(err)
// 			t.FailNow()
// 		}
// 	}

// 	for i := range 10 {
// 		if err := cache.SetHandles(ctx, []sop.RegistryPayload[sop.Handle]{
// 			{
// 				RegistryTable: fmt.Sprintf("foo%v", DefaultMaxCapacity + 1 + i),
// 				IDs: []sop.Handle{
// 					{
// 						LogicalID: sop.NewUUID(),
// 					},
// 				},
// 			},
// 		}, ); err != nil {
// 			t.Error(err)
// 			t.FailNow()
// 		}
// 	}

// 	if cache.Count() != DefaultMaxCapacity {
// 		t.Error(fmt.Errorf("MRU count: got %v, expected %v", cache.Count(), DefaultMaxCapacity))
// 		t.FailNow()
// 	}

// 	// Tell L1 cache to only fetch from local MRU cache, if not found, don't fetch from L2 cache.
// 	getFromMRUOnly = true

// 	for _, uuid := range firstTen {
// 		if h, err := cache.GetHandleByID(ctx, uuid); err != nil {
// 			t.Error(err)
// 		} else if h.LogicalID == uuid {
// 			t.Error(fmt.Errorf("MRU pruning failed, got %v, expected nil", h.LogicalID))
// 			t.FailNow()
// 		}
// 	}

// 	if h, err := cache.GetHandleByID(ctx, lastUUID); err != nil {
// 		t.Error(err)
// 	} else if h.LogicalID != lastUUID {
// 		t.Error(fmt.Errorf("MRU pruning failed, got %v, expected %v", h.LogicalID, lastUUID))
// 		t.FailNow()
// 	}
// }
