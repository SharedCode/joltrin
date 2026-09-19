package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_db "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

type listStoresTestPayload struct {
	Database string                `json:"database"`
	Stores   []listStoresTestStore `json:"stores"`
}

type listStoresTestStore struct {
	Name        string            `json:"name"`
	Schema      map[string]string `json:"schema"`       // Flat schema without prefixes
	KeyFields   []string          `json:"key_fields"`   // Fields in Key
	ValueFields []string          `json:"value_fields"` // Fields in Value
	Relations   []sop.Relation    `json:"relations"`
	Empty       bool              `json:"empty"`
}

func TestToolListStores_SchemaEnrichment(t *testing.T) {
	// 1. Setup Temp Dir
	tmpDir, err := os.MkdirTemp("", "sop_schema_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_schema"
	opts := sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	}

	// 2. Create Agent
	dbs := map[string]sop.DatabaseOptions{
		dbName: opts,
	}
	// Need to initialize generic registry if NewCopilotAgent doesn't do it properly for all tools.
	// But NewCopilotAgent does initialize registry.
	agent := NewCopilotAgent(Config{}, dbs, nil)
	ctx := context.Background()
	agent.Open(ctx)

	// 3. Populate Data directly

	// Create DB helper
	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Store
	store, err := jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		// cleanup
		if tx != nil {
			tx.Rollback(ctx)
		}
		t.Fatalf("CreateObjectStore failed: %v", err)
	}

	// Add Data
	// CreateObjectStore defaults to primitive string key
	if _, err := store.Add(ctx, "u1", map[string]interface{}{"first_name": "John", "age": 30}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add failed: %v", err)
	}

	// Create another store with INT keys (Primitive) to simulate "users_by_age" scenario
	// Note: We use database.NewBtree directly to bypass jsondb helpers for this specific test case.
	idxOpts := sop.StoreOptions{
		IsUnique:       false,
		IsPrimitiveKey: true,
	}
	idxStore, err := core_db.NewBtree[int, string](ctx, opts, "users_by_age", tx, nil, idxOpts)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Failed to create users_by_age: %v", err)
	}
	idxStore.Add(ctx, 30, "u1")

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Set Payload for agent tools
	payload := &ai.SessionPayload{
		CurrentDB: dbName,
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// 4. Call list_stores
	// We pass "database" arg explicitly or rely on session payload?
	// toolListStores logic: if dbName == "" { dbName = p.CurrentDB }
	// So we can pass empty map if p.CurrentDB is set.
	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var resultPayload listStoresTestPayload
	if err := json.Unmarshal([]byte(res), &resultPayload); err != nil {
		t.Fatalf("expected JSON list_stores payload, got %q: %v", res, err)
	}
	if resultPayload.Database != dbName {
		t.Fatalf("expected database %q, got %q", dbName, resultPayload.Database)
	}
	if len(resultPayload.Stores) != 2 {
		t.Fatalf("expected 2 stores in payload, got %+v", resultPayload.Stores)
	}
	users := resultPayload.Stores[1]
	if users.Name != "users" {
		users = resultPayload.Stores[0]
	}
	if users.Name != "users" {
		t.Fatalf("expected payload to contain users store, got %+v", resultPayload.Stores)
	}
	// Schema should have flat format without prefixes
	if users.Schema["first_name"] != "string" {
		t.Fatalf("expected first_name:string in schema, got %+v", users.Schema)
	}
	if users.Schema["age"] != "number" {
		t.Fatalf("expected age:number in schema, got %+v", users.Schema)
	}
	if users.Schema["key"] != "string" {
		t.Fatalf("expected key:string in schema, got %+v", users.Schema)
	}
	// Check KeyFields and ValueFields
	if len(users.KeyFields) != 1 || users.KeyFields[0] != "key" {
		t.Fatalf("expected key_fields:[\"key\"], got %+v", users.KeyFields)
	}
	if len(users.ValueFields) != 2 {
		t.Fatalf("expected 2 value_fields, got %+v", users.ValueFields)
	}
	// ValueFields should contain both first_name and age (order may vary)
	hasFirstName := false
	hasAge := false
	for _, field := range users.ValueFields {
		if field == "first_name" {
			hasFirstName = true
		}
		if field == "age" {
			hasAge = true
		}
	}
	if !hasFirstName || !hasAge {
		t.Fatalf("expected value_fields to contain first_name and age, got %+v", users.ValueFields)
	}
}

