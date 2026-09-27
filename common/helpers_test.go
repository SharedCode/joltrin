package common

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers convertToBlobRequestPayload, convertToRegistryRequestPayload, extractUUIDs, and formatKey.
func Test_NodeRepository_HelperMappers(t *testing.T) {
	ctx := context.Background()

	// Minimal store info and nodes
	so := sop.StoreOptions{Name: "st_helpers", SlotLength: 2}
	si := sop.NewStoreInfo(so)
	n1 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID(), Version: 1}
	n2 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID(), Version: 2}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{
		{First: si, Second: []interface{}{n1, n2}},
	}

	// convert helpers
	bibs := convertToBlobRequestPayload(nodes)
	if len(bibs) != 1 || len(bibs[0].Blobs) != 2 || bibs[0].Blobs[0] != n1.ID || bibs[0].Blobs[1] != n2.ID {
		t.Fatalf("convertToBlobRequestPayload mismatch: %+v", bibs)
	}
	vids := convertToRegistryRequestPayload(nodes)
	if len(vids) != 1 || len(vids[0].IDs) != 2 || vids[0].IDs[0] != n1.ID || vids[0].IDs[1] != n2.ID {
		t.Fatalf("convertToRegistryRequestPayload mismatch: %+v", vids)
	}

	// extractUUIDs
	ids := extractUUIDs(nodes)
	if len(ids) != 2 || ids[0] != n1.ID || ids[1] != n2.ID {
		t.Fatalf("extractUUIDs mismatch: %+v", ids)
	}

	// formatKey
	nr := &nodeRepositoryBackend{}
	if got := nr.formatKey("abc"); got != "Nabc" {
		t.Fatalf("formatKey got %s", got)
	}

	// extractInactiveBlobsIDs
	h1 := sop.NewHandle(n1.ID)
	// Set an inactive ID explicitly by flipping
	h1.FlipActiveID()
	payload := []sop.RegistryPayload[sop.Handle]{{BlobTable: si.BlobTable, IDs: []sop.Handle{h1}}}
	ib := extractInactiveBlobsIDs(payload)
	if len(ib) != 1 || len(ib[0].Blobs) != 1 || ib[0].Blobs[0].IsNil() {
		t.Fatalf("extractInactiveBlobsIDs expected one non-nil inactive id, got %+v", ib)
	}

	_ = ctx
}

// Covers getCommitStoresInfo and getRollbackStoresInfo deltas.
func Test_Transaction_StoreInfo_Deltas(t *testing.T) {
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "st_delta", SlotLength: 4})
	si.Count = 10

	// nodeRepository has a previous count snapshot of 7
	nr := &nodeRepositoryBackend{storeInfo: si, count: 7}
	tx := &Transaction{btreesBackend: []btreeBackend{{
		nodeRepository: nr,
		getStoreInfo:   func() *sop.StoreInfo { return si },
	}}}

	cs, modified := tx.getCommitStoresInfo()
	if !modified || len(cs) != 1 || cs[0].Name != si.Name || cs[0].CountDelta != (si.Count-nr.count) {
		t.Fatalf("getCommitStoresInfo mismatch: %+v", cs)
	}

	rs := tx.getRollbackStoresInfo()
	if len(rs) != 1 || rs[0].Name != si.Name || rs[0].CountDelta != (nr.count-si.Count) {
		t.Fatalf("getRollbackStoresInfo mismatch: %+v", rs)
	}
}

// Covers updateVersionThenPopulateMru and populateMru helpers.
func Test_Transaction_PopulateMRU_And_Versions(t *testing.T) {
	ctx := context.Background()
	redis := mocks.NewMockClient()
	cache.GetGlobalL1Cache(redis)

	tx := &Transaction{l1Cache: cache.GetGlobalL1Cache(redis)}

	si := sop.NewStoreInfo(sop.StoreOptions{Name: "st_mru", SlotLength: 2})
	// Two nodes and their corresponding handles
	n1 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID(), Version: 1}
	n2 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID(), Version: 1}
	h1 := sop.NewHandle(n1.ID)
	h2 := sop.NewHandle(n2.ID)
	// Bump target versions in handles to verify they propagate to nodes
	h1.Version = 3
	h2.Version = 5

	handles := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{h1, h2}}}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n1, n2}}}

	tx.updateVersionThenPopulateMru(ctx, handles, nodes)
	if n1.Version != h1.Version || n2.Version != h2.Version {
		t.Fatalf("versions not propagated: n1=%d n2=%d h1=%d h2=%d", n1.Version, n2.Version, h1.Version, h2.Version)
	}

	// Verify MRU is populated under active IDs
	if v := tx.l1Cache.GetNodeFromMRU(h1, &btree.Node[PersonKey, Person]{}); v == nil {
		t.Fatalf("node for h1 not in MRU")
	}
	if v := tx.l1Cache.GetNodeFromMRU(h2, &btree.Node[PersonKey, Person]{}); v == nil {
		t.Fatalf("node for h2 not in MRU")
	}
}

// Covers toByteArray and toStruct helpers round-trip for a simple payload.
func Test_TransactionLogger_ToFromBytes(t *testing.T) {
	type payload struct {
		A int
		B string
	}
	p := payload{A: 7, B: "x"}
	ba := toByteArray(p)
	got := toStruct[payload](ba)
	if got.A != p.A || got.B != p.B {
		t.Fatalf("roundtrip mismatch: got=%+v want=%+v", got, p)
	}
}
