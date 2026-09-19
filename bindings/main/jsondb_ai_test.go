package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
)

func TestManageVectorDB_ErrorPaths_Extended(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// Setup DB and Transaction
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Create Vector Store
	vecPayload := fmt.Sprintf(`{"name": "test_vec_err", "transaction_id": "%s", "config": {"usage_mode": 0, "content_size": 0}}`, transID)
	vecID := ManageDatabaseForTest(ctxID, OpenVectorStore, dbID, vecPayload)
	metaPayload := fmt.Sprintf(`{"id": "%s", "transaction_id": "%s"}`, vecID, transID)

	// 1. getStore: Invalid store metadata
	res := ManageVectorDBForTest(ctxID, UpsertVector, "invalid-json", "")
	if !strings.Contains(res, "invalid store metadata") {
		t.Errorf("UpsertVector with invalid metadata should fail, got: %s", res)
	}

	// 2. getStore: Invalid transaction UUID
	invalidTransMeta := fmt.Sprintf(`{"id": "%s", "transaction_id": "invalid-uuid"}`, vecID)
	res = ManageVectorDBForTest(ctxID, UpsertVector, invalidTransMeta, "")
	if !strings.Contains(res, "invalid transaction UUID") {
		t.Errorf("UpsertVector with invalid trans UUID should fail, got: %s", res)
	}

	// 3. getStore: Invalid store UUID
	invalidStoreMeta := fmt.Sprintf(`{"id": "invalid-uuid", "transaction_id": "%s"}`, transID)
	res = ManageVectorDBForTest(ctxID, UpsertVector, invalidStoreMeta, "")
	if !strings.Contains(res, "invalid store UUID") {
		t.Errorf("UpsertVector with invalid store UUID should fail, got: %s", res)
	}

	// 4. getStore: Vector Store not found
	notFoundMeta := fmt.Sprintf(`{"id": "00000000-0000-0000-0000-000000000000", "transaction_id": "%s"}`, transID)
	res = ManageVectorDBForTest(ctxID, UpsertVector, notFoundMeta, "")
	if !strings.Contains(res, "vector store not found") {
		t.Errorf("UpsertVector with non-existent store should fail, got: %s", res)
	}

	// 5. getStore: Object is not a Vector Store
	// Create a Btree and try to access it as Vector Store
	btreePayload := fmt.Sprintf(`{"name": "test_btree_as_vec", "is_unique": false, "is_user_data_segment": false, "is_user_data_segment_loaded": false, "is_user_data_segment_modified": false, "index_specification": "", "transaction_id": "%s"}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreePayload)
	btreeMeta := fmt.Sprintf(`{"id": "%s", "transaction_id": "%s"}`, btreeID, transID)
	res = ManageVectorDBForTest(ctxID, UpsertVector, btreeMeta, "")
	if !strings.Contains(res, "object is not a Vector Store") {
		t.Errorf("UpsertVector with Btree ID should fail, got: %s", res)
	}

	// 6. UpsertVector: Invalid item
	res = ManageVectorDBForTest(ctxID, UpsertVector, metaPayload, "invalid-json")
	if !strings.Contains(res, "invalid item") {
		t.Errorf("UpsertVector with invalid item should fail, got: %s", res)
	}

	// 7. UpsertBatchVector: Invalid items
	res = ManageVectorDBForTest(ctxID, UpsertBatchVector, metaPayload, "invalid-json")
	if !strings.Contains(res, "invalid items") {
		t.Errorf("UpsertBatchVector with invalid items should fail, got: %s", res)
	}

	// 8. QueryVector: Invalid query options
	res = ManageVectorDBForTest(ctxID, QueryVector, metaPayload, "invalid-json")
	if !strings.Contains(res, "invalid query options") {
		t.Errorf("QueryVector with invalid options should fail, got: %s", res)
	}

	// 9. GetVector: Item not found
	res = ManageVectorDBForTest(ctxID, GetVector, metaPayload, "non-existent-id")
	if !strings.Contains(res, "not found") {
		t.Errorf("GetVector with non-existent ID should fail, got: %s", res)
	}

	// 10. VectorCount: Success
	res = ManageVectorDBForTest(ctxID, VectorCount, metaPayload, "")
	if res != "0" {
		t.Errorf("VectorCount should be 0, got: %s", res)
	}

	// 11. OptimizeVector: Success
	res = ManageVectorDBForTest(ctxID, OptimizeVector, metaPayload, "")
	if res != "" {
		t.Errorf("OptimizeVector should succeed, got: %s", res)
	}

	// 12. DeleteVector: Invalid metadata
	res = ManageVectorDBForTest(ctxID, DeleteVector, "invalid-json", "some-id")
	if !strings.Contains(res, "invalid store metadata") {
		t.Errorf("DeleteVector with invalid metadata should fail, got: %s", res)
	}
}

func TestVectorDB_More_Coverage(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// 1. Create Database
	dbPayload := fmt.Sprintf(`{"stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	t.Logf("DbID: %s", dbID)

	// 2. Begin Transaction
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, `{"mode": 1}`)
	t.Logf("TransID: %s", transID)

	// 3. Open Vector Store
	vsName := "test_vec_store_more"
	vsPayload := fmt.Sprintf(`{"name": "%s", "transaction_id": "%s"}`, vsName, transID)
	vsID := ManageDatabaseForTest(ctxID, OpenVectorStore, dbID, vsPayload)
	t.Logf("VsID: %s", vsID)

	if vsID == "" {
		t.Fatal("OpenVectorStore failed")
	}

	// Metadata for ManageVectorDB
	meta := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, vsID)

	// 4. Upsert Batch
	batchPayload := `[
		{"id": "item1", "vector": [0.1, 0.2, 0.3], "payload": {"key": "value1"}},
		{"id": "item2", "vector": [0.4, 0.5, 0.6], "payload": {"key": "value2"}}
	]`
	res := ManageVectorDBForTest(ctxID, UpsertBatchVector, meta, batchPayload)
	if res != "" {
		t.Errorf("UpsertBatchVector failed: %s", res)
	}

	// 5. Get Vector
	res = ManageVectorDBForTest(ctxID, GetVector, meta, "item1")
	if res == "" {
		t.Error("GetVector failed")
	}
	// Check if result contains expected data
	// (Simple check, assuming JSON string is returned)

	// 6. Delete Vector
	res = ManageVectorDBForTest(ctxID, DeleteVector, meta, "item1")
	if res != "" {
		t.Errorf("DeleteVector failed: %s", res)
	}

	// 7. Commit
	ManageTransactionForTest(ctxID, Commit, "")
}

