package jsondb

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

func TestJsonDBAnyKey_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, string]{}
	db := &JsonDBAnyKey[string, string]{BtreeInterface: mock}

	items := []Item[string, string]{{Key: "k", Value: ptr("v")}}

	// Add Error
	mock.addErr = errors.New("add error")
	if _, err := db.Add(ctx, items); err == nil {
		t.Error("Add expected error")
	}
	mock.addErr = nil
	mock.addOk = false
	if ok, _ := db.Add(ctx, items); ok {
		t.Error("Add expected false")
	}

	// AddIfNotExist Error
	mock.addIfNotExistErr = errors.New("addIfNotExist error")
	if _, err := db.AddIfNotExist(ctx, items); err == nil {
		t.Error("AddIfNotExist expected error")
	}
	mock.addIfNotExistErr = nil
	mock.addIfNotExistOk = false
	if ok, _ := db.AddIfNotExist(ctx, items); ok {
		t.Error("AddIfNotExist expected false")
	}

	// Update Error
	mock.updateErr = errors.New("update error")
	if _, err := db.Update(ctx, items); err == nil {
		t.Error("Update expected error")
	}
	mock.updateErr = nil
	mock.updateOk = false
	if ok, _ := db.Update(ctx, items); ok {
		t.Error("Update expected false")
	}

	// Upsert Error
	mock.upsertErr = errors.New("upsert error")
	if _, err := db.Upsert(ctx, items); err == nil {
		t.Error("Upsert expected error")
	}
	mock.upsertErr = nil
	mock.upsertOk = false
	if ok, _ := db.Upsert(ctx, items); ok {
		t.Error("Upsert expected false")
	}

	// Remove Error
	mock.removeErr = errors.New("remove error")
	if _, err := db.Remove(ctx, []string{"k"}); err == nil {
		t.Error("Remove expected error")
	}
	mock.removeErr = nil
	mock.removeOk = false
	if ok, _ := db.Remove(ctx, []string{"k"}); ok {
		t.Error("Remove expected false")
	}
}

func TestJsonDBAnyKey_GetKeys_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, string]{}
	db := &JsonDBAnyKey[string, string]{BtreeInterface: mock}

	// GetKeys First Error (when ID is nil)
	mock.getCurrentKey = btree.Item[string, string]{ID: sop.NilUUID}
	mock.firstErr = errors.New("first error")
	if _, err := db.GetKeys(ctx, PagingInfo{PageSize: 1}); err == nil {
		t.Error("GetKeys expected first error")
	}

	// GetKeys Next Error (PageOffset)
	mock.firstErr = nil
	mock.firstOk = true
	mock.nextErr = errors.New("next error")
	if _, err := db.GetKeys(ctx, PagingInfo{PageSize: 1, PageOffset: 1}); err == nil {
		t.Error("GetKeys expected next error")
	}

	// GetKeys Previous Error (PageOffset Backward)
	mock.nextErr = nil
	mock.previousErr = errors.New("previous error")
	if _, err := db.GetKeys(ctx, PagingInfo{PageSize: 1, PageOffset: 1, Direction: Backward}); err == nil {
		t.Error("GetKeys expected previous error")
	}

	// GetKeys Next Error (Fetch)
	mock.previousErr = nil
	mock.nextErr = errors.New("next fetch error")
	// We need to pass PageOffset loop first.
	// If PageOffset=0, it skips loop.
	// Then it loops FetchCount.
	// Inside loop, it calls Next if Forward.
	// But first item is from GetCurrentKey.
	// So we need FetchCount >= 2 to trigger Next.
	if _, err := db.GetKeys(ctx, PagingInfo{PageSize: 2, Direction: Forward}); err == nil {
		t.Error("GetKeys expected next fetch error")
	}

	// GetKeys Previous Error (Fetch)
	mock.nextErr = nil
	mock.previousErr = errors.New("previous fetch error")
	if _, err := db.GetKeys(ctx, PagingInfo{PageSize: 2, Direction: Backward}); err == nil {
		t.Error("GetKeys expected previous fetch error")
	}
}

func TestJsonDBAnyKey_GetItems_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, string]{}
	db := &JsonDBAnyKey[string, string]{BtreeInterface: mock}

	// GetItems First Error
	mock.getCurrentKey = btree.Item[string, string]{ID: sop.NilUUID}
	mock.firstErr = errors.New("first error")
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 1}); err == nil {
		t.Error("GetItems expected first error")
	}

	// GetItems GetCurrentItem Error
	mock.firstErr = nil
	mock.firstOk = true
	mock.getCurrentItemErr = errors.New("get item error")
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 1}); err == nil {
		t.Error("GetItems expected get item error")
	}

	// GetItems Next Error (Fetch)
	mock.getCurrentItemErr = nil
	mock.nextErr = errors.New("next fetch error")
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 2, Direction: Forward}); err == nil {
		t.Error("GetItems expected next fetch error")
	}
}

func TestJsonDBAnyKey_GetValues_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, string]{}
	db := &JsonDBAnyKey[string, string]{BtreeInterface: mock}

	items := []Item[string, string]{{Key: "k"}}

	// Find Error
	mock.findErr = errors.New("find error")
	if _, err := db.GetValues(ctx, items); err == nil {
		t.Error("GetValues expected find error")
	}

	// FindWithID Error
	mock.findErr = nil
	itemsID := []Item[string, string]{{Key: "k", ID: uuid.New()}}
	mock.findWithIDErr = errors.New("findWithID error")
	if _, err := db.GetValues(ctx, itemsID); err == nil {
		t.Error("GetValues expected findWithID error")
	}

	// GetCurrentItem Error
	mock.findWithIDErr = nil
	mock.findWithIDOk = true
	mock.getCurrentItemErr = errors.New("get item error")
	if _, err := db.GetValues(ctx, itemsID); err == nil {
		t.Error("GetValues expected get item error")
	}
}

func TestJsonDBAnyKey_GetItems_PageOffset_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, string]{}
	db := &JsonDBAnyKey[string, string]{BtreeInterface: mock}

	// GetItems Next Error (PageOffset)
	mock.getCurrentKey = btree.Item[string, string]{ID: sop.NilUUID}
	mock.firstOk = true
	mock.nextErr = errors.New("next error")
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 1, PageOffset: 1}); err == nil {
		t.Error("GetItems expected next error")
	}

	// GetItems Previous Error (PageOffset Backward)
	mock.nextErr = nil
	mock.previousErr = errors.New("previous error")
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 1, PageOffset: 1, Direction: Backward}); err == nil {
		t.Error("GetItems expected previous error")
	}
}

func TestJsonDBAnyKey_ToJsonString_Error(t *testing.T) {
	ctx := context.Background()
	mock := &mockBtree[string, any]{}
	db := &JsonDBAnyKey[string, any]{BtreeInterface: mock}

	badVal := any(make(chan int))
	mock.getCurrentItem = btree.Item[string, any]{
		Key:   "k",
		Value: &badVal,
	}
	mock.firstOk = true

	// GetItems calls toJsonString
	if _, err := db.GetItems(ctx, PagingInfo{PageSize: 1}); err == nil {
		t.Error("Expected json marshal error")
	}
}
