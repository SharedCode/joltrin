package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/jsondb"
)

func TestBtree_Add_Find(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// Create DB
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	if dbID == "" {
		t.Fatal("Failed to create DB")
	}

	// Start Trans
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)
	if transID == "" {
		t.Fatal("Failed to start transaction")
	}

	// Create Btree
	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	if btreeID == "" {
		t.Fatal("Failed to create Btree")
	}
	t.Logf("TransID: %s, Len: %d", transID, len(transID))
	t.Logf("BtreeID: %s, Len: %d", btreeID, len(btreeID))

	// Add Item
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)
	t.Logf("ActionPayload: %s", actionPayload)
	itemsPayload := `{"items": [{"key": {"id": 1}, "value": "test_value"}]}`

	res := ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)
	if res != "true" {
		t.Errorf("Expected 'true', got '%s'", res)
	}

	// Find Item
	// For Find, payload2 is ManageBtreePayload with Items containing the key to find.
	keyPayload := `{"items": [{"key": {"id": 1}}]}`

	resFind := NavigateBtreeForTest(ctxID, Find, actionPayload, keyPayload)
	if resFind != "true" {
		t.Errorf("Expected 'true', got '%s'", resFind)
	}

	// Commit
	errStr := ManageTransactionForTest(ctxID, Commit, transID)
	if errStr != "" {
		t.Errorf("Commit failed: %s", errStr)
	}
}

func TestBtree_PrimitiveKey(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Create Btree with Primitive Key
	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_prim",
		"transaction_id": "%s",
		"is_primitive_key": true
	}`, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	if btreeID == "" {
		t.Fatal("Failed to create Btree")
	}

	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)
	// For primitive key, key is just the value, e.g. string or int.
	// But items payload expects []Item.
	// Item has Key and Value.
	// If Key is primitive, it should be a JSON primitive?
	// Let's try string key.
	itemsPayload := `{"items": [{"key": "key1", "value": "value1"}]}`

	res := ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)
	if res != "true" {
		t.Errorf("Expected 'true', got '%s'", res)
	}

	// Find
	keyPayload := `{"items": [{"key": "key1"}]}`
	resFind := NavigateBtreeForTest(ctxID, Find, actionPayload, keyPayload)
	if resFind != "true" {
		t.Errorf("Expected 'true', got '%s'", resFind)
	}

	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestBtree_Navigation_Update_Remove(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_nav",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items
	itemsPayload := `{"items": [
		{"key": {"id": 1}, "value": "val1"},
		{"key": {"id": 2}, "value": "val2"},
		{"key": {"id": 3}, "value": "val3"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Navigation
	// First
	if res := NavigateBtreeForTest(ctxID, First, actionPayload, ""); res != "true" {
		t.Errorf("First failed: %s", res)
	}
	// Next
	if res := NavigateBtreeForTest(ctxID, Next, actionPayload, ""); res != "true" {
		t.Errorf("Next failed: %s", res)
	}
	// Previous
	if res := NavigateBtreeForTest(ctxID, Previous, actionPayload, ""); res != "true" {
		t.Errorf("Previous failed: %s", res)
	}
	// Last
	if res := NavigateBtreeForTest(ctxID, Last, actionPayload, ""); res != "true" {
		t.Errorf("Last failed: %s", res)
	}

	// Update
	updatePayload := `{"items": [{"key": {"id": 2}, "value": "val2_updated"}]}`
	if res := ManageBtreeForTest(ctxID, Update, actionPayload, updatePayload); res != "true" {
		t.Errorf("Update failed: %s", res)
	}

	// Remove
	// Remove expects array of keys, not ManageBtreePayload
	removePayload := `[{"id": 1}]`
	if res := ManageBtreeForTest(ctxID, Remove, actionPayload, removePayload); res != "true" {
		t.Errorf("Remove failed: %s", res)
	}

	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestBtree_Extended(t *testing.T) {
	// 1. Create Context
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// 2. Create DB
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	if dbID == "" {
		t.Fatal("Failed to create DB")
	}

	// 3. Start Transaction
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)
	if transID == "" {
		t.Fatal("Failed to start transaction")
	}

	// 4. Create Btree
	btreeName := "test_btree_ext"
	btreeOpts := fmt.Sprintf(`{
		"name": "%s",
		"transaction_id": "%s",
		"is_unique": true,
		"is_primitive_key": true
	}`, btreeName, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	if btreeID == "" {
		t.Fatal("Failed to create Btree")
	}

	// 5. Add Items
	val := "value1"
	item1 := jsondb.Item[string, string]{
		Key:   "key1",
		Value: &val,
	}
	items := []jsondb.Item[string, string]{item1}
	itemsBytes, _ := json.Marshal(items)

	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)
	itemsPayload := fmt.Sprintf(`{"items": %s}`, string(itemsBytes))

	res := ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)
	if res != "true" {
		t.Errorf("ManageBtreeForTest(Add) returned %s, expected true", res)
	}

	// 6. Test IsUniqueBtree
	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)
	resUnique := IsUniqueBtreeForTest(metaPayload)
	if resUnique != "true" {
		t.Errorf("IsUniqueBtreeForTest returned %s, expected true", resUnique)
	}

	// 7. Test GetStoreInfo
	siRes, siErr := GetStoreInfoForTest(metaPayload)
	if siErr != "" {
		t.Errorf("GetStoreInfoForTest returned error: %s", siErr)
	}
	if siRes == "" {
		t.Error("GetStoreInfoForTest returned empty result")
	}

	// 8. Test GetValues
	keysPayload := `{"items": [{"key": "key1"}]}`
	valRes, valErr := GetValuesForTest(ctxID, metaPayload, keysPayload)
	if valErr != "" {
		t.Errorf("GetValuesForTest returned error: %s", valErr)
	}
	if valRes == "" {
		t.Error("GetValuesForTest returned empty result")
	}
	// Expected result is array of values: ["value1"]
	// But GetValues returns JSON array of values.
	// Let's just check it's not empty for now.

	// 9. Test Update
	val2 := "value2"
	item2 := jsondb.Item[string, string]{
		Key:   "key1",
		Value: &val2,
	}
	items2 := []jsondb.Item[string, string]{item2}
	itemsBytes2, _ := json.Marshal(items2)
	updatePayload := fmt.Sprintf(`{"items": %s}`, string(itemsBytes2))

	resUpdate := ManageBtreeForTest(ctxID, Update, actionPayload, updatePayload) // Update
	if resUpdate != "true" {
		t.Errorf("ManageBtreeForTest(Update) returned %s, expected true", resUpdate)
	}

	// 10. Test Upsert
	val3 := "value3"
	item3 := jsondb.Item[string, string]{
		Key:   "key1",
		Value: &val3,
	}
	items3 := []jsondb.Item[string, string]{item3}
	itemsBytes3, _ := json.Marshal(items3)
	upsertPayload := fmt.Sprintf(`{"items": %s}`, string(itemsBytes3))

	resUpsert := ManageBtreeForTest(ctxID, Upsert, actionPayload, upsertPayload) // Upsert
	if resUpsert != "true" {
		t.Errorf("ManageBtreeForTest(Upsert) returned %s, expected true", resUpsert)
	}

	// 11. Test Remove
	// Remove expects payload2 with keys to remove (JSON array of keys)
	removePayload := `["key1"]`
	resRemove := ManageBtreeForTest(ctxID, Remove, actionPayload, removePayload) // Remove
	if resRemove != "true" {
		t.Errorf("ManageBtreeForTest(Remove) returned %s, expected true", resRemove)
	}

	// 12. Commit
	commitPayload := fmt.Sprintf(`{"transaction_id": "%s", "action": %d}`, transID, 2) // Commit
	ManageTransactionForTest(ctxID, 2, commitPayload)
}

