package jsondb

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/database"
)

func TestJsonDBAnyKey_BasicCRUD(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_anykey"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	defer func() {
		// Cleanup
		if err := os.RemoveAll("test_jsondb_anykey"); err != nil {
			t.Logf("Failed to remove test directory: %v", err)
		}
	}()

	// trans.Begin(ctx) // BeginTransaction already begins it.

	so := sop.StoreOptions{
		Name:       "foo_store",
		SlotLength: 10,
	}
	comparer := func(a, b string) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}

	db, err := NewJsonBtree[string, string](ctx, d, so, trans, comparer)
	if err != nil {
		t.Fatalf("NewJsonBtree failed: %v", err)
	}

	// Test Add
	items := []Item[string, string]{
		{Key: "key1", Value: ptr("value1")},
		{Key: "key2", Value: ptr("value2")},
	}
	ok, err := db.Add(ctx, items)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !ok {
		t.Errorf("Add returned false")
	}

	// Test GetKeys
	keysJson, err := db.GetKeys(ctx, PagingInfo{PageSize: 10, Direction: Forward})
	if err != nil {
		t.Fatalf("GetKeys failed: %v", err)
	}
	if keysJson == "" {
		t.Errorf("GetKeys returned empty string")
	}
	// Simple check if keys are present in JSON
	if len(keysJson) < 10 {
		t.Errorf("GetKeys returned too short string: %s", keysJson)
	}

	// Test GetItems
	itemsJson, err := db.GetItems(ctx, PagingInfo{PageSize: 10, Direction: Forward})
	if err != nil {
		t.Fatalf("GetItems failed: %v", err)
	}
	if itemsJson == "" {
		t.Errorf("GetItems returned empty string")
	}

	// Test GetValues
	valuesJson, err := db.GetValues(ctx, items)
	if err != nil {
		t.Fatalf("GetValues failed: %v", err)
	}
	if valuesJson == "" {
		t.Errorf("GetValues returned empty string")
	}

	// Test Update
	items[0].Value = ptr("value1_updated")
	ok, err = db.Update(ctx, items)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !ok {
		t.Errorf("Update returned false")
	}

	// Test Upsert
	itemsUpsert := []Item[string, string]{
		{Key: "key3", Value: ptr("value3")},
		{Key: "key1", Value: ptr("value1_upserted")},
	}
	ok, err = db.Upsert(ctx, itemsUpsert)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if !ok {
		t.Errorf("Upsert returned false")
	}

	// Test Remove
	keysToRemove := []string{"key2"}
	ok, err = db.Remove(ctx, keysToRemove)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if !ok {
		t.Errorf("Remove returned false")
	}

	// Test AddIfNotExist
	itemsAddIfNotExist := []Item[string, string]{
		{Key: "key4", Value: ptr("value4")},
		{Key: "key1", Value: ptr("should_not_add")},
	}
	ok, err = db.AddIfNotExist(ctx, itemsAddIfNotExist)
	if err != nil {
		t.Fatalf("AddIfNotExist failed: %v", err)
	}
	// It might return false if some items existed, depending on implementation.
	// The implementation says: returns true only if all inserts succeed.
	// Since key1 exists, it should return false (or true if AddIfNotExist returns true for existing items? No, usually false).
	// Let's check implementation of AddIfNotExist in btree.
	// It returns false if item exists.
	if ok {
		t.Errorf("AddIfNotExist should have returned false for existing key")
	}

	trans.Commit(ctx)
}

func TestJsonDBAnyKey_Open(t *testing.T) {
	ctx := context.Background()
	// Setup: Create a store first
	{
		d := sop.DatabaseOptions{
			StoresFolders: []string{"test_jsondb_open"},
			CacheType:     sop.InMemory,
		}
		trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
		so := sop.StoreOptions{Name: "foo_store_open"}
		comparer := func(a, b string) int { return 0 } // Dummy comparer
		_, _ = NewJsonBtree[string, string](ctx, d, so, trans, comparer)
		trans.Commit(ctx)
	}
	defer func() {
		os.RemoveAll("test_jsondb_open")
	}()

	// Test Open
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_open"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}

	comparer := func(a, b string) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	db, err := OpenJsonBtree[string, string](ctx, d, "foo_store_open", trans, comparer)
	if err != nil {
		t.Fatalf("OpenJsonBtree failed: %v", err)
	}
	if db == nil {
		t.Errorf("OpenJsonBtree returned nil")
	}
	trans.Commit(ctx)
}

func ptr[T any](v T) *T {
	return &v
}

