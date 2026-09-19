package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
)

func TestCloneTaskContextClassification_ClonesSlices(t *testing.T) {
	original := &TaskContextClassification{
		Domain:          StoresDomain,
		DBArtifacts:     []string{"main"},
		StoresArtifacts: []string{"users"},
		SpacesArtifacts: []string{"kb_users"},
		Layers:          []LayerInfo{{Name: "Cross-Domain: Stores -> Spaces", CRUD: []string{"R"}}},
	}

	cloned := cloneTaskContextClassification(original)
	if cloned == nil {
		t.Fatal("expected cloned task context")
	}
	original.DBArtifacts[0] = "mutated"
	original.StoresArtifacts[0] = "mutated"
	original.SpacesArtifacts[0] = "mutated"
	original.Layers[0].Name = "mutated"

	if cloned.DBArtifacts[0] != "main" || cloned.StoresArtifacts[0] != "users" || cloned.SpacesArtifacts[0] != "kb_users" || cloned.Layers[0].Name != "Cross-Domain: Stores -> Spaces" {
		t.Fatalf("expected cloneTaskContextClassification to deep-clone slices, got %+v", cloned)
	}
	if cloneTaskContextClassification(nil) != nil {
		t.Fatal("expected nil task context to stay nil")
	}
}

func TestMRUProjectionHelpers(t *testing.T) {
	if !shouldPreserveMRUOnTopicSwitch(MRUItem{Source: MRUSourcePersona}) {
		t.Fatal("expected persona source to survive topic switch")
	}
	if !shouldPreserveMRUOnTopicSwitch(MRUItem{Category: "PERSONA_omni"}) {
		t.Fatal("expected persona-prefixed category to survive topic switch")
	}
	if shouldPreserveMRUOnTopicSwitch(MRUItem{Category: SYSTEM_TOOLS, Source: MRUSourceSystemTools}) {
		t.Fatal("expected non-persona item to be cleared on topic switch")
	}

	if isRehydratableMRUItem(MRUItem{Category: "PERSONA_omni", Scope: MRUScopeAsk}) {
		t.Fatal("expected ask-scoped items to be excluded from rehydration")
	}
	if isRehydratableMRUItem(MRUItem{Category: SYSTEM_TOOLS, Source: MRUSourceSystemTools, Scope: MRUScopeAsk}) {
		t.Fatal("expected ask-scoped system tools to be excluded from rehydration")
	}
	if !isRehydratableMRUItem(MRUItem{Category: "PERSONA_avatar", Scope: "invalid"}) {
		t.Fatal("expected invalid scope to normalize to session and allow persona-prefixed rehydration")
	}
	if isRehydratableMRUItem(MRUItem{Category: "OTHER", Source: MRUSourceUnknown, Scope: MRUScopeSession}) {
		t.Fatal("expected unknown-source non-persona category to stay non-rehydratable")
	}

	if projectedMRUSourcePriority(MRUSourcePersona) >= projectedMRUSourcePriority(MRUSourceSystemTools) {
		t.Fatal("expected persona to outrank system tools")
	}
	if projectedMRUSourceLimit(MRUSourcePlaybook) != maxProjectedPlaybookEntries {
		t.Fatalf("unexpected playbook projection limit: %d", projectedMRUSourceLimit(MRUSourcePlaybook))
	}
	if projectedMRUSourceLimit("unknown") != 0 {
		t.Fatalf("expected unknown source to have zero projection limit, got %d", projectedMRUSourceLimit("unknown"))
	}
	if summarizeProjectedMRU(nil) != "" {
		t.Fatalf("expected empty MRU summary for nil input")
	}
	if normalizeMRUScope("invalid") != MRUScopeSession {
		t.Fatalf("expected invalid scope to normalize to session")
	}
}

