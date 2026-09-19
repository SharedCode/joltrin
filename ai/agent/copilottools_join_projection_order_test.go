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
)

func TestToolJoin_ProjectionOrder_WithFieldsString(t *testing.T) {
	ctx := context.Background()
	dbPath := "test_join_projection_order"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{StoresFolders: []string{dbPath}, CacheType: sop.InMemory}
	sysDB := database.NewDatabase(dbOpts)

	adminAgent := &CopilotAgent{
		Config:    Config{ID: "copilot"},
		databases: map[string]sop.DatabaseOptions{"default": dbOpts},
		systemDB:  sysDB,
	}

	tx, err := sopdb.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	deptStore, err := sopdb.NewBtree[string, any](ctx, dbOpts, "department", tx, nil)
	if err != nil {
		t.Fatalf("NewBtree department failed: %v", err)
	}
	empStore, err := sopdb.NewBtree[string, any](ctx, dbOpts, "employee", tx, nil)
	if err != nil {
		t.Fatalf("NewBtree employee failed: %v", err)
	}
	deptStore.Add(ctx, "d1", map[string]any{"region": "APAC", "department": "HR"})
	empStore.Add(ctx, "e1", map[string]any{"region": "APAC", "department": "HR", "name": "Employee 14"})
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "default"})

	args := map[string]any{
		"database":          "default",
		"left_store":        "department",
		"right_store":       "employee",
		"join_type":         "inner",
		"left_join_fields":  []string{"region", "department"},
		"right_join_fields": []string{"region", "department"},
		// Removed table prefixes (a., b.) as Join produces flat map
		"fields": "region, department, name as employee",
		"limit":  4,
	}

	resRaw, err := adminAgent.toolJoin(ctx, args)
	if err != nil {
		t.Fatalf("toolJoin failed: %v", err)
	}
	res, err := formatToolResult(ctx, resRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	// Verify order in raw JSON output (map-based unmarshalling would lose ordering).
	regionIdx := strings.Index(res, `"region"`)
	deptIdx := strings.Index(res, `"department"`)
	empIdx := strings.Index(res, `"employee"`)
	if regionIdx == -1 || deptIdx == -1 || empIdx == -1 {
		t.Fatalf("Missing expected fields in output: %s", res)
	}
	if !(regionIdx < deptIdx && deptIdx < empIdx) {
		t.Fatalf("Projection order not preserved. Expected region then department then employee. Got: %s", res)
	}
}

