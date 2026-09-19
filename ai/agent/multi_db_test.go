package agent

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestMultiDBScriptExecution(t *testing.T) {
	// 1. Setup Databases
	ctx := context.Background()
	dbPath1 := "test_multidb_1"
	dbPath2 := "test_multidb_2"
	os.RemoveAll(dbPath1)
	os.RemoveAll(dbPath2)
	defer os.RemoveAll(dbPath1)
	defer os.RemoveAll(dbPath2)

	dbOpts1 := sop.DatabaseOptions{StoresFolders: []string{dbPath1}, CacheType: sop.InMemory}
	dbOpts2 := sop.DatabaseOptions{StoresFolders: []string{dbPath2}, CacheType: sop.InMemory}

	// Create Store in DB1
	{
		db := database.NewDatabase(dbOpts1)
		tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
		sopdb.NewBtree[string, any](ctx, dbOpts1, "store1", tx, nil, sop.StoreOptions{Name: "store1", SlotLength: 10})
		tx.Commit(ctx)

		tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
		s, _ := jsondb.OpenStore(ctx, dbOpts1, "store1", tx)
		s.Add(ctx, "key1", "val1")
		tx.Commit(ctx)
	}

	// Create Store in DB2
	{
		db := database.NewDatabase(dbOpts2)
		tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
		sopdb.NewBtree[string, any](ctx, dbOpts2, "store2", tx, nil, sop.StoreOptions{Name: "store2", SlotLength: 10})
		tx.Commit(ctx)

		tx, _ = db.BeginTransaction(ctx, sop.ForWriting)
		s, _ := jsondb.OpenStore(ctx, dbOpts2, "store2", tx)
		s.Add(ctx, "key2", "val2")
		tx.Commit(ctx)
	}

	// 2. Setup Agent
	systemDBPath := "test_multidb_system"
	os.RemoveAll(systemDBPath)
	defer os.RemoveAll(systemDBPath)
	systemDBOpts := sop.DatabaseOptions{StoresFolders: []string{systemDBPath}}
	systemDB := database.NewDatabase(systemDBOpts)

	daAgent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{
		"db1": dbOpts1,
		"db2": dbOpts2,
	}, systemDB)

	ctx = context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{CurrentDB: "db1"})
	daAgent.Open(ctx)

	// 3. Define Script
	script := ai.Script{
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "select",
				Args: map[string]any{
					"database": "db1",
					"store":    "store1",
				},
			},
			{
				Type:    "command",
				Command: "select",
				Args: map[string]any{
					"database": "db2",
					"store":    "store2",
				},
			},
		},
	}

	// Save Script
	{
		tx, _ := systemDB.BeginTransaction(ctx, sop.ForWriting)
		store, _ := systemDB.OpenModelStore(ctx, "scripts", tx)
		store.Save(ctx, ai.DefaultScriptCategory, "multidb_script", &script)
		tx.Commit(ctx)
	}

	// 4. Execute Script via ExecuteTool (simulating LLM call)
	// We need to simulate a session where a transaction is active on "db1" (default)

	// Case A: No active transaction in payload.
	// The script now promotes missing transactions into session scope and commits them at script completion.
	t.Run("AutoCommit", func(t *testing.T) {
		payload := &ai.SessionPayload{
			CurrentDB: "db1",
		}
		ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, payload)

		res, err := daAgent.Execute(ctxWithPayload, "multidb_script", map[string]any{})
		if err != nil {
			t.Fatalf("Expected success, but got error: %v", err)
		}
		t.Logf("Result: %s", res)
	})

	// Case B: Active transaction on DB1
	t.Run("ActiveTransaction_DB1", func(t *testing.T) {
		db1 := database.NewDatabase(dbOpts1)
		tx, _ := db1.BeginTransaction(ctx, sop.ForReading)
		defer tx.Rollback(ctx)

		payload := &ai.SessionPayload{
			CurrentDB:   "db1",
			Transaction: tx,
		}
		ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, payload)

		res, err := daAgent.Execute(ctxWithPayload, "multidb_script", map[string]any{})
		if err != nil {
			t.Fatalf("Expected success, but got error: %v", err)
		}
		t.Logf("Result: %s", res)
	})

	// Case C: Active transactions on both DB1 and DB2
	t.Run("ActiveTransaction_DB1_DB2", func(t *testing.T) {
		db1 := database.NewDatabase(dbOpts1)
		tx1, _ := db1.BeginTransaction(ctx, sop.ForReading)
		defer tx1.Rollback(ctx)

		db2 := database.NewDatabase(dbOpts2)
		tx2, _ := db2.BeginTransaction(ctx, sop.ForReading)
		defer tx2.Rollback(ctx)

		payload := &ai.SessionPayload{
			CurrentDB:   "db1",
			Transaction: tx1,
			Transactions: map[string]any{
				"db1": tx1,
				"db2": tx2,
			},
		}
		ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, payload)

		res, err := daAgent.Execute(ctxWithPayload, "multidb_script", map[string]any{})
		if err != nil {
			t.Fatalf("Expected success with explicit transactions, but got error: %v", err)
		}
		t.Logf("Result: %s", res)
	})
}
