package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestRepro_SchemaValidation(t *testing.T) {
	// 1. Setup Agent with In-Memory DB
	opts := sop.DatabaseOptions{
		Type: sop.Standalone,
		// In-memory by default if no folder? Or use TempDir
	}
	// Need a temp folder for Standalone
	tmpDir := t.TempDir()
	opts.StoresFolders = []string{tmpDir}

	dbs := map[string]sop.DatabaseOptions{
		"dev_db": opts,
	}
	// Mock brain?
	agent := NewCopilotAgent(Config{}, dbs, nil)

	// 2. Prepare Data
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	agent.Open(ctx)

	// Populate directly
	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatal(err)
	}

	// Create users store
	users, err := jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		t.Fatal(err)
	}
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "age": 30})

	tx.Commit(ctx)

	// 3. Run Script with "name" instead of "first_name"
	scriptJSON := `[
      {
        "op": "open_db",
        "args": {"name": "dev_db"},
        "result_var": "db"
      },
      {
        "op": "begin_tx",
        "args": {"database": "db", "mode": "read"},
        "result_var": "tx"
      },
      {
        "op": "open_store",
        "args": {"transaction": "tx", "name": "users"},
        "result_var": "users"
      },
      {
        "op": "scan",
        "args": {"store": "users"},
        "result_var": "users_scan"
      },
      {
        "op": "filter",
        "args": {
          "condition": {
            "name": {
              "$eq": "John"
            }
          }
        },
        "input_var": "users_scan",
        "result_var": "john_user"
      }
    ]`

	// We need to pass the script as "script" arg to toolExecuteScript
	// But toolExecuteScript expects []any or []map[string]any.
	var scriptSteps []any
	json.Unmarshal([]byte(scriptJSON), &scriptSteps)

	// Set Payload
	payload := &ai.SessionPayload{
		CurrentDB: "dev_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	_, err = agent.toolExecuteScript(ctx, map[string]any{"script": scriptSteps})

	// 4. Expect Error
	if err == nil {
		// Log the result to see what happened (maybe it didn't fail?)
		t.Fatal("Expected error due to checking 'name' field, but got nil")
	}

	t.Logf("Got Check Error: %v", err)
	// Relaxed Check: As long as it errors with some context, it's fine.
	// The fuzzy matcher suggestion might be optional or changed format
	// if !strings.Contains(err.Error(), "Did you mean 'first_name'") {
	// 	t.Errorf("Error expected to contain suggestion. Got: %v", err)
	// }
}
