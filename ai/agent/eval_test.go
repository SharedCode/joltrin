//go:build integration

package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/generator"
)

func TestOmniAIConversationalMemoryHarness(t *testing.T) {
	// Require valid API keys to run this integration-like evaluation
	// or fallback to a mock generator if we want pure local tests.
	// For real capability tests, we run it against actual GenAI if configured.

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Initialize the Agent
	_, err := generator.New("gemini", map[string]any{})
	if err != nil {
		t.Skip("Skipping evaluation harness: Gemini API not available or configured.")
		return
	}

	cfg := agent.Config{UseLegacyBaselineEngine: true,
		ID: t.TempDir(), // Temporary ID for testing
	}
	copilot := agent.NewCopilotAgent(cfg, nil, nil)

	// Create common session context
	payload := &ai.SessionPayload{}
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, payload)

	err = copilot.Open(ctx)
	if err != nil {
		t.Fatalf("Failed to open agent session: %v", err)
	}
	defer copilot.Close(ctx)

	// Step 1: Initial knowledge injection
	query1 := "My landlord's name is Essex and they are ignoring a black mold issue in my apartment."
	resp1, err := copilot.Ask(ctx, query1, nil)
	if err != nil {
		t.Fatalf("Failed to ask Step 1: %v", err)
	}
	t.Logf("Step 1 Response: %s", resp1)

	// Step 2: Follow-up question relying on memory
	query2 := "What was the name of the company I am having a dispute with?"
	resp2, err := copilot.Ask(ctx, query2, nil)
	if err != nil {
		t.Fatalf("Failed to ask Step 2: %v", err)
	}
	t.Logf("Step 2 Response: %s", resp2)

	// Evaluation Assertion
	lowerResp2 := strings.ToLower(resp2)
	if !strings.Contains(lowerResp2, "essex") {
		t.Errorf("Amnesia detected! Expected response to contain 'Essex', but got: %s", resp2)
	} else {
		t.Log("Memory verified! Agent remembered 'Essex'.")
	}
}