func TestJsonDBAnyKey_Pagination(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_pagination"},
		CacheType:     sop.InMemory,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}
	defer func() {
		os.RemoveAll("test_jsondb_pagination")
	}()

	// trans.Begin(ctx)

	so := sop.StoreOptions{
		Name:       "pagination_store",
		SlotLength: 10,
	}
	comparer := func(a, b int) int {
		return a - b
	}

	db, err := NewJsonBtree[int, int](ctx, d, so, trans, comparer)
	if err != nil {
		t.Fatalf("NewJsonBtree failed: %v", err)
	}

	// Add 20 items
	items := make([]Item[int, int], 20)
	for i := 0; i < 20; i++ {
		val := i
		items[i] = Item[int, int]{Key: i, Value: &val}
	}
	ok, err := db.Add(ctx, items)
	if err != nil || !ok {
		t.Fatalf("Add failed")
	}

	// Test PageOffset
	// PageSize=5, PageOffset=1 (should skip first 5)
	db.First(ctx)
	keysJson, err := db.GetKeys(ctx, PagingInfo{PageSize: 5, PageOffset: 1, Direction: Forward})
	if err != nil {
		t.Fatalf("GetKeys failed: %v", err)
	}
	// Should contain keys 5,6,7,8,9
	if keysJson == "" {
		t.Errorf("GetKeys returned empty")
	}

	// Test Backward
	// Go to last
	db.Last(ctx)
	// PageSize=5, Direction=Backward
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 5, Direction: Backward})
	if err != nil {
		t.Fatalf("GetKeys Backward failed: %v", err)
	}
	if keysJson == "" {
		t.Errorf("GetKeys Backward returned empty")
	}

	// Test GetItems Pagination
	db.First(ctx)
	itemsJson, err := db.GetItems(ctx, PagingInfo{PageSize: 5, PageOffset: 1, Direction: Forward})
	if err != nil {
		t.Fatalf("GetItems Pagination failed: %v", err)
	}
	if itemsJson == "" {
		t.Errorf("GetItems Pagination returned empty")
	}

	// Test Backward with Offset
	db.Last(ctx)
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 5, PageOffset: 1, Direction: Backward})
	if err != nil {
		t.Fatalf("GetKeys Backward Offset failed: %v", err)
	}
	if keysJson == "" {
		t.Errorf("GetKeys Backward Offset returned empty")
	}

	// Test FetchCount > PageSize
	db.First(ctx)
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 2, FetchCount: 5, Direction: Forward})
	if err != nil {
		t.Fatalf("GetKeys FetchCount failed: %v", err)
	}
	if keysJson == "" {
		t.Errorf("GetKeys FetchCount > PageSize returned empty")
	}

	// Test FetchCount = 0
	db.First(ctx)
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 3, FetchCount: 0, Direction: Forward})
	if err != nil {
		t.Fatalf("GetKeys FetchCount=0 failed: %v", err)
	}
	if keysJson == "" {
		t.Errorf("GetKeys FetchCount=0 returned empty")
	}

	// Test GetItems Backward Offset
	db.Last(ctx)
	itemsJson, err = db.GetItems(ctx, PagingInfo{PageSize: 5, PageOffset: 1, Direction: Backward})
	if err != nil {
		t.Fatalf("GetItems Backward Offset failed: %v", err)
	}
	if itemsJson == "" {
		t.Errorf("GetItems Backward Offset returned empty")
	}

	// Test FetchCount reaching end (Forward)
	db.First(ctx)
	// 20 items. Fetch 25.
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 25, Direction: Forward})
	if err != nil {
		t.Fatalf("GetKeys FetchCount ReachEnd failed: %v", err)
	}
	// Should return 20 items.
	if len(keysJson) < 20 {
		t.Errorf("GetKeys FetchCount ReachEnd returned too few items")
	}

	// Test FetchCount reaching end (Backward)
	db.Last(ctx)
	keysJson, err = db.GetKeys(ctx, PagingInfo{PageSize: 25, Direction: Backward})
	if err != nil {
		t.Fatalf("GetKeys FetchCount ReachEnd Backward failed: %v", err)
	}
	if len(keysJson) < 20 {
		t.Errorf("GetKeys FetchCount ReachEnd Backward returned too few items")
	}

	// Test GetItems reaching end
	db.First(ctx)
	itemsJson, err = db.GetItems(ctx, PagingInfo{PageSize: 25, Direction: Forward})
	if err != nil {
		t.Fatalf("GetItems ReachEnd failed: %v", err)
	}
	if len(itemsJson) < 20 {
		t.Errorf("GetItems ReachEnd returned too few items")
	}

	// Test GetItems reaching end (Backward)
	db.Last(ctx)
	itemsJson, err = db.GetItems(ctx, PagingInfo{PageSize: 25, Direction: Backward})
	if err != nil {
		t.Fatalf("GetItems ReachEnd Backward failed: %v", err)
	}
	if len(itemsJson) < 20 {
		t.Errorf("GetItems ReachEnd Backward returned too few items")
	}

	// Test GetItems reaching end (PageOffset)
	// 20 items. PageSize=5. PageOffset=5 (skip 25 items). Should fail.
	_, err = db.GetItems(ctx, PagingInfo{PageSize: 5, PageOffset: 5, Direction: Forward})
	if err == nil {
		t.Errorf("GetItems PageOffset ReachEnd expected error")
	}

	// Test GetItems reaching end (PageOffset Backward)
	db.Last(ctx)
	_, err = db.GetItems(ctx, PagingInfo{PageSize: 5, PageOffset: 5, Direction: Backward})
	if err == nil {
		t.Errorf("GetItems PageOffset ReachEnd Backward expected error")
	}

	trans.Commit(ctx)
}

