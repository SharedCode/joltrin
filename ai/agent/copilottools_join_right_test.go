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

func TestToolJoin_RightOuterJoin(t *testing.T) {
	// 1. Setup Temp DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	// Initialize SystemDB
	sysDB := database.NewDatabase(dbOpts)

	// 2. Create Test Stores
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Store 1: clients (Left Side)
	// Has Client 1 (Alice). Missing Client 2.
	b3Left, err := core_database.NewBtree[string, any](ctx, dbOpts, "clients_rj", tx, nil)
	if err != nil {
		t.Fatalf("Failed to create left store: %v", err)
	}
	b3Left.Add(ctx, "c1", map[string]any{"client_id": 1.0, "name": "Alice"})

	// Store 2: orders (Right Side - The Driver)
	// Has Order 101 (Client 1), Order 102 (Client 2).
	b3Right, err := core_database.NewBtree[string, any](ctx, dbOpts, "orders_rj", tx, nil)
	if err != nil {
		t.Fatalf("Failed to create right store: %v", err)
	}
	b3Right.Add(ctx, "o1", map[string]any{"order_id": 101.0, "client_id": 1.0, "desc": "Book"})
	b3Right.Add(ctx, "o2", map[string]any{"order_id": 102.0, "client_id": 2.0, "desc": "Pen"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit setup: %v", err)
	}

	// 3. Prepare Agent
	// We manually construct the agent to avoid dependency on global/unexported constructors
	agentCfg := Config{
		ID: "test_admin",
	}
	agent := &CopilotAgent{
		Config:    agentCfg,
		databases: map[string]sop.DatabaseOptions{"test_db": dbOpts},
		systemDB:  sysDB,
	}

	// 4. Run Atomic Script via toolExecuteScript
	// We use the JSON-based atomic script which uses ScriptEngine (and thus our new RightOuterJoinStoreCursor)
	// We must Open DB -> Begin TX -> Open Stores -> Scan -> Join
	scriptJSON := `[
		{"op": "open_db", "args": {"name": "test_db"}, "result_var": "test_db"},
		{"op": "begin_tx", "args": {"database": "test_db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "clients_rj"}, "result_var": "clients_store"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "orders_rj"}, "result_var": "orders_store"},
		{"op": "scan", "args": {"store": "clients_store"}, "result_var": "clients_list"},
		{"op": "join", "args": {"store": "@orders_store", "type": "right", "on": {"client_id": "client_id"}}, "input_var": "clients_list", "result_var": "output"},
		{"op": "commit_tx", "args": {"transaction": "tx"}}
	]`

	// Setup payload for DB resolution
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "test_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// Since we use jsondb.OpenStore default, and we created stores using core_database.NewBtree in the same process/path,
	// it should work fine as long as cache doesn't conflict or lock.
	// But checks are in-memory with sop.InMemory cache type, so it should be shared if same cache implementation is used?
	// Actually, sysDB is separate instance from what toolExecuteScript opens (via NewDatabase).
	// But they share the same 'tmpDir'.
	// Since sop.InMemory cache is likely per-instance or global?
	// sop.InMemory cache is usually per Database instance unless configured otherwise.
	// If they are separate instances, they might not see each other's in-memory changes if not committed to disk?
	// The test setup Commits the transaction (creating stores).
	// So data is in the "Store".
	// The script opens a NEW transaction and NEW store instance.
	// This should work if it reads from persistence.
	// But with CacheType: sop.InMemory, persistence is mocked or just in RAM?
	// If InMemory, it might be transient.
	// We might need to ensure they share the same registry or usage.

	// To be safe, we can inject a StoreOpener that returns the already opened stores?
	// Or relies on the fact that we passed 'dbOpts' which points to the same tmpDir.
	// For sop.InMemory, it usually means "No Disk I/O", so if instances share the same Registry it works.
	// If not, it fails.
	// Let's rely on standard flow first.

	respRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptJSON})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	resp, err := formatToolResult(ctx, respRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	t.Logf("Response: %s", resp)

	// 5. Validate Results
	// resp matches return of toolExecuteScript. It returns the final result serialized.
	// We need to parse it.
	var list []map[string]any
	if err := json.Unmarshal([]byte(resp), &list); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(list))
	}

	// Map items for easier verification by order_id
	results := make(map[float64]map[string]any)
	for _, item := range list {
		if oid, ok := item["order_id"].(float64); ok {
			results[oid] = item
		}
	}

	// Verify Order 101 (Matched)
	if row, ok := results[101]; !ok {
		// Dump results for debugging
		t.Logf("Results: %+v", list)
		t.Error("Missing Order 101")
	} else {
		if row["clients_rj.name"] != "Alice" {
			t.Errorf("Order 101 should match Alice, got %v", row["clients_rj.name"])
		}
	}

	// Verify Order 102 (Unmatched)
	if row, ok := results[102]; !ok {
		t.Error("Missing Order 102")
	} else {
		// Now fields for left table should use clients_rj
		if row["clients_rj.name"] != nil {
			t.Errorf("Order 102 should have nil clients_rj.name, got %v", row["clients_rj.name"])
		}
		if row["desc"] != "Pen" {
			t.Errorf("Order 102 desc mismatch: %v", row["desc"])
		}
	}
}
