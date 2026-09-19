package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
)

func TestProject_JoinedFields_PrefixIssue(t *testing.T) {
	// 1. Setup DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Stores
	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"uid": "1", "first_name": "John", "last_name": "Doe"})
	users.Add(ctx, "u2", map[string]any{"uid": "2", "first_name": "Jane", "last_name": "Smith"})

	orders, _ := core_database.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"key": "ORD-1", "uid": "1", "total_amount": 600, "status": "Delivered"})
	orders.Add(ctx, "o2", map[string]any{"key": "ORD-2", "uid": "1", "total_amount": 100, "status": "Pending"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agentCfg := Config{ID: "test_admin"}
	agent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	// 3. Script mimicking 'john_orders' logic
	// Scan users -> Join orders -> Filter -> Project
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "db"},
{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},

{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "s_users"},
{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "s_orders"},

{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream"},

{"op": "join", "args": {"store": "@s_orders", "type": "inner", "on": {"uid": "uid"}}, "input_var": "stream", "result_var": "joined"},

{"op": "project", "args": {"fields": ["users.first_name", "users.last_name", "orders.key", "orders.total_amount", "orders.status"]}, "input_var": "joined", "result_var": "output"},

{"op": "commit_tx", "args": {"transaction": "tx"}}
]`

	sessionPayload := &ai.SessionPayload{CurrentDB: "test_db"}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	t.Logf("Response: %s", resp)

	// Valdiate JSON structure
	var results []map[string]any
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got empty list")
	}

	row := results[0]
	// Check fields
	if _, ok := row["first_name"]; !ok {
		// Maybe it kept prefix?
		if _, ok2 := row["users.first_name"]; !ok2 {
			t.Errorf("Missing fields: got %v", row)
		}
	}
}

func TestProject_EmptyFields_Flattening(t *testing.T) {
	// 1. Setup DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)

	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Stores
	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"uid": "1", "first_name": "John", "Age": 50}) // Add Age to verify flattening

	orders, _ := core_database.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"key": "ORD-1", "uid": "1"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agentCfg := Config{ID: "test_admin"}
	agent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	// 3. Script with EMPTY fields []
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "db"},
{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "s_users"},
{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "s_orders"},
{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream"},
{"op": "join", "args": {"store": "@s_orders", "type": "inner", "on": {"uid": "uid"}}, "input_var": "stream", "result_var": "joined"},
{"op": "project", "args": {"fields": []}, "input_var": "joined", "result_var": "output"},

{"op": "commit_tx", "args": {"transaction": "tx"}}
]`

	sessionPayload := &ai.SessionPayload{CurrentDB: "test_db"}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	t.Logf("Response: %s", resp)

	// Validate JSON structure
	var results []map[string]any
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got empty list")
	}

	row := results[0]
	// Check fields - Expect EMPTY (No Flattening)
	if _, ok := row["Age"]; ok {
		t.Errorf("Expected NO Age field (strict projection), got %v", row)
	}
}

