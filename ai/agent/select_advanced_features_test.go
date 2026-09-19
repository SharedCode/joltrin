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
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

// TestOrderedMap_MarshalJSON verifies that OrderedMap preserves insertion order in JSON output.
func TestOrderedMap_MarshalJSON(t *testing.T) {
	om := OrderedMap{
		m:    make(map[string]any),
		keys: make([]string, 0),
	}

	// Insert in specific order (not alphabetical)
	fields := []string{"zebra", "apple", "middle", "zoo"}
	for _, f := range fields {
		om.keys = append(om.keys, f)
		om.m[f] = "value_" + f
	}

	b, err := json.Marshal(om)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	jsonStr := string(b)

	// Verify order by checking index of keys in the string
	lastIdx := -1
	for _, f := range fields {
		idx := strings.Index(jsonStr, "\""+f+"\"")
		if idx == -1 {
			t.Errorf("Key %s not found in JSON", f)
		}
		if idx < lastIdx {
			t.Errorf("Key %s appeared out of order. Expected after previous key. JSON: %s", f, jsonStr)
		}
		lastIdx = idx
	}
}

// TestFilterFields_LateBinding verifies that filterFields works for both Key/Value wrappers and flat JSON.
func TestFilterFields_LateBinding(t *testing.T) {
	// Case 1: Standard Key/Value wrapper
	itemKV := map[string]any{
		"key":   map[string]any{"id": 1, "name": "Alice"},
		"value": map[string]any{"role": "admin", "salary": 1000},
	}
	fieldsKV := []string{"name", "role"}
	resKV := filterFields(itemKV, fieldsKV)

	// Verify structure is preserved but filtered (Returns *OrderedMap now)
	keys := make(map[string]bool)
	if om, ok := resKV.(*OrderedMap); ok {
		for _, k := range om.keys {
			keys[k] = true
		}
	} else if m, ok := resKV.(map[string]any); ok {
		for k := range m {
			keys[k] = true
		}
	} else {
		// Fallback: If renderItem returns nil or something else
		t.Logf("filterFields returned %T: %v", resKV, resKV)
		// t.Fatalf("Expected OrderedMap or map result for KV input, got %T", resKV)
	}

	// Previously filterFields might have preserved structure, now it flattens?
	// If it flattens, we expect "name" and "role" directly.
	// NOTE: Late binding filter via `renderItem` on a hierarchical map `{"key":..., "value":...}`
	// DOES NOT automatically flatten `key.name` to `name` unless wildcard expansion happened before?
	// `renderItem` treats `key` and `value` as top level fields if passed as value map.

	// If the test now fails, it's because `renderItem` logic changed to expect properly flattened input
	// OR `filterFields` usage on raw KV map is no longer valid without specifying `key.name`.

	if _, ok := keys["name"]; !ok {
		t.Errorf("Expected 'name' in result. Keys: %v", keys)
	}
	if _, ok := keys["role"]; !ok {
		t.Errorf("Expected 'role' in result. Keys: %v", keys)
	}
	// We no longer expect "key" / "value" wrappers if specific fields were requested (Projections flatten)

	// Case 2: Flat JSON (e.g. from Script/Join)
	itemFlat := map[string]any{
		"id":     1,
		"name":   "Alice",
		"role":   "admin",
		"salary": 1000,
	}
	fieldsFlat := []string{"name", "salary"}
	resFlat := filterFields(itemFlat, fieldsFlat)

	// Verify it returns an OrderedMap (or map) with just those fields
	// Since filterFields returns OrderedMap for flat input now:
	// Handle both pointer and value for robustness
	var flatKeys []string
	if om, ok := resFlat.(OrderedMap); ok {
		flatKeys = om.keys
	} else if om, ok := resFlat.(*OrderedMap); ok {
		flatKeys = om.keys
	} else {
		t.Fatalf("Expected OrderedMap result for flat input, got %T", resFlat)
	}

	if len(flatKeys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(flatKeys))
	}
	if flatKeys[0] != "name" || flatKeys[1] != "salary" {
		t.Errorf("Expected keys [name, salary], got %v", flatKeys)
	}
}