func TestModelStore_More_Coverage(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// 1. Create Database
	dbPayload := fmt.Sprintf(`{"stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	t.Logf("DbID: %s", dbID)

	// 2. Begin Transaction
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, `{"mode": 1}`)
	t.Logf("TransID: %s", transID)

	// 3. Open Model Store
	msName := "test_model_store_more"
	msPayload := fmt.Sprintf(`{"path": "%s", "transaction_id": "%s"}`, msName, transID)
	msID := ManageDatabaseForTest(ctxID, OpenModelStore, dbID, msPayload)
	t.Logf("MsID: %s", msID)

	if msID == "" {
		t.Fatal("OpenModelStore failed")
	}

	// Metadata for ManageModelStore
	meta := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, msID)

	// 4. Save Model
	modelPayload := `{"name": "model1", "model": {"algorithm": "algo1", "hyper_parameters": {"p1": "v1"}}}`
	res := ManageModelStoreForTest(ctxID, SaveModel, meta, modelPayload)
	if res != "" {
		t.Errorf("SaveModel failed: %s", res)
	}

	// 5. Get Model (Not implemented in ManageModelStore? Let's check)
	// ManageModelStore has SaveModel, DeleteModel, ListModels.

	// 6. List Models
	res = ManageModelStoreForTest(ctxID, ListModels, meta, "")
	if res == "" {
		t.Error("ListModels failed")
	}

	// 7. Delete Model
	deletePayload := `{"name": "model1"}`
	res = ManageModelStoreForTest(ctxID, DeleteModel, meta, deletePayload)
	if res != "" {
		t.Errorf("DeleteModel failed: %s", res)
	}

	// 8. Commit
	ManageTransactionForTest(ctxID, Commit, "")
}

func TestVectorDB_Upsert_Query(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Open VectorStore
	vectorOpts := fmt.Sprintf(`{
		"transaction_id": "%s",
		"name": "test_vector",
		"config": {"usage_mode": 0, "content_size": 0}
	}`, transID)

	vectorID := ManageDatabaseForTest(ctxID, OpenVectorStore, dbID, vectorOpts)
	if vectorID == "" {
		t.Fatal("Failed to open vector store")
	}

	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, vectorID)

	// Upsert Vector
	itemPayload := fmt.Sprintf(`{
		"id": "%s",
		"vector": [0.1, 0.2, 0.3],
		"payload": {"meta": "data"}
	}`, sop.NewUUID().String())

	res := ManageVectorDBForTest(ctxID, UpsertVector, actionPayload, itemPayload)
	if res != "" {
		t.Errorf("UpsertVector failed: %s", res)
	}

	// Query Vector
	queryPayload := `{"vector": [0.1, 0.2, 0.3], "top_k": 1}`
	resQuery := ManageVectorDBForTest(ctxID, QueryVector, actionPayload, queryPayload)
	if resQuery == "" {
		t.Error("QueryVector failed (empty result)")
	}

	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestModelStore_Save_List(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Open ModelStore
	modelOpts := fmt.Sprintf(`{
		"transaction_id": "%s",
		"path": "test_model_store"
	}`, transID)

	modelID := ManageDatabaseForTest(ctxID, OpenModelStore, dbID, modelOpts)
	if modelID == "" {
		t.Fatal("Failed to open model store")
	}

	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, modelID)

	// Save Model
	// Data is base64 encoded "test_data" -> "dGVzdF9kYXRh"
	savePayload := `{"name": "model1", "version": "v1", "data": "dGVzdF9kYXRh"}`

	res := ManageModelStoreForTest(ctxID, SaveModel, actionPayload, savePayload)
	if res != "" {
		t.Errorf("SaveModel failed: %s", res)
	}

	// List Models
	resList := ManageModelStoreForTest(ctxID, ListModels, actionPayload, "")
	if resList == "" {
		t.Error("ListModels failed")
	}

	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestVectorDB_Extended(t *testing.T) {
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

	// Open Vector Store
	storeName := "test_vector_store"
	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"name": "%s",
		"storage_path": "%s",
		"config": {
			"usage_mode": 0,
			"content_size": 0
		}
	}`, transID, storeName, dir)

	storeID := ManageDatabaseForTest(ctxID, 6, dbID, openPayload) // OpenVectorStore = 6
	if storeID == "" {
		t.Fatal("Failed to open Vector Store")
	}

	// Add Vector
	vecID := sop.NewUUID()
	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)
	addPayload := fmt.Sprintf(`{
		"id": "%s",
		"vector": [0.1, 0.2, 0.3],
		"payload": {"info": "test"}
	}`, vecID)

	resAdd := ManageVectorDBForTest(ctxID, 1, metaPayload, addPayload) // Add
	if resAdd != "" {
		t.Errorf("ManageVectorDBForTest(Add) returned %s, expected empty string", resAdd)
	}

	// Update Vector
	updatePayload := fmt.Sprintf(`{
		"id": "%s",
		"vector": [0.1, 0.2, 0.3],
		"payload": {"info": "updated"}
	}`, vecID)

	resUpdate := ManageVectorDBForTest(ctxID, 1, metaPayload, updatePayload) // Update
	if resUpdate != "" {
		t.Errorf("ManageVectorDBForTest(Update) returned %s, expected empty string", resUpdate)
	}

	// Search Vector
	searchPayload := `{
		"vector": [0.4, 0.5, 0.6],
		"top_k": 1
	}`

	resSearch := ManageVectorDBForTest(ctxID, 5, metaPayload, searchPayload) // Search
	if resSearch == "" {
		t.Error("ManageVectorDBForTest(Search) returned empty result")
	}

	// Upsert Batch Vector
	batchPayload := fmt.Sprintf(`[
		{
			"id": "%s",
			"vector": [0.7, 0.8, 0.9],
			"payload": {"info": "batch1"}
		},
		{
			"id": "%s",
			"vector": [0.1, 0.2, 0.3],
			"payload": {"info": "batch2"}
		}
	]`, sop.NewUUID(), sop.NewUUID())

	resBatch := ManageVectorDBForTest(ctxID, 2, metaPayload, batchPayload) // UpsertBatch
	if resBatch != "" {
		t.Errorf("ManageVectorDBForTest(UpsertBatch) returned %s, expected empty string", resBatch)
	}

	// Get Vector
	getPayload := vecID.String()
	resGet := ManageVectorDBForTest(ctxID, 3, metaPayload, getPayload) // Get
	if resGet == "" {
		t.Error("ManageVectorDBForTest(Get) returned empty result")
	}

	// Vector Count
	resCount := ManageVectorDBForTest(ctxID, 6, metaPayload, "") // Count
	if resCount == "" {
		t.Error("ManageVectorDBForTest(Count) returned empty result")
	}

	// Optimize Vector
	// resOpt := ManageVectorDBForTest(ctxID, 7, metaPayload, "") // Optimize
	// if resOpt != "" {
	// 	t.Errorf("ManageVectorDBForTest(Optimize) returned %s, expected empty string", resOpt)
	// }

	// Commit
	commitPayload := fmt.Sprintf(`{"transaction_id": "%s", "action": %d}`, transID, 2) // Commit
	ManageTransactionForTest(ctxID, 2, commitPayload)
}