func TestResetTopicSwitchProjection_ClearsRoutingStateAndCarryover(t *testing.T) {
	session := NewRunnerSession()
	session.Memory.SetRoutingState(&TaskContextClassification{Domain: StoresDomain, DBArtifacts: []string{"main"}})
	session.Memory.SetCarryoverState(&ai.CarryoverState{Provider: "chatgpt", ConversationHandle: "resp_prev_123"})
	session.Memory.SetMRUSnapshot([]MRUItem{{Category: "PERSONA_omni", Context: "persona context", Source: MRUSourcePersona, Scope: MRUScopeSession}})

	payload := &ai.SessionPayload{Variables: map[string]any{"RoutingState": &TaskContextClassification{Domain: StoresDomain}}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	resetTopicSwitchProjection(ctx, session)

	if _, ok := payload.Variables["RoutingState"]; ok {
		t.Fatal("expected routing state payload to be cleared on topic switch")
	}
	if got := session.Memory.GetRoutingState(); got != nil {
		t.Fatalf("expected routing state projection to be cleared, got %+v", got)
	}
	if got := session.Memory.GetCarryoverState(); got != nil {
		t.Fatalf("expected carryover projection to be cleared, got %+v", got)
	}
	if got := session.Memory.GetMRUSnapshot(); len(got) == 0 {
		t.Fatal("expected non-topic MRU to remain available after topic switch reset")
	}
}

func TestRehydrateMRUFromMemory_RestoresRoutingAndProjectsActiveKB(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	ag.service.session.Memory.SetRoutingState(&TaskContextClassification{Domain: StoresDomain, DBArtifacts: []string{"main"}})
	ag.service.session.Memory.SetMRUSnapshot([]MRUItem{
		{Category: "PERSONA_omni", Context: "persona context", Source: MRUSourcePersona, Scope: MRUScopeSession, LastAccessed: 30},
		{Category: SYSTEM_TOOLS, Context: "tool context", Source: MRUSourceSystemTools, Scope: MRUScopeSession, LastAccessed: 20},
		{Category: askOutcomeMRUCategoryQuery, Context: "Find users", Source: MRUSourceAskOutcome, Scope: MRUScopeSession, LastAccessed: 10},
		{Category: "IGNORED_ASK", Context: "ignore me", Source: MRUSourceAskOutcome, Scope: MRUScopeAsk, LastAccessed: 40},
	})
	ag.service.session.Memory.AddThread(&ConversationThread{Exchanges: []Interaction{{ActiveKB: "sop, medical"}}})

	payload := &ai.SessionPayload{Variables: map[string]any{}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	ag.rehydrateMRUFromMemory(ctx)

	routing, ok := payload.Variables["RoutingState"].(*TaskContextClassification)
	if !ok || routing == nil || routing.Domain != StoresDomain || len(routing.DBArtifacts) != 1 || routing.DBArtifacts[0] != "main" {
		t.Fatalf("expected routing state to be restored from STM, got %#v", payload.Variables["RoutingState"])
	}
	routing.DBArtifacts[0] = "mutated"
	restoredAgain := ag.service.session.Memory.GetRoutingState()
	if restoredAgain.DBArtifacts[0] != "main" {
		t.Fatalf("expected restored routing state to be cloned, got %+v", restoredAgain)
	}

	snapshot := ag.getMRUSnapshot()
	if len(snapshot) == 0 {
		t.Fatal("expected projected MRU items to be rehydrated into session MRU")
	}
	if _, ok := ag.getMRUCategoryBySource("PERSONA_omni", MRUSourcePersona, false); !ok {
		t.Fatalf("expected persona MRU to be projected, got %+v", snapshot)
	}
	if _, ok := ag.getMRUCategoryBySource(SYSTEM_TOOLS, MRUSourceSystemTools, false); !ok {
		t.Fatalf("expected system tools MRU to be projected, got %+v", snapshot)
	}
	if _, ok := ag.getMRUCategoryBySource(playbookMRUCategory("sop"), MRUSourcePlaybook, false); !ok {
		t.Fatalf("expected active KB playbook to be projected, got %+v", snapshot)
	}
	if _, ok := ag.getMRUCategoryBySource(playbookMRUCategory("medical"), MRUSourcePlaybook, false); !ok {
		t.Fatalf("expected second active KB playbook to be projected, got %+v", snapshot)
	}
	for _, item := range snapshot {
		if item.Category == "IGNORED_ASK" {
			t.Fatalf("did not expect ask-scoped MRU item to be rehydrated, got %+v", snapshot)
		}
	}
	if summary := summarizeProjectedMRU(projectMRUItemsFromSTM(ag.service.session.Memory.GetMRUSnapshot(), "sop")); !strings.Contains(summary, "playbook:PLAYBOOK_sop") {
		t.Fatalf("expected summary to include projected playbook label, got %q", summary)
	}
}

func TestProjectMRUItemsFromSTM_IgnoresSessionScopedAskProgressCorruption(t *testing.T) {
	projected := projectMRUItemsFromSTM([]MRUItem{
		{Category: askProgressMRUCategoryHeader, Context: "In-Flight Ask Progress:", Source: MRUSourceAskProgress, Scope: MRUScopeSession, LastAccessed: 100},
		{Category: askProgressMRUCategoryResult, Context: "- Working answer so far: stale provisional summary", Source: MRUSourceAskProgress, Scope: MRUScopeSession, LastAccessed: 99},
		{Category: askOutcomeMRUCategoryHeader, Context: "Recent Ask Outcome:", Source: MRUSourceAskOutcome, Scope: MRUScopeSession, LastAccessed: 10},
		{Category: askOutcomeMRUCategoryResult, Context: "- Last outcome: canonical answer", Source: MRUSourceAskOutcome, Scope: MRUScopeSession, LastAccessed: 9},
	}, "")

	if len(projected) != 2 {
		t.Fatalf("expected only canonical ask outcome items to project, got %+v", projected)
	}
	for _, item := range projected {
		if item.Source == MRUSourceAskProgress || strings.Contains(item.Context, "stale provisional summary") {
			t.Fatalf("expected projection to ignore stale ask-progress corruption, got %+v", projected)
		}
	}
	if projected[0].Source != MRUSourceAskOutcome || projected[1].Source != MRUSourceAskOutcome {
		t.Fatalf("expected projected continuity to come only from ask outcome MRU, got %+v", projected)
	}
}
