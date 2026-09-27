package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

// TestScriptParameterizationWorkflow demonstrates how a recorded script with hardcoded values
// is converted into a parameterized script using the /script parameterize command.
func TestScriptParameterizationWorkflow(t *testing.T) {
	// 1. Setup Infrastructure
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// 2. Simulate a "Recorded" Script
	// Imagine the user recorded: "select * from employees where department = 'Sales'"
	scriptName := "find_sales_employees"
	originalScript := ai.Script{
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Command: "select",
				Args: map[string]any{
					"query": "select * from employees where department = 'Sales'",
				},
			},
			{
				Type:   "ask",
				Prompt: "Summarize the performance of the Sales team",
			},
		},
	}

	// Save this "recorded" script to the system DB
	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin tx: %v", err)
	}
	store, err := sysDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	if err := store.Save(ctx, ai.DefaultScriptCategory, scriptName, originalScript); err != nil {
		t.Fatalf("Failed to save script: %v", err)
	}
	tx.Commit(ctx)

	// 3. Initialize Service
	// We only need the systemDB to be working for this test
	svc := NewService(nil, sysDB, nil, nil, nil, nil, false)

	// 4. Execute Parameterization Command
	// User wants to replace 'Sales' with a parameter named 'dept'
	// Command: /script parameterize <script_name> <param_name> <value_to_replace>
	cmd := "/parameterize find_sales_employees dept Sales"

	response, err := svc.Ask(ctx, cmd, nil)
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}
	t.Logf("Command Response: %s", response)

	// 5. Verify the Transformation
	// Reload the script
	tx, _ = sysDB.BeginTransaction(ctx, sop.ForReading)
	store, _ = sysDB.OpenModelStore(ctx, "scripts", tx)
	var updatedScript ai.Script
	store.Load(ctx, ai.DefaultScriptCategory, scriptName, &updatedScript)
	tx.Commit(ctx)

	// Check Parameters list
	foundParam := false
	for _, p := range updatedScript.Parameters {
		if p == "dept" {
			foundParam = true
			break
		}
	}
	if !foundParam {
		t.Errorf("Expected 'dept' in Parameters list, got: %v", updatedScript.Parameters)
	}

	// Check Step 1 (Command Arg)
	argQuery := updatedScript.Steps[0].Args["query"].(string)
	expectedQuery := "select * from employees where department = '{{.dept}}'"
	if argQuery != expectedQuery {
		t.Errorf("Step 1 replacement failed.\nGot:      %s\nExpected: %s", argQuery, expectedQuery)
	}

	// Check Step 2 (Prompt)
	prompt := updatedScript.Steps[1].Prompt
	expectedPrompt := "Summarize the performance of the {{.dept}} team"
	if prompt != expectedPrompt {
		t.Errorf("Step 2 replacement failed.\nGot:      %s\nExpected: %s", prompt, expectedPrompt)
	}
}
