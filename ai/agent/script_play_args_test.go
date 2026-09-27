package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func TestScript_Play_ArgumentParsing_Correct(t *testing.T) {
	// 1. Setup Temp DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	// 2. Create a Script with Parameters
	ctx := context.Background()
	scriptName := "test_script_args"
	script := ai.Script{
		Parameters: []string{"table", "role", "limit"},
		Steps: []ai.ScriptStep{
			{
				Type:   "ask",
				Prompt: "Selecting from {{.table}} where role={{.role}} limit={{.limit}}",
			},
		},
	}

	// Save Script
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, ai.DefaultScriptCategory, scriptName, &script)
	tx.Commit(ctx)

	// 3. Initialize Service
	// Note: We pass nil for dependencies we don't need for arg parsing check
	svc := NewService(nil, sysDB, nil, nil, nil, nil, false)

	tests := []struct {
		name      string
		cmd       string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "All Named",
			cmd:       "/run test_script_args table=users role=admin limit=10",
			wantError: false,
		},
		{
			name:      "All Positional",
			cmd:       "/run test_script_args users admin 10",
			wantError: false,
		},
		{
			name:      "Mixed (Positional then Named)",
			cmd:       "/run test_script_args users role=admin limit=10",
			wantError: false,
		},
		{
			name:      "Missing Parameter",
			cmd:       "/run test_script_args users",
			wantError: true,
			errorMsg:  "Missing required parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We expect Ask to fail eventually because we didn't set up a Generator or Domain,
			// but we want to check if it fails at the ARGUMENT PARSING stage.

			out, err := svc.Ask(ctx, tt.cmd, nil)

			// If we expect an arg parsing error, it usually comes as a successful "response" string starting with "Error:"
			// or sometimes as an error. The current implementation returns "Error: ..." as the string response for validation failures.

			if tt.wantError {
				if err == nil && !strings.Contains(out, "Error:") {
					t.Errorf("Expected error for cmd '%s', got success: %s", tt.cmd, out)
				}
				if tt.errorMsg != "" && !strings.Contains(out, tt.errorMsg) {
					t.Errorf("Expected error message containing '%s', got: %s", tt.errorMsg, out)
				}
			} else {
				// If we don't expect an arg parsing error, we might get other errors (like "Error initializing session" or "embedding failed")
				// But we should NOT get "Missing required parameters".
				if strings.Contains(out, "Missing required parameters") {
					t.Errorf("Unexpected arg parsing error for cmd '%s': %s", tt.cmd, out)
				}
			}
		})
	}
}
