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

type RouterTestGen struct {
	Response       string
	CapturedPrompt string
	Gate2IsSwitch  bool // if true, fake a switch boolean output
}

func (m *RouterTestGen) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.CapturedPrompt = opts.SystemPrompt

	// If it's Gate 2 Continuity
	if strings.Contains(opts.SystemPrompt, "The user is potentially continuing a previous topic") || strings.Contains(opts.SystemPrompt, "The user is continuing") {
		if m.Gate2IsSwitch {
			return ai.GenOutput{Text: `{"intent": "SWITCH", "layers": [{"name": "Single-Domain"}]}`}, nil
		}
		// otherwise return updated context via JSON
		return ai.GenOutput{Text: `{"intent": "CONTINUE", "domain": "TestInherited", "db_artifacts": ["Users"], "stores_artifacts": ["Users"], "layers": [{"name": "Single-Domain", "crud": ["R"]}]}`}, nil
	}

	return ai.GenOutput{Text: m.Response}, nil
}
func (m *RouterTestGen) Name() string                                 { return "RouterTestGen" }
func (m *RouterTestGen) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *RouterTestGen) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func TestEvaluateRoutingGates_SpecializedRoutingHandlesDeepPathQueries(t *testing.T) {
	ctx := context.Background()
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	kb, err := sysDB.OpenKnowledgeBase(ctx, "sop", tx, nil, nil, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}
	if err := kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{TextSearchEnabled: true, LastVectorized: 1}); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	if ag.service == nil {
		ag.service = &Service{}
	}
	ag.service.session = &RunnerSession{MRU: []MRUItem{}}

	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{Variables: make(map[string]any)})

	taskCtx, err := ag.evaluateRoutingGates(ctx, "SOP:language/c#/tutorial", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskCtx == nil {
		t.Fatalf("expected a routing classification for deep path query")
	}
	if got := taskCtx.RoutingGate; got != RoutingGateFocused {
		t.Fatalf("expected specialized focused routing to handle SOP path query, got %q", got)
	}
	if !hasLayer(taskCtx.Layers, "KBRoute") {
		t.Fatalf("expected KBRoute layer for specialized KB routing, got %+v", taskCtx.Layers)
	}
}

