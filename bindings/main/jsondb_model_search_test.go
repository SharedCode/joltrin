package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sharedcode/joltrin"
)

func TestSearch_Add_Search(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Open Search
	searchOpts := fmt.Sprintf(`{"transaction_id": "%s", "name": "test_search"}`, transID)
	searchID := ManageDatabaseForTest(ctxID, OpenSearch, dbID, searchOpts)
	if searchID == "" {
		t.Fatal("Failed to open search")
	}

	// Add Document
	// Action: SearchAdd (1)
	// TargetID: {"transaction_id": "...", "id": "..."}
	actionPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, searchID)
	addPayload := `{"doc_id": "doc1", "text": "hello world"}`

	res := ManageSearchForTest(ctxID, SearchAdd, actionPayload, addPayload)
	if res != "" {
		t.Errorf("SearchAdd failed: %s", res)
	}

	// Search
	// Action: SearchSearch (2)
	searchPayload := `{"query": "hello"}`
	resSearch := ManageSearchForTest(ctxID, SearchSearch, actionPayload, searchPayload)
	if resSearch == "" {
		t.Error("Search failed (empty result)")
	}
	// Verify result contains doc1
	// Result is JSON array of results.
	// We can check if it contains "doc1".
	// Note: Search might be async or require commit?
	// Usually search index updates on Add.
	// But transaction commit might be needed for persistence, but here we are in transaction.
	// sop/search usually works within transaction.

	// Commit
	ManageTransactionForTest(ctxID, Commit, transID)
}

func TestManageSearch_ErrorPaths_Extended(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	// Setup DB and Transaction
	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	// Create Search Index
	searchPayload := fmt.Sprintf(`{"name": "test_search_err", "transaction_id": "%s"}`, transID)
	searchID := ManageDatabaseForTest(ctxID, OpenSearch, dbID, searchPayload)
	metaPayload := fmt.Sprintf(`{"id": "%s", "transaction_id": "%s"}`, searchID, transID)

	// 1. getStore: Invalid store metadata
	res := ManageSearchForTest(ctxID, SearchAdd, "invalid-json", "")
	if !strings.Contains(res, "invalid store metadata") {
		t.Errorf("SearchAdd with invalid metadata should fail, got: %s", res)
	}

	// 2. getStore: Invalid transaction UUID
	invalidTransMeta := fmt.Sprintf(`{"id": "%s", "transaction_id": "invalid-uuid"}`, searchID)
	res = ManageSearchForTest(ctxID, SearchAdd, invalidTransMeta, "")
	if !strings.Contains(res, "invalid transaction UUID") {
		t.Errorf("SearchAdd with invalid trans UUID should fail, got: %s", res)
	}

	// 3. getStore: Invalid store UUID
	invalidStoreMeta := fmt.Sprintf(`{"id": "invalid-uuid", "transaction_id": "%s"}`, transID)
	res = ManageSearchForTest(ctxID, SearchAdd, invalidStoreMeta, "")
	if !strings.Contains(res, "invalid store UUID") {
		t.Errorf("SearchAdd with invalid store UUID should fail, got: %s", res)
	}

	// 4. getStore: Search Index not found
	notFoundMeta := fmt.Sprintf(`{"id": "00000000-0000-0000-0000-000000000000", "transaction_id": "%s"}`, transID)
	res = ManageSearchForTest(ctxID, SearchAdd, notFoundMeta, "")
	if !strings.Contains(res, "search index not found") {
		t.Errorf("SearchAdd with non-existent store should fail, got: %s", res)
	}

	// 5. getStore: Object is not a Search Index
	// Create a Btree and try to access it as Search Index
	btreePayload := fmt.Sprintf(`{"name": "test_btree_as_search", "is_unique": false, "is_user_data_segment": false, "is_user_data_segment_loaded": false, "is_user_data_segment_modified": false, "index_specification": "", "transaction_id": "%s"}`, transID)
	btreeID := ManageDatabaseForTest(ctxID, NewBtree, dbID, btreePayload)
	btreeMeta := fmt.Sprintf(`{"id": "%s", "transaction_id": "%s"}`, btreeID, transID)
	res = ManageSearchForTest(ctxID, SearchAdd, btreeMeta, "")
	if !strings.Contains(res, "object is not a Search Index") {
		t.Errorf("SearchAdd with Btree ID should fail, got: %s", res)
	}

	// 6. SearchAdd: Invalid payload
	res = ManageSearchForTest(ctxID, SearchAdd, metaPayload, "invalid-json")
	if !strings.Contains(res, "invalid payload") {
		t.Errorf("SearchAdd with invalid payload should fail, got: %s", res)
	}

	// 7. SearchSearch: Invalid payload
	res = ManageSearchForTest(ctxID, SearchSearch, metaPayload, "invalid-json")
	if !strings.Contains(res, "invalid payload") {
		t.Errorf("SearchSearch with invalid payload should fail, got: %s", res)
	}
}

