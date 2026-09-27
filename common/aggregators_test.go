package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

func Test_Transaction_Aggregators_InvokeClosures(t *testing.T) {
	ctx := context.Background()

	// Flags to verify calls
	calledCheck := 0
	calledLock := 0
	calledUnlock := 0
	calledCommitVals := 0

	// Closures
	hb1 := btreeBackend{
		hasTrackedItems: func() bool { return false },
		checkTrackedItems: func(context.Context) error {
			calledCheck++
			return nil
		},
		lockTrackedItems: func(context.Context, time.Duration) error {
			calledLock++
			return nil
		},
		unlockTrackedItems: func(context.Context) error {
			calledUnlock++
			return nil
		},
		commitTrackedItemsValues: func(context.Context) error {
			calledCommitVals++
			return nil
		},
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
			return &sop.BlobsPayload[sop.UUID]{Blobs: []sop.UUID{sop.NewUUID()}}
		},
		getObsoleteTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
			return &sop.BlobsPayload[sop.UUID]{Blobs: []sop.UUID{sop.NewUUID()}}
		},
		getStoreInfo:   func() *sop.StoreInfo { s := sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 2}); return s },
		nodeRepository: &nodeRepositoryBackend{storeInfo: sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 2})},
	}
	hb2 := btreeBackend{
		hasTrackedItems: func() bool { return true },
		checkTrackedItems: func(context.Context) error {
			calledCheck++
			return errors.New("boom")
		},
		lockTrackedItems: func(context.Context, time.Duration) error {
			calledLock++
			return errors.New("lockerr")
		},
		unlockTrackedItems: func(context.Context) error {
			calledUnlock++
			return errors.New("unlockerr")
		},
		commitTrackedItemsValues: func(context.Context) error {
			calledCommitVals++
			return errors.New("commiterr")
		},
		getForRollbackTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
			return &sop.BlobsPayload[sop.UUID]{Blobs: []sop.UUID{sop.NewUUID()}}
		},
		getObsoleteTrackedItemsValues: func() *sop.BlobsPayload[sop.UUID] {
			return &sop.BlobsPayload[sop.UUID]{Blobs: []sop.UUID{sop.NewUUID()}}
		},
		getStoreInfo:   func() *sop.StoreInfo { s := sop.NewStoreInfo(sop.StoreOptions{Name: "y", SlotLength: 2}); return s },
		nodeRepository: &nodeRepositoryBackend{storeInfo: sop.NewStoreInfo(sop.StoreOptions{Name: "y", SlotLength: 2})},
	}
	tx := &Transaction{btreesBackend: []btreeBackend{hb1, hb2}}

	if !tx.hasTrackedItems() {
		t.Fatalf("expected hasTrackedItems true")
	}
	if err := tx.checkTrackedItems(ctx); err == nil {
		t.Fatalf("expected checkTrackedItems to return error")
	}
	if err := tx.lockTrackedItems(ctx); err == nil {
		t.Fatalf("expected lockTrackedItems to return error")
	}
	if err := tx.unlockTrackedItems(ctx); err == nil {
		t.Fatalf("expected unlockTrackedItems to return error")
	}
	if err := tx.commitTrackedItemsValues(ctx); err == nil {
		t.Fatalf("expected commitTrackedItemsValues to return error")
	}

	// Verify closures invoked across backends
	if calledCheck != 1 || calledLock != 1 || calledUnlock != 1 || calledCommitVals != 2 {
		t.Fatalf("closures not all invoked: check=%d lock=%d unlock=%d commit=%d", calledCheck, calledLock, calledUnlock, calledCommitVals)
	}

	// Aggregate getters should include entries from both backends
	fr := tx.getForRollbackTrackedItemsValues()
	if len(fr) != 2 {
		t.Fatalf("expected 2 rollback value payloads, got %d", len(fr))
	}
	ob := tx.getObsoleteTrackedItemsValues()
	if len(ob) != 2 {
		t.Fatalf("expected 2 obsolete value payloads, got %d", len(ob))
	}
}

func Test_Transaction_ClassifyModifiedNodes(t *testing.T) {
	// Build a backend with various localCache actions

	s := sop.NewStoreInfo(sop.StoreOptions{Name: "cls", SlotLength: 2})
	n1 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID()}
	n2 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID()}
	n3 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID()}
	n4 := &btree.Node[PersonKey, Person]{ID: sop.NewUUID()}

	nr := &nodeRepositoryBackend{storeInfo: s, localCache: map[sop.UUID]cachedNode{
		n1.ID: {node: n1, action: updateAction},
		n2.ID: {node: n2, action: removeAction},
		n3.ID: {node: n3, action: addAction},
		n4.ID: {node: n4, action: getAction},
	}}
	be := btreeBackend{nodeRepository: nr, getStoreInfo: func() *sop.StoreInfo { return s }}
	tx := &Transaction{btreesBackend: []btreeBackend{be}}

	up, rm, add, fetch, root := tx.classifyModifiedNodes()
	if len(up) != 1 || len(up[0].Second) != 1 {
		t.Fatalf("expected one updated node")
	}
	if len(rm) != 1 || len(rm[0].Second) != 1 {
		t.Fatalf("expected one removed node")
	}
	if len(add) != 1 || len(add[0].Second) != 1 {
		t.Fatalf("expected one added node")
	}
	if len(fetch) != 1 || len(fetch[0].Second) != 1 {
		t.Fatalf("expected one fetched node")
	}
	if len(root) != 0 {
		t.Fatalf("expected no root nodes")
	}
}
