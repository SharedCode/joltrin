package jsondb

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/encoding"
)

func TestJsonDBMapKey_IndexSpecAndOpen(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_idx_open"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_idx_open")
	}()
	// trans.Begin(ctx)

	idxSpec := NewIndexSpecification([]IndexFieldSpecification{
		{FieldName: "name", AscendingSortOrder: true},
		{FieldName: "age", AscendingSortOrder: false},
	})
	ba, _ := encoding.DefaultMarshaler.Marshal(idxSpec)

	db, err := NewJsonBtreeMapKey(ctx, d, sop.StoreOptions{
		Name:       "users_idx",
		SlotLength: 10,
	}, trans, string(ba))
	if err != nil {
		t.Fatalf("NewJsonBtreeMapKey failed: %v", err)
	}

	val1 := any(struct{ City string }{"NY"})
	item1 := Item[map[string]any, any]{
		Key:   map[string]any{"name": "alice", "age": 30},
		Value: &val1,
	}
	val2 := any(struct{ City string }{"LA"})
	item2 := Item[map[string]any, any]{
		Key:   map[string]any{"name": "bob", "age": 25},
		Value: &val2,
	}

	// Add items
	if ok, err := db.Add(ctx, []Item[map[string]any, any]{item1, item2}); !ok || err != nil {
		t.Errorf("Add failed: %v, %v", ok, err)
	}

	trans.Commit(ctx)

	// Re-open
	d = sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_idx_open"},
		CacheType:     sop.InMemory,
	}
	trans, _ = database.BeginTransaction(ctx, d, sop.ForWriting)
	// trans.Begin(ctx)

	db2, err := OpenJsonBtreeMapKey(ctx, d, "users_idx", trans)
	if err != nil {
		t.Fatalf("OpenJsonBtreeMapKey failed: %v", err)
	}

	// Verify index spec is restored (indirectly via behavior)
	if ok, err := db2.First(ctx); !ok || err != nil {
		t.Errorf("First failed: %v, %v", ok, err)
	}

	k := db2.BtreeInterface.GetCurrentKey()
	if k.Key["name"] != "alice" {
		t.Errorf("Expected alice, got %v", k.Key["name"])
	}

	// Add another item to trigger proxyComparer -> indexSpecification.Comparer
	val3 := any(struct{ City string }{"SF"})
	item3 := Item[map[string]any, any]{
		Key:   map[string]any{"name": "charlie", "age": 35},
		Value: &val3,
	}
	if ok, err := db2.Add(ctx, []Item[map[string]any, any]{item3}); !ok || err != nil {
		t.Errorf("Add after reopen failed: %v, %v", ok, err)
	}
}

func TestJsonDBMapKey_BasicCRUD(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	defer func() {
		if err := os.RemoveAll("test_jsondb_mapkey"); err != nil {
			t.Logf("Failed to remove test directory: %v", err)
		}
	}()

	// trans.Begin(ctx)

	so := sop.StoreOptions{
		Name:       "map_store",
		SlotLength: 10,
	}

	// Test with default comparer (no index spec)
	db, err := NewJsonBtreeMapKey(ctx, d, so, trans, "")
	if err != nil {
		t.Fatalf("NewJsonBtreeMapKey failed: %v", err)
	}

	key1 := map[string]any{"id": 1, "name": "foo"}
	var val1 any = "value1"
	key2 := map[string]any{"id": 2, "name": "bar"}
	var val2 any = "value2"
	items := []Item[map[string]any, any]{
		{Key: key1, Value: &val1},
		{Key: key2, Value: &val2},
	}

	ok, err := db.Add(ctx, items)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !ok {
		t.Errorf("Add returned false")
	}

	// Test GetItems
	itemsJson, err := db.GetItems(ctx, PagingInfo{PageSize: 10, Direction: Forward})
	if err != nil {
		t.Fatalf("GetItems failed: %v", err)
	}
	if itemsJson == "" {
		t.Errorf("GetItems returned empty string")
	}

	trans.Commit(ctx)
}

func TestJsonDBMapKey_WithIndexSpec(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_idx"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_idx")
	}()

	// trans.Begin(ctx)

	so := sop.StoreOptions{
		Name:       "map_store_idx",
		SlotLength: 10,
	}

	// Simple index spec
	indexSpec := `{"index_fields": [{"field_name": "id", "ascending_sort_order": true}]}`

	db, err := NewJsonBtreeMapKey(ctx, d, so, trans, indexSpec)
	if err != nil {
		t.Fatalf("NewJsonBtreeMapKey failed: %v", err)
	}

	key1 := map[string]any{"id": 1, "name": "foo"}
	var val1 any = "value1"
	items := []Item[map[string]any, any]{
		{Key: key1, Value: &val1},
	}

	ok, err := db.Add(ctx, items)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !ok {
		t.Errorf("Add returned false")
	}

	// Add another item to verify sorting
	key2 := map[string]any{"id": 2, "name": "bar"}
	var val2 any = "value2"
	items2 := []Item[map[string]any, any]{
		{Key: key2, Value: &val2},
	}
	ok, err = db.Add(ctx, items2)
	if err != nil {
		t.Fatalf("Add 2 failed: %v", err)
	}
	if !ok {
		t.Errorf("Add 2 returned false")
	}

	// Verify order (id 1 < id 2)
	db.First(ctx)
	k := db.BtreeInterface.GetCurrentKey()
	if id, ok := k.Key["id"].(int); ok {
		if id != 1 {
			t.Errorf("Expected first key id 1, got %v", id)
		}
	} else {
		t.Errorf("Expected id to be int, got %T", k.Key["id"])
	}

	trans.Commit(ctx)
}