func TestToolJoin_ProjectionOrder_Complex_Wildcard(t *testing.T) {
	// 1. Setup DB
	ctx := context.Background()
	dbPath := "test_proj_order_complex"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}
	db := database.NewDatabase(dbOpts)
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 2. Populate Stores
	users, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, sop.StoreOptions{Name: "users", SlotLength: 10, IsPrimitiveKey: true})
	if err != nil {
		t.Fatalf("Setup users failed: %v", err)
	}
	users.Add(ctx, "u1", map[string]any{"name": "Alice", "age": 30, "city": "NY"})

	orders, err := sopdb.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil, sop.StoreOptions{Name: "orders", SlotLength: 10, IsPrimitiveKey: true})
	if err != nil {
		t.Fatalf("Setup orders failed: %v", err)
	}
	orders.Add(ctx, "o1", map[string]any{"user_id": "u1", "amount": 100})

	err = tx.Commit(ctx)
	if err != nil {
		t.Fatalf("Setup Commit failed: %v", err)
	}

	// 3. Script Execution
	agent := &CopilotAgent{
		Config: Config{ID: "test_admin"},
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
	}

	// Helper
	runScriptHelper := func(t *testing.T, script []map[string]any, name string, check func(string, []any)) {
		scriptJSON, _ := json.Marshal(script)
		var records []any
		streamCtx := context.WithValue(ctx, ai.CtxKeyEventStreamer, func(eventType string, payload any) {
			if eventType == "record" {
				records = append(records, payload)
			}
		})
		resRaw, err := agent.toolExecuteScript(streamCtx, map[string]any{"script": string(scriptJSON)})
		if err != nil {
			t.Fatalf("%s: Script failed: %v", name, err)
		}
		res, err := formatToolResult(streamCtx, resRaw)
		if err != nil {
			t.Fatalf("%s: formatToolResult failed: %v", name, err)
		}
		t.Logf("%s Result: %s", name, res)
		check(res, records)
	}

	// CASE 1: users.* first
	script1 := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "dev_db"}, "result_var": "db"},
		{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx"}, "result_var": "users"},
		{"op": "open_store", "args": map[string]any{"name": "orders", "transaction": "tx"}, "result_var": "orders"},
		{"op": "scan", "args": map[string]any{"store": "orders"}, "result_var": "orders_scan"},
		{"op": "join_right",
			"args": map[string]any{
				"store": "users",
				"on":    map[string]any{"orders.user_id": "key"},
			},
			"input_var":  "orders_scan",
			"result_var": "joined",
		},
		{"op": "project",
			"args": map[string]any{
				"fields": []string{"users.*", "orders.amount as Amount"},
			},
			"input_var":  "joined",
			"result_var": "result",
		},
		{"op": "return", "args": map[string]any{"value": "result"}},
	}

	runScriptHelper(t, script1, "CASE 1 (users.*, Amount)", func(res string, records []any) {
		if res != "" {
			t.Logf("non-empty passthrough result: %s", res)
		}
		if len(records) == 0 {
			t.Fatal("expected streamed records for explicit return")
		}
		payloadJSON, _ := json.Marshal(records[0])
		res = string(payloadJSON)
		// Expect 'name' (from users.*) BEFORE 'Amount'
		// Note: 'Amount' (A) < 'name' (n) alphabetically.
		// If sorted, Amount would be first.
		// If preserving projection, users.* (name) should be first.
		idxName := strings.Index(res, "\"name\"")
		idxAmount := strings.Index(res, "\"Amount\"")

		if idxName == -1 || idxAmount == -1 {
			t.Fatalf("Missing keys: %s", res)
		}
		if idxName > idxAmount {
			t.Errorf("CASE 1 FAIL: Expected 'users.*' (name) before 'Amount'. Got: %s", res)
		}

		if !strings.Contains(res, "\"name\"") || !strings.Contains(res, "\"Amount\"") {
			t.Fatalf("CASE 1 FAIL: Expected projected fields to be present. Got: %s", res)
		}
	})

	// CASE 2: Amount first
	script2 := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "dev_db"}, "result_var": "db"},
		{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx"}, "result_var": "users"},
		{"op": "open_store", "args": map[string]any{"name": "orders", "transaction": "tx"}, "result_var": "orders"},
		{"op": "scan", "args": map[string]any{"store": "orders"}, "result_var": "orders_scan"},
		{"op": "join_right",
			"args": map[string]any{
				"store": "users",
				"on":    map[string]any{"orders.user_id": "key"},
			},
			"input_var":  "orders_scan",
			"result_var": "joined",
		},
		{"op": "project",
			"args": map[string]any{
				"fields": []string{"orders.amount as Amount", "users.*"},
			},
			"input_var":  "joined",
			"result_var": "result",
		},
		{"op": "return", "args": map[string]any{"value": "result"}},
	}

	runScriptHelper(t, script2, "CASE 2 (Amount, users.*)", func(res string, records []any) {
		if len(records) == 0 {
			t.Fatal("expected streamed records for explicit return")
		}
		payloadJSON, _ := json.Marshal(records[0])
		res = string(payloadJSON)
		// Expect 'Amount' BEFORE 'name'
		idxName := strings.Index(res, "\"name\"")
		idxAmount := strings.Index(res, "\"Amount\"")

		if idxName == -1 || idxAmount == -1 {
			t.Fatalf("Missing keys: %s", res)
		}
		if idxAmount > idxName {
			t.Errorf("CASE 2 FAIL: Expected 'Amount' before 'users.*' (name). Got: %s", res)
		}
	})
}
