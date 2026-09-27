package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
)

func TestBtree_Getters(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_getters",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items
	itemsPayload := `{"items": [
		{"key": {"id": 1}, "value": "val1"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Count
	count, errStr := GetBtreeItemCountForTest(actionPayload)
	if errStr != "" {
		t.Errorf("Count failed: %s", errStr)
	}
	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	// Move First
	NavigateBtreeForTest(ctxID, First, actionPayload, "")

	// GetCurrentKey
	res, errStr := GetFromBtreeForTest(ctxID, GetCurrentKey, actionPayload, "{}")
	if errStr != "" {
		t.Errorf("GetCurrentKey failed: %s", errStr)
	}
	if !strings.Contains(res, `"id":1`) && !strings.Contains(res, `"id": 1`) {
		t.Errorf("Expected key containing id:1, got %s", res)
	}

	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestBtree_Getters_More_Coverage(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// 1. Complex Key (JsonDBAnyKey)
	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_getters_more",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items
	itemsPayload := `{"items": [{"key": {"id": 1}, "value": "val1"}, {"key": {"id": 2}, "value": "val2"}]}`
	res := ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)
	if res != "true" {
		t.Errorf("Add failed: %s", res)
	}

	// GetStoreInfo
	res, _ = GetFromBtreeForTest(ctxID, GetStoreInfo, actionPayload, "")
	if res == "" {
		t.Error("GetStoreInfo failed")
	}

	// GetKeys
	pagingPayload := `{"page_size": 10}`
	res, err := GetFromBtreeForTest(ctxID, GetKeys, actionPayload, pagingPayload)
	if res == "" || err != "" {
		t.Errorf("GetKeys failed: res='%s', err='%s'", res, err)
	}

	// GetItems
	res, err = GetFromBtreeForTest(ctxID, GetItems, actionPayload, pagingPayload)
	if res == "" || err != "" {
		t.Errorf("GetItems failed: res='%s', err='%s'", res, err)
	}

	// MoveTo First
	NavigateBtreeForTest(ctxID, First, actionPayload, "")

	// GetCurrentKey
	// Payload2 must be valid JSON for PagingInfo, even if empty
	res, err = GetFromBtreeForTest(ctxID, GetCurrentKey, actionPayload, "{}")
	if res == "" || err != "" {
		t.Errorf("GetCurrentKey failed: res='%s', err='%s'", res, err)
	}

	// MoveTo Next
	NavigateBtreeForTest(ctxID, Next, actionPayload, "")

	// MoveTo Previous
	NavigateBtreeForTest(ctxID, Previous, actionPayload, "")

	// MoveTo Last
	NavigateBtreeForTest(ctxID, Last, actionPayload, "")

	// Find
	findPayload := `{"items": [{"key": {"id": 1}}]}`
	NavigateBtreeForTest(ctxID, Find, actionPayload, findPayload)

	// GetBtreeItemCount
	count, _ := GetBtreeItemCountForTest(actionPayload)
	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}

	// 2. Primitive Key (JsonDBMapKey)
	transID2 := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)
	btreeOptsPrim := fmt.Sprintf(`{
		"name": "test_btree_getters_more_prim",
		"transaction_id": "%s",
		"is_primitive_key": true
	}`, transID2)
	btreeIDPrim := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOptsPrim)
	actionPayloadPrim := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID2, btreeIDPrim)

	// Add items
	itemsPayloadPrim := `{"items": [{"key": "key1", "value": "val1"}, {"key": "key2", "value": "val2"}]}`
	res = ManageBtreeForTest(ctxID, Add, actionPayloadPrim, itemsPayloadPrim)
	if res != "true" {
		t.Errorf("Add Prim failed: %s", res)
	}

	// GetStoreInfo
	res, _ = GetFromBtreeForTest(ctxID, GetStoreInfo, actionPayloadPrim, "")
	if res == "" {
		t.Error("GetStoreInfo Prim failed")
	}

	// GetKeys
	res, err = GetFromBtreeForTest(ctxID, GetKeys, actionPayloadPrim, pagingPayload)
	if res == "" || err != "" {
		t.Errorf("GetKeys Prim failed: res='%s', err='%s'", res, err)
	}

	// GetItems
	res, err = GetFromBtreeForTest(ctxID, GetItems, actionPayloadPrim, pagingPayload)
	if res == "" || err != "" {
		t.Errorf("GetItems Prim failed: res='%s', err='%s'", res, err)
	}

	// MoveTo First
	NavigateBtreeForTest(ctxID, First, actionPayloadPrim, "")

	// MoveTo Last
	NavigateBtreeForTest(ctxID, Last, actionPayloadPrim, "")

	// MoveTo Next
	NavigateBtreeForTest(ctxID, Next, actionPayloadPrim, "")

	// MoveTo Previous
	NavigateBtreeForTest(ctxID, Previous, actionPayloadPrim, "")

	// GetCurrentKey
	res, err = GetFromBtreeForTest(ctxID, GetCurrentKey, actionPayloadPrim, "{}")
	if res == "" || err != "" {
		t.Errorf("GetCurrentKey Prim failed: res='%s', err='%s'", res, err)
	}

	// MoveTo Next
	NavigateBtreeForTest(ctxID, Next, actionPayloadPrim, "")

	// MoveTo Previous
	NavigateBtreeForTest(ctxID, Previous, actionPayloadPrim, "")

	// MoveTo Last
	NavigateBtreeForTest(ctxID, Last, actionPayloadPrim, "")

	// Find
	findPayloadPrim := `{"items": [{"key": "key1"}]}`
	NavigateBtreeForTest(ctxID, Find, actionPayloadPrim, findPayloadPrim)

	// GetBtreeItemCount
	count, _ = GetBtreeItemCountForTest(actionPayloadPrim)
	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}
}

func TestBtree_Getters_Out(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	btreeOpts := fmt.Sprintf(`{
		"name": "test_btree_getters_out",
		"transaction_id": "%s",
		"is_primitive_key": false
	}`, transID)

	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreeOpts)
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "btree_id": "%s"}`, transID, btreeID)

	// Add items
	itemsPayload := `{"items": [
		{"key": {"id": 1}, "value": "val1"}
	]}`
	ManageBtreeForTest(ctxID, Add, actionPayload, itemsPayload)

	// Count Out
	count, errStr := GetBtreeItemCountOutForTest(actionPayload)
	if errStr != "" {
		t.Errorf("Count Out failed: %s", errStr)
	}
	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	// Move First
	NavigateBtreeForTest(ctxID, First, actionPayload, "")

	// GetCurrentKey Out
	res, errStr := GetFromBtreeOutForTest(ctxID, GetCurrentKey, actionPayload, "{}")
	if errStr != "" {
		t.Errorf("GetCurrentKey Out failed: %s", errStr)
	}
	if res == "" {
		t.Error("GetCurrentKey Out returned empty result")
	}
}

