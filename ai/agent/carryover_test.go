package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

type carryoverTestGenerator struct{}

func (carryoverTestGenerator) Name() string { return "gemini" }
func (carryoverTestGenerator) CarryoverCapability() ai.CarryoverCapability {
	return ai.CarryoverCapability{Provider: "gemini", Model: "gemini-test", SupportsCompact: true}
}
func (carryoverTestGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	_ = ctx
	_ = prompt
	_ = opts
	return ai.GenOutput{Text: "ok"}, nil
}
func (carryoverTestGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (carryoverTestGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func TestDecideCarryover_UsesCompactModeForGeminiContinuation(t *testing.T) {
	session := NewRunnerSession()
	threadID := sop.NewUUID()
	session.Memory.AddThread(&ConversationThread{ID: threadID, RootPrompt: "Find John", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Done", ActiveKB: "sop"}}})
	session.Memory.SetCarryoverState(&ai.CarryoverState{
		Provider:             "gemini",
		Model:                "gemini-test",
		TopicFingerprint:     threadID.String(),
		KBFingerprint:        "sop",
		LastUsedAtUnixMilli:  time.Now().Add(-2 * time.Minute).UnixMilli(),
		AskCount:             1,
		LastOutcomeFacts:     []string{"execute_script confirmed filter field=first_name op=$eq"},
		LastRecipeIDs:        []string{"implicit.execute_script.research_then_retry"},
		LastToolNames:        []string{"list_stores", "execute_script"},
		LastUserQuery:        "Find users named John",
		LastAssistantSummary: "Found John Jones.",
	})

	decision := decideCarryover(context.Background(), session, carryoverTestGenerator{}, &TopicAssessment{IsNewTopic: false, TopicUUID: threadID.String()}, strings.Repeat("history detail ", 50))
	if decision.Mode != ai.CarryoverModeCompact {
		t.Fatalf("expected compact carryover mode, got %+v", decision)
	}
	if !decision.SuppressHistory {
		t.Fatal("expected compact mode to suppress broad history replay")
	}
	if !strings.Contains(decision.Summary, "Carryover Continuity") || !strings.Contains(decision.Summary, "Found John Jones") {
		t.Fatalf("expected compact carryover summary, got %q", decision.Summary)
	}
	if decision.EstimatedCarryTokens == 0 {
		t.Fatalf("expected carryover token estimate, got %+v", decision)
	}
}

func TestDecideCarryover_ResetsOnNewTopic(t *testing.T) {
	session := NewRunnerSession()
	session.Memory.SetCarryoverState(&ai.CarryoverState{Provider: "gemini", LastUsedAtUnixMilli: time.Now().UnixMilli()})
	decision := decideCarryover(context.Background(), session, carryoverTestGenerator{}, &TopicAssessment{IsNewTopic: true}, "history")
	if decision.Mode != ai.CarryoverModeOff {
		t.Fatalf("expected carryover off on new topic, got %+v", decision)
	}
	if decision.Reason != ai.CarryoverResetTopicSwitch {
		t.Fatalf("expected topic switch reset, got %+v", decision)
	}
	if decision.SuppressHistory {
		t.Fatal("did not expect history suppression when carryover is off")
	}
}

func TestDecideCarryover_UsesCapabilityProviderNotHardcodedGemini(t *testing.T) {
	session := NewRunnerSession()
	threadID := sop.NewUUID()
	session.Memory.AddThread(&ConversationThread{ID: threadID, RootPrompt: "Continue", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Done", ActiveKB: "sop"}}})
	testGen := capabilityOnlyGenerator{provider: "anthropic", model: "claude-test", supportsCompact: true}
	session.Memory.SetCarryoverState(&ai.CarryoverState{
		Provider:             "anthropic",
		Model:                "claude-test",
		TopicFingerprint:     threadID.String(),
		KBFingerprint:        "sop",
		LastUsedAtUnixMilli:  time.Now().UnixMilli(),
		AskCount:             1,
		LastOutcomeFacts:     []string{"fact"},
		LastUserQuery:        "continue",
		LastAssistantSummary: "done",
	})
	decision := decideCarryover(context.Background(), session, testGen, &TopicAssessment{IsNewTopic: false, TopicUUID: threadID.String()}, "history")
	if decision.Mode != ai.CarryoverModeCompact {
		t.Fatalf("expected compact mode via provider capability, got %+v", decision)
	}
}

func TestDecideCarryover_PrefersLiveModeWhenProviderSupportsHandleReuse(t *testing.T) {
	session := NewRunnerSession()
	threadID := sop.NewUUID()
	session.Memory.AddThread(&ConversationThread{ID: threadID, RootPrompt: "Continue", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Done", ActiveKB: "sop"}}})
	testGen := capabilityOnlyGenerator{provider: "chatgpt", model: "gpt-test", supportsCompact: true, supportsLive: true}
	session.Memory.SetCarryoverState(&ai.CarryoverState{
		Provider:             "chatgpt",
		Model:                "gpt-test",
		Mode:                 ai.CarryoverModeLive,
		ConversationHandle:   "resp_prev_123",
		TopicFingerprint:     threadID.String(),
		KBFingerprint:        "sop",
		LastUsedAtUnixMilli:  time.Now().UnixMilli(),
		AskCount:             1,
		LastOutcomeFacts:     []string{"confirmed users store"},
		LastUserQuery:        "continue",
		LastAssistantSummary: "done",
	})

	decision := decideCarryover(context.Background(), session, testGen, &TopicAssessment{IsNewTopic: false, TopicUUID: threadID.String()}, "history")
	if decision.Mode != ai.CarryoverModeLive {
		t.Fatalf("expected live carryover mode, got %+v", decision)
	}
	if decision.Reason != ai.CarryoverResetLiveContinuation {
		t.Fatalf("expected live carryover reason, got %+v", decision)
	}
	if !decision.SuppressHistory {
		t.Fatal("expected live mode to suppress broad history replay")
	}
	if decision.Summary != "" {
		t.Fatalf("expected live mode to avoid compact summary injection, got %q", decision.Summary)
	}
	if decision.State == nil || decision.State.ConversationHandle != "resp_prev_123" {
		t.Fatalf("expected live carryover state to preserve conversation handle, got %+v", decision.State)
	}
}

func TestDecideCarryover_FallsBackToMemorySummaryWhenBudgetExceeded(t *testing.T) {
	session := NewRunnerSession()
	threadID := sop.NewUUID()
	session.Memory.AddThread(&ConversationThread{ID: threadID, RootPrompt: "Continue", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Done", ActiveKB: "sop"}}})
	session.Memory.SetCarryoverState(&ai.CarryoverState{
		Provider:             "gemini",
		Model:                "gemini-test",
		TopicFingerprint:     threadID.String(),
		KBFingerprint:        "sop",
		LastUsedAtUnixMilli:  time.Now().UnixMilli(),
		AskCount:             ai.DefaultCarryoverPolicy().LiveCarryoverMaxAsks,
		LastOutcomeFacts:     []string{"fact"},
		LastUserQuery:        "continue",
		LastAssistantSummary: "done",
	})
	decision := decideCarryover(context.Background(), session, carryoverTestGenerator{}, &TopicAssessment{IsNewTopic: false, TopicUUID: threadID.String()}, "history")
	if decision.Mode != ai.CarryoverModeOff {
		t.Fatalf("expected carryover off when budget exceeded, got %+v", decision)
	}
	if decision.Reason != ai.CarryoverResetBudgetExceeded {
		t.Fatalf("expected budget exceeded reset, got %+v", decision)
	}
	if !strings.Contains(decision.Summary, "Memory Continuity Fallback") || !strings.Contains(decision.Summary, "continue") {
		t.Fatalf("expected memory continuity fallback summary, got %q", decision.Summary)
	}
	if decision.State == nil || decision.State.LastResetReason != ai.CarryoverResetBudgetExceeded {
		t.Fatalf("expected carryover state tagged with reset reason, got %+v", decision.State)
	}
}

type capabilityOnlyGenerator struct {
	provider        string
	model           string
	supportsCompact bool
	supportsLive    bool
}

func (g capabilityOnlyGenerator) Name() string                                 { return g.provider }
func (g capabilityOnlyGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (g capabilityOnlyGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (g capabilityOnlyGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	_ = ctx
	_ = prompt
	_ = opts
	return ai.GenOutput{Text: "ok"}, nil
}
func (g capabilityOnlyGenerator) CarryoverCapability() ai.CarryoverCapability {
	return ai.CarryoverCapability{Provider: g.provider, Model: g.model, SupportsCompact: g.supportsCompact, SupportsLive: g.supportsLive}
}

func TestBuildCarryoverState_MergesProviderRuntimeState(t *testing.T) {
	session := NewRunnerSession()
	threadID := sop.NewUUID()
	thread := &ConversationThread{ID: threadID, RootPrompt: "Continue", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Done", ActiveKB: "sop"}}}
	session.Memory.AddThread(thread)
	gen := capabilityOnlyGenerator{provider: "chatgpt", model: "gpt-test", supportsCompact: true, supportsLive: true}
	providerState := &ai.CarryoverState{
		Mode:                   ai.CarryoverModeLive,
		ConversationID:         "conv_123",
		ConversationHandle:     "thread-handle-123",
		EstimatedRawToolTokens: 144,
	}

	state := buildCarryoverState(context.Background(), session, gen, thread, "continue", "done", nil, []string{"fact"}, nil, providerState)
	if state == nil {
		t.Fatal("expected carryover state")
	}
	if state.Mode != ai.CarryoverModeLive {
		t.Fatalf("expected live mode to survive merge, got %+v", state)
	}
	if state.ConversationID != "conv_123" {
		t.Fatalf("expected provider conversation ID, got %+v", state)
	}
	if state.ConversationHandle != "thread-handle-123" {
		t.Fatalf("expected provider conversation handle, got %+v", state)
	}
	if state.EstimatedRawToolTokens != 144 {
		t.Fatalf("expected raw tool tokens from provider state, got %+v", state)
	}
	if state.Provider != "chatgpt" || state.Model != "gpt-test" {
		t.Fatalf("expected provider/model metadata, got %+v", state)
	}
}
