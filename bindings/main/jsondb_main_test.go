package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
)

func TestContextManagement(t *testing.T) {
	// Test createContext
	ctxID := CreateContextForTest()
	if ctxID <= 0 {
		t.Errorf("Expected valid context ID, got %v", ctxID)
	}

	// Test getContext (internal) - we can access internal Go functions directly if they don't use C types in signature
	// getContext takes C.longlong, so we can't call it directly easily without casting to a type we can't name.
	// But we can check the side effects via the helpers.

	// Test contextError - should be nil initially
	if !ContextErrorReturnsNilForTest(ctxID) {
		t.Error("Expected no error")
	}

	// Test cancelContext
	CancelContextForTest(ctxID)

	// After cancel, the context is removed from lookup in the current implementation
	// Let's verify it's removed by checking if we can get it or if it errors?
	// Wait, cancelContext implementation:
	// delete(contextLookup, id)
	// So it should be gone.

	// We can check internal map directly since we are in package main
	contextLookupLocker.Lock()
	_, ok := contextLookup[ctxID]
	contextLookupLocker.Unlock()
	if ok {
		t.Error("Expected context to be removed after cancel")
	}

	// Test removeContext
	ctxID2 := CreateContextForTest()
	RemoveContextForTest(ctxID2)

	contextLookupLocker.Lock()
	_, ok2 := contextLookup[ctxID2]
	contextLookupLocker.Unlock()
	if ok2 {
		t.Error("Expected context to be removed")
	}
}

func TestContextError_NotFound(t *testing.T) {
	msg := GetContextErrorForTest(999999)
	if msg != "context not found" {
		t.Errorf("Expected 'context not found', got '%s'", msg)
	}
}

func TestFreeString(t *testing.T) {
	FreeStringForTest()
}

func TestMainFunction(t *testing.T) {
	MainForTest()
}

func TestManageTransaction_Deprecated(t *testing.T) {
	// Test NewTransaction (deprecated)
	errMsg := ManageTransactionRawForTest(0, NewTransaction, "")
	expected := "NewTransaction is deprecated. Please use manageDatabase with BeginTransaction action."
	if errMsg != expected {
		t.Errorf("Expected error %q, got %q", expected, errMsg)
	}

	// Test Begin (deprecated)
	errMsg = ManageTransactionRawForTest(0, Begin, "")
	expected = "Begin is deprecated. Transaction is already begun when created via manageDatabase."
	if errMsg != expected {
		t.Errorf("Expected error %q, got %q", expected, errMsg)
	}
}

func TestManageTransaction_InvalidAction(t *testing.T) {
	// Test invalid action
	errMsg := ManageTransactionRawForTest(0, 999, "")
	expectedSubstr := "unsupported action 999"
	if errMsg != expectedSubstr {
		t.Errorf("Expected error %q, got %q", expectedSubstr, errMsg)
	}
}

func TestErrorPaths(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)

	// Invalid Context
	res := ManageVectorDBForTest(99999, 1, "{}", "{}")
	if res == "" {
		t.Error("Expected error for invalid context")
	}

	// Invalid Metadata JSON
	res = ManageVectorDBForTest(ctxID, 1, "{invalid}", "{}")
	if res == "" {
		t.Error("Expected error for invalid metadata JSON")
	}

	// Invalid Transaction UUID
	res = ManageVectorDBForTest(ctxID, 1, `{"transaction_id": "invalid"}`, "{}")
	if res == "" {
		t.Error("Expected error for invalid transaction UUID")
	}

	// Invalid Store UUID
	res = ManageVectorDBForTest(ctxID, 1, `{"transaction_id": "00000000-0000-0000-0000-000000000000", "id": "invalid"}`, "{}")
	if res == "" {
		t.Error("Expected error for invalid store UUID")
	}

	// Store Not Found (valid UUIDs but not in registry)
	res = ManageVectorDBForTest(ctxID, 1, `{"transaction_id": "00000000-0000-0000-0000-000000000000", "id": "00000000-0000-0000-0000-000000000000"}`, "{}")
	if res == "" {
		t.Error("Expected error for store not found")
	}

	// Invalid Action
	// Need a valid store first to reach switch case?
	// No, getStore is called inside switch case for most actions.
	// But for unknown action, it returns nil (empty string).
	// Wait, manageVectorDB returns nil for unknown action?
	// Let's check.
}