func TestProject_Unprefixed_Match_Prefixed(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"uid": "1", "name": "John"})
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	agentCfg := Config{ID: "test_admin"}
	agent := &CopilotAgent{Config: agentCfg, databases: map[string]sop.DatabaseOptions{"test_db": dbOpts}, systemDB: sysDB}

	// Script: Scan users (produces users.name) -> Project "name"
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "db"},
{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "s_users"},
{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream"},
{"op": "project", "args": {"fields": ["name"]}, "input_var": "stream", "result_var": "output"},
{"op": "commit_tx", "args": {"transaction": "tx"}}
]`

	sessionPayload := &ai.SessionPayload{CurrentDB: "test_db"}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)
	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	t.Logf("Response: %s", resp)
	var results []map[string]any
	json.Unmarshal([]byte(resp), &results)
	if len(results) == 0 {
		t.Fatalf("Empty results")
	}

	row := results[0]
	if _, ok := row["name"]; !ok {
		t.Errorf("FAIL: Projecting 'name' failed to find 'users.name'!")
	}
}

func TestProject_Mixed_Prefix_Scenarios(t *testing.T) {
	// Scenario: Data source has mixed prefixed and unprefixed fields.
	// Users: {"users.name": "John"} (Prefixed)
	// Config: {"version": "1.0"} (Unprefixed)

	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Store 'config'
	config, _ := core_database.NewBtree[string, any](ctx, dbOpts, "config", tx, nil)
	config.Add(ctx, "c1", map[string]any{"version": "1.0"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	agentCfg := Config{ID: "test_admin"}
	agent := &CopilotAgent{Config: agentCfg, databases: map[string]sop.DatabaseOptions{"test_db": dbOpts}, systemDB: sysDB}

	// Script:
	// Scan config -> Project "version", "config.version"
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "db"},
{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
{"op": "open_store", "args": {"transaction": "tx", "name": "config"}, "result_var": "s_config"},
{"op": "scan", "args": {"store": "s_config"}, "result_var": "stream"},
{"op": "project", "args": {"fields": ["version", "config.version"]}, "input_var": "stream", "result_var": "output"},
{"op": "commit_tx", "args": {"transaction": "tx"}}
]`

	sessionPayload := &ai.SessionPayload{CurrentDB: "test_db"}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)
	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	var results []map[string]any
	json.Unmarshal([]byte(resp), &results)
	if len(results) == 0 {
		t.Fatalf("Empty results")
	}

	row := results[0]

	// Verify "version" found
	if v, ok := row["version"]; !ok || v != "1.0" {
		t.Errorf("Failed to resolve 'version' (Unprefixed Request). Got: %v", row)
	}

	// Verify "config.version" found (using prefixed request)
	if v, ok := row["config.version"]; !ok || v != "1.0" {
		t.Errorf("Failed to resolve 'config.version' (Prefixed Request). Got: %v", row)
	}
}

func TestProject_Alias_Explicit(t *testing.T) {
	// Scenario: Ensure "AS Alias" still works despite cleanName changes.

	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("Transaction failed: %v", err)
	}

	// Store 'config' with flat data
	config, _ := core_database.NewBtree[string, any](ctx, dbOpts, "config", tx, nil)
	config.Add(ctx, "c1", map[string]any{"version": "1.0"})

	// Store 'users' with prefixed data
	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"users.name": "John"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	agentCfg := Config{ID: "test_admin"}
	agent := &CopilotAgent{Config: agentCfg, databases: map[string]sop.DatabaseOptions{"test_db": dbOpts}, systemDB: sysDB}

	// Script:
	// 1. Project strict flat field with alias: "version AS v"
	// 2. Project prefixed field with alias: "config.version AS cv"
	// 3. Project prefixed data with alias: "users.name AS un"
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "db"},
{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
{"op": "open_store", "args": {"transaction": "tx", "name": "config"}, "result_var": "s_config"},
{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "s_users"},

{"op": "scan", "args": {"store": "s_config"}, "result_var": "stream1"},
{"op": "project", "args": {"fields": ["version AS v", "config.version AS cv"]}, "input_var": "stream1", "result_var": "out1"},

{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream2"},
{"op": "project", "args": {"fields": ["users.name AS un"]}, "input_var": "stream2", "result_var": "out2"},

{"op": "return", "args": {"value": ["@out1", "@out2"]}}
]`

	sessionPayload := &ai.SessionPayload{CurrentDB: "test_db"}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	var allRes [][]map[string]any
	if err := json.Unmarshal([]byte(resp), &allRes); err != nil {
		t.Fatalf("Parse error: %v. Resp: %s", err, resp)
	}

	// out1 checks
	if len(allRes) < 2 || len(allRes[0]) == 0 {
		t.Fatalf("Results missing or empty: %v", allRes)
	}

	out1 := allRes[0][0]
	if val, ok := out1["v"]; !ok || val != "1.0" {
		t.Errorf("Alias 'version AS v' failed. Got: %v", out1)
	}
	if val, ok := out1["cv"]; !ok || val != "1.0" {
		t.Errorf("Alias 'config.version AS cv' failed. Got: %v", out1)
	}
	// Ensure original names are NOT present
	if _, ok := out1["version"]; ok {
		t.Errorf("Original name 'version' leaked")
	}
	if _, ok := out1["config.version"]; ok {
		t.Errorf("Original name 'config.version' leaked")
	}

	// out2 checks
	out2 := allRes[1][0]
	if val, ok := out2["un"]; !ok || val != "John" {
		t.Errorf("Alias 'users.name AS un' failed. Got: %v", out2)
	}
}
