package common

import (
	"context"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/common/mocks"
	"testing"
	"time"
)

func Test_Transaction_MergeNodesKeys_Table(t *testing.T) {
	// Create mock data.
	lc := mocks.NewMockClient()
	existing := lc.CreateLockKeys([]string{sop.NewUUID().String(), sop.NewUUID().String(), sop.NewUUID().String()})
	for _, k := range existing {
		_ = lc.Set(ctx, k.Key, k.LockID.String(), time.Minute)
		k.IsLockOwner = true
	}
	updated := []sop.Tuple[*sop.StoreInfo, []interface{}]{}
	removed := []sop.Tuple[*sop.StoreInfo, []interface{}]{}
	keepUUID, _ := sop.ParseUUID(existing[1].Key[1:])
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "x", SlotLength: 2})
	updated = append(updated, sop.Tuple[*sop.StoreInfo, []interface{}]{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: keepUUID}}})
	remUUID, _ := sop.ParseUUID(existing[0].Key[1:])
	removed = append(removed, sop.Tuple[*sop.StoreInfo, []interface{}]{First: si, Second: []interface{}{&btree.Node[PersonKey, Person]{ID: remUUID}}})

	cases := []struct {
		name             string
		updated, removed []sop.Tuple[*sop.StoreInfo, []any]
	}{
		{"empty", nil, nil},
		{"only updated", updated, nil},
		{"only removed", nil, removed},
		{"both", updated, removed},
	}
	tr := &Transaction{}
	tr.l2Cache = lc
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr.mergeNodesKeys(ctx, tc.updated, tc.removed)
		})
	}
}

func Test_Transaction_UnlockNodesKeys_Table(t *testing.T) {
	tr := &Transaction{}
	ctx := context.Background()
	_ = tr.unlockNodesKeys(ctx)
}

func Test_Transaction_TimedOut_Table(t *testing.T) {
	tr := &Transaction{}
	tr.maxTime = time.Duration(2 * time.Hour)
	ctx := context.Background()
	now := sop.Now()
	cases := []struct {
		name    string
		start   time.Time
		wantErr bool
	}{
		{"not timed out", now, false},
		{"timed out", now.Add(-2 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tr.timedOut(ctx, tc.start)
			if (err != nil) != tc.wantErr {
				t.Errorf("timedOut(%v) = %v, wantErr %v", tc.start, err, tc.wantErr)
			}
		})
	}
}

func Test_Transaction_TrackedItemsWrappers_Table(t *testing.T) {
	tr := &Transaction{}
	ctx := context.Background()
	if tr.hasTrackedItems() {
		t.Error("expected false for hasTrackedItems on new Transaction")
	}
	_ = tr.checkTrackedItems(ctx)
	_ = tr.lockTrackedItems(ctx)
	_ = tr.unlockTrackedItems(ctx)
}
