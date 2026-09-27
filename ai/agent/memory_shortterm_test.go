package agent

import (
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

func TestShortTermMemory_AddThreadPromoteAndGetCurrent(t *testing.T) {
	stm := NewShortTermMemory()
	firstID := sop.NewUUID()
	secondID := sop.NewUUID()
	stm.AddThread(&ConversationThread{ID: firstID, RootPrompt: "first"})
	stm.AddThread(&ConversationThread{ID: secondID, RootPrompt: "second"})

	if current := stm.GetCurrentThread(); current == nil || current.ID != secondID {
		t.Fatalf("expected current thread to be second, got %+v", current)
	}

	stm.PromoteThread(firstID)
	if current := stm.GetCurrentThread(); current == nil || current.ID != firstID {
		t.Fatalf("expected promote to switch current thread, got %+v", current)
	}
	if stm.Order[len(stm.Order)-1] != firstID {
		t.Fatalf("expected promoted thread at end of order, got %+v", stm.Order)
	}
}

func TestShortTermMemory_SettersCloneSnapshots(t *testing.T) {
	stm := NewShortTermMemory()
	routing := &TaskContextClassification{Domain: StoresDomain, DBArtifacts: []string{"main"}}
	mru := []MRUItem{{Category: "PERSONA_A", Context: "ctx", Source: MRUSourcePersona, Scope: MRUScopeSession}}
	recipes := []RecipeItem{{ID: "r1", Protocol: []string{"step"}, Confidence: 0.7}}
	carryover := &ai.CarryoverState{Provider: "gemini", LastOutcomeFacts: []string{"fact"}, LastRecipeIDs: []string{"r1"}}

	stm.SetRoutingState(routing)
	stm.SetMRUSnapshot(mru)
	stm.SetRecipeSnapshot(recipes)
	stm.SetCarryoverState(carryover)

	routing.DBArtifacts[0] = "mutated"
	mru[0].Context = "mutated"
	recipes[0].Protocol[0] = "mutated"
	carryover.LastOutcomeFacts[0] = "mutated"

	if got := stm.GetRoutingState(); got == nil || got.DBArtifacts[0] != "main" {
		t.Fatalf("expected routing state clone, got %+v", got)
	}
	if got := stm.GetMRUSnapshot(); got == nil || got[0].Context != "ctx" {
		t.Fatalf("expected MRU snapshot clone, got %+v", got)
	}
	if got := stm.GetRecipeSnapshot(); got == nil || got[0].Protocol[0] != "step" {
		t.Fatalf("expected recipe snapshot clone, got %+v", got)
	}
	if got := stm.GetCarryoverState(); got == nil || got.LastOutcomeFacts[0] != "fact" {
		t.Fatalf("expected carryover snapshot clone, got %+v", got)
	}
}

func TestShortTermMemory_ResetProjectionForTopicSwitch(t *testing.T) {
	stm := NewShortTermMemory()
	stm.SetRoutingState(&TaskContextClassification{Domain: StoresDomain})
	stm.SetMRUSnapshot([]MRUItem{
		{Category: "PERSONA_A", Source: MRUSourcePersona, Scope: MRUScopeSession},
		{Category: SYSTEM_TOOLS, Source: MRUSourceSystemTools, Scope: MRUScopeSession},
	})
	stm.SetRecipeSnapshot([]RecipeItem{{ID: "r1", Confidence: 0.9}})

	stm.ResetProjectionForTopicSwitch()

	if got := stm.GetRoutingState(); got != nil {
		t.Fatalf("expected routing state reset on topic switch, got %+v", got)
	}
	if got := stm.GetMRUSnapshot(); len(got) != 1 || got[0].Category != "PERSONA_A" {
		t.Fatalf("expected only persona MRU items to survive topic switch, got %+v", got)
	}
	if got := stm.GetRecipeSnapshot(); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("expected topic switch to leave recipe snapshot intact, got %+v", got)
	}
	if got := stm.GetCarryoverState(); got != nil {
		t.Fatalf("expected carryover state reset on topic switch, got %+v", got)
	}
}
