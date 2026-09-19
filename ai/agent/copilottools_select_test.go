package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/encoding"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestToolSelect_WithFilter(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_select"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "users"
	// Create the store first
	storeOpts := sop.StoreOptions{
		Name:           storeName,
		SlotLength:     10,
		IsPrimitiveKey: false, // JSON keys
	}
	if _, err := sopdb.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil, storeOpts); err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// Start new transaction for population
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	store, err := jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Add items
	// Keys: {id, group, role}
	items := []struct {
		Key   map[string]any
		Value string
	}{
		{Key: map[string]any{"id": 1, "group": "A", "role": "admin"}, Value: "Alice"},
		{Key: map[string]any{"id": 2, "group": "A", "role": "user"}, Value: "Bob"},
		{Key: map[string]any{"id": 3, "group": "B", "role": "admin"}, Value: "Charlie"},
		{Key: map[string]any{"id": 4, "group": "B", "role": "user"}, Value: "Dave"},
		{Key: map[string]any{"id": 5, "group": "A", "role": "guest"}, Value: "Eve"},
	}

	for _, item := range items {
		if _, err := store.Add(ctx, item.Key, item.Value); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Test Cases
	tests := []struct {
		name          string
		filter        map[string]any
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "Filter by Group A",
			filter:        map[string]any{"group": "A"},
			expectedCount: 3, // Alice, Bob, Eve
			expectedNames: []string{"Alice", "Bob", "Eve"},
		},
		{
			name:          "Filter by Role Admin",
			filter:        map[string]any{"role": "admin"},
			expectedCount: 2, // Alice, Charlie
			expectedNames: []string{"Alice", "Charlie"},
		},
		{
			name:          "Filter by Group A and Role Admin",
			filter:        map[string]any{"group": "A", "role": "admin"},
			expectedCount: 1, // Alice
			expectedNames: []string{"Alice"},
		},
		{
			name:          "Filter by Non-existent",
			filter:        map[string]any{"group": "C"},
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"store":     storeName,
				"key_match": tt.filter,
			}

			result, err := agent.toolSelect(ctx, args)
			if err != nil {
				t.Fatalf("toolSelect failed: %v", err)
			}

			if tt.expectedCount == 0 {
				// Expect empty JSON array
				var items []map[string]any
				if err := json.Unmarshal([]byte(result), &items); err != nil {
					t.Fatalf("failed to unmarshal result (expected empty array): %v. Result: %s", err, result)
				}
				if len(items) != 0 {
					t.Errorf("expected 0 items, got %d", len(items))
				}
			} else {
				var items []map[string]any
				if err := json.Unmarshal([]byte(result), &items); err != nil {
					t.Fatalf("failed to unmarshal result: %v", err)
				}

				if len(items) != tt.expectedCount {
					t.Errorf("expected %d items, got %d", tt.expectedCount, len(items))
				}

				// Verify names
				for _, name := range tt.expectedNames {
					found := false
					for _, item := range items {
						if item["value"] == name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected to find %s in result", name)
					}
				}
			}
		})
	}
}

func TestToolSelect_WithAlias(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_select_alias"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "employees"
	storeOpts := sop.StoreOptions{
		Name:           storeName,
		SlotLength:     10,
		IsPrimitiveKey: false,
	}
	if _, err := sopdb.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil, storeOpts); err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// Populate
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	store, err := jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Add item
	// Key: {id: 101}
	// Value: {name: "John Doe", dept: "Engineering", salary: 100000}
	key := map[string]any{"id": 101}
	value := map[string]any{"name": "John Doe", "dept": "Engineering", "salary": 100000}

	if _, err := store.Add(ctx, key, value); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Execute Select with Alias
	args := map[string]any{
		"store": storeName,
		"fields": []string{
			"id AS employee_id",
			"name AS full_name",
			"dept", // No alias
		},
	}

	resultJSON, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	// 4. Verify Result
	var result []map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(result))
	}

	item := result[0]

	// Check Key Alias (Now flattened)
	if _, ok := item["employee_id"]; !ok {
		// Only fail if we expected it to be at root (flattened)
		// But in this test case, we inserted {"id": 101}, "John Doe", {"dept": "Sales"}
		// Wait, the input data:
		// agent.toolAdd(ctx, map[string]any{"store": "test_alias", "key": map[string]any{"id": 101}, "value": map[string]any{"name": "John Doe", "dept": "Sales"}})
		// So Key IS a map, Value IS a map. Flattening IS happening.
		t.Errorf("Expected key 'employee_id', got keys: %v", item)
	}
	if val, _ := item["employee_id"].(float64); val != 101 {
		t.Errorf("Expected employee_id 101, got %v", item["employee_id"])
	}

	// Check Value Alias (Now flattened)
	if _, ok := item["full_name"]; !ok {
		t.Errorf("Expected value 'full_name', got keys: %v", item)
	}
	if val, _ := item["full_name"].(string); val != "John Doe" {
		t.Errorf("Expected full_name 'John Doe', got %v", item["full_name"])
	}

	// Check No Alias
	if _, ok := item["dept"]; !ok {
		t.Errorf("Expected value 'dept', got keys: %v", item)
	}
}