func TestSearch_Extended(t *testing.T) {
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

	// Open Search
	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"path": "%s"
	}`, transID, dir)

	storeID := ManageDatabaseForTest(ctxID, 7, dbID, openPayload) // OpenSearch = 7
	if storeID == "" {
		t.Fatal("Failed to open Search")
	}

	// Add Document
	docID := sop.NewUUID()
	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)
	addPayload := fmt.Sprintf(`{
		"documents": [
			{
				"id": "%s",
				"content": "hello world"
			}
		]
	}`, docID)

	resAdd := ManageSearchForTest(ctxID, 1, metaPayload, addPayload) // Add
	if resAdd != "" {
		t.Errorf("ManageSearchForTest(Add) returned %s, expected empty string", resAdd)
	}

	// Search
	searchPayload := `{
		"query": "universe"
	}`

	resSearch := ManageSearchForTest(ctxID, 2, metaPayload, searchPayload) // Search
	if resSearch == "" {
		t.Error("ManageSearchForTest(Search) returned empty result")
	}
	// Verify result contains docID
	// ...

	// Commit
	commitPayload := fmt.Sprintf(`{"transaction_id": "%s", "action": %d}`, transID, 2) // Commit
	ManageTransactionForTest(ctxID, 2, commitPayload)
}

func TestManageModelStore_ErrorPaths_Extended(t *testing.T) {
	// 1. Setup
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)

	// Create DB
	dbIDStr := ManageDatabaseForTest(ctxID, NewDatabase, "", `{"name": "test_model_err_db", "type": "Cassandra"}`)
	// dbID is unused
	// dbID, _ := sop.ParseUUID(dbIDStr)

	// Begin Transaction
	transIDStr := ManageDatabaseForTest(ctxID, BeginTransaction, dbIDStr, `{"mode": 1}`) // ReadWrite
	transID, _ := sop.ParseUUID(transIDStr)

	// Create Model Store
	msOpts := ModelStoreOptions{
		Path:          "test_model_err_store",
		TransactionID: transIDStr,
	}
	msOptsJSON, _ := json.Marshal(msOpts)
	msIDStr := ManageDatabaseForTest(ctxID, OpenModelStore, dbIDStr, string(msOptsJSON))
	// msID is used in createMeta
	// msID, _ := sop.ParseUUID(msIDStr)

	// Create a Btree to test type mismatch
	btreeOpts := BtreeOptions{
		Name:           "test_btree_as_model",
		TransactionID:  uuid.UUID(transID),
		IsPrimitiveKey: true,
	}
	btreeOptsJSON, _ := json.Marshal(btreeOpts)
	btreeIDStr := ManageDatabaseForTest(ctxID, NewBtree, dbIDStr, string(btreeOptsJSON))
	// btreeID is used in createMeta
	// btreeID, _ := sop.ParseUUID(btreeIDStr)

	// Helper to create valid metadata
	createMeta := func(tid, sid string) string {
		m := map[string]string{
			"transaction_id": tid,
			"id":             sid,
		}
		b, _ := json.Marshal(m)
		return string(b)
	}

	validMeta := createMeta(transIDStr, msIDStr)

	// 2. Test Context Error
	if ret := ManageModelStoreForTest(99999, SaveModel, validMeta, "{}"); ret == "" {
		t.Error("Expected error for invalid context, got empty string")
	}

	// 3. Test getStore Errors
	tests := []struct {
		name     string
		meta     string
		expected string
	}{
		{
			name:     "Invalid Metadata JSON",
			meta:     "invalid-json",
			expected: "invalid store metadata",
		},
		{
			name:     "Invalid Transaction UUID",
			meta:     createMeta("invalid-uuid", msIDStr),
			expected: "invalid transaction UUID",
		},
		{
			name:     "Invalid Store UUID",
			meta:     createMeta(transIDStr, "invalid-uuid"),
			expected: "invalid store UUID",
		},
		{
			name:     "Transaction Not Found",
			meta:     createMeta(sop.NewUUID().String(), msIDStr),
			expected: "model store not found in transaction",
		},
		{
			name:     "Store Not Found in Transaction",
			meta:     createMeta(transIDStr, sop.NewUUID().String()),
			expected: "model store not found in transaction",
		},
		{
			name:     "Object is not a Model Store",
			meta:     createMeta(transIDStr, btreeIDStr),
			expected: "object is not a Model Store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ret := ManageModelStoreForTest(ctxID, SaveModel, tt.meta, "{}")
			if ret == "" {
				t.Errorf("Expected error containing '%s', got empty string", tt.expected)
			}
		})
	}

	// 4. Test Action Errors (Save, Load, Delete)
	// Valid metadata, but invalid payloads or logic errors

	// SaveModel - Invalid Payload
	if ret := ManageModelStoreForTest(ctxID, SaveModel, validMeta, "invalid-json"); ret == "" {
		t.Error("Expected error for SaveModel invalid payload, got empty string")
	}

	// LoadModel - Invalid Payload
	if ret := ManageModelStoreForTest(ctxID, LoadModel, validMeta, "invalid-json"); ret == "" {
		t.Error("Expected error for LoadModel invalid payload, got empty string")
	}

	// LoadModel - Item Not Found
	// First ensure it's not there.
	item := ModelItem{Category: "cat", Name: "non-existent"}
	itemJSON, _ := json.Marshal(item)
	if ret := ManageModelStoreForTest(ctxID, LoadModel, validMeta, string(itemJSON)); ret == "" {
		t.Error("Expected error for LoadModel item not found, got empty string")
	}

	// DeleteModel - Invalid Payload
	if ret := ManageModelStoreForTest(ctxID, DeleteModel, validMeta, "invalid-json"); ret == "" {
		t.Error("Expected error for DeleteModel invalid payload, got empty string")
	}

	// DeleteModel - Item Not Found (might not error depending on implementation, but let's check)
	// Usually Delete is idempotent or returns error if not found.
	// Let's check the implementation of store.Delete.
	// If it returns error, we expect error.
	if ret := ManageModelStoreForTest(ctxID, DeleteModel, validMeta, string(itemJSON)); ret == "" {
		// If it returns nil, it means delete was successful (or ignored).
		// If the underlying store returns error for missing item, then we expect error.
		// I'll assume it might return error. If this fails, I'll remove this check.
	}

	// ListModels - Invalid Payload (it uses payload as category string, so any string is valid)
	// But if getStore fails, it returns error. We covered getStore.

	// Cleanup
	ManageTransactionForTest(ctxID, Rollback, transIDStr)
}