func TestJsonDBAnyKey_Open_Fail(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_open_fail"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForReading)
	defer func() {
		os.RemoveAll("test_jsondb_open_fail")
	}()
	// trans.Begin(ctx)

	_, err := OpenJsonBtree[string, string](ctx, d, "non_existent_store", trans, nil)
	if err == nil {
		t.Error("Expected error for non-existent store")
	}
	trans.Commit(ctx)
}

func TestJsonDBAnyKey_WithReplication(t *testing.T) {
	ctx := context.Background()
	// Need 2 folders for replication
	folders := []string{"test_jsondb_repl_1", "test_jsondb_repl_2"}
	defer func() {
		os.RemoveAll(folders[0])
		os.RemoveAll(folders[1])
	}()

	ec := map[string]sop.ErasureCodingConfig{
		"": {
			DataShardsCount:             1,
			ParityShardsCount:           1,
			BaseFolderPathsAcrossDrives: folders,
		},
	}

	d := sop.DatabaseOptions{
		StoresFolders: folders,
		CacheType:     sop.InMemory,
		ErasureConfig: ec,
	}
	trans, err := database.BeginTransaction(ctx, d, sop.ForWriting)
	if err != nil {
		t.Fatalf("NewTransactionWithReplication failed: %v", err)
	}
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "repl_store"}
	db, err := NewJsonBtree[string, string](ctx, d, so, trans, nil)
	if err != nil {
		t.Fatalf("NewJsonBtree with replication failed: %v", err)
	}
	if db == nil {
		t.Error("NewJsonBtree returned nil")
	}
	trans.Commit(ctx)
}

func TestJsonDBAnyKey_NewBtree_Failure(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_fail"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_fail")
	}()
	// trans.Begin(ctx)
	trans.Commit(ctx)

	so := sop.StoreOptions{
		Name: "fail_store",
	}
	_, err := NewJsonBtree[string, string](ctx, d, so, trans, nil)
	if err == nil {
		t.Error("Expected error for committed transaction")
	}
}

func TestJsonDBAnyKey_GetValues_NotFound(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_getvalues"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_getvalues")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "getvalues_store"}
	comparer := func(a, b int) int { return a - b }
	db, _ := NewJsonBtree[int, int](ctx, d, so, trans, comparer)

	// Add one item
	val := 100
	db.Add(ctx, []Item[int, int]{{Key: 1, Value: &val}})

	// Try to get value for non-existent key
	items := []Item[int, int]{{Key: 999}}
	jsonStr, err := db.GetValues(ctx, items)
	if err != nil {
		t.Fatalf("GetValues failed: %v", err)
	}
	// Should return item with empty value (or however it's marshaled)
	if jsonStr == "" {
		t.Errorf("GetValues returned empty string")
	}

	trans.Commit(ctx)
}