func TestToolListStores_FiltersRequestedStores(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_schema_filter_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_schema_filter"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	users, err := jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore users failed: %v", err)
	}
	if _, err := users.Add(ctx, "u1", map[string]interface{}{"first_name": "John"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add users failed: %v", err)
	}

	orders, err := jsondb.CreateObjectStore(ctx, opts, "orders", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore orders failed: %v", err)
	}
	if _, err := orders.Add(ctx, "o1", map[string]interface{}{"total_amount": 1500}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add orders failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: dbName})
	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName, "stores": []any{"orders"}})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var payload listStoresTestPayload
	if err := json.Unmarshal([]byte(res), &payload); err != nil {
		t.Fatalf("expected JSON list_stores payload, got %q: %v", res, err)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "orders" {
		t.Fatalf("expected only orders store, got %+v", payload.Stores)
	}
	// Check flat schema format
	if payload.Stores[0].Schema["total_amount"] != "number" {
		t.Fatalf("expected total_amount:number in schema, got %+v", payload.Stores[0].Schema)
	}
	if len(payload.Stores[0].ValueFields) == 0 {
		t.Fatalf("expected value_fields to be populated, got %+v", payload.Stores[0].ValueFields)
	}
}

func TestToolListStores_FuzzyMatchesRequestedStores(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_schema_fuzzy_filter_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_schema_fuzzy_filter"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	orders, err := jsondb.CreateObjectStore(ctx, opts, "orders", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore orders failed: %v", err)
	}
	if _, err := orders.Add(ctx, "o1", map[string]interface{}{"total_amount": 1500}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add orders failed: %v", err)
	}

	usersOrders, err := jsondb.CreateObjectStore(ctx, opts, "users_orders", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore users_orders failed: %v", err)
	}
	if _, err := usersOrders.Add(ctx, "uo1", map[string]interface{}{"user_id": "u1"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add users_orders failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: dbName, CurrentUserQuery: "Find order totals"})
	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName, "stores": []any{"order"}})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var payload listStoresTestPayload
	if err := json.Unmarshal([]byte(res), &payload); err != nil {
		t.Fatalf("expected JSON list_stores payload, got %q: %v", res, err)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "orders" {
		t.Fatalf("expected fuzzy match to resolve to orders only, got %+v", payload.Stores)
	}
}

func TestToolListStores_InfersLikelyStoresFromUserQuery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_schema_query_filter_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_schema_query_filter"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	fixtures := map[string]map[string]interface{}{
		"users":        {"first_name": "John"},
		"orders":       {"total_amount": 1500},
		"users_orders": {"user_id": "u1", "order_id": "o1"},
		"payments":     {"status": "paid"},
	}
	for name, sample := range fixtures {
		store, err := jsondb.CreateObjectStore(ctx, opts, name, tx)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("CreateObjectStore %s failed: %v", name, err)
		}
		if _, err := store.Add(ctx, name+"_1", sample); err != nil {
			tx.Rollback(ctx)
			t.Fatalf("Add %s failed: %v", name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: dbName, CurrentUserQuery: "Find orders for users named John"})
	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var payload listStoresTestPayload
	if err := json.Unmarshal([]byte(res), &payload); err != nil {
		t.Fatalf("expected JSON list_stores payload, got %q: %v", res, err)
	}
	seen := make(map[string]bool, len(payload.Stores))
	for _, store := range payload.Stores {
		seen[store.Name] = true
	}
	if !seen["users"] || !seen["orders"] || !seen["users_orders"] {
		t.Fatalf("expected query-derived narrowing to keep likely related stores, got %+v", payload.Stores)
	}
	if seen["payments"] {
		t.Fatalf("expected query-derived narrowing to exclude unrelated stores, got %+v", payload.Stores)
	}
}

