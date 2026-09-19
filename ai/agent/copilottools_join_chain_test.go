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

func TestToolJoin_Chained_InnerThenRight(t *testing.T) {
	// Scenario: (Users INNER JOIN Orders) RIGHT JOIN Feedbacks
	// Users: 1 (Alice), 2 (Bob)
	// Orders: 101 (User 1), 102 (User 1). User 2 has no orders.
	// Feedbacks: F1 (Order 101 - Good), F2 (Order 999 - Orphan/External)

	// Expected Result:
	// 1. (U join O) -> { (U1, O101), (U1, O102) }
	// 2. (Result1 right join F) ->
	//    - F1 matches O101 -> { F1, U1, O101 }
	//    - F2 matches O999 -> No Match -> { F2, null, null }

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
	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users_chain", tx, nil)
	users.Add(ctx, "u1", map[string]any{"uid": 1.0, "name": "Alice"})
	users.Add(ctx, "u2", map[string]any{"uid": 2.0, "name": "Bob"})

	orders, _ := core_database.NewBtree[string, any](ctx, dbOpts, "orders_chain", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"oid": 101.0, "uid": 1.0, "item": "Book"})
	orders.Add(ctx, "o2", map[string]any{"oid": 102.0, "uid": 1.0, "item": "Pen"})

	feedbacks, _ := core_database.NewBtree[string, any](ctx, dbOpts, "feedbacks_chain", tx, nil)
	feedbacks.Add(ctx, "f1", map[string]any{"fid": "F1", "oid": 101.0, "msg": "Great"})
	feedbacks.Add(ctx, "f2", map[string]any{"fid": "F2", "oid": 999.0, "msg": "Unknown"})

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

	// 3. Script
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "test_db"},
{"op": "begin_tx", "args": {"database": "test_db", "mode": "read"}, "result_var": "tx"},

{"op": "open_store", "args": {"transaction": "tx", "name": "users_chain"}, "result_var": "s_users"},
{"op": "open_store", "args": {"transaction": "tx", "name": "orders_chain"}, "result_var": "s_orders"},
{"op": "open_store", "args": {"transaction": "tx", "name": "feedbacks_chain"}, "result_var": "s_feedbacks"},

{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream_users"},

{"op": "join", "args": {"store": "@s_orders", "type": "inner", "on": {"uid": "uid"}}, "input_var": "stream_users", "result_var": "users_orders"},

{"op": "join", "args": {"store": "@s_feedbacks", "type": "right", "on": {"s_orders.oid": "oid"}}, "input_var": "users_orders", "result_var": "output"},

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

	// 4. Validate
	var results []map[string]any
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	resultMap := make(map[string]map[string]any)
	for _, r := range results {
		var fid string
		if v, ok := r["fid"]; ok {
			fid = v.(string)
		} else if v, ok := r["feedbacks_chain.fid"]; ok {
			fid = v.(string)
		} else {
			t.Fatalf("Missing fid. Item: %+v", r)
		}
		resultMap[fid] = r
	}

	// Check F1 (Matched)
	if r, ok := resultMap["F1"]; !ok {
		t.Error("Missing F1")
	} else {
		name := r["name"]
		if name == nil {
			name = r["users_chain.name"]
		}
		if name != "Alice" {
			t.Errorf("F1 should map to Alice, got %v", name)
		}
		item := r["item"]
		if item == nil {
			item = r["orders_chain.item"]
		}
		if item != "Book" {
			t.Errorf("F1 should map to Book, got %v", item)
		}
	}

	// Check F2 (Unmatched)
	if r, ok := resultMap["F2"]; !ok {
		t.Error("Missing F2")
	} else {
		if r["name"] != nil {
			t.Errorf("F2 should have nil name, got %v", r["name"])
		}
		if r["s_orders.item"] != nil {
			t.Errorf("F2 should have nil item, got %v", r["s_orders.item"])
		}
		msg := r["msg"]
		if msg == nil {
			msg = r["feedbacks_chain.msg"]
		}
		if msg != "Unknown" {
			t.Errorf("F2 msg mismatch. Got %v", msg)
		}
	}
}

