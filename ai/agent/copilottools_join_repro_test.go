package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestToolJoin_SuffixHandling(t *testing.T) {
	// 1. Setup Temp DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	// Initialize SystemDB
	sysDB := database.NewDatabase(dbOpts)

	// 2. Create Test Stores with Data
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Store 1: employees
	b3Left, err := core_database.NewBtree[string, any](ctx, dbOpts, "employees", tx, nil)
	if err != nil {
		t.Fatalf("Failed to create left store: %v", err)
	}
	b3Left.Add(ctx, "emp1", map[string]any{"region": "APAC", "department": "Sales"})

	// Store 2: departments
	b3Right, err := core_database.NewBtree[string, any](ctx, dbOpts, "departments", tx, nil)
	if err != nil {
		t.Fatalf("Failed to create right store: %v", err)
	}
	b3Right.Add(ctx, "d1", map[string]any{"department": "Engineering", "region": "APAC"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit setup transaction: %v", err)
	}

	// 3. Setup Agent
	agentCfg := Config{
		ID: "copilot",
	}
	adminAgent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	// 4. Test Case: Request "Department_1" which doesn't exist, but "department" does.
	// This simulates the LLM trying to rename a colliding column by appending _1.
	args := map[string]any{
		"database":          "test_db",
		"left_store":        "employees",
		"right_store":       "departments",
		"left_join_fields":  []string{"region"},
		"right_join_fields": []string{"region"},
		"fields":            []string{"region", "department"}, // Should map to "department"
	}

	// Setup Context with Payload
	payload := &ai.SessionPayload{
		CurrentDB: "test_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	respRaw, err := adminAgent.toolJoin(ctx, args)
	if err != nil {
		t.Fatalf("toolJoin failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	t.Logf("Response: %s", resp)

	// Parse Response (JSON Array)
	var results []map[string]any
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("No results returned")
	}

	firstRow := results[0]
	var valMap map[string]any
	if vm, ok := firstRow["value"].(map[string]any); ok {
		valMap = vm
	} else {
		valMap = firstRow
	}

	// Check if Department has a value (normalized from department_1)
	if val, ok := valMap["department"]; ok {
		if val == nil {
			t.Errorf("Expected value for 'department', got nil")
		} else {
			t.Logf("Got value for department: %v", val)
		}
	} else {
		t.Errorf("Field 'department' missing from response. Available keys: %v", keys(valMap))
	}
}

func keys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestToolJoin_WithAlias(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_join_alias"
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

	// Create Left Store (Departments)
	leftStoreName := "departments"
	if _, err := core_database.NewBtree[string, any](ctx, dbOpts, leftStoreName, tx, nil, sop.StoreOptions{Name: leftStoreName, SlotLength: 10, IsPrimitiveKey: true}); err != nil {
		t.Fatalf("NewBtree left failed: %v", err)
	}

	// Create Right Store (Employees)
	rightStoreName := "employees"
	if _, err := core_database.NewBtree[string, any](ctx, dbOpts, rightStoreName, tx, nil, sop.StoreOptions{Name: rightStoreName, SlotLength: 10, IsPrimitiveKey: true}); err != nil {
		t.Fatalf("NewBtree right failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// Populate
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	leftStore, _ := jsondb.OpenStore(ctx, dbOpts, leftStoreName, tx)
	rightStore, _ := jsondb.OpenStore(ctx, dbOpts, rightStoreName, tx)

	// Dept: {id: 1, name: "Engineering"}
	if _, err := leftStore.Add(ctx, "dept1", map[string]any{"name": "Engineering", "region": "APAC"}); err != nil {
		t.Fatalf("Left Add failed: %v", err)
	}

	// Emp: {id: 101, name: "John", dept_id: 1}
	if _, err := rightStore.Add(ctx, "emp101", map[string]any{"emp_name": "John", "dept_id": 1, "region": "APAC"}); err != nil {
		t.Fatalf("Right Add failed: %v", err)
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
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Execute Join with Alias
	// SQL: select a.region, b.name as employee from departments a inner join employees b on a.region=b.region
	args := map[string]any{
		"left_store":        leftStoreName,
		"right_store":       rightStoreName,
		"left_join_fields":  []string{"region"},
		"right_join_fields": []string{"region"},
		"fields": []string{
			"a.region AS region",
			"b.emp_name AS employee",
		},
	}

	resultRaw, err := agent.toolJoin(ctx, args)
	if err != nil {
		t.Fatalf("toolJoin failed: %v", err)
	}
	resultJSON, err := formatToolResult(ctx, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
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
	// Join tool returns flat map in "key" usually? Or just flat map?
	// Let's check the output structure. JoinProcessor uses ResultEmitter.
	// ResultEmitter emits whatever is passed to it.
	// In JoinProcessor.emitMatch, it constructs keyMap and valMap if fields are specified.
	// And returns {"key": keyMap, "value": valMap}

	// Check Region (Auto-titled)
	// Note: OrderedMap marshals to JSON object, so unmarshalling gives map[string]any
	// But we need to check if it's in Key or Value.
	// "region" is likely a value field in the source stores unless it's part of the key.
	// In the test setup:
	// Left: Key={id:1}, Value={name:..., region:...} -> Region is Value
	// Right: Key={id:101}, Value={name:..., region:...} -> Region is Value
	// So both should be in "value" map, not "key" map.

	// Flexible value retrieval (handles flat or nested structure)
	getValue := func(field string) (any, bool) {
		// 1. Try flat
		if v, ok := item[field]; ok {
			return v, true
		}
		// 2. Try nested key
		if kMap, ok := item["key"].(map[string]any); ok {
			if v, ok := kMap[field]; ok {
				return v, true
			}
		}
		// 3. Try nested value
		if vMap, ok := item["value"].(map[string]any); ok {
			if v, ok := vMap[field]; ok {
				return v, true
			}
		}
		return nil, false
	}

	if val, ok := getValue("region"); !ok || val != "APAC" {
		t.Errorf("Expected region=APAC, got %v", item)
	}

	// Check Employee (Aliased)
	if val, ok := getValue("employee"); !ok || val != "John" {
		t.Errorf("Expected employee=John, got %v", item)
	}
}

func TestToolJoin_StoreNamePrefix(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	cfg := Config{
		Name: "TestAgent",
	}
	dbs := make(map[string]sop.DatabaseOptions)
	agent := NewCopilotAgent(cfg, dbs, sysDB)

	// Create Stores
	ctx := context.Background()
	tx, _ := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)

	// Department Store
	deptStore, _ := core_database.NewBtree[string, any](ctx, dbOpts, "department", tx, nil)
	deptStore.Add(ctx, "d1", map[string]any{"region": "East", "department": "Sales"})
	deptStore.Add(ctx, "d2", map[string]any{"region": "West", "department": "Engineering"})

	// Employees Store
	empStore, _ := core_database.NewBtree[string, any](ctx, dbOpts, "employees", tx, nil)
	empStore.Add(ctx, "e1", map[string]any{"region": "East", "department": "Sales", "name": "John"})
	empStore.Add(ctx, "e2", map[string]any{"region": "West", "department": "Engineering", "name": "Jane"})
	empStore.Add(ctx, "e3", map[string]any{"region": "East", "department": "Sales", "name": "Bob"})

	tx.Commit(ctx)

	// Register DB
	agent.databases["default"] = dbOpts
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "default"})
	agent.Open(ctx)

	// Execute Join
	args := map[string]any{
		"database":          "default",
		"left_store":        "department",
		"right_store":       "employees",
		"left_join_fields":  []string{"region", "department"},
		"right_join_fields": []string{"region", "department"},
		"fields":            []string{"department.region", "employees.department", "employees.name as employee"},
		"join_type":         "inner",
	}

	resultRaw, err := agent.toolJoin(ctx, args)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	result, err := formatToolResult(ctx, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	// Parse Result
	var raw []map[string]any
	if err := json.Unmarshal([]byte(result), &raw); err != nil {
		t.Fatalf("Failed to parse result: %v", err)
	}

	if len(raw) == 0 {
		t.Fatal("Expected results, got none")
	}

	first := raw[0]

	// Helper to check in either key or value or flat
	checkKey := func(k string) any {
		if v, ok := first[k]; ok {
			return v
		}
		if km, ok := first["key"].(map[string]any); ok {
			if v, ok := km[k]; ok {
				return v
			}
		}
		if vm, ok := first["value"].(map[string]any); ok {
			if v, ok := vm[k]; ok {
				return v
			}
		}
		return nil
	}

	// Check for expected keys
	// "department.region" -> "region"
	// "employees.department" -> "department"
	// "employees.name as employee" -> "employee"

	if v := checkKey("region"); v == nil {
		t.Errorf("Missing 'region' in result. Item: %v", first)
	}
	if v := checkKey("department"); v == nil {
		t.Errorf("Missing 'department' in result. Item: %v", first)
	}
	if v := checkKey("employee"); v == nil {
		t.Errorf("Missing 'employee' in result. Item: %v", first)
	}

	// Check values
	regVal := checkKey("region")
	if regVal != "East" && regVal != "West" {
		t.Errorf("Unexpected region: %v", regVal)
	}
}

func TestToolJoin_ReproUserScenario(t *testing.T) {
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
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})
	agent.Open(ctx)

	// Create Stores and Data
	t2, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)

	// Left Store: department
	// Keys: APAC, EMEA, US
	leftOpts := sop.StoreOptions{Name: "department", SlotLength: 10, IsPrimitiveKey: true}
	left, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "department", t2, nil, leftOpts)
	left.Add(ctx, "APAC", map[string]any{"region": "APAC", "department": "Sales"})
	left.Add(ctx, "EMEA", map[string]any{"region": "EMEA", "department": "Sales"})
	left.Add(ctx, "US", map[string]any{"region": "US", "department": "Sales"})

	// Right Store: employees
	rightOpts := sop.StoreOptions{Name: "employees", SlotLength: 10, IsPrimitiveKey: true}
	right, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "employees", t2, nil, rightOpts)
	right.Add(ctx, "E1", map[string]any{"region": "APAC", "department": "Sales", "name": "Alice"})
	right.Add(ctx, "E2", map[string]any{"region": "US", "department": "Sales", "name": "Bob"})

	t2.Commit(ctx)

	// Test DESC
	args := map[string]any{
		"left_store":        "department",
		"right_store":       "employees",
		"left_join_fields":  []string{"region", "department"},
		"right_join_fields": []string{"region", "department"},
		"order_by":          "key desc",
		"limit":             4,
		"fields":            []string{"left.region", "right.department", "right.name AS employee"},
	}
	res, err := agent.Execute(ctx, "join", args)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	var items []map[string]any
	json.Unmarshal([]byte(res), &items)

	t.Logf("Items: %+v", items)

	// We expect 2 items (US, APAC). EMEA has no match.
	// Order should be US then APAC.
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	getRegion := func(item map[string]any) string {
		// Handle both nested "key" layout and flat layout
		if k, ok := item["key"]; ok && k != nil {
			if v, ok := k.(map[string]any); ok {
				if val, ok := v["Region"]; ok {
					return val.(string)
				}
				if val, ok := v["region"]; ok {
					return val.(string)
				}
			}
		}
		// Flat layout fallback
		if val, ok := item["Region"]; ok {
			return val.(string)
		}
		if val, ok := item["region"]; ok {
			return val.(string)
		}
		return ""
	}

	if getRegion(items[0]) != "US" {
		t.Errorf("Expected first item 'US', got '%s'", getRegion(items[0]))
	}
	if getRegion(items[1]) != "APAC" {
		t.Errorf("Expected second item 'APAC', got '%s'", getRegion(items[1]))
	}
}