func TestToolListStores_ReturnsProgressEnvelopeForNativeHints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_schema_native_hint_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_schema_native_hint"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	users, err := jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore users failed: %v", err)
	}
	if _, err := users.Add(ctx, "u1", map[string]interface{}{"first_name": "John"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add users failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: dbName})
	ctx = context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)

	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var envelope ai.ToolResultEnvelope
	if err := json.Unmarshal([]byte(res), &envelope); err != nil {
		t.Fatalf("expected native hint envelope, got %q: %v", res, err)
	}
	if envelope.ProgressHint == nil || envelope.ProgressHint.Status != "progressing" {
		t.Fatalf("expected progressing hint, got %+v", envelope.ProgressHint)
	}
	if len(envelope.ProgressHint.SuggestedNextTools) != 1 || envelope.ProgressHint.SuggestedNextTools[0] != "execute_script" {
		t.Fatalf("expected execute_script as suggested next tool, got %+v", envelope.ProgressHint)
	}
	var payload listStoresTestPayload
	if err := json.Unmarshal(envelope.ToolResult, &payload); err != nil {
		t.Fatalf("expected structured tool_result payload, got %s: %v", string(envelope.ToolResult), err)
	}
	if len(payload.Stores) != 1 || payload.Stores[0].Name != "users" {
		t.Fatalf("expected tool result payload to contain users store, got %+v", payload.Stores)
	}
	if payload.Stores[0].Schema["first_name"] != "string" {
		t.Fatalf("expected users schema without prefix in tool_result payload, got %+v", payload.Stores[0].Schema)
	}
	if len(envelope.ProgressHint.Clues) == 0 || !strings.Contains(envelope.ProgressHint.Clues[0], "users") {
		t.Fatalf("expected grounded clue in progress hint, got %+v", envelope.ProgressHint)
	}
}

// UserKey is a composite struct key for testing Key.field_name schema format
type UserKey struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func compareUserKey(a, b UserKey) int {
	if a.FirstName != b.FirstName {
		if a.FirstName < b.FirstName {
			return -1
		}
		return 1
	}
	if a.LastName < b.LastName {
		return -1
	}
	if a.LastName > b.LastName {
		return 1
	}
	return 0
}

func TestToolListStores_StructKeySchema(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_struct_key_schema_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_struct_key_schema"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create store with struct key using core_db.NewBtree directly
	storeOpts := sop.StoreOptions{
		Name:           "users_by_name",
		IsUnique:       true,
		IsPrimitiveKey: false,
	}
	store, err := core_db.NewBtree[UserKey, map[string]interface{}](ctx, opts, "users_by_name", tx, compareUserKey, storeOpts)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("NewBtree failed: %v", err)
	}

	// Add item with struct key
	key := UserKey{FirstName: "John", LastName: "Doe"}
	value := map[string]interface{}{"age": 30, "email": "john.doe@example.com"}
	if _, err := store.Add(ctx, key, value); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Query schema via list_stores tool
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: dbName})
	res, err := agent.toolListStores(ctx, map[string]any{"database": dbName})
	if err != nil {
		t.Fatalf("toolListStores failed: %v", err)
	}

	var payload listStoresTestPayload
	if err := json.Unmarshal([]byte(res), &payload); err != nil {
		t.Fatalf("expected JSON payload, got %q: %v", res, err)
	}

	if len(payload.Stores) != 1 {
		t.Fatalf("expected 1 store, got %d: %+v", len(payload.Stores), payload.Stores)
	}

	store_info := payload.Stores[0]
	if store_info.Name != "users_by_name" {
		t.Fatalf("expected store name 'users_by_name', got %q", store_info.Name)
	}

	// Verify flat schema (no prefixes)
	if store_info.Schema["first_name"] != "string" {
		t.Fatalf("expected first_name:string in schema, got %+v", store_info.Schema)
	}
	if store_info.Schema["last_name"] != "string" {
		t.Fatalf("expected last_name:string in schema, got %+v", store_info.Schema)
	}
	if store_info.Schema["age"] != "number" {
		t.Fatalf("expected age:number in schema, got %+v", store_info.Schema)
	}
	if store_info.Schema["email"] != "string" {
		t.Fatalf("expected email:string in schema, got %+v", store_info.Schema)
	}

	// Verify KeyFields contains first_name and last_name
	if len(store_info.KeyFields) != 2 {
		t.Fatalf("expected 2 key_fields, got %+v", store_info.KeyFields)
	}
	hasFirstName := false
	hasLastName := false
	for _, field := range store_info.KeyFields {
		if field == "first_name" {
			hasFirstName = true
		}
		if field == "last_name" {
			hasLastName = true
		}
	}
	if !hasFirstName || !hasLastName {
		t.Fatalf("expected key_fields to contain first_name and last_name, got %+v", store_info.KeyFields)
	}

	// Verify ValueFields contains age and email
	if len(store_info.ValueFields) != 2 {
		t.Fatalf("expected 2 value_fields, got %+v", store_info.ValueFields)
	}
	hasAge := false
	hasEmail := false
	for _, field := range store_info.ValueFields {
		if field == "age" {
			hasAge = true
		}
		if field == "email" {
			hasEmail = true
		}
	}
	if !hasAge || !hasEmail {
		t.Fatalf("expected value_fields to contain age and email, got %+v", store_info.ValueFields)
	}
}
