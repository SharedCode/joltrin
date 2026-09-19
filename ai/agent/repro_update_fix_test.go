package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

func TestReproScriptUpdateCorruption(t *testing.T) {
	// 1. Setup DB
	dbPath := "test_repro_script_update_db"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)

	// 2. Setup CopilotAgent
	// Verify NewCopilotAgent signature
	// func NewCopilotAgent(cfg Config, databases map[string]sop.DatabaseOptions, systemDB *database.Database) *CopilotAgent
	agent := NewCopilotAgent(Config{}, nil, sysDB)
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})
	agent.Open(ctx)

	// 3. Create the script (Preparation)
	ctx = context.Background()

	// Direct DB access helper
	// We need to create the 'scripts' store in the Transaction if it doesn't exist?
	// The ModelStore implementation usually handles the underlying BTree.

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	store, err := sysDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		t.Fatalf("OpenModelStore failed: %v", err)
	}

	scriptName := "test_corruption"
	script := ai.Script{
		Steps: []ai.ScriptStep{
			{
				Type:    "command",
				Name:    "OriginalFunction",
				Command: "original_command",
				Args:    map[string]any{"p1": "v1"},
			},
		},
	}

	if err := store.Save(ctx, ai.DefaultScriptCategory, scriptName, script); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 4. Run Update (The operation under test)
	// Simulate UI sending update
	updateArgs := map[string]any{
		"script":  scriptName,
		"index":   float64(0),
		"name":    "NewFunction",
		"command": "", // UI clears command
	}

	_, err = agent.toolScriptUpdateStep(ctx, updateArgs)
	if err != nil {
		t.Fatalf("toolScriptUpdateStep failed: %v", err)
	}

	// 5. Verify Result
	// We expect the script to be loadable (valid JSON) AND the command to be preserved.

	tx2, err := sysDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	store2, err := sysDB.OpenModelStore(ctx, "scripts", tx2)
	if err != nil {
		t.Fatalf("OpenModelStore 2 failed: %v", err)
	}

	var loadedScript ai.Script
	err = store2.Load(ctx, ai.DefaultScriptCategory, scriptName, &loadedScript)
	if err != nil {
		// This is where "unexpected end of JSON input" would appear
		t.Fatalf("Load failed (Data was corrupted?): %v", err)
	}

	step := loadedScript.Steps[0]
	if step.Name != "NewFunction" {
		t.Errorf("FAIL: Name not updated. Got %s, want NewFunction", step.Name)
	}
	if step.Command != "original_command" {
		t.Errorf("FAIL: Command was overwritten/cleared! Got '%s'", step.Command)
	}

	// Debug output
	b, _ := json.MarshalIndent(loadedScript, "", "  ")
	t.Logf("Final Script State:\n%s", string(b))

	tx2.Rollback(ctx)
}