// Merged TestToolSelect_OutputFormat
func TestToolSelect_OutputFormat(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	db := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// Seed Data
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeOpts := sop.StoreOptions{
		IsPrimitiveKey: true,
	}
	b3, err := sopdb.NewBtree[string, any](ctx, dbOpts, "employees", tx, nil, storeOpts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	val := map[string]any{
		"Name": "John Doe",
		"Age":  30,
		"Role": "Engineer",
	}
	b3.Add(ctx, "emp1", val)
	b3.Add(ctx, "emp2", map[string]any{"Name": "Jane", "Age": 25, "Role": "Designer"})

	tx.Commit(ctx)

	// Invoke Select
	cfg := Config{}
	agent := NewCopilotAgent(cfg, nil, nil)
	agent.databases = map[string]sop.DatabaseOptions{"test": dbOpts}

	payload := &ai.SessionPayload{
		CurrentDB: "test",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	payload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// Test 1: Select All
	args := map[string]any{
		"store": "employees",
	}

	result, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	// Verify Output is JSON array
	trimmed := strings.TrimSpace(result)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		t.Errorf("Expected JSON array output, got: %s", trimmed)
	}

	// Verify content
	if !strings.Contains(trimmed, "John Doe") {
		t.Errorf("Expected John Doe in output, got: %s", trimmed)
	}

	// Parsing verification
	var validJSON []any
	if err := json.Unmarshal([]byte(trimmed), &validJSON); err != nil {
		t.Errorf("Output is not valid JSON: %v", err)
	}
	if len(validJSON) != 2 {
		t.Errorf("Expected 2 items, got %d", len(validJSON))
	}
}

// Merged TestToolSelect_OrderBy
func TestToolSelect_OrderBy(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	cfg := Config{Name: "TestAgent"}
	dbs := make(map[string]sop.DatabaseOptions)
	agent := NewCopilotAgent(cfg, dbs, sysDB)

	ctx := context.Background()
	payload := &ai.SessionPayload{CurrentDB: "system"}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)
	agent.Open(ctx)

	// Create Store and Data using sopdb directly
	t2, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	storeOpts := sop.StoreOptions{
		Name:           "items",
		SlotLength:     10,
		IsPrimitiveKey: true,
	}
	s, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "items", t2, nil, storeOpts)
	s.Add(ctx, "1", map[string]any{"name": "one"})
	s.Add(ctx, "2", map[string]any{"name": "two"})
	s.Add(ctx, "3", map[string]any{"name": "three"})
	t2.Commit(ctx)

	readTx, err := sysDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	payload.Transaction = readTx

	// Helper to extract name from key map
	getKeyName := func(item map[string]any) string {
		k := item["key"]
		if s, ok := k.(string); ok {
			return s
		}
		return ""
	}

	// Test 1: Default (ASC)
	args := map[string]any{
		"store": "items",
	}
	res, err := agent.Execute(ctx, "select", args)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}

	var items []map[string]any
	json.Unmarshal([]byte(res), &items)
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	if getKeyName(items[0]) != "1" {
		t.Errorf("Expected first item key '1', got '%v'", items[0]["key"])
	}

	// Test 2: Explicit ASC
	args = map[string]any{
		"store":    "items",
		"order_by": "key asc",
	}
	res, err = agent.Execute(ctx, "select", args)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	json.Unmarshal([]byte(res), &items)
	if getKeyName(items[0]) != "1" {
		t.Errorf("Expected first item key '1', got '%v'", items[0]["key"])
	}

	// Test 3: DESC
	args = map[string]any{
		"store":    "items",
		"order_by": "key desc",
	}
	res, err = agent.Execute(ctx, "select", args)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	json.Unmarshal([]byte(res), &items)
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}
	if getKeyName(items[0]) != "3" {
		t.Errorf("Expected first item key '3', got '%v'", items[0]["key"])
	}
	if getKeyName(items[1]) != "2" {
		t.Errorf("Expected second item key '2', got '%v'", items[1]["key"])
	}
	// Test 4: Implicit Key DESC (just "desc")
	args = map[string]any{
		"store":    "items",
		"order_by": "desc",
	}
	res, err = agent.Execute(ctx, "select", args)
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	json.Unmarshal([]byte(res), &items)
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}
	if getKeyName(items[0]) != "3" {
		t.Errorf("Expected first item key '3', got '%v'", items[0]["key"])
	}
}