func TestBtree_Getters_Coverage_Get_InvalidPayload2(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB, Trans, Btree
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dir},
	}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	transOpts := sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{dir},
	}
	transOptsJSON, _ := json.Marshal(transOpts)
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, string(transOptsJSON))
	txID, _ := sop.ParseUUID(txIDStr)

	b3o := BtreeOptions{
		Name:          "test_btree_get_invalid_payload",
		TransactionID: uuid.UUID(txID),
	}
	b3oJSON, _ := json.Marshal(b3o)
	b3IDStr := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oJSON))
	if strings.HasPrefix(b3IDStr, "error") {
		t.Fatalf("NewBtree failed: %s", b3IDStr)
	}
	b3ID, _ := sop.ParseUUID(b3IDStr)

	// Prepare payload
	payload := map[string]string{
		"transaction_id": txID.String(),
		"btree_id":       b3ID.String(),
	}
	payloadJSON, _ := json.Marshal(payload)

	// Test GetKeys with invalid payload2
	res, errStr := GetFromBtreeForTest(ctx, GetKeys, string(payloadJSON), "{invalid-json}")
	if !strings.HasPrefix(errStr, "error Unmarshal keys array") {
		t.Errorf("Expected error 'error Unmarshal keys array...', got '%s', res: '%s'", errStr, res)
	}
}

