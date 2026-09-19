package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

// omniMockGenerator simulates the Omni Persona outputting a routing tool call,
// and subsequently the Avatar sub-agent outputting its domain response.
type omniMockGenerator struct {
	Step         int
	ExpectedTool string
	ExpectedArgs map[string]any
	AvatarOutput string
}

func (m *omniMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.Step++
	// fmt.Printf("mockGen.Generate called, Step: %d\n", m.Step)
	if m.Step == 1 {
		// First pass: Omni outputs the handoff tool
		return ai.GenOutput{
			ToolCalls: []ai.ToolCall{
				{
					Name: m.ExpectedTool,
					Args: m.ExpectedArgs,
				},
			},
		}, nil
	}

	if strings.Contains(prompt, "Analyze the tool response") {
		return ai.GenOutput{Text: m.AvatarOutput + " via legal_kb"}, nil
	}

	// Second pass: The Avatar is executing
	// We can assert what SystemPrompt was passed to the sub-execution
	if !strings.Contains(opts.SystemPrompt, "You are the Legal Avatar.") {
		return ai.GenOutput{Text: "FAILED_ASSERTION: Avatar Prompt missing. Got: " + opts.SystemPrompt}, nil
	}

	return ai.GenOutput{
		Text: m.AvatarOutput,
	}, nil
}

func (m *omniMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *omniMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *omniMockGenerator) Name() string { return "mock" }

func TestOmni_HandoffToAvatar(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		CurrentDB: "default",
	})

	// 1. Setup in-memory system DB
	sysDB := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{t.TempDir()},
	})

	// 2. Pre-seed the Avatar Knowledge Base Configuration
	avatarName := "legal_kb"

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	kb, err := sysDB.OpenKnowledgeBase(ctx, avatarName, tx, nil, nil, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}
	kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{
		SystemPrompt: "You are the Legal Avatar. Only speak about legal matters.",
	})
	_ = tx.Commit(ctx)

	// 3. Setup Agent Context
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)

	mockGen := &omniMockGenerator{
		Step:         0,
		ExpectedTool: "handoff_to_avatar",
		ExpectedArgs: map[string]any{
			"avatar_kb_name": avatarName,
			"task_context":   map[string]any{"task": "review_contract", "file": "doc1"},
		},
		AvatarOutput: "I have reviewed the contract. It looks compliant.",
	}
	agent.brain = mockGen

	// Register tools natively
	agent.registerTools(ctx)

	// 4. Create ReAct Engine simulating Omni Level Execution
	engine := &NativeReActEngine{
		EnableObfuscation: false,
	}

	req := ai.ReasoningRequest{
		SystemPrompt: "You are Omni, route tasks.",
		UserQuery:    "Please ask the legal avatar to review my contract doc1.",
		Executor:     agent, // Using agent holding the mocked generator
		Generator:    mockGen,
	}

	// 5. Execute Run
	res, err := engine.Run(ctx, req)
	if err != nil {
		t.Fatalf("engine failed: %v", err)
	}

	// 6. Assertions
	if !strings.Contains(res.FinalText, mockGen.AvatarOutput) {
		t.Errorf("Expected result to contain Avatar output, got: %s", res.FinalText)
	}

	if !strings.Contains(res.FinalText, "legal_kb") {
		t.Errorf("Expected result to contain Avatar name, got: %s", res.FinalText)
	}

	if mockGen.Step != 3 {
		t.Errorf("Expected mock generator to be called exactly three times, was called %d times", mockGen.Step)
	}
}

type restrictedMockGenerator struct {
	Step         int
	ExpectedTool string
	ExpectedArgs map[string]any
	AvatarOutput string
}

func (m *restrictedMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.Step++
	if m.Step == 1 {
		return ai.GenOutput{
			ToolCalls: []ai.ToolCall{
				{
					Name: "handoff_to_avatar",
					Args: map[string]any{
						"avatar_kb_name": "restricted_kb",
						"task_context":   "Do the thing",
					},
				},
			},
		}, nil
	}

	if m.Step == 2 {
		return ai.GenOutput{
			ToolCalls: []ai.ToolCall{
				{
					Name: "execute_shell_command",
					Args: map[string]any{"command": "echo hacked"},
				},
			},
		}, nil
	}

	return ai.GenOutput{Text: "finished"}, nil
}

func (m *restrictedMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *restrictedMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *restrictedMockGenerator) Name() string { return "mock" }

func TestOmni_RestrictedAvatarTools(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		CurrentDB: "default",
	})

	sysDB := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{t.TempDir()},
	})

	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	kb, _ := sysDB.OpenKnowledgeBase(ctx, "restricted_kb", tx, nil, nil, false)
	kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{
		SystemPrompt: "You are the restricted avatar.",
		AllowedTools: []string{"search_space"},
	})
	_ = tx.Commit(ctx)

	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)

	mockGen := &restrictedMockGenerator{}
	agent.brain = mockGen
	agent.registerTools(ctx)

	engine := &NativeReActEngine{EnableObfuscation: false}
	req := ai.ReasoningRequest{
		SystemPrompt: "You are Omni, route tasks.",
		UserQuery:    "Please use the avatar.",
		Executor:     agent,
		Generator:    mockGen,
	}

	_, err := engine.Run(ctx, req)
	if err == nil {
		t.Fatalf("expected error from restricted tool execution")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied error, got: %v", err)
	}
}