func TestBtree_Navigation_Extended(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_nav_ext",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items: key1, key2, key3
	itemsPayload := `{"items": [
		{"key": {"id": 1}, "value": "val1"},
		{"key": {"id": 2}, "value": "val2"},
		{"key": {"id": 3}, "value": "val3"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Move First
	res := NavigateBtreeForTest(ctxID, First, actionPayload, "")
	if res != "true" {
		t.Error("First failed")
	}

	// Move Next
	res = NavigateBtreeForTest(ctxID, Next, actionPayload, "")
	if res != "true" {
		t.Error("Next failed")
	}

	// Move Last
	res = NavigateBtreeForTest(ctxID, Last, actionPayload, "")
	if res != "true" {
		t.Error("Last failed")
	}

	// Move Previous
	res = NavigateBtreeForTest(ctxID, Previous, actionPayload, "")
	if res != "true" {
		t.Error("Previous failed")
	}

	// Find (Search)
	searchPayload := `{"items": [{"key": {"id": 2}}]}`
	res = NavigateBtreeForTest(ctxID, Find, actionPayload, searchPayload)
	if res != "true" {
		t.Error("Find failed")
	}
}

func TestBtree_Getters_Extended(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_getters_ext",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add item
	itemsPayload := `{"items": [
		{"key": {"id": 1}, "value": "val1"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Move First
	NavigateBtreeForTest(ctxID, First, actionPayload, "")

	// GetCurrentKey
	res, _ := GetFromBtreeForTest(ctxID, GetCurrentKey, actionPayload, "{}")
	if res == "" {
		t.Error("GetCurrentKey returned empty")
	}

	// GetValues
	keysPayload := `{"items": [{"key": {"id": 1}}]}`
	res, _ = GetFromBtreeForTest(ctxID, GetValues, actionPayload, keysPayload)
	if res == "" {
		t.Error("GetValues returned empty")
	}

	// GetItems (Range)
	pagingPayload := `{"fetch_count": 10}`
	res, _ = GetFromBtreeForTest(ctxID, GetItems, actionPayload, pagingPayload)
	if res == "" {
		t.Error("GetItems returned empty")
	}
}

func TestBtree_Navigation_Primitive(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_nav_prim",
		"transaction_id": "%s",
		"is_primitive_key": true
	}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items: key1, key2, key3
	itemsPayload := `{"items": [
		{"key": "key1", "value": "val1"},
		{"key": "key2", "value": "val2"},
		{"key": "key3", "value": "val3"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Move First
	res := NavigateBtreeForTest(ctxID, First, actionPayload, "")
	if res != "true" {
		t.Error("First failed")
	}

	// Move Next
	res = NavigateBtreeForTest(ctxID, Next, actionPayload, "")
	if res != "true" {
		t.Error("Next failed")
	}

	// Move Last
	res = NavigateBtreeForTest(ctxID, Last, actionPayload, "")
	if res != "true" {
		t.Error("Last failed")
	}

	// Move Previous
	res = NavigateBtreeForTest(ctxID, Previous, actionPayload, "")
	if res != "true" {
		t.Error("Previous failed")
	}

	// Find (Search)
	searchPayload := `{"items": [{"key": "key2"}]}`
	res = NavigateBtreeForTest(ctxID, Find, actionPayload, searchPayload)
	if res != "true" {
		t.Error("Find failed")
	}
}