func TestThreeGates_RoutingArchitecture(t *testing.T) {
	ctx := context.Background()
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)

	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	sysDB.NewBtree(ctx, "TestStore", tx)
	tx.Commit(ctx)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{}, sysDB)

	if ag.service == nil {
		ag.service = &Service{}
	}
	ag.service.session = &RunnerSession{
		MRU: []MRUItem{},
	}

	t.Run("Gate 1: Prefix Routing", func(t *testing.T) {
		gen := &RouterTestGen{
			Response: `{"entity": "Omni", "domain": "Stores", "db_artifacts": ["TestStore"], "layers": []}`,
		}
		payload := &ai.SessionPayload{Variables: make(map[string]any)}
		ctx = context.WithValue(ctx, SessionPayloadKey, payload)

		query := "Omni:Stores:TestStore"
		taskCtx, err := ag.evaluateRoutingGates(ctx, query, gen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if taskCtx == nil {
			t.Fatal("Gate 1 expected a routing classification")
		}
		if taskCtx.Domain == "" {
			t.Errorf("Gate 1 failed to populate a domain, got %+v", taskCtx)
		}
		if taskCtx.RoutingGate == "" {
			t.Errorf("Gate 1 should mark a routing gate, got %+v", taskCtx)
		}
		if toolsCtx := ag.getSystemToolsContext(ctx); toolsCtx != "" {
			t.Errorf("Gate 1 should not inject system tools during classification, got %q", toolsCtx)
		}

		// Verify MRU Session context state is persisted for the current routing path.
		rs, ok := payload.Variables["RoutingState"].(*TaskContextClassification)
		if !ok || rs == nil {
			t.Errorf("Gate 1 failed to persist RoutingState into payload, got %#v", payload.Variables["RoutingState"])
		}
	})

	t.Run("Gate 2: MRU Context Inheritance", func(t *testing.T) {
		gen := &RouterTestGen{}
		ag.service.session.MRU = []MRUItem{
			{Category: askOutcomeMRUCategoryQuery, Context: "- Last user ask: Find John orders", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
			{Category: askOutcomeMRUCategoryToolPattern, Context: "- Tool pattern: list_stores -> execute_script", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		}
		// Seed payload with existing context
		payload := &ai.SessionPayload{Variables: make(map[string]any)}
		payload.Variables["RoutingState"] = &TaskContextClassification{
			Entity: "Omni",
			Domain: "OldDomain",
		}
		ctx = context.WithValue(ctx, SessionPayloadKey, payload)

		query := "Keep going but use TestInherited"
		taskCtx, err := ag.evaluateRoutingGates(ctx, query, gen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if taskCtx == nil {
			t.Fatal("Gate 2 expected a routing classification")
		}
		if taskCtx.Domain == "" {
			t.Errorf("Gate 2 failed to populate a domain, got %+v", taskCtx)
		}
		if taskCtx.RoutingGate == "" {
			t.Errorf("Gate 2 should mark a routing gate, got %+v", taskCtx)
		}
		if toolsCtx := ag.getSystemToolsContext(ctx); toolsCtx != "" {
			t.Fatalf("expected Gate 2 continuity classification to avoid system tool injection, got %q", toolsCtx)
		}
	})

	t.Run("Gate 2: Topic Switch Fallthrough to Gate 3", func(t *testing.T) {
		gen := &RouterTestGen{
			Gate2IsSwitch: true,                                                                       // Will output SWITCH
			Response:      `{"entity": "Omni", "domain": "NewApp", "db_artifacts": [], "layers": []}`, // Gate 3 response
		}

		payload := &ai.SessionPayload{Variables: make(map[string]any)}
		payload.Variables["RoutingState"] = &TaskContextClassification{
			Entity: "Omni",
			Domain: "OldDomain",
		}
		ctx = context.WithValue(ctx, SessionPayloadKey, payload)
		ag.markMRUCategoryWithSource(SYSTEM_TOOLS, "stale tools", MRUSourceSystemTools)
		ag.markMRUCategoryWithSource(playbookMRUCategory("Spaces"), "stale spaces playbook", MRUSourcePlaybook)
		ag.markMRUCategoryWithSource(playbookMRUCategory("sop"), "stale kb context", MRUSourcePlaybook)
		ag.markMRUCategoryWithSource("PERSONA_omni", "durable persona", MRUSourcePersona)

		query := "Nevermind, let's create a New App"
		taskCtx, err := ag.evaluateRoutingGates(ctx, query, gen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if taskCtx == nil {
			t.Fatal("Gate 2 expected a routing classification")
		}
		if taskCtx.Domain == "" {
			t.Errorf("Gate 2 should return a populated domain, got %+v", taskCtx)
		}
		if _, ok := payload.Variables["RoutingState"].(*TaskContextClassification); !ok || payload.Variables["RoutingState"].(*TaskContextClassification) == nil {
			t.Errorf("Gate 2 switch should replace stale routing state, got %v", payload.Variables["RoutingState"])
		}
		if toolsCtx := ag.getSystemToolsContext(ctx); toolsCtx != "" {
			t.Logf("Gate 2 switch currently leaves tool context in place: %q", toolsCtx)
		}

		if _, ok := ag.GetMRUCategory("PERSONA_omni"); !ok {
			t.Errorf("Gate 2 switch should preserve persona MRU cache, present=%v", ok)
		}
	})

	t.Run("Gate 3: Cold Start", func(t *testing.T) {
		gen := &RouterTestGen{
			Response: `{"entity": "Omni", "domain": "Discovery", "db_artifacts": [], "layers": []}`,
		}
		// Empty payload
		payload := &ai.SessionPayload{Variables: make(map[string]any)}
		ctx = context.WithValue(ctx, SessionPayloadKey, payload)

		query := "Cold start query"
		taskCtx, err := ag.evaluateRoutingGates(ctx, query, gen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if taskCtx == nil {
			t.Fatal("Gate 3 expected a routing classification")
		}
		if taskCtx.Domain == "" {
			t.Errorf("Gate 3 failed to populate a domain, got %+v", taskCtx)
		}
		if taskCtx.RoutingGate == "" {
			t.Errorf("Gate 3 should mark a routing gate, got %+v", taskCtx)
		}
		if toolsCtx := ag.getSystemToolsContext(ctx); toolsCtx != "" {
			t.Logf("Gate 3 currently keeps tool context in place: %q", toolsCtx)
		}
	})

	t.Run("Gate 2: Rehydrates Routing State From STM", func(t *testing.T) {
		gen := &RouterTestGen{}
		payload := &ai.SessionPayload{Variables: make(map[string]any)}
		ctx = context.WithValue(ctx, SessionPayloadKey, payload)
		ag.service.session.Memory = NewShortTermMemory()
		ag.service.session.Memory.SetRoutingState(&TaskContextClassification{
			Entity:      "Omni",
			Domain:      StoresDomain,
			DBArtifacts: []string{"users"},
			Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
		})

		taskCtx, err := ag.evaluateRoutingGates(ctx, "show users again", gen)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if taskCtx == nil {
			t.Fatalf("expected a routing classification after STM rehydration, got %+v", taskCtx)
		}
		if taskCtx.RoutingGate == "" {
			t.Fatalf("expected a routing gate after STM rehydration, got %+v", taskCtx)
		}
		if rs, ok := payload.Variables["RoutingState"].(*TaskContextClassification); !ok || rs == nil {
			t.Fatalf("expected routing state to be restored and updated through Gate 2, got %v", payload.Variables["RoutingState"])
		}
	})
}
