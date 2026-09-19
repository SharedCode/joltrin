package agent_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
)

// MockContaminationGenerator implements ai.Generator for testing
type MockContaminationGenerator struct {
	Responses []ai.GenOutput
	Calls     []string
}

func (m *MockContaminationGenerator) Generate(ctx context.Context, prompt string, options ai.GenOptions) (ai.GenOutput, error) {
	m.Calls = append(m.Calls, prompt)
	if len(m.Responses) > 0 {
		res := m.Responses[0]
		m.Responses = m.Responses[1:]
		return res, nil
	}
	return ai.GenOutput{Text: "default response"}, nil
}

func (m *MockContaminationGenerator) Name() string                                 { return "mock" }
func (m *MockContaminationGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *MockContaminationGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

// MockContaminationExecutor implements ai.ToolExecutor
type MockContaminationExecutor struct {
	Results []string
}

func (m *MockContaminationExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if len(m.Results) > 0 {
		res := m.Results[0]
		m.Results = m.Results[1:]
		return res, nil
	}
	return fmt.Sprintf("Executed %s", toolName), nil
}

func (m *MockContaminationExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return nil, nil
}

func TestContextHistoryControl(t *testing.T) {
	// mockGen is reused BUT we will check calls
	mockGen := &MockContaminationGenerator{
		Responses: []ai.GenOutput{
			// Step 1 - Call 1: Identify Topic (SKIPPED because memory empty)
			// Step 1 - Call 1: Main Generation -> Tool Call (select users)
			{Text: `{"tool":"select_users", "args":{}}`},

			// Step 2 - Call 1: Identify Topic (SKIPPED because history injection is false)
			// Step 2 - Call 1: Main Generation -> Tool Call (orders)
			{Text: `{"tool":"select_orders", "args":{}}`},
		},
	}

	mockExecutor := &MockContaminationExecutor{
		Results: []string{
			// Tool Result 1
			"User1, User2, User3",
			// Tool Result 2
			"OrderA, OrderB, OrderC",
		},
	}

	// minimal deps
	// Using sop.Standalone which corresponds to a local/in-memory setup typically
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone})

	// Create Service
	svc := agent.NewService(
		nil,
		sysDB,
		map[string]sop.DatabaseOptions{"testdb": {Type: sop.Standalone}},
		mockGen,
		nil,
		nil,
		false,
	)

	// Ensure History Injection is OFF (verify we can disable it)
	svc.SetFeature("history_injection", false)

	ctx := context.Background()
	// Inject Executor
	ctx = context.WithValue(ctx, ai.CtxKeyExecutor, mockExecutor)

	// Set Payload to avoid implicit transaction errors and set CurrentDB
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, &ai.SessionPayload{
		// CurrentDB: "testdb", // Commented out to avoid transaction panic in minimal mock
	})

	// 2. Run Step 1: "Show Users"
	t.Log("--- Executing Step 1: Show Users ---")
	resp1, err := svc.Ask(ctx, "Show users", nil)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}
	t.Logf("Step 1 Response: %s", resp1)

	// 3. Run Step 2: "Find orders"
	t.Log("--- Executing Step 2: Find orders ---")
	resp2, err := svc.Ask(ctx, "Find orders", nil)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}
	t.Logf("Step 2 Response: %s", resp2)

	// Analyze the calls to see if contamination occurred
	// We check call #2 (index 1), which is the main generation for Step 2.
	// Index 0: Gen 1 (Step 1 Main)
	// Index 1: Gen 2 (Step 2 Main)
	if len(mockGen.Calls) < 2 {
		t.Fatalf("Expected at least 2 LLM calls (1 for Step 1, 1 for Step 2), got %d", len(mockGen.Calls))
	}

	step2Prompt := mockGen.Calls[1]
	if strings.Contains(step2Prompt, "User1, User2, User3") {
		t.Errorf("CONTAMINATION DETECTED: Step 2 prompt contains Step 1 data!\nPrompt snippet: %s", step2Prompt[:min(len(step2Prompt), 500)])
	} else {
		t.Log("No contamination detected in Step 2 prompt.")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