func TestJsonDBMapKey_Open(t *testing.T) {
	ctx := context.Background()
	// Setup
	{
		d := sop.DatabaseOptions{
			StoresFolders: []string{"test_jsondb_mapkey_open"},
			CacheType:     sop.InMemory,
		}
		trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
		// trans.Begin(ctx)
		so := sop.StoreOptions{Name: "map_store_open"}
		_, _ = NewJsonBtreeMapKey(ctx, d, so, trans, "")
		trans.Commit(ctx)
	}
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_open")
	}()

	// Test Open
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_open"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	// trans.Begin(ctx)

	db, err := OpenJsonBtreeMapKey(ctx, d, "map_store_open", trans)
	if err != nil {
		t.Fatalf("OpenJsonBtreeMapKey failed: %v", err)
	}
	if db == nil {
		t.Errorf("OpenJsonBtreeMapKey returned nil")
	}
	trans.Commit(ctx)
}

func TestJsonDBMapKey_OpenNoIndexSpec(t *testing.T) {
	ctx := context.Background()
	// Setup: Create a store without index spec
	{
		d := sop.DatabaseOptions{
			StoresFolders: []string{"test_jsondb_mapkey_no_idx"},
			CacheType:     sop.InMemory,
		}
		trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
		// trans.Begin(ctx)
		so := sop.StoreOptions{Name: "map_store_no_idx"}
		_, _ = NewJsonBtreeMapKey(ctx, d, so, trans, "")
		trans.Commit(ctx)
	}
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_no_idx")
	}()

	// Open and use it
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_no_idx"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	// trans.Begin(ctx)

	db, err := OpenJsonBtreeMapKey(ctx, d, "map_store_no_idx", trans)
	if err != nil {
		t.Fatalf("OpenJsonBtreeMapKey failed: %v", err)
	}

	// Add an item to trigger proxyComparer -> defaultComparer
	key1 := map[string]any{"id": 1, "name": "foo"}
	val1 := any("value1")
	items := []Item[map[string]any, any]{
		{Key: key1, Value: &val1},
	}
	ok, err := db.Add(ctx, items)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !ok {
		t.Errorf("Add returned false")
	}

	// Add second item to force comparison
	key2 := map[string]any{"id": 2, "name": "bar"}
	val2 := any("value2")
	items2 := []Item[map[string]any, any]{
		{Key: key2, Value: &val2},
	}
	ok, err = db.Add(ctx, items2)
	if err != nil {
		t.Fatalf("Add 2 failed: %v", err)
	}
	if !ok {
		t.Errorf("Add 2 returned false")
	}
	trans.Commit(ctx)
}

func TestJsonDBMapKey_InvalidSpec(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_invalid"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_invalid")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "map_store_invalid"}
	// Invalid JSON
	_, err := NewJsonBtreeMapKey(ctx, d, so, trans, "{invalid_json")
	if err == nil {
		t.Error("Expected error for invalid JSON spec")
	}
	trans.Commit(ctx)
}

func TestJsonDBMapKey_DefaultComparer_DifferentKeys(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_diff_keys"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_diff_keys")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "map_store_diff_keys"}
	db, _ := NewJsonBtreeMapKey(ctx, d, so, trans, "")

	// Add item with key "a"
	key1 := map[string]any{"a": 1}
	val1 := any(1)
	db.Add(ctx, []Item[map[string]any, any]{{Key: key1, Value: &val1}})

	// Add item with key "b"
	// This will trigger defaultComparer with "a" field.
	// key2["a"] is nil.
	key2 := map[string]any{"b": 2}
	val2 := any(2)
	db.Add(ctx, []Item[map[string]any, any]{{Key: key2, Value: &val2}})

	trans.Commit(ctx)
}

func TestJsonDBMapKey_New_Failure(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_fail"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_fail")
	}()
	// trans.Begin(ctx)
	trans.Commit(ctx) // Commit to force failure

	so := sop.StoreOptions{Name: "fail_map_store"}
	_, err := NewJsonBtreeMapKey(ctx, d, so, trans, "")
	if err == nil {
		t.Error("Expected error for committed transaction")
	}
}

func TestJsonDBMapKey_Open_Failure(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_mapkey_open_fail"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForReading)
	defer func() {
		os.RemoveAll("test_jsondb_mapkey_open_fail")
	}()
	// trans.Begin(ctx)

	_, err := OpenJsonBtreeMapKey(ctx, d, "non_existent", trans)
	if err == nil {
		t.Error("Expected error for non-existent store")
	}
	trans.Commit(ctx)
}
