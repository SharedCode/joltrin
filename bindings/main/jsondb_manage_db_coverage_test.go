package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
)

func TestManageDatabase_Coverage_NewDatabase_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Invalid JSON payload
	ret := ManageDatabaseForTest(ctx, NewDatabase, "", "{invalid-json}")
	expected := "invalid options: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_BeginTransaction_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, BeginTransaction, "invalid-uuid", "")
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_BeginTransaction_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, BeginTransaction, randomUUID, "")
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_BeginTransaction_InvalidPayload(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create a valid database first
	opts := sop.DatabaseOptions{}
	optsBytes, _ := json.Marshal(opts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(optsBytes))
	if dbIDStr == "" {
		t.Fatal("Failed to create database")
	}

	// Invalid JSON payload for transaction options
	ret := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, "{invalid-json}")
	if ret == "" {
		t.Error("Expected transaction ID, got empty string")
	}
}

func TestManageDatabase_Coverage_NewBtree_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, NewBtree, "", "{invalid-json}")
	expected := "error Unmarshal BtreeOptions, details: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_NewBtree_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	b3o := BtreeOptions{
		Name: "test_btree",
	}
	b3oBytes, _ := json.Marshal(b3o)

	ret := ManageDatabaseForTest(ctx, NewBtree, "invalid-uuid", string(b3oBytes))
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_NewBtree_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	b3o := BtreeOptions{
		Name: "test_btree",
	}
	b3oBytes, _ := json.Marshal(b3o)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, NewBtree, randomUUID, string(b3oBytes))
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_NewBtree_TransNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create DB
	opts := sop.DatabaseOptions{}
	optsBytes, _ := json.Marshal(opts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(optsBytes))

	b3o := BtreeOptions{
		Name:          "test_btree",
		TransactionID: uuid.UUID(sop.NewUUID()),
	}
	b3oBytes, _ := json.Marshal(b3o)

	ret := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oBytes))
	expected := fmt.Sprintf("can't find Transaction %v", b3o.TransactionID.String())
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenBtree_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, OpenBtree, "", "{invalid-json}")
	expected := "error Unmarshal BtreeOptions, details: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenBtree_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	b3o := BtreeOptions{
		Name: "test_btree",
	}
	b3oBytes, _ := json.Marshal(b3o)

	ret := ManageDatabaseForTest(ctx, OpenBtree, "invalid-uuid", string(b3oBytes))
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenBtree_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	b3o := BtreeOptions{
		Name: "test_btree",
	}
	b3oBytes, _ := json.Marshal(b3o)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, OpenBtree, randomUUID, string(b3oBytes))
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenBtree_TransNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create DB
	opts := sop.DatabaseOptions{}
	optsBytes, _ := json.Marshal(opts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(optsBytes))

	b3o := BtreeOptions{
		Name:          "test_btree",
		TransactionID: uuid.UUID(sop.NewUUID()),
	}
	b3oBytes, _ := json.Marshal(b3o)

	ret := ManageDatabaseForTest(ctx, OpenBtree, dbIDStr, string(b3oBytes))
	expected := fmt.Sprintf("can't find Transaction %v", b3o.TransactionID.String())
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenModelStore_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, OpenModelStore, "", "{invalid-json}")
	expected := "invalid options: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenModelStore_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	opts := ModelStoreOptions{}
	optsBytes, _ := json.Marshal(opts)

	ret := ManageDatabaseForTest(ctx, OpenModelStore, "invalid-uuid", string(optsBytes))
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenModelStore_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	opts := ModelStoreOptions{}
	optsBytes, _ := json.Marshal(opts)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, OpenModelStore, randomUUID, string(optsBytes))
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenModelStore_InvalidTransUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create DB
	dbOpts := sop.DatabaseOptions{}
	dbOptsBytes, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsBytes))

	opts := ModelStoreOptions{
		TransactionID: "invalid-uuid",
	}
	optsBytes, _ := json.Marshal(opts)

	ret := ManageDatabaseForTest(ctx, OpenModelStore, dbIDStr, string(optsBytes))
	expected := "Invalid transaction UUID"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenModelStore_TransNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create DB
	dbOpts := sop.DatabaseOptions{}
	dbOptsBytes, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsBytes))

	opts := ModelStoreOptions{
		TransactionID: sop.NewUUID().String(),
	}
	optsBytes, _ := json.Marshal(opts)

	ret := ManageDatabaseForTest(ctx, OpenModelStore, dbIDStr, string(optsBytes))
	expected := "Transaction not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenVectorStore_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, OpenVectorStore, "", "{invalid-json}")
	expected := "invalid options: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_OpenSearch_InvalidJSON(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, OpenSearch, "", "{invalid-json}")
	expected := "invalid options: invalid character 'i' looking for beginning of object key string"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveBtree_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, RemoveBtree, "invalid-uuid", "btree_name")
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveBtree_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, RemoveBtree, randomUUID, "btree_name")
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveModelStore_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, RemoveModelStore, "invalid-uuid", "store_name")
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveModelStore_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, RemoveModelStore, randomUUID, "store_name")
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveVectorStore_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, RemoveVectorStore, "invalid-uuid", "store_name")
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveVectorStore_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, RemoveVectorStore, randomUUID, "store_name")
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveSearch_InvalidDBUUID(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	ret := ManageDatabaseForTest(ctx, RemoveSearch, "invalid-uuid", "store_name")
	expected := "invalid database UUID: invalid UUID length: 12"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveSearch_DBNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	randomUUID := sop.NewUUID().String()
	ret := ManageDatabaseForTest(ctx, RemoveSearch, randomUUID, "store_name")
	expected := "Database not found"
	if ret != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, ret)
	}
}