// Merged TestToolSelect_OrderedOutput
func TestToolSelect_OrderedOutput(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_select_ordered"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "users_ordered"

	// Define Index Spec: "role" then "group" then "id"
	// Note: Alphabetical is group, id, role.
	// We want role first.
	idxSpec := jsondb.NewIndexSpecification([]jsondb.IndexFieldSpecification{
		{FieldName: "role", AscendingSortOrder: true},
		{FieldName: "group", AscendingSortOrder: true},
		{FieldName: "id", AscendingSortOrder: true},
	})
	idxSpecBytes, _ := encoding.DefaultMarshaler.Marshal(idxSpec)

	storeOpts := sop.StoreOptions{
		Name:                     storeName,
		SlotLength:               10,
		IsPrimitiveKey:           false,
		MapKeyIndexSpecification: string(idxSpecBytes),
	}

	if _, err := sopdb.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil, storeOpts); err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// Start new transaction for population
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	store, err := jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Add item
	itemKey := map[string]any{"id": 1, "group": "A", "role": "admin"}
	if _, err := store.Add(ctx, itemKey, "Alice"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Test
	args := map[string]any{
		"store": storeName,
	}

	result, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	// Check order in JSON string
	// Expected order: role, group, id
	// "role": "admin" ... "group": "A" ... "id": 1
	// Note: json.MarshalIndent adds spaces after colons.

	roleIdx := strings.Index(result, "\"role\": \"admin\"")
	groupIdx := strings.Index(result, "\"group\": \"A\"")
	idIdx := strings.Index(result, "\"id\": 1")

	if roleIdx == -1 || groupIdx == -1 || idIdx == -1 {
		// Fallback to check without spaces if indentation changes
		roleIdx = strings.Index(result, "\"role\":\"admin\"")
		groupIdx = strings.Index(result, "\"group\":\"A\"")
		idIdx = strings.Index(result, "\"id\":1")
	}

	if roleIdx == -1 || groupIdx == -1 || idIdx == -1 {
		t.Fatalf("Missing fields in result: %s", result)
	}

	// NOTE: With IndexSpec support removed, output is alphabetical (group, id, role)
	if !(groupIdx < idIdx && idIdx < roleIdx) {
		t.Errorf("Ordering failed! Expected group < id < role.\nIndices: role=%d, group=%d, id=%d\nOutput: %s", roleIdx, groupIdx, idIdx, result)
	}

	// Test case 2: Partial match
	// Index: role, group, id
	// Item: id=2, role=user (missing group)
	// Expected: role, id (group skipped)

	// We need a new transaction to add more items
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	store, err = jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore 2 failed: %v", err)
	}

	itemKey2 := map[string]any{"id": 2, "role": "user"}
	if _, err := store.Add(ctx, itemKey2, "Bob"); err != nil {
		t.Fatalf("Add 2 failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit 2 failed: %v", err)
	}

	// Close old read transaction and start a new one to see Bob
	readTx.Rollback(ctx)
	readTx, err = db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read 2 failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// Select again
	result, err = agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect 2 failed: %v", err)
	}

	// Check Bob's entry
	roleUserIdx := strings.Index(result, "\"role\": \"user\"")
	id2Idx := strings.Index(result, "\"id\": 2")

	if roleUserIdx == -1 {
		roleUserIdx = strings.Index(result, "\"role\":\"user\"")
	}
	if id2Idx == -1 {
		id2Idx = strings.Index(result, "\"id\":2")
	}

	if roleUserIdx == -1 || id2Idx == -1 {
		t.Fatalf("Missing fields for Bob: %s", result)
	}

	// Alphabetical: id < role
	if id2Idx > roleUserIdx {
		t.Errorf("Ordering failed! Expected id before role for Bob (Alpha).\nIndices: role=%d, id=%d\nOutput: %s", roleUserIdx, id2Idx, result)
	}
}