func TestToolJoin_Chained_InnerThenLeft(t *testing.T) {
	// Scenario: (Users INNER JOIN Orders) LEFT JOIN Details
	// Users: u1, u2, u3
	// Orders: o1(u1), o2(u2). u3 has no orders.
	// Details: d1(o1). o2 has no details.

	// Expected Result:
	// 1. (Users join Orders) -> { (u1, o1), (u2, o2) } (u3 dropped implicit inner)
	// 2. (... left join Details) ->
	//    - o1 matches d1 -> { u1, o1, d1 }
	//    - o2 matches nothing -> { u2, o2, null }

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
	users, _ := core_database.NewBtree[string, any](ctx, dbOpts, "users_chain_l", tx, nil)
	users.Add(ctx, "u1", map[string]any{"uid": 1.0, "name": "Alice"})
	users.Add(ctx, "u2", map[string]any{"uid": 2.0, "name": "Bob"})
	users.Add(ctx, "u3", map[string]any{"uid": 3.0, "name": "Charlie"})

	orders, _ := core_database.NewBtree[string, any](ctx, dbOpts, "orders_chain_l", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"oid": 101.0, "uid": 1.0, "title": "Book"})
	orders.Add(ctx, "o2", map[string]any{"oid": 102.0, "uid": 2.0, "title": "Pen"})

	details, _ := core_database.NewBtree[string, any](ctx, dbOpts, "details_chain_l", tx, nil)
	details.Add(ctx, "d1", map[string]any{"did": "D1", "oid": 101.0, "info": "Hardcover"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agentCfg := Config{ID: "test_admin_left"}
	agent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	// 3. Script
	scriptJSON := `[
{"op": "open_db", "args": {"name": "test_db"}, "result_var": "test_db"},
{"op": "begin_tx", "args": {"database": "test_db", "mode": "read"}, "result_var": "tx"},

{"op": "open_store", "args": {"transaction": "tx", "name": "users_chain_l"}, "result_var": "s_users"},
{"op": "open_store", "args": {"transaction": "tx", "name": "orders_chain_l"}, "result_var": "s_orders"},
{"op": "open_store", "args": {"transaction": "tx", "name": "details_chain_l"}, "result_var": "s_details"},

{"op": "scan", "args": {"store": "s_users"}, "result_var": "stream_users"},

{"op": "join", "args": {"store": "@s_orders", "type": "inner", "on": {"uid": "uid"}}, "input_var": "stream_users", "result_var": "users_orders"},

{"op": "join", "args": {"store": "@s_details", "type": "left", "on": {"s_orders.oid": "oid"}}, "input_var": "users_orders", "result_var": "output"},

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

	// 4. Validate
	var results []map[string]any
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Expect 2 records
	if len(results) != 2 {
		t.Errorf("Expected 2 records, got %d", len(results))
	}

	// Find Alice (should have details)
	var alice map[string]any
	var bob map[string]any
	for _, r := range results {
		if r["name"] == "Alice" {
			alice = r
		} else if r["name"] == "Bob" {
			bob = r
		}
	}

	if alice == nil {
		t.Error("Alice not found (Inner Join failure?)")
	} else {
		// Use "D1" as value or "s_details.info"
		// The key from details is d1. The on field is oid.
		// Value is map{"did":"D1", "oid":101, "info":"Hardcover"}
		// If we didn't specify alias, it might be prefixed with "details_chain_l." or "Right."
		// However, script alias "left" logic might default to using Store Name as prefix.
		// Let's check keys available
		// t.Logf("Alice Keys: %v", alice)
		if alice["s_details.info"] != "Hardcover" {
			// Try fallback
			if alice["info"] != "Hardcover" {
				t.Errorf("Alice missing details: %v", alice)
			}
		}
	}

	if bob == nil {
		t.Error("Bob not found (Inner Join failure?)")
	} else {
		if val, ok := bob["s_details.info"]; ok && val != nil {
			t.Errorf("Bob should not have details, got: %v", val)
		}
	}
}

func TestToolJoin_Full(t *testing.T) {
	// Scenario: Full Join A(1,2) and B(2,3)
	// Expect: 1(A), 2(AB), 3(B)

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
	storeA, _ := core_database.NewBtree[string, any](ctx, dbOpts, "store_a", tx, nil)
	storeA.Add(ctx, "a1", map[string]any{"id": 1.0, "val": "A1"})
	storeA.Add(ctx, "a2", map[string]any{"id": 2.0, "val": "A2"})

	storeB, _ := core_database.NewBtree[string, any](ctx, dbOpts, "store_b", tx, nil)
	storeB.Add(ctx, "b2", map[string]any{"id": 2.0, "val": "B2"})
	storeB.Add(ctx, "b3", map[string]any{"id": 3.0, "val": "B3"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	agentCfg := Config{ID: "test_full"}
	agent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	scriptJSON := `[
		{"op": "open_db", "args": {"name": "test_db"}, "result_var": "test_db"},
		{"op": "begin_tx", "args": {"database": "test_db", "mode": "read"}, "result_var": "tx"},
		
		{"op": "open_store", "args": {"transaction": "tx", "name": "store_a"}, "result_var": "s_a"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "store_b"}, "result_var": "s_b"},

		{"op": "scan", "args": {"store": "s_a"}, "result_var": "stream_a"},
		
		{"op": "join", "args": {"store": "@s_b", "type": "full", "on": {"id": "id"}, "right_alias": "B"}, "input_var": "stream_a", "result_var": "output"},

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
	if err := json.Unmarshal([]byte(resp), &results); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d. Results: %v", len(results), results)
	}

	foundA1 := false
	foundMatch := false
	foundB3 := false

	for _, r := range results {
		// Helper to look up ID from various possible keys (due to prefixing/collapsing behavior)
		var idA float64
		if v, ok := r["store_a.id"]; ok && v != nil {
			idA, _ = v.(float64)
		} else if v, ok := r["id"]; ok && v != nil {
			idA, _ = v.(float64)
		}

		var idB float64
		if v, ok := r["id"]; ok && v != nil {
			// If store_a.id exists, it's A2-B2 match. So the main 'id' belongs to B.
			if r["store_a.id"] != nil {
				idB, _ = v.(float64)
			} else {
				// otherwise it's either A1 or B3.
				// a1 is 'val' == "A1", b3 is 'val' == "B3"
				if r["val"] == "B3" {
					idB, _ = v.(float64)
				}
			}
		}

		if idA == 1.0 {
			foundA1 = true
		}
		if idA == 2.0 && idB == 2.0 {
			foundMatch = true
		}
		if idB == 3.0 {
			foundB3 = true
		}
	}

	if !foundA1 {
		t.Error("Missing A1 (Left Only)")
	}
	if !foundMatch {
		t.Error("Missing A2-B2 (Match)")
	}
	if !foundB3 {
		t.Error("Missing B3 (Right Only). Dump:", results)
	}
}