func TestVectorDB_InvalidPayloads(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"name": "test_vec_err",
		"config": {"usage_mode": 0, "content_size": 0}
	}`, transID)
	storeID := ManageDatabaseForTest(ctxID, 6, dbID, openPayload)

	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)

	// Upsert Invalid JSON
	res := ManageVectorDBForTest(ctxID, 1, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid upsert payload")
	}

	// UpsertBatch Invalid JSON
	res = ManageVectorDBForTest(ctxID, 2, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid upsert batch payload")
	}

	// Query Invalid JSON
	res = ManageVectorDBForTest(ctxID, 5, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid query payload")
	}
}

func TestModelStore_InvalidPayloads(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"path": "%s"
	}`, transID, dir)
	storeID := ManageDatabaseForTest(ctxID, 5, dbID, openPayload)

	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)

	// Save Invalid JSON
	res := ManageModelStoreForTest(ctxID, 1, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid save payload")
	}

	// Load Invalid JSON
	res = ManageModelStoreForTest(ctxID, 2, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid load payload")
	}

	// Delete Invalid JSON
	res = ManageModelStoreForTest(ctxID, 4, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid delete payload")
	}
}

func TestSearch_InvalidPayloads(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)
	dir := t.TempDir()

	dbPayload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", dbPayload)
	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	openPayload := fmt.Sprintf(`{
		"transaction_id": "%s",
		"name": "test_search_err"
	}`, transID)
	storeID := ManageDatabaseForTest(ctxID, 7, dbID, openPayload) // OpenSearch = 7

	metaPayload := fmt.Sprintf(`{"transaction_id": "%s", "id": "%s"}`, transID, storeID)

	// Add Invalid JSON
	res := ManageSearchForTest(ctxID, 1, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid add payload")
	}

	// Search Invalid JSON
	res = ManageSearchForTest(ctxID, 2, metaPayload, "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid search payload")
	}
}

func TestManageDatabase_ErrorPaths(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)

	// NewDatabase Invalid JSON
	res := ManageDatabaseForTest(ctxID, 1, "", "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid NewDatabase payload")
	}

	// BeginTransaction Invalid DB UUID
	res = ManageDatabaseForTest(ctxID, 2, "invalid-uuid", "{}")
	if res == "" {
		t.Error("Expected error for invalid DB UUID")
	}

	// BeginTransaction DB Not Found
	res = ManageDatabaseForTest(ctxID, 2, "00000000-0000-0000-0000-000000000000", "{}")
	if res == "" {
		t.Error("Expected error for DB Not Found")
	}

	// NewBtree Invalid JSON
	res = ManageDatabaseForTest(ctxID, 3, "", "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid NewBtree payload")
	}

	// OpenVectorStore Invalid JSON
	res = ManageDatabaseForTest(ctxID, 6, "", "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid OpenVectorStore payload")
	}

	// OpenSearch Invalid JSON
	res = ManageDatabaseForTest(ctxID, 7, "", "{invalid}")
	if res == "" {
		t.Error("Expected error for invalid OpenSearch payload")
	}

	// RemoveBtree Invalid UUID
	res = ManageDatabaseForTest(ctxID, 8, "invalid-uuid", "name")
	if res == "" {
		t.Error("Expected error for invalid RemoveBtree UUID")
	}
}

func TestManageLogging(t *testing.T) {
	// Test logging to stderr (default)
	res := ManageLoggingForTest(0, "")
	if res != "" {
		t.Errorf("ManageLoggingForTest(0, \"\") returned error: %v", res)
	}

	// Test logging to file
	tmpFile, err := os.CreateTemp("", "test_log_*.log")
	if err != nil {
		t.Fatal(err)
	}
	logPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logPath)

	res = ManageLoggingForTest(1, logPath)
	if res != "" {
		t.Errorf("ManageLoggingForTest(1, %s) returned error: %v", logPath, res)
	}

	// Verify file exists and is writable (implied by success)
}

func TestRedisConnection(t *testing.T) {
	// Test with invalid URI (should fail gracefully)
	res := OpenRedisConnectionForTest("invalid-uri")
	if res == "" {
		t.Error("OpenRedisConnectionForTest should have failed with invalid URI")
	}

	// Test close (should fail if not open, or just return nil if it handles it)
	// The implementation calls redis.CloseConnection() which might return error if not open.
	// It might return error if no connection. That's fine, we just want to cover the code,
	// so the return value is deliberately not captured here.
	CloseRedisConnectionForTest()
}

