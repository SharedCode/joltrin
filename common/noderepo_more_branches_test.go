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

// Covers commitNewRootNodes branch where an existing non-empty root is found -> returns (false, nil, nil)
func Test_CommitNewRootNodes_NonEmptyRoot_ReturnsFalseNil_More(t *testing.T) {
	ctx := context.Background()

	// Setup transaction and backend
	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: reg, blobStore: mocks.NewMockBlobStore()}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "nr_root", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), storeInfo: si}

	// Seed registry with an existing handle for the intended logical ID
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})

	// Prepare nodes payload with the same logical ID to simulate non-empty root
	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 0}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}

	ok, handles, err := nr.commitNewRootNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitNewRootNodes err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when non-empty root exists")
	}
	if handles != nil {
		t.Fatalf("expected nil handles on non-empty root")
	}
}

// Covers commitUpdatedNodes branch where version mismatch returns (false, nil, nil)
func Test_CommitUpdatedNodes_VersionMismatch_ReturnsFalse_More(t *testing.T) {
	ctx := context.Background()

	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), registry: reg, blobStore: mocks.NewMockBlobStore()}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "nr_upd", SlotLength: 4})
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), storeInfo: si}

	// Logical ID and handle version set to 2, but node version will be 1 -> mismatch
	lid := sop.NewUUID()
	h := sop.NewHandle(lid)
	h.Version = 2
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: si.RegistryTable, IDs: []sop.Handle{h}}})

	n := &btree.Node[PersonKey, Person]{ID: lid, Version: 1}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}

	ok, handles, err := nr.commitUpdatedNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("commitUpdatedNodes err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false on version mismatch")
	}
	if handles != nil {
		t.Fatalf("expected nil handles on version mismatch")
	}
}

// Covers rollbackNewRootNodes branch where committedState <= commitNewRootNodes -> do not remove from registry
func Test_RollbackNewRootNodes_NotCommitted_KeepsRegistryEntry_More(t *testing.T) {
	ctx := context.Background()

	l2 := mocks.NewMockClient()
	cache.GetGlobalL1Cache(l2)
	bs := mocks.NewMockBlobStore()
	reg := mocks.NewMockRegistry(false)
	tx := &Transaction{l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2), blobStore: bs, registry: reg}
	nr := &nodeRepositoryBackend{transaction: tx, l2Cache: l2, l1Cache: cache.GetGlobalL1Cache(l2)}
	tx.btreesBackend = []btreeBackend{{nodeRepository: nr}}
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	tx.logger = tl

	// Seed a root entry in registry and blob/cache
	rootLID := sop.NewUUID()
	rootNodeBlobID := sop.NewUUID()
	_ = reg.Add(ctx, []sop.RegistryPayload[sop.Handle]{{RegistryTable: "rt_root_keep", IDs: []sop.Handle{sop.NewHandle(rootLID)}}})
	_ = bs.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{BlobTable: "bt_root_keep", Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{Key: rootNodeBlobID, Value: []byte("x")}}}})
	_ = l2.SetStruct(ctx, nr.formatKey(rootLID.String()), &btree.Node[PersonKey, Person]{ID: rootNodeBlobID}, time.Minute)

	// Prepare finalize payload for commitNewRootNodes and invoke rollback with committedState set to commitNewRootNodes (not greater)
	vids := []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root_keep", IDs: []sop.UUID{rootLID}}}
	bibs := []sop.BlobsPayload[sop.UUID]{{BlobTable: "bt_root_keep", Blobs: []sop.UUID{rootNodeBlobID}}}
	bv := sop.Tuple[[]sop.RegistryPayload[sop.UUID], []sop.BlobsPayload[sop.UUID]]{First: vids, Second: bibs}
	tl.committedState = commitNewRootNodes
	// Include a later commit phase so rollback() triggers rollbackNewRootNodes, but keep
	// committedState == commitNewRootNodes so registry unregister branch is skipped.
	// Append a finalizeCommit as the last log so lastCommittedFunctionLog > commitNewRootNodes.
	// This triggers rollbackNewRootNodes to run, which should remove blob/cache but not unregister
	// because committedState is only commitNewRootNodes.
	logs := []sop.KeyValuePair[int, []byte]{
		{Key: commitNewRootNodes, Value: toByteArray(bv)},
		{Key: finalizeCommit, Value: nil},
	}

	if err := tl.rollback(ctx, tx, sop.NewUUID(), logs); err != nil {
		t.Fatalf("rollback err: %v", err)
	}

	// Blob/cache should be removed
	if ba, _ := bs.GetOne(ctx, "bt_root_keep", rootNodeBlobID); len(ba) != 0 {
		t.Fatalf("expected root blob removed on rollback")
	}
	var n btree.Node[PersonKey, Person]
	if ok, _ := l2.GetStruct(ctx, nr.formatKey(rootLID.String()), &n); ok {
		t.Fatalf("expected root cache evicted on rollback")
	}
	// Registry should still have the entry since not committed yet
	got, _ := reg.Get(ctx, []sop.RegistryPayload[sop.UUID]{{RegistryTable: "rt_root_keep", IDs: []sop.UUID{rootLID}}})
	if len(got) == 0 || len(got[0].IDs) == 0 {
		t.Fatalf("expected registry entry to remain when committedState <= commitNewRootNodes")
	}
}