func TestJsonDBAnyKey_EdgeCases(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_edge"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_edge")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "edge_store"}
	comparer := func(a, b int) int { return a - b }
	db, _ := NewJsonBtree[int, int](ctx, d, so, trans, comparer)

	// 1. GetKeys on empty tree
	_, err := db.GetKeys(ctx, PagingInfo{PageSize: 5})
	if err == nil || err.Error() != "can't fetch from an empty btree" {
		t.Errorf("Expected empty btree error, got: %v", err)
	}

	// GetItems on empty tree
	_, err = db.GetItems(ctx, PagingInfo{PageSize: 5})
	if err == nil || err.Error() != "can't fetch from an empty btree" {
		t.Errorf("Expected empty btree error for GetItems, got: %v", err)
	}

	// 2. GetKeys with PageOffset != 0 on uninitialized cursor
	_, err = db.GetKeys(ctx, PagingInfo{PageSize: 5, PageOffset: 1})
	if err == nil {
		t.Errorf("Expected error for PageOffset != 0 without cursor")
	}

	// GetItems with PageOffset != 0 on uninitialized cursor
	_, err = db.GetItems(ctx, PagingInfo{PageSize: 5, PageOffset: 1})
	if err == nil {
		t.Errorf("Expected error for GetItems PageOffset != 0 without cursor")
	}

	// Add items
	for i := 0; i < 10; i++ {
		val := i
		db.Add(ctx, []Item[int, int]{{Key: i, Value: &val}})
	}

	// 3. GetKeys reaching end
	// PageSize=5, PageOffset=2 (skip 10 items), should fail as there are only 10 items
	// Wait, PageOffset=1 skips 1 page (5 items). PageOffset=2 skips 2 pages (10 items).
	// So it tries to move Next 10 times.
	// If it reaches end, it returns error.
	_, err = db.GetKeys(ctx, PagingInfo{PageSize: 5, PageOffset: 2, Direction: Forward})
	if err == nil {
		t.Errorf("Expected error reaching end of btree")
	}

	// 4. GetValues with ID
	// First get an item to get its ID
	db.First(ctx)
	k := db.BtreeInterface.GetCurrentKey()
	// Now fetch by ID
	itemWithID := Item[int, int]{Key: k.Key, ID: uuid.UUID(k.ID)}
	jsonStr, err := db.GetValues(ctx, []Item[int, int]{itemWithID})

	if err != nil {
		t.Errorf("GetValues with ID failed: %v", err)
	}
	if jsonStr == "" {
		t.Errorf("GetValues with ID returned empty")
	}

	trans.Commit(ctx)
}

func TestJsonDBAnyKey_Open_Fallback(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_open_fallback"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_open_fallback")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "fallback_store"}
	// Create with NewBtree (no replication)
	// We can use NewJsonBtree because we know it falls back to NewBtree if transaction has no replication.
	// And we are using NewTransaction (no replication).
	_, err := NewJsonBtree[string, string](ctx, d, so, trans, nil)
	if err != nil {
		t.Fatalf("NewJsonBtree failed: %v", err)
	}
	trans.Commit(ctx)

	// Open with OpenJsonBtree
	d = sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_open_fallback"},
		CacheType:     sop.InMemory,
	}
	trans, _ = database.BeginTransaction(ctx, d, sop.ForReading)
	// trans.Begin(ctx)

	// This should try OpenBtreeWithReplication -> fail -> OpenBtree -> success
	db, err := OpenJsonBtree[string, string](ctx, d, "fallback_store", trans, nil)
	if err != nil {
		t.Fatalf("OpenJsonBtree fallback failed: %v", err)
	}
	if db == nil {
		t.Error("OpenJsonBtree returned nil")
	}
	trans.Commit(ctx)
}

func TestJsonDBAnyKey_GetItems_AutoNavigate(t *testing.T) {
	ctx := context.Background()
	d := sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_autonav"},
		CacheType:     sop.InMemory,
	}
	trans, _ := database.BeginTransaction(ctx, d, sop.ForWriting)
	defer func() {
		os.RemoveAll("test_jsondb_autonav")
	}()
	// trans.Begin(ctx)

	so := sop.StoreOptions{Name: "autonav_store"}
	db, _ := NewJsonBtree[int, int](ctx, d, so, trans, nil)

	// Add items
	val := 1
	db.Add(ctx, []Item[int, int]{{Key: 1, Value: &val}})
	trans.Commit(ctx)

	d = sop.DatabaseOptions{
		StoresFolders: []string{"test_jsondb_autonav"},
		CacheType:     sop.InMemory,
	}
	trans, _ = database.BeginTransaction(ctx, d, sop.ForReading)
	// trans.Begin(ctx)
	db, _ = OpenJsonBtree[int, int](ctx, d, "autonav_store", trans, nil)

	// Call GetItems without First/Last
	itemsJson, err := db.GetItems(ctx, PagingInfo{PageSize: 1})
	if err != nil {
		t.Fatalf("GetItems AutoNavigate failed: %v", err)
	}
	if itemsJson == "" {
		t.Errorf("GetItems AutoNavigate returned empty")
	}

	trans.Commit(ctx)
}