// Merged TestToolSelect_LegacyOrderedOutput
func TestToolSelect_LegacyOrderedOutput(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_select_legacy"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.NoCache,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "users_legacy"

	// Define Index Spec: "role" then "group" then "id"
	idxSpec := jsondb.NewIndexSpecification([]jsondb.IndexFieldSpecification{
		{FieldName: "role", AscendingSortOrder: true},
		{FieldName: "group", AscendingSortOrder: true},
		{FieldName: "id", AscendingSortOrder: true},
	})
	idxSpecBytes, _ := encoding.DefaultMarshaler.Marshal(idxSpec)

	// Simulate legacy store by putting spec in CELexpression and leaving MapKeyIndexSpecification empty
	storeOpts := sop.StoreOptions{
		Name:                     storeName,
		SlotLength:               10,
		IsPrimitiveKey:           false,
		CELexpression:            string(idxSpecBytes), // Legacy field
		MapKeyIndexSpecification: "",                   // Empty
		CacheConfig: &sop.StoreCacheConfig{
			StoreInfoCacheDuration: 1 * time.Nanosecond, // Force expire immediately
		},
	}

	// Note: We use NewBtree directly to bypass some of the jsondb helpers that might auto-populate MapKeyIndexSpecification
	if _, err := sopdb.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil, storeOpts); err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// TAMPERING: Manually modify storeinfo.txt to simulate legacy state
	// (Empty MapKeyIndexSpecification, populated LegacyCELexpression)
	storeInfoPath := dbPath + "/users_legacy/storeinfo.txt"
	infoBytes, err := os.ReadFile(storeInfoPath)
	if err != nil {
		t.Fatalf("Failed to read storeinfo: %v", err)
	}

	// We need to manipulate the JSON directly because unmarshaling into StoreInfo might normalize it.
	// Or we can unmarshal to map[string]any.
	var infoMap map[string]any
	if err := encoding.DefaultMarshaler.Unmarshal(infoBytes, &infoMap); err != nil {
		t.Fatalf("Failed to unmarshal storeinfo: %v", err)
	}

	// Move mapkey_index_spec to cel_expression if needed, or just ensure state.
	if spec, ok := infoMap["mapkey_index_spec"]; ok {
		infoMap["cel_expression"] = spec
		infoMap["mapkey_index_spec"] = ""
	}

	newInfoBytes, _ := encoding.DefaultMarshaler.Marshal(infoMap)
	if err := os.WriteFile(storeInfoPath, newInfoBytes, 0644); err != nil {
		t.Fatalf("Failed to write tampered storeinfo: %v", err)
	}

	// Start new transaction for population
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	// We must use OpenJsonBtreeMapKey to ensure it reads the legacy spec correctly?
	// Or just OpenStore.
	store, err := jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Add item
	itemKey := map[string]any{"id": 1, "group": "A", "role": "admin"}
	if _, err := store.Add(ctx, itemKey, "Alice"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Test
	args := map[string]any{
		"store": storeName,
	}

	result, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	// 4. Verify Order
	// Expected: Alphabetical order (default for maps)
	// JSON: {"group":"A","id":1,"role":"admin"}

	// Check if "group" appears before "role"
	roleIdx := strings.Index(result, "\"role\"")
	groupIdx := strings.Index(result, "\"group\"")

	if roleIdx == -1 || groupIdx == -1 {
		t.Fatalf("Missing keys in result: %s", result)
	}

	if groupIdx > roleIdx {
		t.Errorf("Expected 'group' before 'role' (alphabetical), but got: %s", result)
	}
}

// Merged TestReproSelect_NestedFilter
func TestReproSelect_NestedFilter(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_repro_select_nested"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "products"
	store, err := jsondb.CreateObjectStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("CreateObjectStore failed: %v", err)
	}

	// Add items
	items := []struct {
		ID    string
		Value map[string]any
	}{
		{ID: "p1", Value: map[string]any{"name": "TV", "category": "Electronics", "price": 500}},
	}

	for _, item := range items {
		if _, err := store.Add(ctx, item.ID, item.Value); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	sessionPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Run Select with nested filter
	args := map[string]any{
		"store": "products",
		// The LLM is passing this structure
		"filter": map[string]any{
			"category": "Electronics",
		},
	}

	// Currently, toolSelect treats `filter` as just another field matching argument?
	// It puts "filter" -> {...} into `valueMatch`.
	// Then `matchesKey(itemValue, valueMatch)` is called.
	// If itemValue is {category: Electronics}, and valueMatch is {filter: {category: Electronics}}.
	// matchesKey will look for "filter" field in itemValue. It won't find it.
	// So it returns false. Result is null/empty.

	result, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	t.Logf("Result: %s", result)

	if !strings.Contains(result, "TV") {
		t.Errorf("Expected TV in results. Nested dictionary 'filter' support missing.")
	}
}
