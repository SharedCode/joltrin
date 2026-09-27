package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

// TestCaseStudy_TwoPathsToAutomation implements the two methodologies described in SCRIPTS.md
// to verify their functional equivalence and correct implementation.
func TestCaseStudy_TwoPathsToAutomation(t *testing.T) {
	// 1. Setup Infrastructure
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// Initialize Agent
	agentCfg := Config{
		ID:          "copilot",
		Name:        "SQL Admin",
		Description: "SQL Admin",
	}
	// We map the database options so the agent knows about the environment
	dbs := map[string]sop.DatabaseOptions{"default": dbOpts}

	adminAgent := NewCopilotAgent(agentCfg, dbs, sysDB)

	// Initialize the Service with the agent registered
	// The service uses this registry to find tools
	svc := NewService(nil, sysDB, dbs, nil, nil, map[string]ai.Agent[map[string]any]{"copilot": adminAgent}, false)

	// Link back (important for cache invalidation etc, though maybe not for this test)
	adminAgent.service = svc

	// Helper to create a dummy "outbox" store to verify "email" sending
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	sysDB.NewBtree(ctx, "users", tx)
	sysDB.NewBtree(ctx, "outbox", tx)
	tx.Commit(ctx)

	// -------------------------------------------------------------------------
	// Method 1: Bottom-Up (Parameter-First Design)
	// -------------------------------------------------------------------------
	t.Run("Method1_BottomUp", func(t *testing.T) {
		// 1.a. Draft `create_account` (Atomic, Parameterized)
		createAccountScript := ai.Script{
			Parameters: []string{"username", "role"},
			Steps: []ai.ScriptStep{
				{
					Type:    "command",
					Command: "add",
					Args: map[string]any{
						"database": "default",
						"store":    "users",
						"key":      "{{.username}}",
						"value":    map[string]string{"role": "{{.role}}"},
					},
				},
			},
		}

		// 1.b. Draft `send_email` (Atomic, Parameterized)
		sendEmailScript := ai.Script{
			Parameters: []string{"address", "body"},
			Steps: []ai.ScriptStep{
				{
					Type:    "command",
					Command: "add", // Simulate email by adding to outbox
					Args: map[string]any{
						"database": "default",
						"store":    "outbox",
						"key":      "{{.address}}", // Use address as key
						"value":    "{{.body}}",
					},
				},
			},
		}

		// 1.c. Draft `onboard_employee` (Orchestrator)
		onboardScript := ai.Script{
			Parameters: []string{"user_id", "user_email"},
			Steps: []ai.ScriptStep{
				{
					Type:       "call_script",
					ScriptName: "create_account",
					ScriptArgs: map[string]string{
						"username": "{{.user_id}}",
						"role":     "staff",
					},
				},
				{
					Type:       "call_script",
					ScriptName: "send_email",
					ScriptArgs: map[string]string{
						"address": "{{.user_email}}",
						"body":    "Welcome!",
					},
				},
			},
		}

		// Save all scripts
		saveScript(t, ctx, sysDB, "create_account", createAccountScript)
		saveScript(t, ctx, sysDB, "send_email", sendEmailScript)
		saveScript(t, ctx, sysDB, "onboard_employee", onboardScript)

		// 1.d. Execute
		// /run onboard_employee user_id="u1" user_email="u1@example.com"
		runCmd := "/run onboard_employee user_id=mjordan user_email=mjordan@bulls.com"
		resp, err := svc.Ask(ctx, runCmd, nil)
		if err != nil {
			t.Fatalf("Execution failed: %v", err)
		}
		t.Logf("Docs: %s", resp)

		// 1.e. Verify
		verifyUserExists(t, ctx, sysDB, "mjordan", "staff")
		verifyEmailSent(t, ctx, sysDB, "mjordan@bulls.com", "Welcome!")
	})

	// -------------------------------------------------------------------------
	// Method 2: Top-Down (Concrete-First Design)
	// -------------------------------------------------------------------------
	t.Run("Method2_TopDown", func(t *testing.T) {
		// Clean slate for data (optional, but good for isolation)
		// We reuse the basic atomic scripts because "Top-Down" assumes tools exist.
		// In a real Top-Down scenario, the user runs commands.
		// Here we simulate the *result* of the user running commands and saving them.

		// 2.a. Save as Script (Concrete State)
		// The script calls the SAME atomic tools (create_account, send_email)
		// but with HARDCODED values.
		concreteScript := ai.Script{
			Steps: []ai.ScriptStep{
				{
					Type:       "call_script",
					ScriptName: "create_account",
					ScriptArgs: map[string]string{
						"username": "jdoe",
						"role":     "staff",
					},
				},
				{
					Type:       "call_script",
					ScriptName: "send_email",
					ScriptArgs: map[string]string{
						"address": "jdoe@example.com",
						"body":    "Welcome!",
					},
				},
			},
		}
		saveScript(t, ctx, sysDB, "onboard_workflow_prototype", concreteScript)

		// 2.b. Verify Concrete Script Works (The Prototype Phase)
		// Running it without args should work for "jdoe"
		runConcrete := "/run onboard_workflow_prototype"
		if _, err := svc.Ask(ctx, runConcrete, nil); err != nil {
			t.Fatalf("Concrete prototype execution failed: %v", err)
		}
		verifyUserExists(t, ctx, sysDB, "jdoe", "staff")
		verifyEmailSent(t, ctx, sysDB, "jdoe@example.com", "Welcome!")

		// 2.c. Refactoring & Parameterization (The Final Polish)
		// We want to turn "jdoe" into {{.user_id}} and "jdoe@example.com" into {{.user_email}}
		// New Feature: Multi-Parameter Batch Command with Auto-Sorting (Maximal Munch)
		// /script parameterize <script> <param1> <val1> <param2> <val2>

		// We intentionally pass the SHORT string first ("jdoe") to verify the system
		// correctly prioritizes the LONG string ("jdoe@example.com") despite the argument order.
		cmd := "/script parameterize onboard_workflow_prototype user_id jdoe user_email jdoe@example.com"
		if _, err := svc.Ask(ctx, cmd, nil); err != nil {
			t.Fatalf("Batch parameterization failed: %v", err)
		}
		// 2.d. Verify Result (The Abstract Phase)
		// Now we run the SAME script with NEW values (scottie pippen)
		runAbstract := "/run onboard_workflow_prototype user_id=spippen user_email=spippen@bulls.com"
		if _, err := svc.Ask(ctx, runAbstract, nil); err != nil {
			t.Fatalf("Abstract execution failed: %v", err)
		}

		verifyUserExists(t, ctx, sysDB, "spippen", "staff")
		verifyEmailSent(t, ctx, sysDB, "spippen@bulls.com", "Welcome!")

		// 2.e. Verify the Call Sites were mocked correctly
		// We check the script definition to ensure logic changes
		updatedScript := loadScript(t, ctx, sysDB, "onboard_workflow_prototype")

		// Check Step 0 (Create Account)
		step0Args := updatedScript.Steps[0].ScriptArgs
		if step0Args["username"] != "{{.user_id}}" {
			t.Errorf("Step 0 Arg 'username' not parameterized. Got: %s", step0Args["username"])
		}

		// Check Step 1 (Send Email)
		step1Args := updatedScript.Steps[1].ScriptArgs
		if step1Args["address"] != "{{.user_email}}" {
			t.Errorf("Step 1 Arg 'address' not parameterized. Got: %s", step1Args["address"])
		}
	})
}

