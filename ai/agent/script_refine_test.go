package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

func TestScript_Refine(t *testing.T) {
	// 1. Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.NoCache,
	}
	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()

	// Mock Generator for Refine
	mockGen := &MockScriptedGenerator{
		Responses: []string{
			// Response for /script refine
			`{
				"summary": "Fetches user data for a specific region.",
				"new_parameters": ["region"],
				"replacements": [
					{"value": "US", "parameter": "region", "description": "Replaced US with region"}
				]
			}`,
		},
	}

	// Create Service
	svc := NewService(&MockDomain{}, sysDB, map[string]sop.DatabaseOptions{
		"system": dbOpts,
	}, mockGen, nil, nil, false)

	// Initialize session payload
	payload := &ai.SessionPayload{
		CurrentDB: "system",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// 2. Create Script with hardcoded value
	_, _, err := svc.handleSessionCommand(ctx, "/create refine_test --category general", sysDB)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	svc.session.LastStep = &ai.ScriptStep{
		Type:    "command",
		Command: "select",
		Args:    map[string]any{"store": "users", "region": "US"},
	}
	_, _, err = svc.handleSessionCommand(ctx, "/step", sysDB)
	if err != nil {
		t.Fatalf("Step add failed: %v", err)
	}

	svc.handleSessionCommand(ctx, "/save", sysDB)

	// 3. Run /refine
	resp, handled, err := svc.handleSessionCommand(ctx, "/refine refine_test", sysDB)
	if err != nil {
		t.Fatalf("Refine failed: %v", err)
	}
	if !handled {
		t.Fatalf("Refine not handled")
	}
	if !strings.Contains(resp, "Refinement Proposal") {
		t.Errorf("Unexpected refine response: %s", resp)
	}
	if !strings.Contains(resp, "US -> {{.region}}") {
		t.Errorf("Refine response missing replacement info: %s", resp)
	}

	// 4. Run /refine apply
	resp, handled, err = svc.handleSessionCommand(ctx, "/refine apply", sysDB)
	if err != nil {
		t.Fatalf("Refine apply failed: %v", err)
	}
	if !strings.Contains(resp, "updated successfully") {
		t.Errorf("Unexpected apply response: %s", resp)
	}

	// 5. Verify Script Changes (Summary & Params)
	resp, handled, err = svc.handleSessionCommand(ctx, "/show refine_test", sysDB)
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if !strings.Contains(resp, "Parameters: [region]") {
		t.Errorf("Show output missing parameters: %s", resp)
	}
	if !strings.Contains(resp, "Description: Fetches user data") {
		t.Errorf("Show output missing new description: %s", resp)
	}

}