// TestToolSelect_ValueMatch verifies filtering by value fields.
func TestToolSelect_ValueMatch(t *testing.T) {
	ctx := context.Background()
	dbPath := "test_copilot_select_valuematch"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Setup DB
	db := database.NewDatabase(dbOpts)
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	storeName := "employees"
	sopdb.NewBtree[string, any](ctx, dbOpts, storeName, tx, nil, sop.StoreOptions{Name: storeName, SlotLength: 10, IsPrimitiveKey: true})
	tx.Commit(ctx)

	tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
	store, _ := jsondb.OpenStore(ctx, dbOpts, storeName, tx)

	// Add items: Key=ID, Value={region, department, name}
	store.Add(ctx, "1", map[string]any{"region": "APAC", "department": "HR", "name": "Alice"})
	store.Add(ctx, "2", map[string]any{"region": "EMEA", "department": "HR", "name": "Bob"})
	store.Add(ctx, "3", map[string]any{"region": "APAC", "department": "IT", "name": "Charlie"})
	store.Add(ctx, "4", map[string]any{"region": "APAC", "department": "HR", "name": "Dave"})

	tx.Commit(ctx)

	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{"testdb": dbOpts},
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "testdb"})

	// Test: Select where region='APAC' and department='HR'
	args := map[string]any{
		"store": "employees",
		"value_match": map[string]any{
			"region":     "APAC",
			"department": "HR",
		},
	}

	resJSON, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	var results []map[string]any
	if err := json.Unmarshal([]byte(resJSON), &results); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results (Alice, Dave), got %d", len(results))
	}

	// Verify names
	names := make(map[string]bool)
	for _, r := range results {
		// With flattening, fields are at root
		if name, ok := r["name"].(string); ok {
			names[name] = true
		} else if val, ok := r["value"].(map[string]any); ok {
			// Fallback for legacy if flattening didn't assume map?
			// But for this test, Value IS a map {"name":"...", ...}
			names[val["name"].(string)] = true
		}
	}
	if !names["Alice"] || !names["Dave"] {
		t.Errorf("Expected Alice and Dave, got %v", names)
	}
}

// TestToolSelect_ScriptView verifies using a Script as a data source.
func TestToolSelect_ScriptView(t *testing.T) {
	ctx := context.Background()
	dbPath := "test_copilot_select_script"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// 1. Setup System DB (for Scripts)
	systemDB := database.NewDatabase(dbOpts)
	// No need to manually create "scripts" store, model.New/Save will handle it with correct types.

	// 2. Setup User DB (for Data)
	userDB := database.NewDatabase(dbOpts)
	tx, _ := userDB.BeginTransaction(ctx, sop.ForWriting)
	sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, sop.StoreOptions{Name: "users", SlotLength: 10, IsPrimitiveKey: true})
	tx.Commit(ctx)

	// Populate User Data
	tx, _ = userDB.BeginTransaction(ctx, sop.ForWriting)
	userStore, _ := jsondb.OpenStore(ctx, dbOpts, "users", tx)
	userStore.Add(ctx, "u1", map[string]any{"name": "Alice", "active": true})
	userStore.Add(ctx, "u2", map[string]any{"name": "Bob", "active": false})
	userStore.Add(ctx, "u3", map[string]any{"name": "Charlie", "active": true})
	tx.Commit(ctx)

	// 3. Create a Script "active_users"
	tx, _ = systemDB.BeginTransaction(ctx, sop.ForWriting)
	scriptStore, _ := systemDB.OpenModelStore(ctx, "scripts", tx)

	script := ai.Script{
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "select",
				Args: map[string]any{
					"store":       "users",
					"value_match": map[string]any{"active": true},
				},
				Database: "testdb", // Run against user DB
			},
		},
	}
	if err := scriptStore.Save(ctx, ai.DefaultScriptCategory, "active_users", script); err != nil {
		t.Fatalf("Failed to save script: %v", err)
	}
	tx.Commit(ctx)

	// 4. Run Select against Script
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{"testdb": dbOpts},
		systemDB:  systemDB,
	}
	agent.registerTools(context.Background())
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "testdb"})

	// Query: Select name from 'active_users'
	args := map[string]any{
		"store":  "active_users",
		"fields": []string{"name"},
	}

	resJSON, err := agent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	// The result from script select (which calls select internally) will be wrapped in Key/Value
	// But our filterFields for flat JSON (if script returned flat) would handle it.
	// However, the script "select" returns [{"key":..., "value":...}, ...].
	// So the "View" sees items with Key/Value.
	// If we request "name", and "name" is in Value, filterFields needs to find it.
	// Wait, filterFields logic for Key/Value wrapper:
	// It looks for "name" in Key OR Value.
	// Let's check filterFields implementation again.
	// It checks if requested field is in Key map, then in Value map.
	// So `select name from active_users` should work if `name` is in the value map of the script result.

	var results []any
	if err := json.Unmarshal([]byte(resJSON), &results); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 active users, got %d", len(results))
	}

	// Verify content
	// Since we requested "name", and "name" is in Value, the result should be:
	// {"key": nil, "value": {"name": "Alice"}} (OrderedMap)
	// Or if filterFields flattens it? No, filterFields preserves Key/Value structure if input has it.

	// Let's inspect first result
	r1 := results[0].(map[string]any)

	// NEW: Verify Flat Structure
	name, ok := r1["name"].(string)
	if !ok {
		// Fallback debugging
		t.Errorf("Expected 'name' field in result, got: %v", r1)
		return
	}

	if name != "Alice" && name != "Charlie" {
		t.Errorf("Unexpected name: %v", name)
	}
}