func TestVectorDB_Remove(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// Create DB
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	// Start Trans
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Open Vector Store
	storeName := "test_vector_remove"
	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"name": "%s",
		"storage_path": "%s",
		"config": {
			"usage_mode": 0,
			"content_size": 0
		}
	}`, transID, storeName, dir)

	storeID := ManageDatabaseForTest(ctxID, 6, dbID, openPayload)

	// Add Vector
	vecID := sop.NewUUID()
	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)
	addPayload := fmt.Sprintf(`{
		"id": "%s",
		"vector": [0.1, 0.2, 0.3],
		"payload": {"info": "test"}
	}`, vecID)

	ManageVectorDBForTest(ctxID, 1, metaPayload, addPayload)

	// Remove Vector
	removePayload := vecID.String()
	resRemove := ManageVectorDBForTest(ctxID, 4, metaPayload, removePayload) // Remove
	if resRemove != "" {
		t.Errorf("ManageVectorDBForTest(Remove) returned %s, expected empty string", resRemove)
	}

	// Commit
	commitPayload := fmt.Sprintf(`{"transaction_id": "%s", "action": %d}`, transID, 2) // Commit
	ManageTransactionForTest(ctxID, 2, commitPayload)
}

func TestModelStore_Extended(t *testing.T) {
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

	// Open Model Store
	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"path": "%s"
	}`, transID, dir)

	storeID := ManageDatabaseForTest(ctxID, 5, dbID, openPayload) // OpenModelStore = 5
	if storeID == "" {
		t.Fatal("Failed to open Model Store")
	}

	// Save Model
	modelName := "test_model"
	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)
	savePayload := fmt.Sprintf(`{
		"category": "test_category",
		"name": "%s",
		"model": {
			"version": "v1",
			"url": "http://example.com/model"
		}
	}`, modelName)

	resSave := ManageModelStoreForTest(ctxID, 1, metaPayload, savePayload) // Save
	if resSave != "" {
		t.Errorf("ManageModelStoreForTest(Save) returned %s, expected empty string", resSave)
	}

	// Load Model
	loadPayload := fmt.Sprintf(`{
		"category": "test_category",
		"name": "%s"
	}`, modelName)

	resLoad := ManageModelStoreForTest(ctxID, 2, metaPayload, loadPayload) // Load
	if resLoad == "" {
		t.Error("ManageModelStoreForTest(Load) returned empty result")
	}

	// List Models
	listPayload := "test_category"

	resList := ManageModelStoreForTest(ctxID, 3, metaPayload, listPayload) // List
	if resList == "" {
		t.Error("ManageModelStoreForTest(List) returned empty result")
	}

	// Remove Model
	removePayload := fmt.Sprintf(`{
		"category": "test_category",
		"name": "%s"
	}`, modelName)

	resRemove := ManageModelStoreForTest(ctxID, 4, metaPayload, removePayload) // Remove
	if resRemove != "" {
		t.Errorf("ManageModelStoreForTest(Remove) returned %s, expected empty string", resRemove)
	}

	// Commit
	commitPayload := fmt.Sprintf(`{"transaction_id": "%s", "action": %d}`, transID, 2) // Commit
	ManageTransactionForTest(ctxID, 2, commitPayload)
}