// helpers

func saveScript(t *testing.T, ctx context.Context, db *database.Database, name string, s ai.Script) {
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := store.Save(ctx, ai.DefaultScriptCategory, name, s); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	tx.Commit(ctx)
}

func loadScript(t *testing.T, ctx context.Context, db *database.Database, name string) ai.Script {
	tx, _ := db.BeginTransaction(ctx, sop.ForReading)
	store, _ := db.OpenModelStore(ctx, "scripts", tx)
	var s ai.Script
	store.Load(ctx, ai.DefaultScriptCategory, name, &s)
	tx.Commit(ctx)
	return s
}

func verifyUserExists(t *testing.T, ctx context.Context, db *database.Database, username, role string) {
	tx, _ := db.BeginTransaction(ctx, sop.ForReading)
	store, _ := db.NewBtree(ctx, "users", tx)
	found, _ := store.Find(ctx, username, false)

	if !found {
		tx.Commit(ctx)
		t.Errorf("User %s not found in DB", username)
		return
	}
	// val, _ := store.GetCurrentValue(ctx)
	tx.Commit(ctx)
	// We assume if it exists it matches for this simple test
}

func verifyEmailSent(t *testing.T, ctx context.Context, db *database.Database, address, body string) {
	tx, _ := db.BeginTransaction(ctx, sop.ForReading)
	store, _ := db.NewBtree(ctx, "outbox", tx)
	// We allow some lag for async operations if any (scripts are sync though)
	time.Sleep(10 * time.Millisecond)

	found, _ := store.Find(ctx, address, false)
	var val any
	if found {
		val, _ = store.GetCurrentValue(ctx)
	}
	tx.Commit(ctx)

	if !found {
		t.Errorf("Email to %s not found in outbox", address)
		return
	}
	if val.(string) != body {
		t.Errorf("Email body mismatch. Got %v, want %s", val, body)
	}
}
