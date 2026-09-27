package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func TestScriptParameterization_Nested(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// 2. Create Parent Script that calls a Child Script
	// Parent Script: "Audit Sales"
	// Step 1: Call "Find Employees" with dept="Sales"
	scriptName := "audit_department"
	parentScript := ai.Script{
		Steps: []ai.ScriptStep{
			{
				Type:       "script",
				ScriptName: "find_employees",
				ScriptArgs: map[string]string{
					"dept": "Sales",
					"mode": "verbose",
				},
			},
		},
	}

	// Save Parent Script
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, ai.DefaultScriptCategory, scriptName, &parentScript)
	tx.Commit(ctx)

	// 3. Initialize Service
	svc := NewService(nil, sysDB, nil, nil, nil, nil, false)

	// 4. Parameterize "Sales" -> "target_dept"
	cmd := "/parameterize audit_department target_dept Sales"
	resp, err := svc.Ask(ctx, cmd, nil)
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}
	t.Logf("Response: %s", resp)

	// 5. Verify
	tx, _ = sysDB.BeginTransaction(ctx, sop.ForReading)
	store, _ = sysDB.OpenModelStore(ctx, "scripts", tx)
	var updatedScript ai.Script
	store.Load(ctx, ai.DefaultScriptCategory, scriptName, &updatedScript)
	tx.Commit(ctx)

	// Check Parameters
	hasParam := false
	for _, p := range updatedScript.Parameters {
		if p == "target_dept" {
			hasParam = true
			break
		}
	}
	if !hasParam {
		t.Errorf("Expected 'target_dept' in parameters, got: %v", updatedScript.Parameters)
	}

	// Check Nested Script Arg
	step := updatedScript.Steps[0]
	if step.Type != "script" {
		t.Fatalf("Expected step type 'script', got '%s'", step.Type)
	}

	val, ok := step.ScriptArgs["dept"]
	if !ok {
		t.Fatalf("Missing 'dept' arg in nested script step")
	}

	expected := "{{.target_dept}}"
	if val != expected {
		t.Errorf("Nested script arg replacement failed.\nGot:      %s\nExpected: %s", val, expected)
	}

	// Ensure other args are untouched
	if step.ScriptArgs["mode"] != "verbose" {
		t.Errorf("Expected 'mode' to remain 'verbose', got '%s'", step.ScriptArgs["mode"])
	}
}