func TestBtree_Getters_Coverage_GetValues_InvalidPayload2(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB, Trans, Btree
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dir},
	}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	transOpts := sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{dir},
	}
	transOptsJSON, _ := json.Marshal(transOpts)
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, string(transOptsJSON))
	txID, _ := sop.ParseUUID(txIDStr)

	b3o := BtreeOptions{
		Name:          "test_btree_getvalues_invalid_payload",
		TransactionID: uuid.UUID(txID),
	}
	b3oJSON, _ := json.Marshal(b3o)
	b3IDStr := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oJSON))
	if strings.HasPrefix(b3IDStr, "error") {
		t.Fatalf("NewBtree failed: %s", b3IDStr)
	}
	b3ID, _ := sop.ParseUUID(b3IDStr)

	// Prepare payload
	payload := map[string]string{
		"transaction_id": txID.String(),
		"btree_id":       b3ID.String(),
	}
	payloadJSON, _ := json.Marshal(payload)

	// Test GetValues with invalid payload2
	res, errStr := GetFromBtreeForTest(ctx, GetValues, string(payloadJSON), "{invalid-json}")
	if !strings.HasPrefix(errStr, "error Unmarshal keys array") {
		t.Errorf("Expected error 'error Unmarshal keys array...', got '%s', res: '%s'", errStr, res)
	}
}

func TestBtree_Getters_Coverage_Find_InvalidPayload2(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB, Trans, Btree
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dir},
	}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	transOpts := sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{dir},
	}
	transOptsJSON, _ := json.Marshal(transOpts)
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, string(transOptsJSON))
	txID, _ := sop.ParseUUID(txIDStr)

	b3o := BtreeOptions{
		Name:          "test_btree_find_invalid_payload",
		TransactionID: uuid.UUID(txID),
	}
	b3oJSON, _ := json.Marshal(b3o)
	b3IDStr := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oJSON))
	if strings.HasPrefix(b3IDStr, "error") {
		t.Fatalf("NewBtree failed: %s", b3IDStr)
	}
	b3ID, _ := sop.ParseUUID(b3IDStr)

	// Prepare payload
	payload := map[string]string{
		"transaction_id": txID.String(),
		"btree_id":       b3ID.String(),
	}
	payloadJSON, _ := json.Marshal(payload)

	// Test Find with invalid payload2
	res := NavigateBtreeForTest(ctx, Find, string(payloadJSON), "{invalid-json}")
	if !strings.HasPrefix(res, "error Unmarshal keys array") {
		t.Errorf("Expected error 'error Unmarshal keys array...', got '%s'", res)
	}
}

func TestBtree_Getters_Coverage_MoveTo_Error(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB, Trans, Btree
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dir},
	}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	transOpts := sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{dir},
	}
	transOptsJSON, _ := json.Marshal(transOpts)
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, string(transOptsJSON))
	txID, _ := sop.ParseUUID(txIDStr)

	b3o := BtreeOptions{
		Name:          "test_btree_moveto_error",
		TransactionID: uuid.UUID(txID),
	}
	b3oJSON, _ := json.Marshal(b3o)
	b3IDStr := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oJSON))
	if strings.HasPrefix(b3IDStr, "error") {
		t.Fatalf("NewBtree failed: %s", b3IDStr)
	}
	b3ID, _ := sop.ParseUUID(b3IDStr)

	// Prepare payload
	payload := map[string]string{
		"transaction_id": txID.String(),
		"btree_id":       b3ID.String(),
	}
	payloadJSON, _ := json.Marshal(payload)

	// Close transaction to trigger error in Next
	ManageTransactionForTest(ctx, Rollback, txIDStr)

	// Test Next with closed transaction
	res := NavigateBtreeForTest(ctx, Next, string(payloadJSON), "")
	if !strings.HasPrefix(res, "error moving cursor to Next item of B-tree") {
		t.Errorf("Expected error 'error moving cursor to Next item of B-tree...', got '%s'", res)
	}
}

