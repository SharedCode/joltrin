package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
)

func TestScript_Transactions(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		// Use NoCache to ensure data is flushed to disk and visible across DB instances
		CacheType: sop.NoCache,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// Create a user database
	userDBName := "userdb"
	userDB := database.NewDatabase(dbOpts)

	// Initialize "employees" store in userDB using core_database.NewBtree
	tx, _ := userDB.BeginTransaction(ctx, sop.ForWriting)
	storeOpts := sop.StoreOptions{
		IsPrimitiveKey: true,
	}
	_, err := core_database.NewBtree[string, any](ctx, dbOpts, "employees", tx, nil, storeOpts)
	if err != nil {
		t.Fatalf("Failed to create employees store: %v", err)
	}
	tx.Commit(ctx)

	databases := map[string]sop.DatabaseOptions{
		userDBName: dbOpts,
	}

	// 2. Define Scripts

	// Script 1: Implicit Transaction (Auto-commit)
	// Should succeed and persist data.
	scriptImplicit := ai.Script{
		Database: userDBName,
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "add",
				Args: map[string]any{
					"store": "employees",
					"key":   "emp_implicit",
					"value": "Implicit Data",
				},
			},
		},
	}

	// Script 2: Explicit Transaction (Commit)
	// Should succeed and persist data.
	scriptExplicitCommit := ai.Script{
		Database: userDBName,
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "manage_transaction",
				Args:    map[string]any{"action": "begin"},
			},
			{
				Type:    "command",
				Command: "add",
				Args: map[string]any{
					"store": "employees",
					"key":   "emp_explicit_commit",
					"value": "Explicit Commit Data",
				},
			},
			{
				Type:    "command",
				Command: "manage_transaction",
				Args:    map[string]any{"action": "commit"},
			},
		},
	}

	// Script 3: Explicit Transaction (Rollback)
	// Should NOT persist data.
	scriptExplicitRollback := ai.Script{
		Database: userDBName,
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "manage_transaction",
				Args:    map[string]any{"action": "begin"},
			},
			{
				Type:    "command",
				Command: "add",
				Args: map[string]any{
					"store": "employees",
					"key":   "emp_explicit_rollback",
					"value": "Explicit Rollback Data",
				},
			},
			{
				Type:    "command",
				Command: "manage_transaction",
				Args:    map[string]any{"action": "rollback"},
			},
		},
	}

	// Script 4: Uncommitted Explicit Transaction (Safety Rollback)
	// Should NOT persist data because the session closer should rollback uncommitted explicit txs.
	scriptUncommitted := ai.Script{
		Database: userDBName,
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "manage_transaction",
				Args:    map[string]any{"action": "begin"},
			},
			{
				Type:    "command",
				Command: "add",
				Args: map[string]any{
					"store": "employees",
					"key":   "emp_uncommitted",
					"value": "Uncommitted Data",
				},
			},
		},
	}

	// Save Scripts
	tx, _ = sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, ai.DefaultScriptCategory, "implicit_tx", scriptImplicit)
	store.Save(ctx, ai.DefaultScriptCategory, "explicit_commit", scriptExplicitCommit)
	store.Save(ctx, ai.DefaultScriptCategory, "explicit_rollback", scriptExplicitRollback)
	store.Save(ctx, ai.DefaultScriptCategory, "uncommitted", scriptUncommitted)
	tx.Commit(ctx)

	// 3. Initialize Service
	// We need a CopilotAgent in the registry to handle the commands
	registry := make(map[string]ai.Agent[map[string]any])
	cfg := Config{
		ID:   "copilot",
		Name: "Data Admin",
	}
	copilot := NewCopilotAgent(cfg, databases, sysDB)
	ctx = context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})
	copilot.Open(ctx)
	registry["copilot"] = copilot

	svc := NewService(nil, sysDB, databases, nil, nil, registry, false)

	// Helper to check if key exists
	checkKey := func(key string, shouldExist bool) {
		// Create a fresh DB instance to ensure we see changes committed by other instances (the agent)
		// This simulates a real scenario where the verifier is a separate client
		userDBChecker := database.NewDatabase(dbOpts)

		tx, err := userDBChecker.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			t.Fatalf("CheckKey: BeginTransaction failed: %v", err)
		}
		// Use the same userDB object to open the store, to ensure consistency
		store, err := userDBChecker.OpenBtreeCursor(ctx, "employees", tx)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("CheckKey: OpenBtreeCursor failed: %v", err)
		}

		found, err := store.Find(ctx, key, false)
		if err != nil {
			tx.Rollback(ctx)
			t.Fatalf("CheckKey: Find failed: %v", err)
		}

		tx.Commit(ctx)
		if found != shouldExist {
			t.Errorf("Key '%s' existence mismatch. Expected: %v, Got: %v", key, shouldExist, found)
		}
	}

	// Test 1: Implicit
	if _, err := svc.Ask(ctx, "/run implicit_tx", nil); err != nil {
		t.Fatalf("Implicit script failed: %v", err)
	}
	checkKey("emp_implicit", true)

	// Test 2: Explicit Commit
	if _, err := svc.Ask(ctx, "/run explicit_commit", nil); err != nil {
		t.Fatalf("Explicit Commit script failed: %v", err)
	}
	checkKey("emp_explicit_commit", true)

	// Test 3: Explicit Rollback
	if _, err := svc.Ask(ctx, "/run explicit_rollback", nil); err != nil {
		t.Fatalf("Explicit Rollback script failed: %v", err)
	}
	checkKey("emp_explicit_rollback", false)

	// Test 4: Uncommitted (Safety Rollback)
	if _, err := svc.Ask(ctx, "/run uncommitted", nil); err != nil {
		t.Fatalf("Uncommitted script failed: %v", err)
	}
	checkKey("emp_uncommitted", false)
}
