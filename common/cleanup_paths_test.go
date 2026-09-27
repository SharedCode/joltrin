package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_Transaction_GetToBeObsoleteEntries(t *testing.T) {
	ctx := context.Background()
	_ = ctx

	// Build a transaction with only fields used by getToBeObsoleteEntries
	tx := &Transaction{}

	// Updated node handle with inactive ID set (B), active A
	lidUpd := sop.NewUUID()
	inactiveID := sop.NewUUID()
	hUpd := sop.NewHandle(lidUpd)
	hUpd.PhysicalIDB = inactiveID
	hUpd.IsActiveIDB = false // active A, inactive B
	upd := []sop.RegistryPayload[sop.Handle]{
		{RegistryTable: "rt_upd", BlobTable: "bt_upd", IDs: []sop.Handle{hUpd}},
	}

	// Removed node handle; active ID will be used as unused blob
	lidRem := sop.NewUUID()
	activeID := sop.NewUUID()
	hRem := sop.NewHandle(lidRem)
	hRem.PhysicalIDA = activeID
	hRem.IsActiveIDB = false // active A
	rem := []sop.RegistryPayload[sop.Handle]{
		{RegistryTable: "rt_rem", BlobTable: "bt_rem", IDs: []sop.Handle{hRem}},
	}

	tx.updatedNodeHandles = upd
	tx.removedNodeHandles = rem

	out := tx.getToBeObsoleteEntries()
	// Deleted registry IDs should contain the logical ID from removed handles
	if len(out.First) != 1 || len(out.First[0].IDs) != 1 || out.First[0].IDs[0] != lidRem {
		t.Fatalf("unexpected deleted IDs payload: %+v", out.First)
	}
	if out.First[0].RegistryTable != "rt_rem" {
		t.Fatalf("unexpected registry table for deleted IDs: %s", out.First[0].RegistryTable)
	}
	// Unused blobs should include the inactive (updated) and active (removed)
	if len(out.Second) != 2 {
		t.Fatalf("unexpected unused blobs count: %d", len(out.Second))
	}
	if out.Second[0].BlobTable != "bt_upd" || len(out.Second[0].Blobs) != 1 || out.Second[0].Blobs[0] != inactiveID {
		t.Fatalf("unexpected updated unused blobs: %+v", out.Second[0])
	}
	if out.Second[1].BlobTable != "bt_rem" || len(out.Second[1].Blobs) != 1 || out.Second[1].Blobs[0] != activeID {
		t.Fatalf("unexpected removed unused blobs: %+v", out.Second[1])
	}
}

func Test_Transaction_DeleteObsoleteEntries(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	c := cache.GetGlobalL1Cache(redis)
	l1 := c
	blobs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)

	tx := &Transaction{l1Cache: l1, l2Cache: redis, blobStore: blobs, registry: reg}

	// Seed: two node IDs, one from updated (inactive), one from removed (active)
	inactiveID := sop.NewUUID()
	activeID := sop.NewUUID()

	// Put nodes into L1 and L2 via SetNode so DeleteNodes will remove from both
	n1 := &btree.Node[PersonKey, Person]{ID: inactiveID, Version: 1}
	n2 := &btree.Node[PersonKey, Person]{ID: activeID, Version: 1}
	l1.SetNode(ctx, inactiveID, n1, time.Minute)
	l1.SetNode(ctx, activeID, n2, time.Minute)

	// Seed blobs for both IDs
	_ = blobs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{
		{BlobTable: "bt_upd", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: inactiveID, Value: []byte("x")}}},
		{BlobTable: "bt_rem", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: activeID, Value: []byte("y")}}},
	})

	// Seed registry with a logical ID to be deleted
	lidRem := sop.NewUUID()
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_rem", IDs: []sop.Handle{sop.NewHandle(lidRem)}}})

	deletedIDs := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_rem", IDs: []sop.UUID{lidRem}}}
	unused := []sop.BlobsPayload[sop.UUID]{
		{BlobTable: "bt_upd", Blobs: []sop.UUID{inactiveID}},
		{BlobTable: "bt_rem", Blobs: []sop.UUID{activeID}},
	}

	if err := tx.deleteObsoleteEntries(ctx, deletedIDs, unused); err != nil {
		t.Fatalf("deleteObsoleteEntries error: %v", err)
	}

	// L1 should no longer have entries; L2 should be missing as well
	var out btree.Node[PersonKey, Person]
	if ok, _ := redis.GetStruct(ctx, cache.FormatNodeKey(inactiveID.String()), &out); ok {
		t.Fatalf("inactive node still in L2")
	}
	if ok, _ := redis.GetStruct(ctx, cache.FormatNodeKey(activeID.String()), &out); ok {
		t.Fatalf("active node still in L2")
	}

	// Blobs should be removed
	if ba, _ := blobs.GetOne(ctx, "bt_upd", inactiveID); len(ba) != 0 {
		t.Fatalf("inactive blob not removed")
	}
	if ba, _ := blobs.GetOne(ctx, "bt_rem", activeID); len(ba) != 0 {
		t.Fatalf("active blob not removed")
	}

	// Registry entry should be removed
	g, _ := reg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_rem", IDs: []sop.UUID{lidRem}}})
	if len(g) > 0 && len(g[0].IDs) > 0 {
		t.Fatalf("registry ID not removed")
	}
}