func TestBtree_Getters_ErrorPaths_Extended(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// Setup DB and Transaction
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Create Btree (Map Key)
	btreePayload := fmt.Sprintf(`{"name": "test_btree_get_err", "is_unique": false, "is_user_data_segment": false, "is_user_data_segment_loaded": false, "is_user_data_segment_modified": false, "index_specification": "", "transaction_id": "%s"}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreePayload)
	metaPayload := fmt.Sprintf(`{"btree_id": "%s", "transaction_id": "%s"}`, btreeID, transID)

	// 1. navigateBtree: Unknown action
	res := NavigateBtreeForTest(ctxID, 999, metaPayload, "")
	if !strings.Contains(res, "unsupported manage action") {
		t.Errorf("navigateBtree with unknown action should fail, got: %s", res)
	}

	// 2. isUniqueBtree: Invalid metadata
	res = IsUniqueBtreeForTest("invalid-json")
	if !strings.Contains(res, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("isUniqueBtree with invalid metadata should fail, got: %s", res)
	}

	// 3. getBtreeItemCount: Invalid metadata
	_, errStr := GetBtreeItemCountForTest("invalid-json")
	if !strings.Contains(errStr, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("getBtreeItemCount with invalid metadata should fail, got: %s", errStr)
	}

	// 4. getFromBtree: Unknown action
	_, errStr = GetFromBtreeForTest(ctxID, 999, metaPayload, "")
	if !strings.Contains(errStr, "unsupported manage action") {
		t.Errorf("getFromBtree with unknown action should fail, got: %s", errStr)
	}

	// 5. getFromBtree: Context not found
	_, errStr = GetFromBtreeForTest(99999, GetKeys, metaPayload, "")
	if !strings.Contains(errStr, "context with ID 99999 not found") {
		t.Errorf("getFromBtree with invalid context should fail, got: %s", errStr)
	}

	// 6. getStoreInfo: Invalid metadata
	_, errStr = GetStoreInfoForTest("invalid-json")
	if !strings.Contains(errStr, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("getStoreInfo with invalid metadata should fail, got: %s", errStr)
	}

	// 7. get: Invalid metadata
	_, errStr = GetFromBtreeForTest(ctxID, GetKeys, "invalid-json", "")
	if !strings.Contains(errStr, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("get with invalid metadata should fail, got: %s", errStr)
	}

	// 8. get: Invalid payload2 (Unmarshal failure)
	_, errStr = GetFromBtreeForTest(ctxID, GetKeys, metaPayload, "invalid-json")
	if !strings.Contains(errStr, "error Unmarshal keys array") {
		t.Errorf("get with invalid payload2 should fail, got: %s", errStr)
	}

	// 9. getValues: Invalid metadata
	_, errStr = GetValuesForTest(ctxID, "invalid-json", "")
	if !strings.Contains(errStr, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("getValues with invalid metadata should fail, got: %s", errStr)
	}

	// 10. getValues: Invalid payload2 (Unmarshal failure)
	_, errStr = GetValuesForTest(ctxID, metaPayload, "invalid-json")
	if !strings.Contains(errStr, "error Unmarshal keys array") {
		t.Errorf("getValues with invalid payload2 should fail, got: %s", errStr)
	}

	// 11. find: Invalid metadata
	res = NavigateBtreeForTest(ctxID, Find, "invalid-json", "")
	if !strings.Contains(res, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("find with invalid metadata should fail, got: %s", res)
	}

	// 12. find: Invalid payload2 (Unmarshal failure)
	res = NavigateBtreeForTest(ctxID, Find, metaPayload, "invalid-json")
	if !strings.Contains(res, "error Unmarshal keys array") {
		t.Errorf("find with invalid payload2 should fail, got: %s", res)
	}

	// 13. moveTo: Invalid metadata
	res = NavigateBtreeForTest(ctxID, First, "invalid-json", "")
	if !strings.Contains(res, "error Unmarshal ManageBtreeMetaData") {
		t.Errorf("moveTo with invalid metadata should fail, got: %s", res)
	}

	// Primitive Key tests for Unmarshal failure
	btreePrimPayload := fmt.Sprintf(`{"name": "test_btree_get_err_prim", "is_unique": false, "is_user_data_segment": false, "is_user_data_segment_loaded": false, "is_user_data_segment_modified": false, "index_specification": "", "transaction_id": "%s", "is_primitive_key": true}`, transID)
	btreePrimID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreePrimPayload)
	metaPrimPayload := fmt.Sprintf(`{"btree_id": "%s", "transaction_id": "%s", "is_primitive_key": true}`, btreePrimID, transID)

	// 14. get (prim): Invalid payload2
	_, errStr = GetFromBtreeForTest(ctxID, GetKeys, metaPrimPayload, "invalid-json")
	if !strings.Contains(errStr, "error Unmarshal keys array") {
		t.Errorf("get (prim) with invalid payload2 should fail, got: %s", errStr)
	}

	// 15. getValues (prim): Invalid payload2
	_, errStr = GetValuesForTest(ctxID, metaPrimPayload, "invalid-json")
	if !strings.Contains(errStr, "error Unmarshal keys array") {
		t.Errorf("getValues (prim) with invalid payload2 should fail, got: %s", errStr)
	}

	// 16. find (prim): Invalid payload2
	res = NavigateBtreeForTest(ctxID, Find, metaPrimPayload, "invalid-json")
	if !strings.Contains(res, "error Unmarshal keys array") {
		t.Errorf("find (prim) with invalid payload2 should fail, got: %s", res)
	}
}
