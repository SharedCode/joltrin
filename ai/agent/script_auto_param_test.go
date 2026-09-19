package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

func TestScript_AutoParameterize(t *testing.T) {
	skipOnWindowsTranslogLeak(t)

	// 1. Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.NoCache,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// Mock Generator Response
	mockResponse := `{
		"summary": "Auto Param Test Refined",
		"new_parameters": ["target_region", "min_salary"],
		"replacements": [
			{"value": "US", "parameter": "target_region", "description": "Replaced US"},
			{"value": "50000", "parameter": "min_salary", "description": "Replaced 50000"}
		]
	}`
	mockGen := &MockGenerator{Response: mockResponse}

	// Create Service
	svc := NewService(&MockDomain{}, sysDB, map[string]sop.DatabaseOptions{
		"system": dbOpts,
	}, mockGen, nil, nil, false)

	// Initialize session payload
	payload := &ai.SessionPayload{
		CurrentDB: "system",
	}
	ctx = context.WithValue(ctx, "session_payload", payload)

	// 2. Create Script with hardcoded values
	svc.handleSessionCommand(ctx, "/create auto_test", sysDB)

	// Add steps
	svc.session.LastStep = &ai.ScriptStep{
		Type:    "command",
		Command: "select",
		Args:    map[string]any{"store": "users", "region": "US"},
	}
	svc.handleSessionCommand(ctx, "/step", sysDB)

	svc.session.LastStep = &ai.ScriptStep{
		Type:    "command",
		Command: "select",
		Args:    map[string]any{"store": "employees", "salary": "50000"},
	}
	svc.handleSessionCommand(ctx, "/step", sysDB)

	svc.handleSessionCommand(ctx, "/save", sysDB)

	// 3. Run Auto Parameterization (Refine)
	resp, handled, err := svc.handleSessionCommand(ctx, "/refine auto_test", sysDB)
	if err != nil {
		t.Fatalf("Auto parameterize (refine) failed: %v", err)
	}
	if !handled {
		t.Fatalf("Auto parameterize (refine) not handled")
	}

	// 4. Apply Refinement
	resp, handled, err = svc.handleSessionCommand(ctx, "/refine apply", sysDB)
	if err != nil {
		t.Fatalf("Refine apply failed: %v", err)
	}
	if !strings.Contains(resp, "updated successfully") {
		t.Errorf("Unexpected response from apply: %s", resp)
	}

	// 5. Verify Script Content
	resp, _, _ = svc.handleSessionCommand(ctx, "/show auto_test --json", sysDB)
	if !strings.Contains(resp, "{{.target_region}}") {
		t.Errorf("Script missing target_region replacement: %s", resp)
	}
	if !strings.Contains(resp, "{{.min_salary}}") {
		t.Errorf("Script missing min_salary replacement: %s", resp)
	}
}