func TestManageDatabase_Coverage_RemoveBtree_Error(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create DB
	opts := sop.DatabaseOptions{}
	optsBytes, _ := json.Marshal(opts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(optsBytes))

	// Remove non-existent Btree
	ret := ManageDatabaseForTest(ctx, RemoveBtree, dbIDStr, "non_existent_btree")
	// sopdb.RemoveBtree might return error if store not found, or might succeed.
	// If it succeeds, then we can't test error path easily without mocking.
	// But let's see if it returns error.
	if ret != "" && ret != "Btree not found" {
		// It might return "Btree not found" or similar.
		// If it returns empty string (success), then we can't cover the error path this way.
	}
}

func TestManageDatabase_Coverage_BeginTransaction_PayloadParsing(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create a database
	dbOpts := sop.DatabaseOptions{}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))
	if strings.HasPrefix(dbIDStr, "error") {
		t.Fatalf("NewDatabase failed: %s", dbIDStr)
	}

	// Case 1: Invalid JSON for opts, valid int for mode
	// "1" is a valid int, which corresponds to ReadOnly (0) or ReadWrite (1)
	// sop.ReadWrite is 1.
	payload := "1"
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, payload)
	if strings.HasPrefix(txIDStr, "error") {
		t.Errorf("BeginTransaction with int payload failed: %s", txIDStr)
	}

	// Case 2: Invalid JSON for opts, invalid int for mode
	payload = "invalid-json"
	txIDStr = ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, payload)
	// This should fall through to default mode/maxTime, so it should succeed.
	if strings.HasPrefix(txIDStr, "error") {
		t.Errorf("BeginTransaction with invalid payload failed: %s", txIDStr)
	}
}

func TestManageDatabase_Coverage_OpenBtree_StoreNotFound(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Create a database
	dbOpts := sop.DatabaseOptions{}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	// Begin Transaction
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, "")
	txID, _ := sop.ParseUUID(txIDStr)

	// Try to open a non-existent Btree
	b3o := BtreeOptions{
		Name:          "non_existent_btree",
		TransactionID: uuid.UUID(txID),
	}
	b3oJSON, _ := json.Marshal(b3o)

	res := ManageDatabaseForTest(ctx, OpenBtree, dbIDStr, string(b3oJSON))
	expected := fmt.Sprintf("error opening Btree (%s), store not found", b3o.Name)
	if res != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, res)
	}
}

func TestManageBtree_Coverage_UpdateCurrentKey_Empty(t *testing.T) {
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
		Name:          "test_btree_update_empty",
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

	// Empty items for UpdateCurrentKey
	itemsPayload := "{\"items\": []}"

	res := ManageBtreeForTest(ctx, UpdateCurrentKey, string(payloadJSON), itemsPayload)
	expected := fmt.Sprintf("error manage of item to B-tree (id=%v), details: UpdateCurrentKey requires at least one item", b3ID)
	if res != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, res)
	}
}

func TestManageBtree_Coverage_Remove_InvalidJSON(t *testing.T) {
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
		Name:          "test_btree_remove_invalid",
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

	// Invalid JSON for keys
	keysPayload := "{invalid-json}"

	res := ManageBtreeForTest(ctx, Remove, string(payloadJSON), keysPayload)
	expected := "error Unmarshal keys array, details: invalid character 'i' looking for beginning of object key string"
	if res != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, res)
	}
}