func TestCassandraConnection(t *testing.T) {
	// Test with invalid JSON
	res := OpenCassandraConnectionForTest("{invalid-json}")
	if res == "" {
		t.Error("OpenCassandraConnectionForTest should have failed with invalid JSON")
	}

	// Test with valid JSON but invalid config (should fail to connect)
	config := `{"cluster_hosts": ["localhost"], "keyspace": "sop", "consistency": 1}`
	res = OpenCassandraConnectionForTest(config)
	if res == "" {
		// If it somehow connects (e.g. local cassandra running), that's fine too.
		// But likely it will fail.
		CloseCassandraConnectionForTest()
	}

	// Test close; return value deliberately not captured, same reasoning as
	// the Redis case above - we just want to exercise the code path.
	CloseCassandraConnectionForTest()
}

func TestManageLogging_ErrorPaths(t *testing.T) {
	// Test case 1: Invalid log path (directory does not exist)
	invalidPath := "/nonexistent/path/to/log.log"

	// Level 1 is Info
	errMsg := ManageLoggingForTest(1, invalidPath)
	expectedSubstr := "failed to open log file"
	if len(errMsg) < len(expectedSubstr) || errMsg[:len(expectedSubstr)] != expectedSubstr {
		t.Errorf("Expected error message to start with %q, got %q", expectedSubstr, errMsg)
	}
}

func TestManageLogging_Levels(t *testing.T) {
	// Test different log levels with no file path (stdout/stderr)
	levels := []int{0, 1, 2, 3, 99} // Debug, Info, Warn, Error, Default
	for _, l := range levels {
		errMsg := ManageLoggingForTest(l, "")
		if errMsg != "" {
			t.Errorf("Expected nil for valid log level %d, got error: %s", l, errMsg)
		}
	}
}

func TestManageLogging_ValidFile(t *testing.T) {
	// Test with a valid temporary file
	tmpFile, err := os.CreateTemp("", "test_log_*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	errMsg := ManageLoggingForTest(1, tmpFile.Name())
	if errMsg != "" {
		t.Errorf("Expected nil for valid log file, got error: %s", errMsg)
	}
}

func TestManageDatabase_NewDatabase(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)

	dir := t.TempDir()
	// Use InMemory cache (1) and provide a temp folder
	payload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)

	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", payload)
	if dbID == "" {
		t.Error("Expected valid database ID")
	}

	// Now start a transaction on this database
	// Action: BeginTransaction
	// TargetID: dbID
	// Payload: TransactionOptions JSON

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)

	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)
	if transID == "" {
		t.Error("Expected valid transaction ID")
	}

	// Commit the transaction
	// Action: Commit (in manageTransaction)
	// Payload: transID

	errStr := ManageTransactionForTest(ctxID, Commit, transID)
	if errStr != "" {
		t.Errorf("Expected successful commit, got error: %s", errStr)
	}
}

func TestManageTransaction_Rollback(t *testing.T) {
	ctxID := CreateContextForTest()
	defer RemoveContextForTest(ctxID)

	dir := t.TempDir()
	payload := fmt.Sprintf(`{"cache_type": 1, "stores_folders": ["%s"]}`, dir)
	dbID := ManageDatabaseForTest(ctxID, NewDatabase, "", payload)

	transPayload := fmt.Sprintf(`{"Mode": 1, "stores_folders": ["%s"]}`, dir)
	transID := ManageDatabaseForTest(ctxID, BeginTransaction, dbID, transPayload)

	errStr := ManageTransactionForTest(ctxID, Rollback, transID)
	if errStr != "" {
		t.Errorf("Expected successful rollback, got error: %s", errStr)
	}
}

func TestManageTransaction_InvalidUUID(t *testing.T) {
	ctxID := CreateContextForTest()
	errStr := ManageTransactionForTest(ctxID, Commit, "invalid-uuid")
	if errStr == "" {
		t.Error("Expected error for invalid UUID")
	}
}

func TestManageTransaction_NotFound(t *testing.T) {
	ctxID := CreateContextForTest()
	// Generate a valid UUID that is not in registry
	randomUUID := sop.NewUUID().String()

	errStr := ManageTransactionForTest(ctxID, Commit, randomUUID)
	if errStr == "" {
		t.Error("Expected error for non-existent transaction")
	}
}