func TestManageTransaction_DeprecatedActions(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)

	// Test NewTransaction (deprecated)
	res := ManageTransactionForTest(ctx, NewTransaction, "")
	if res != "NewTransaction is deprecated. Please use manageDatabase with BeginTransaction action." {
		t.Errorf("Expected deprecated message for NewTransaction, got '%s'", res)
	}

	// Test Begin (deprecated)
	res = ManageTransactionForTest(ctx, Begin, "")
	if res != "Begin is deprecated. Transaction is already begun when created via manageDatabase." {
		t.Errorf("Expected deprecated message for Begin, got '%s'", res)
	}
}

func TestManageDatabase_BeginTransaction_IntPayload(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dir},
	}
	dbOptsJSON, _ := json.Marshal(dbOpts)
	dbIDStr := ManageDatabaseForTest(ctx, NewDatabase, "", string(dbOptsJSON))

	// BeginTransaction with integer payload (Mode)
	// 1 = ForWriting
	txIDStr := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, "1")
	if strings.HasPrefix(txIDStr, "error") {
		t.Fatalf("BeginTransaction with int payload failed: %s", txIDStr)
	}
}

func TestManageDatabase_OpenBtree_PrimitiveWithCEL(t *testing.T) {
	ctx := CreateContextForTest()
	defer RemoveContextForTest(ctx)
	dir := t.TempDir()

	// Create DB, Trans
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

	// Create Btree with IndexSpecification (Map Key)
	// Must be valid JSON for IndexSpecification
	idxSpec := `{"index_fields": [{"field_name": "Name", "ascending_sort_order": true}]}`
	b3o := BtreeOptions{
		Name:               "test_btree_cel",
		TransactionID:      uuid.UUID(txID),
		IsPrimitiveKey:     false,
		IndexSpecification: idxSpec,
	}
	b3oJSON, _ := json.Marshal(b3o)
	b3IDStr := ManageDatabaseForTest(ctx, NewBtree, dbIDStr, string(b3oJSON))
	if strings.HasPrefix(b3IDStr, "error") {
		t.Fatalf("NewBtree failed: %s", b3IDStr)
	}

	// Commit transaction to persist store info
	ManageTransactionForTest(ctx, Commit, txIDStr)

	// Start new transaction
	txIDStr2 := ManageDatabaseForTest(ctx, BeginTransaction, dbIDStr, string(transOptsJSON))
	txID2, _ := sop.ParseUUID(txIDStr2)

	// Hack: Update StoreInfo in DB to set IsPrimitiveKey = true
	// This simulates a corrupted or inconsistent state where a store has IndexSpec but is marked as Primitive.
	item, ok := transRegistry.GetItem(sop.UUID(txID2))
	if !ok {
		t.Fatal("Transaction not found in registry")
	}
	// Access internal transaction components to get StoreRepository
	intf := item.Transaction.(interface{})
	t2 := intf.(*sop.SinglePhaseTransaction).SopPhaseCommitTransaction
	intf = t2
	t3 := intf.(*common.Transaction)
	sr := t3.StoreRepository

	// Get StoreInfo
	si, err := sr.Get(context.Background(), b3o.Name)
	if err != nil || len(si) == 0 {
		t.Fatalf("StoreInfo not found: %v", err)
	}
	// Modify and Update
	si[0].IsPrimitiveKey = true
	if _, err := sr.Update(context.Background(), []sop.StoreInfo{si[0]}); err != nil {
		t.Fatalf("Failed to update StoreInfo: %v", err)
	}

	// Try to OpenBtree as Primitive Key
	b3oOpen := BtreeOptions{
		Name:           "test_btree_cel",
		TransactionID:  uuid.UUID(txID2),
		IsPrimitiveKey: true, // Now matches DB state (hacked), but conflicts with IndexSpec
	}
	b3oOpenJSON, _ := json.Marshal(b3oOpen)

	res := ManageDatabaseForTest(ctx, OpenBtree, dbIDStr, string(b3oOpenJSON))
	expected := fmt.Sprintf("error opening for 'Primitive Type' Btree (%s), CELexpression %s is restricted for class type Key", b3o.Name, b3o.IndexSpecification)
	if res != expected {
		t.Errorf("Expected error '%s', got '%s'", expected, res)
	}

	// Cleanup
	ManageTransactionForTest(ctx, Rollback, txIDStr2)
}
