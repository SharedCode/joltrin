package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

type pipelineFakeGen struct {
	Response  string
	CallCount int
}

func (m *pipelineFakeGen) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.CallCount++
	return ai.GenOutput{Text: m.Response}, nil
}
func (m *pipelineFakeGen) Name() string                                 { return "fake_pipeline" }
func (m *pipelineFakeGen) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *pipelineFakeGen) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

type retryMetaAskMockGen struct {
	CapturedPrompt string
}

func (m *retryMetaAskMockGen) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.CapturedPrompt = prompt
	return ai.GenOutput{Text: "Retried previous ask."}, nil
}

func (m *retryMetaAskMockGen) Name() string                                 { return "retry_meta_ask_mock" }
func (m *retryMetaAskMockGen) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *retryMetaAskMockGen) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func TestRewriteRetryMetaAsk_UsesLatestAskOutcomeQuery(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find orders for users with first_name 'John' with total amount > 500",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	rewritten, ok := ag.rewriteRetryMetaAsk(ctx, "Can we retry the same ask?")
	if !ok {
		t.Fatalf("expected retry meta ask to rewrite")
	}
	if rewritten != "Find orders for users with first_name 'John' with total amount > 500" {
		t.Fatalf("unexpected rewritten query: %q", rewritten)
	}
	if payload.CurrentUserQuery != rewritten {
		t.Fatalf("expected payload current query to update, got %q", payload.CurrentUserQuery)
	}
	if payload.RetryRewriteState == nil || payload.RetryRewriteState.ResolvedQuery != rewritten {
		t.Fatalf("expected typed retry rewrite state to be set, got %+v", payload.RetryRewriteState)
	}
	if payload.RetryRewriteState.OriginalQuery != "Can we retry the same ask?" {
		t.Fatalf("expected typed retry state to preserve original query, got %+v", payload.RetryRewriteState)
	}
}

func TestRewriteRetryMetaAsk_AcceptsQualifiedRetryQuery(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find orders for users with first_name 'John' with total amount > 500",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	query := "Since you failed, can you retry the same ask? perhaps I can supply missing info you need."
	rewritten, ok := ag.rewriteRetryMetaAsk(ctx, query)
	if !ok {
		t.Fatalf("expected qualified retry meta ask to rewrite")
	}
	if rewritten != "Find orders for users with first_name 'John' with total amount > 500" {
		t.Fatalf("unexpected rewritten query: %q", rewritten)
	}
	if payload.CurrentUserQuery != rewritten {
		t.Fatalf("expected payload current query to update, got %q", payload.CurrentUserQuery)
	}
	if payload.RetryRewriteState == nil || payload.RetryRewriteState.ResolvedQuery != rewritten {
		t.Fatalf("expected typed retry rewrite state to be set, got %+v", payload.RetryRewriteState)
	}
	if payload.RetryRewriteState.OriginalQuery != query {
		t.Fatalf("expected typed retry state to preserve original query, got %+v", payload.RetryRewriteState)
	}
}

func TestRewriteRetryMetaAsk_IgnoresNonRetryQuery(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ctx := context.Background()

	rewritten, ok := ag.rewriteRetryMetaAsk(ctx, "Show me users")
	if ok {
		t.Fatalf("did not expect plain query to rewrite, got %q", rewritten)
	}
	if rewritten != "Show me users" {
		t.Fatalf("expected plain query to remain unchanged, got %q", rewritten)
	}
}

func TestCopilotAsk_RewritesRetryMetaAskBeforeRoutingAndEngine(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	gen := &retryMetaAskMockGen{}
	ag.SetGenerator(gen)
	payload := &ai.SessionPayload{Variables: map[string]any{
		"RoutingState": &TaskContextClassification{Entity: "Omni", Domain: StoresDomain, DBArtifacts: []string{"users"}, StoresArtifacts: []string{"users"}, Layers: []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}}},
	}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	resp, err := ag.Ask(ctx, "Can we retry the same ask?", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if resp != "Retried previous ask." {
		t.Fatalf("unexpected response: %q", resp)
	}
	if !strings.Contains(gen.CapturedPrompt, "User Query: Find John orders") {
		t.Fatalf("expected native loop prompt to use rewritten query, got: %s", gen.CapturedPrompt)
	}
	if strings.Contains(gen.CapturedPrompt, "Can we retry the same ask?") {
		t.Fatalf("did not expect meta ask to reach native loop prompt, got: %s", gen.CapturedPrompt)
	}
	if payload.CurrentUserQuery != "Find John orders" {
		t.Fatalf("expected payload current query to match rewritten ask, got %q", payload.CurrentUserQuery)
	}
	if payload.RetryRewriteState != nil {
		t.Fatalf("expected retry rewrite state to clear after epilogue, got %+v", payload.RetryRewriteState)
	}
}

func TestCopilotAsk_RewritesQualifiedRetryMetaAskBeforeRoutingAndEngine(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	gen := &retryMetaAskMockGen{}
	ag.SetGenerator(gen)
	payload := &ai.SessionPayload{Variables: map[string]any{
		"RoutingState": &TaskContextClassification{Entity: "Omni", Domain: StoresDomain, DBArtifacts: []string{"users"}, StoresArtifacts: []string{"users"}, Layers: []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}}},
	}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	query := "Since you failed, can you retry the same ask? perhaps I can supply missing info you need."
	resp, err := ag.Ask(ctx, query, nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if resp != "Retried previous ask." {
		t.Fatalf("unexpected response: %q", resp)
	}
	if !strings.Contains(gen.CapturedPrompt, "User Query: Find John orders") {
		t.Fatalf("expected native loop prompt to use rewritten query, got: %s", gen.CapturedPrompt)
	}
	if strings.Contains(gen.CapturedPrompt, query) {
		t.Fatalf("did not expect qualified meta ask to reach native loop prompt, got: %s", gen.CapturedPrompt)
	}
	if payload.CurrentUserQuery != "Find John orders" {
		t.Fatalf("expected payload current query to match rewritten ask, got %q", payload.CurrentUserQuery)
	}
	if payload.RetryRewriteState != nil {
		t.Fatalf("expected retry rewrite state to clear after epilogue, got %+v", payload.RetryRewriteState)
	}
}

func TestAsk_ClearsAskProgressMRUBeforeEarlyReturn(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.brain = nil
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.MRU = []MRUItem{
		{Category: askProgressMRUCategoryHeader, Context: "In-Flight Ask Progress:", Source: MRUSourceAskProgress, Scope: MRUScopeAsk},
		{Category: askOutcomeMRUCategoryHeader, Context: "Recent Ask Outcome:", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
	}

	resp, err := ag.Ask(context.Background(), "Hello", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if !strings.Contains(resp, "AI Copilot Disabled") {
		t.Fatalf("expected disabled response, got %q", resp)
	}

	snapshot := ag.getMRUSnapshot()
	for _, item := range snapshot {
		if item.Source == MRUSourceAskProgress {
			t.Fatalf("expected ask-progress MRU to be cleared before early return, got %+v", snapshot)
		}
	}
	foundOutcome := false
	for _, item := range snapshot {
		if item.Source == MRUSourceAskOutcome {
			foundOutcome = true
		}
	}
	if !foundOutcome {
		t.Fatalf("expected session ask outcome MRU to remain after early return, got %+v", snapshot)
	}
}

func TestRewriteConversationalMetaAsk_UsesLastAssistantQuestionAndTargetAsk(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Find John orders", Exchanges: []Interaction{{Role: RoleUser, Content: "Find John orders"}, {Role: RoleAssistant, Content: "Wait, join outputs a combined record. Do you want flat joined fields or nested objects?"}}})
	payload := &ai.SessionPayload{Variables: make(map[string]any), ClarificationState: &ai.ClarificationState{TargetQuery: "Find John orders", AssistantQuestion: "Wait, join outputs a combined record. Do you want flat joined fields or nested objects?", Status: "pending"}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	rewritten, ok := ag.rewriteConversationalMetaAsk(ctx, "Flat joined fields. Keep dotted names.")
	if !ok {
		t.Fatalf("expected conversational meta ask to rewrite")
	}
	if !strings.Contains(rewritten, "Target ask: Find John orders") || !strings.Contains(rewritten, "Assistant clarification question: Wait, join outputs a combined record. Do you want flat joined fields or nested objects?") || !strings.Contains(rewritten, "User clarification: Flat joined fields. Keep dotted names.") {
		t.Fatalf("unexpected conversational rewrite: %q", rewritten)
	}
	if payload.CurrentUserQuery != rewritten {
		t.Fatalf("expected payload current query to update, got %q", payload.CurrentUserQuery)
	}
	if payload.ClarificationState == nil {
		t.Fatal("expected explicit clarification state to remain until epilogue persists the resumed ask")
	}
	if payload.ClarificationState.TargetQuery != "Find John orders" || payload.ClarificationState.UserClarification != "Flat joined fields. Keep dotted names." || payload.ClarificationState.Status != "resolved" {
		t.Fatalf("unexpected explicit clarification state after rewrite: %+v", payload.ClarificationState)
	}
}

func TestIsLikelyMetaQuestion_DetectsClarifyingQuestionVariants(t *testing.T) {
	positives := []string{
		"Before I proceed, which output shape do you want for joined rows: flat dotted fields or nested objects?",
		"To answer this correctly, should I return flat joined fields or nested objects?",
		"Can you confirm which fields you want projected after the join?",
		"How would you like the joined result formatted?",
		"Do you want me to continue with flat joined fields?",
		"Is your goal to remove the hardcoded queries from Go and adjust the MD file so the agent searches the KB naturally based on the use case?",
	}
	for _, text := range positives {
		if !isLikelyMetaQuestion(text) {
			t.Fatalf("expected meta question detector to accept %q", text)
		}
	}

	negatives := []string{
		"Found matching orders.",
		"The join returns flat joined fields by default.",
		"Users and orders were matched successfully.",
	}
	for _, text := range negatives {
		if isLikelyMetaQuestion(text) {
			t.Fatalf("did not expect meta question detector to accept %q", text)
		}
	}
}

func TestIsLikelyMetaConversationFollowUp_UsesQuestionMarkAndClarificationMarkers(t *testing.T) {
	positives := []string{
		"Should I go with flat joined fields?",
		"Flat joined fields. Keep dotted names.",
		"Yes, remove the hardcoded queries and let the agent search on demand.",
		"Use nested objects instead.",
	}
	for _, text := range positives {
		if !isLikelyMetaConversationFollowUp(text) {
			t.Fatalf("expected follow-up detector to accept %q", text)
		}
	}

	negatives := []string{
		"Explain SOP architecture",
		"Find users in the orders database",
		"Create a script named expensive_orders",
	}
	for _, text := range negatives {
		if isLikelyMetaConversationFollowUp(text) {
			t.Fatalf("did not expect follow-up detector to accept %q", text)
		}
	}
}

func TestRewriteConversationalMetaAsk_DetectsUserQuestionReply(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Find John orders", Exchanges: []Interaction{{Role: RoleUser, Content: "Find John orders"}, {Role: RoleAssistant, Content: "Before I proceed, which output shape do you want for joined rows: flat dotted fields or nested objects?"}}})
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	rewritten, ok := ag.rewriteConversationalMetaAsk(ctx, "Should I go with flat joined fields?")
	if !ok {
		t.Fatalf("expected user question reply to rewrite as meta follow-up")
	}
	if !strings.Contains(rewritten, "User clarification: Should I go with flat joined fields?") {
		t.Fatalf("expected rewritten query to preserve user question reply, got %q", rewritten)
	}
}

func TestRewriteConversationalMetaAsk_DetectsBroaderClarifyingQuestionStyle(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Find John orders", Exchanges: []Interaction{{Role: RoleUser, Content: "Find John orders"}, {Role: RoleAssistant, Content: "Before I proceed, which output shape do you want for joined rows: flat dotted fields or nested objects?"}}})
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	rewritten, ok := ag.rewriteConversationalMetaAsk(ctx, "Flat dotted fields.")
	if !ok {
		t.Fatalf("expected broader clarifying question style to rewrite")
	}
	if !strings.Contains(rewritten, "Assistant clarification question: Before I proceed, which output shape do you want for joined rows: flat dotted fields or nested objects?") {
		t.Fatalf("expected rewritten query to include broader clarifying question style, got %q", rewritten)
	}
}

func TestRewriteConversationalMetaAsk_DetectsTranscriptStyleGoalQuestion(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Should the agent search the KB naturally for execute_script docs?",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Should the agent search the KB naturally for execute_script docs?", Exchanges: []Interaction{{Role: RoleUser, Content: "Should the agent search the KB naturally for execute_script docs?"}, {Role: RoleAssistant, Content: "Is your goal to remove the hardcoded queries from Go and adjust the MD file so the agent searches the KB naturally based on the use case?"}}})
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	rewritten, ok := ag.rewriteConversationalMetaAsk(ctx, "Yes, remove the hardcoded queries and let the agent search on demand.")
	if !ok {
		t.Fatalf("expected transcript-style goal question to rewrite")
	}
	if !strings.Contains(rewritten, "Target ask: Should the agent search the KB naturally for execute_script docs?") || !strings.Contains(rewritten, "Assistant clarification question: Is your goal to remove the hardcoded queries from Go and adjust the MD file so the agent searches the KB naturally based on the use case?") {
		t.Fatalf("expected transcript-style goal question to be preserved in rewrite, got %q", rewritten)
	}
}

func TestRewriteConversationalMetaAsk_IgnoresNonMetaAssistantTurn(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Find John orders", Exchanges: []Interaction{{Role: RoleUser, Content: "Find John orders"}, {Role: RoleAssistant, Content: "Found matching orders."}}})
	ctx := context.Background()

	rewritten, ok := ag.rewriteConversationalMetaAsk(ctx, "Flat joined fields.")
	if ok {
		t.Fatalf("did not expect non-meta assistant turn to rewrite, got %q", rewritten)
	}
	if rewritten != "Flat joined fields." {
		t.Fatalf("expected plain query to remain unchanged, got %q", rewritten)
	}
}

func TestEpilogueAndCleanup_SetsPendingClarificationState(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	ag.epilogueAndCleanup(ctx, "Find John orders", ai.IntentOmni, "Before I proceed, which output shape do you want for joined rows: flat dotted fields or nested objects?", nil, nil, nil, nil)

	if payload.ClarificationState == nil {
		t.Fatal("expected pending clarification state to be set")
	}
	if payload.ClarificationState.TargetQuery != "Find John orders" || payload.ClarificationState.Status != "pending" {
		t.Fatalf("unexpected clarification state: %+v", payload.ClarificationState)
	}
}

func TestEpilogueAndCleanup_ClearsPendingClarificationStateOnNormalAnswer(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	payload := &ai.SessionPayload{Variables: make(map[string]any), ClarificationState: &ai.ClarificationState{TargetQuery: "Find John orders", AssistantQuestion: "Which output shape?", Status: "pending"}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	ag.epilogueAndCleanup(ctx, "Find John orders", ai.IntentOmni, "Found matching orders.", nil, nil, nil, nil)

	if payload.ClarificationState != nil {
		t.Fatalf("expected pending clarification state to clear on normal answer, got %+v", payload.ClarificationState)
	}
}

func TestCopilotAsk_RewritesConversationalMetaAskBeforeRoutingAndEngine(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.service.session.MRU = []MRUItem{{
		Category: askOutcomeMRUCategoryQuery,
		Context:  "- Last user ask: Find John orders",
		Source:   MRUSourceAskOutcome,
		Scope:    MRUScopeSession,
	}}
	ag.service.session.Memory.AddThread(&ConversationThread{ID: sop.NewUUID(), RootPrompt: "Find John orders", Exchanges: []Interaction{{Role: RoleUser, Content: "Find John orders"}, {Role: RoleAssistant, Content: "Wait, join outputs a combined record. Do you want flat joined fields or nested objects?"}}})
	gen := &retryMetaAskMockGen{}
	ag.SetGenerator(gen)
	payload := &ai.SessionPayload{Variables: map[string]any{
		"RoutingState": &TaskContextClassification{Entity: "Omni", Domain: StoresDomain, DBArtifacts: []string{"users"}, StoresArtifacts: []string{"users"}, Layers: []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}}},
	}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	resp, err := ag.Ask(ctx, "Flat joined fields. Keep dotted names.", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if resp != "Retried previous ask." {
		t.Fatalf("unexpected response: %q", resp)
	}
	if !strings.Contains(gen.CapturedPrompt, "Target ask: Find John orders") || !strings.Contains(gen.CapturedPrompt, "Assistant clarification question: Wait, join outputs a combined record. Do you want flat joined fields or nested objects?") || !strings.Contains(gen.CapturedPrompt, "User clarification: Flat joined fields. Keep dotted names.") {
		t.Fatalf("expected native loop prompt to use conversational rewrite, got: %s", gen.CapturedPrompt)
	}
	if strings.Contains(gen.CapturedPrompt, "User Query: Flat joined fields. Keep dotted names.") {
		t.Fatalf("did not expect raw meta reply to reach native loop prompt, got: %s", gen.CapturedPrompt)
	}
	if payload.ClarificationState != nil {
		t.Fatalf("expected clarification state to clear after resumed ask completes, got %+v", payload.ClarificationState)
	}
}

func TestBuildAskOutcomeMRUItems_UsesAskOutcomeOverride(t *testing.T) {
	payload := &ai.SessionPayload{Variables: make(map[string]any), ClarificationState: &ai.ClarificationState{TargetQuery: "Find John orders", Status: "resolved"}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)
	items := buildAskOutcomeMRUItems(ctx, "Flat joined fields. Keep dotted names.", "Found orders", nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Category == askOutcomeMRUCategoryQuery {
			found = strings.Contains(item.Context, "Find John orders") && !strings.Contains(item.Context, "Flat joined fields")
		}
	}
	if !found {
		t.Fatalf("expected ask outcome MRU to persist the override target ask, got %+v", items)
	}
}

func TestBuildAskOutcomeMRUItems_UsesRetryRewriteState(t *testing.T) {
	payload := &ai.SessionPayload{Variables: make(map[string]any), RetryRewriteState: &ai.RetryRewriteState{OriginalQuery: "Can we retry the same ask?", ResolvedQuery: "Find John orders", Status: "resolved"}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)
	items := buildAskOutcomeMRUItems(ctx, "Can we retry the same ask?", "Found orders", nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Category == askOutcomeMRUCategoryQuery {
			found = strings.Contains(item.Context, "Find John orders") && !strings.Contains(item.Context, "Can we retry")
		}
	}
	if !found {
		t.Fatalf("expected ask outcome MRU to persist the typed retry target ask, got %+v", items)
	}
}

func TestCopilotPipeline_Phases(t *testing.T) {
	ctx := context.Background()

	os.RemoveAll("./test_data/pipeline")
	defer os.RemoveAll("./test_data/pipeline")
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{"./test_data/pipeline"}}
	sysDB := database.NewDatabase(sysDBOptions)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{}, sysDB)

	if ag.service == nil {
		ag.service = &Service{}
	}
	ag.service.session = &RunnerSession{
		MRU: []MRUItem{},
	}

	ag.Memory = memory.NewMemoryUnit("test_pipeline_agent")

	// Create mock generator
	fakeGen := &pipelineFakeGen{Response: "OMNI"}

	query := "Help me create an SOP Space for my new project."

	// --- Phase 1: Intent Routing ---
	intent := ag.classifyIntent(ctx, query, fakeGen)
	if intent != "OMNI" {
		t.Fatalf("Expected OMNI, got %s", intent)
	}

	// --- Phase 2: Domain Classification ---
	fakeGen.Response = `{"domain": "Spaces"}` // Simulate LLM returning 'Spaces' domain
	taskCtx, err := ag.evaluateRoutingGates(ctx, query, fakeGen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskCtx == nil {
		t.Fatalf("Expected non-nil TaskContextClassification")
	}
	if taskCtx.Domain != "Spaces" {
		t.Errorf("Expected 'Spaces' domain classification, got %v", taskCtx.Domain)
	}

	if toolsCtx := ag.getSystemToolsContext(ctx); toolsCtx != "" {
		t.Fatalf("Expected evaluateRoutingGates to avoid mutating System_Tools, got %q", toolsCtx)
	}

	// --- Phase 3: MRU Tracking & Propagation Verification ---
	// Let's manually inject "Spaces" MRU to simulate previous context
	// --- Phase 3: MRU Tracking & Propagation Verification ---
	// Let's manually inject "Spaces" MRU to simulate previous context
	ag.markMRUCategoryWithSource(playbookMRUCategory("Spaces"), "MRU Context: The user likes blue theme spaces", MRUSourcePlaybook)

	if ag.service.session.Memory == nil {
		ag.service.session.Memory = NewShortTermMemory()
	}
	thread := &ConversationThread{
		ID:        sop.NewUUID(),
		Exchanges: []Interaction{{Entity: intent, ActiveKB: "sop", Content: "previous space query"}},
	}
	ag.service.session.Memory.AddThread(thread)
	ag.service.session.Memory.CurrentThreadID = thread.ID

	ag.trackEpisodeMetadata(ctx, intent) // This triggers the cross-domain propagation logic

	// --- Phase 4: Prompt Construction Snapshotting ---
	// Mock Session Payload so Domains are linked properly with MRU Cache pulling
	payload := &ai.SessionPayload{
		ActiveDomain: "Spaces",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, query, *taskCtx)

	if !strings.Contains(prompt, "\"component\": \"focused_execution_context\"") {
		t.Fatalf("Snapshot Failure: Built prompt missing focused execution context component")
	}
	if !strings.Contains(prompt, "Domain: Spaces") || !strings.Contains(prompt, "Relevant Space Operations") {
		t.Fatalf("Snapshot Failure: Built prompt missing focused Spaces execution context")
	}

	if !strings.Contains(prompt, "System Tools") && !strings.Contains(prompt, "System_Tools") && !strings.Contains(prompt, "active memory") {
		// "active memory" tests the Persona portion
		t.Fatalf("Snapshot Failure: Built prompt is missing Persona/System Tools framework")
	}

	if !strings.Contains(prompt, "MRU Context: The user likes blue theme spaces") {
		t.Errorf("Snapshot Failure: Built prompt missing MRU injected knowledge")
	}

	// --- Phase 5: Delegating to Engine ---
	// Mock a JSON payload wrapped in code blocks to simulate reasoning layer tool use output
	fakeGen.Response = "I can definitely help with that. ```json\n[{\"tool\":\"search_space\",\"args\":{}}]\n```"
	finalText, toolCalls, outcomeFacts, outcomeRecipes, carryoverState, err := ag.delegateToReasoningEngine(ctx, query, fakeGen, prompt)
	if err != nil {
		t.Fatalf("Engine delegation failed: %v", err)
	}
	if carryoverState != nil {
		t.Fatalf("expected no carryover state from baseline mock delegation path, got %+v", carryoverState)
	}
	if !strings.Contains(finalText, "I can definitely help") && !strings.Contains(finalText, "search_space") {
		t.Errorf("Unexpected delegation output: %s", finalText)
	}
	if len(toolCalls) != 0 {
		t.Fatalf("expected no native tool calls from baseline mock delegation path, got %#v", toolCalls)
	}
	if len(outcomeFacts) != 0 {
		t.Fatalf("expected no grounded outcome facts from baseline mock delegation path, got %#v", outcomeFacts)
	}
	if len(outcomeRecipes) != 0 {
		t.Fatalf("expected no learned outcome recipes from baseline mock delegation path, got %#v", outcomeRecipes)
	}
}

func TestCopilotPipeline_StoresSchemaFallback_NoArtifacts(t *testing.T) {
	ctx := context.Background()

	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("failed to begin setup transaction: %v", err)
	}
	if _, err := sysDB.NewBtree(ctx, "users_schema_fallback", tx); err != nil {
		t.Fatalf("failed to create users_schema_fallback store: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("failed to commit setup transaction: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	if ag.service == nil {
		ag.service = &Service{}
	}
	ag.service.session = &RunnerSession{MRU: []MRUItem{}}

	payload := &ai.SessionPayload{
		CurrentDB: SystemDBName,
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, "List stores", TaskContextClassification{
		Domain: StoresDomain,
		Layers: []LayerInfo{{
			Name: "Single-Domain",
			CRUD: []string{"R"},
		}},
	})

	if !strings.Contains(prompt, "\"component\": \"focused_execution_context\"") {
		t.Fatalf("expected focused execution context component in prompt")
	}
	if !strings.Contains(prompt, "Domain: Stores") || !strings.Contains(prompt, "CRUD Scope: R") {
		t.Fatalf("expected focused Stores execution details in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "\"component\": \"schema\"") {
		t.Fatalf("expected schema component fallback for Stores with no classified artifacts")
	}
	if !strings.Contains(prompt, "Available Stores") || !strings.Contains(prompt, "users_schema_fallback") {
		t.Fatalf("expected generic schema fallback to include active store listing: %s", prompt)
	}
}

func TestBuildSystemPrompt_AppliesBudgetWithoutBreakingMemoryFlow(t *testing.T) {
	ctx := context.Background()
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}, Memory: NewShortTermMemory()}}

	longTools := strings.Repeat("TOOL_RULE ", 1200)
	ag.markMRUCategoryWithSource(SYSTEM_TOOLS, longTools, MRUSourceSystemTools)

	thread := &ConversationThread{ID: sop.NewUUID(), Exchanges: []Interaction{
		{Role: RoleUser, Content: strings.Repeat("User context ", 600)},
		{Role: RoleAssistant, Content: strings.Repeat("Assistant context ", 600)},
	}}
	ag.service.session.Memory.AddThread(thread)
	ag.service.session.Memory.CurrentThreadID = thread.ID

	prompt := ag.buildSystemPrompt(ctx, "Find users in users store", TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	if len(prompt) > 17000 {
		t.Fatalf("expected budgeted prompt length <= 17000 chars, got %d", len(prompt))
	}
	if !strings.Contains(prompt, "\"component\": \"focused_execution_context\"") {
		t.Fatalf("expected focused context to survive budgeting: %s", prompt)
	}
	if !strings.Contains(prompt, "\"component\": \"system_tools\"") {
		t.Fatalf("expected system tools to survive budgeting: %s", prompt)
	}
	if !strings.Contains(prompt, "\"component\": \"conversation_history\"") {
		t.Fatalf("expected STM-backed history to remain present after budgeting: %s", prompt)
	}
	if !strings.Contains(prompt, "[truncated]") {
		t.Fatalf("expected oversized components to be truncated under budget: %s", prompt)
	}
	if !strings.Contains(prompt, "Find users in users store") {
		t.Fatalf("expected user query to remain intact: %s", prompt)
	}
}

func TestPromptBudgetProfile_UsesRoutingGate(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)

	discovery := ag.promptBudgetProfile(TaskContextClassification{
		Domain: StoresDomain,
		Layers: []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})
	focused := ag.promptBudgetProfile(TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
		RoutingGate: RoutingGateFocused,
	})
	continuity := ag.promptBudgetProfile(TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
		RoutingGate: RoutingGateContinuity,
	})

	if focused.ComponentCharBudgets[ComponentFocusedContext] <= discovery.ComponentCharBudgets[ComponentFocusedContext] {
		t.Fatalf("expected focused gate to prioritize focused context budget, got focused=%d discovery=%d", focused.ComponentCharBudgets[ComponentFocusedContext], discovery.ComponentCharBudgets[ComponentFocusedContext])
	}
	if focused.ComponentCharBudgets[ComponentHistory] >= discovery.ComponentCharBudgets[ComponentHistory] {
		t.Fatalf("expected focused gate to reduce history budget, got focused=%d discovery=%d", focused.ComponentCharBudgets[ComponentHistory], discovery.ComponentCharBudgets[ComponentHistory])
	}
	if continuity.ComponentCharBudgets[ComponentHistory] <= discovery.ComponentCharBudgets[ComponentHistory] {
		t.Fatalf("expected continuity gate to expand history budget, got continuity=%d discovery=%d", continuity.ComponentCharBudgets[ComponentHistory], discovery.ComponentCharBudgets[ComponentHistory])
	}
	if continuity.TotalChars <= focused.TotalChars {
		t.Fatalf("expected continuity gate to allow more total context than focused gate, got continuity=%d focused=%d", continuity.TotalChars, focused.TotalChars)
	}
}

func TestBuildSystemPrompt_UsesLeanAssemblyForGroundedStoresAsk(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}, Memory: NewShortTermMemory()}}
	ag.markMRUCategoryWithSource("PERSONA_omni", strings.Repeat("persona rule ", 400), MRUSourcePersona)
	ag.markMRUCategoryWithSource(playbookMRUCategory("sop"), strings.Repeat("playbook context ", 200), MRUSourcePlaybook)

	prompt := ag.buildSystemPrompt(ctx, "Find orders for users named John", TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users", "orders"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}

	personaChars := 0
	hasSemantic := false
	hasPlaybooks := false
	hasFocused := false
	for _, element := range elements {
		switch element.Component {
		case ComponentPersona:
			personaChars = len(element.Content)
		case ComponentSemanticMemory:
			hasSemantic = true
		case ComponentPlaybooks:
			hasPlaybooks = true
		case ComponentFocusedContext:
			hasFocused = strings.Contains(element.Content, "Domain: Stores")
		}
	}
	if personaChars == 0 || personaChars > 920 {
		t.Fatalf("expected lean assembly to cap persona content, got %d chars", personaChars)
	}
	if hasSemantic {
		t.Fatalf("expected lean grounded Stores assembly to omit semantic memory, got %+v", elements)
	}
	if hasPlaybooks {
		t.Fatalf("expected lean grounded Stores assembly to omit playbooks, got %+v", elements)
	}
	if !hasFocused {
		t.Fatalf("expected focused execution context to remain present, got %+v", elements)
	}
	if !strings.Contains(prompt, "Find orders for users named John") {
		t.Fatalf("expected user query to remain intact: %s", prompt)
	}
}

func TestBuildSystemPrompt_IncludesCoreSOPPlaybooksWhenNoKBIsSelected(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	kb, err := sysDB.OpenKnowledgeBase(ctx, ai.DefaultKBName, tx, nil, nil, false)
	if err != nil {
		t.Fatalf("open kb: %v", err)
	}
	if err := kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{SystemPrompt: "You are Omni from SOP KB."}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}, Memory: NewShortTermMemory()}}
	ag.markMRUCategoryWithSource(playbookMRUCategory("sop"), "Retrieved Semantics:\n- Context (Score: 1.00): SOP playbook context", MRUSourcePlaybook)
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		SelectedKBs: nil,
	})

	prompt := ag.buildSystemPrompt(ctx, "Explain SOP architecture", TaskContextClassification{
		Domain: StoresDomain,
		Layers: []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}
	hasPlaybooks := false
	hasPersona := false
	for _, element := range elements {
		if element.Component == ComponentPlaybooks && strings.Contains(element.Content, "SOP playbook context") {
			hasPlaybooks = true
		}
		if element.Component == ComponentPersona && strings.Contains(element.Content, "You are Omni from SOP KB.") {
			hasPersona = true
		}
	}
	if !hasPersona {
		t.Fatalf("expected SOP KB persona to be loaded, got %+v", elements)
	}
	if !hasPlaybooks {
		t.Fatalf("expected core SOP playbooks to remain available when no KB is selected, got %+v", elements)
	}
}

func TestBuildSystemPrompt_DoesNotUseSelectedKBPersonaForOmni(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	sopKB, err := sysDB.OpenKnowledgeBase(ctx, ai.DefaultKBName, tx, nil, nil, false)
	if err != nil {
		t.Fatalf("open sop kb: %v", err)
	}
	if err := sopKB.SetConfig(ctx, &memory.KnowledgeBaseConfig{SystemPrompt: "You are Omni from SOP KB."}); err != nil {
		t.Fatalf("set sop config: %v", err)
	}
	avatarKB, err := sysDB.OpenKnowledgeBase(ctx, "legal_kb", tx, nil, nil, false)
	if err != nil {
		t.Fatalf("open avatar kb: %v", err)
	}
	if err := avatarKB.SetConfig(ctx, &memory.KnowledgeBaseConfig{SystemPrompt: "You are the Legal Avatar."}); err != nil {
		t.Fatalf("set avatar config: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}, Memory: NewShortTermMemory()}}
	ag.Memory.AgentID = "legal_kb"
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		CurrentDB: SystemDBName,
		SelectedKBs: []ai.ArtifactReference{{
			Name:         "legal_kb",
			Type:         ai.ArtifactTypeSpace,
			DatabaseName: SystemDBName,
		}},
	})

	prompt := ag.buildSystemPrompt(ctx, "Explain the platform", TaskContextClassification{Domain: StoresDomain})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}
	personaText := ""
	for _, element := range elements {
		if element.Component == ComponentPersona {
			personaText = element.Content
			break
		}
	}
	if !strings.Contains(personaText, "You are Omni from SOP KB.") {
		t.Fatalf("expected Omni persona from SOP KB, got %q", personaText)
	}
	if strings.Contains(personaText, "You are the Legal Avatar.") {
		t.Fatalf("expected selected KB persona not to override Omni, got %q", personaText)
	}
}

func TestPromptBudgetProfile_UsesLeanStoresAssembly(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	profile := ag.promptBudgetProfile(TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users", "orders"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	if profile.ComponentCharBudgets[ComponentPersona] != 900 {
		t.Fatalf("expected lean stores persona budget, got %d", profile.ComponentCharBudgets[ComponentPersona])
	}
	if profile.ComponentCharBudgets[ComponentSemanticMemory] != 0 {
		t.Fatalf("expected lean stores semantic memory budget 0, got %d", profile.ComponentCharBudgets[ComponentSemanticMemory])
	}
	if profile.ComponentCharBudgets[ComponentPlaybooks] != 0 {
		t.Fatalf("expected lean stores playbooks budget 0, got %d", profile.ComponentCharBudgets[ComponentPlaybooks])
	}
	if profile.ComponentCharBudgets[ComponentFocusedContext] <= profile.ComponentCharBudgets[ComponentSystemTools] {
		t.Fatalf("expected focused context to outrank system tools in lean stores budget, got focused=%d tools=%d", profile.ComponentCharBudgets[ComponentFocusedContext], profile.ComponentCharBudgets[ComponentSystemTools])
	}
}

func TestBuildSystemPrompt_IncludesWorkflowRecipesComponent(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	prompt := ag.buildSystemPrompt(ctx, "Create a script named expensive_orders to find orders over 1000", TaskContextClassification{
		Domain:          StoresDomain,
		DBArtifacts:     []string{"orders"},
		Layers:          []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
		ScriptAuthoring: true,
	})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}

	recipeContent := ""
	for _, element := range elements {
		if element.Component == ComponentRecipes {
			recipeContent = element.Content
			break
		}
	}
	if recipeContent == "" {
		t.Fatalf("expected workflow_recipes component in prompt: %s", prompt)
	}
	if strings.Contains(recipeContent, "[truncated]") {
		t.Fatalf("expected explicit recipe component to fit without truncation, got: %s", recipeContent)
	}
	if !strings.Contains(recipeContent, "Reusable script authoring") || !strings.Contains(recipeContent, "Stores schema-first research") || !strings.Contains(recipeContent, "Stores read transaction flow") || !strings.Contains(recipeContent, "Stores join slice repair") || !strings.Contains(recipeContent, "Stores predicate grounding") {
		t.Fatalf("expected workflow recipes to include script-authoring and stores protocols, got: %s", recipeContent)
	}
	if !strings.Contains(recipeContent, "begin_tx(mode=read)") || !strings.Contains(recipeContent, "create_script") {
		t.Fatalf("expected workflow recipes to retain protocol anchors, got: %s", recipeContent)
	}
	if !strings.Contains(recipeContent, "scope list_stores with stores:[...]") {
		t.Fatalf("expected workflow recipes to include scoped list_stores guidance, got: %s", recipeContent)
	}
}

func TestBuildSystemPrompt_IncludesClarificationQuestionStyleGuidance(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	prompt := ag.buildSystemPrompt(ctx, "Explain SOP architecture", TaskContextClassification{Domain: StoresDomain})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}

	personaContent := ""
	for _, element := range elements {
		if element.Component == ComponentPersona {
			personaContent = element.Content
			break
		}
	}
	if personaContent == "" {
		t.Fatalf("expected persona component in prompt: %s", prompt)
	}
	checks := []string{"Ask one short direct clarification question", "Do you want...", "Which...", "Is your goal...", "Before I proceed..."}
	for _, check := range checks {
		if !strings.Contains(personaContent, check) {
			t.Fatalf("expected persona guardrail to retain clarification style guidance %q, got: %s", check, personaContent)
		}
	}
}

func TestBuildSystemPrompt_RehydratesSystemToolsFromSTMProjection(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	ag.service.session.Memory.SetMRUSnapshot([]MRUItem{{
		Category: SYSTEM_TOOLS,
		Context:  "STM_REHYDRATED_TOOLS",
		Source:   MRUSourceSystemTools,
	}})

	prompt := ag.buildSystemPrompt(context.Background(), "List stores", TaskContextClassification{})
	if !strings.Contains(prompt, "STM_REHYDRATED_TOOLS") {
		t.Fatalf("expected buildSystemPrompt to rehydrate system tools from STM projection, got %s", prompt)
	}
}

func TestBuildSystemPrompt_RehydratesAskOutcomeFromSTMProjection(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	ag.service.session.Memory.SetMRUSnapshot([]MRUItem{
		{Category: askOutcomeMRUCategoryHeader, Context: "Recent Ask Outcome:", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryQuery, Context: "- Last user ask: Find users named John", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryResult, Context: "- Last outcome: Found matching users", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryStoreSchema + "_USERS", Context: "- Confirmed: list_stores confirmed users schema=key:string, first_name:string", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryRelations + "_USERS__USERS_ORDERS__USERS_ORDERS_KEY__USERS_KEY", Context: "- Confirmed: list_stores confirmed users relations=[users_orders(key->users.key)]", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryGuidance, Context: "- Reuse confirmed facts and successful patterns from this outcome before broadening scope.", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
	})

	prompt := ag.buildSystemPrompt(context.Background(), "Find orders for those users", TaskContextClassification{})
	if !strings.Contains(prompt, "Recent Ask Outcome:") || !strings.Contains(prompt, "Find users named John") || !strings.Contains(prompt, "list_stores confirmed users schema=key:string, first_name:string") || !strings.Contains(prompt, "list_stores confirmed users relations=[users_orders(key-") {
		t.Fatalf("expected buildSystemPrompt to rehydrate ask outcome from STM projection, got %s", prompt)
	}
}

func TestBuildSystemPrompt_IgnoresStaleAskProgressWhenRehydratingFromSTM(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	ag.service.session.Memory.SetMRUSnapshot([]MRUItem{
		{Category: askProgressMRUCategoryHeader, Context: "In-Flight Ask Progress:", Source: MRUSourceAskProgress, Scope: MRUScopeSession},
		{Category: askProgressMRUCategoryResult, Context: "- Working answer so far: stale provisional summary", Source: MRUSourceAskProgress, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryHeader, Context: "Recent Ask Outcome:", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryQuery, Context: "- Last user ask: Find users named John", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
		{Category: askOutcomeMRUCategoryResult, Context: "- Last outcome: Found matching users", Source: MRUSourceAskOutcome, Scope: MRUScopeSession},
	})

	prompt := ag.buildSystemPrompt(context.Background(), "Find orders for those users", TaskContextClassification{})
	if strings.Contains(prompt, "stale provisional summary") || strings.Contains(prompt, "In-Flight Ask Progress:") {
		t.Fatalf("expected buildSystemPrompt to ignore stale ask-progress MRU from STM, got %s", prompt)
	}
	if !strings.Contains(prompt, "Recent Ask Outcome:") || !strings.Contains(prompt, "Found matching users") {
		t.Fatalf("expected buildSystemPrompt to keep canonical ask outcome continuity, got %s", prompt)
	}
}

func TestBuildSystemPrompt_RehydratesImplicitRecipesFromSTM(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	ag.service.session.Memory.SetRecipeSnapshot([]RecipeItem{{
		ID:         "implicit.execute_script.research_then_retry",
		Kind:       RecipeKindImplicit,
		Scope:      RecipeScopeMicro,
		Domain:     StoresDomain,
		Topic:      "Research grounded schema before execute_script retry",
		Trigger:    "execute_script repair requires missing schema or relation facts",
		Protocol:   []string{"Call list_stores first to confirm schema and relations.", "Retry execute_script with corrected grounded arguments."},
		Invariants: []string{"Preserve valid script slices.", "Do not broaden scope before the retry."},
		Confidence: 0.95,
		Source:     "inner_loop_success",
	}})

	prompt := ag.buildSystemPrompt(context.Background(), "Find users named John", TaskContextClassification{Domain: StoresDomain})
	if !strings.Contains(prompt, "workflow_recipes") || !strings.Contains(prompt, "Research grounded schema before execute_script retry") || !strings.Contains(prompt, "Retry execute_script with corrected grounded arguments") {
		t.Fatalf("expected buildSystemPrompt to include implicit recipe snapshot, got %s", prompt)
	}
}

func TestBuildSystemPrompt_AddsMemoryContinuationFallbackForContinuityRouting(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession()}
	threadID := sop.NewUUID()
	ag.service.session.Memory.AddThread(&ConversationThread{ID: threadID, RootPrompt: "Find users named John", Exchanges: []Interaction{{Role: RoleAssistant, Content: "Found John Jones.", ActiveKB: "sop"}}})
	ag.service.session.Memory.SetCarryoverState(&ai.CarryoverState{
		Provider:             "gemini",
		Model:                "gemini-test",
		TopicFingerprint:     threadID.String(),
		KBFingerprint:        "sop",
		LastUsedAtUnixMilli:  time.Now().UnixMilli(),
		LastOutcomeFacts:     []string{"list_stores confirmed users schema=key:string, first_name:string"},
		LastRecipeIDs:        []string{"implicit.execute_script.research_then_retry"},
		LastToolNames:        []string{"list_stores", "execute_script"},
		LastUserQuery:        "Find users named John",
		LastAssistantSummary: "Found John Jones.",
	})

	prompt := ag.buildSystemPrompt(context.Background(), "Find orders for those users", TaskContextClassification{RoutingGate: RoutingGateContinuity, Domain: StoresDomain})
	if !strings.Contains(prompt, "Memory Continuity Fallback") || !strings.Contains(prompt, "Find users named John") || !strings.Contains(prompt, "Found John Jones") {
		t.Fatalf("expected continuity prompt to include memory fallback summary, got %s", prompt)
	}
}

func TestBuildAskOutcomeMRUItems_IncludesCarryoverPayload(t *testing.T) {
	items := buildAskOutcomeMRUItems(context.Background(), "Find users", "Found users", nil, nil, &ai.CarryoverState{
		Provider:           "chatgpt",
		ConversationID:     "conv_123",
		ConversationHandle: "resp_456",
	})
	if len(items) == 0 {
		t.Fatal("expected carryover MRU items to be produced")
	}
	found := false
	for _, item := range items {
		if strings.Contains(item.Context, "conv_123") || strings.Contains(item.Context, "resp_456") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected carryover payload to appear in MRU items, got %+v", items)
	}
}

func TestBuildAskOutcomeMRUItems_TypesConfirmedStoreFacts(t *testing.T) {
	items := buildAskOutcomeMRUItems(context.Background(), "Find users", "Found users", []ai.ToolCall{{Name: "list_stores"}}, []string{
		"list_stores confirmed users schema=key:string, first_name:string",
		"list_stores confirmed users relations=[users_orders(key->users.key)]",
		"execute_script confirmed join store=users_orders on=users.key->key",
		"execute_script confirmed filter field=first_name op=$eq",
		"execute_script returned: [{\"first_name\":\"John\"}]",
	}, nil)

	seenCategories := map[string]bool{}
	for _, item := range items {
		seenCategories[item.Category] = true
	}

	if !seenCategories[askOutcomeMRUCategoryStoreSchema+"_USERS"] {
		t.Fatalf("expected typed schema category, got %+v", items)
	}
	if !seenCategories[askOutcomeMRUCategoryRelations+"_USERS__USERS_ORDERS__USERS_ORDERS_KEY__USERS_KEY"] {
		t.Fatalf("expected typed relations category, got %+v", items)
	}
	if !seenCategories[askOutcomeMRUCategoryJoinSelection+"_JOIN__USERS_ORDERS__USERS_KEY__KEY"] {
		t.Fatalf("expected typed join selection category, got %+v", items)
	}
	if !seenCategories[askOutcomeMRUCategoryFilterSelection+"_FIRST_NAME___EQ"] {
		t.Fatalf("expected typed filter selection category, got %+v", items)
	}
	if !seenCategories[askOutcomeMRUCategoryConfirmed+"_01"] {
		t.Fatalf("expected generic fallback category for unstructured confirmed fact, got %+v", items)
	}
}

func TestPersistAskProgressMRU_UsesAskScopeOnly(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.persistAskProgressMRU(ai.MemoryHydrationUpdate{
		ToolCalls:    []ai.ToolCall{{Name: "list_stores"}, {Name: "execute_script"}},
		OutcomeFacts: []string{"list_stores confirmed users schema=key:string, first_name:string"},
	})

	snapshot := ag.getMRUSnapshot()
	if len(snapshot) == 0 {
		t.Fatal("expected ask-progress MRU items")
	}
	seenProgress := false
	for _, item := range snapshot {
		if item.Source != MRUSourceAskProgress {
			continue
		}
		seenProgress = true
		if item.Scope != MRUScopeAsk {
			t.Fatalf("expected ask-progress items to stay ask-scoped, got %+v", item)
		}
	}
	if !seenProgress {
		t.Fatalf("expected ask-progress items in snapshot, got %+v", snapshot)
	}
}

func TestClearAskProgressMRU_RemovesOnlyAskScopedProgress(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.markMRUCategory(askProgressMRUCategoryHeader, "In-Flight Ask Progress:", MRUSourceAskProgress, MRUScopeAsk)
	ag.markMRUCategory(askOutcomeMRUCategoryHeader, "Recent Ask Outcome:", MRUSourceAskOutcome, MRUScopeSession)

	ag.clearAskProgressMRU()

	snapshot := ag.getMRUSnapshot()
	for _, item := range snapshot {
		if item.Source == MRUSourceAskProgress {
			t.Fatalf("expected ask-progress MRU to be cleared, got %+v", snapshot)
		}
	}
	foundOutcome := false
	for _, item := range snapshot {
		if item.Source == MRUSourceAskOutcome {
			foundOutcome = true
		}
	}
	if !foundOutcome {
		t.Fatalf("expected session ask outcome MRU to remain, got %+v", snapshot)
	}
}

func TestEpilogueAndCleanup_FinalAskOutcomeWinsOverAskProgress(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.brain = nil
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	ag.persistAskProgressMRU(ai.MemoryHydrationUpdate{
		FinalText:    "Still researching",
		ToolCalls:    []ai.ToolCall{{Name: "list_stores"}},
		OutcomeFacts: []string{"list_stores confirmed users schema=id,first_name"},
	})

	ag.epilogueAndCleanup(context.Background(), "Find John", ai.IntentOmni, "Found John Jones.", []ai.ToolCall{{Name: "execute_script"}}, []string{"execute_script returned: [{\"first_name\":\"John Jones\"}]"}, nil, nil)

	snapshot := ag.getMRUSnapshot()
	for _, item := range snapshot {
		if item.Source == MRUSourceAskProgress {
			t.Fatalf("expected provisional ask-progress MRU to be cleared after epilogue, got %+v", snapshot)
		}
	}
	seenOutcome := false
	for _, item := range snapshot {
		if item.Source == MRUSourceAskOutcome && item.Category == askOutcomeMRUCategoryResult && strings.Contains(item.Context, "Found John Jones") {
			seenOutcome = true
		}
	}
	if !seenOutcome {
		t.Fatalf("expected final ask outcome MRU to win after epilogue, got %+v", snapshot)
	}
	stmSnapshot := ag.service.session.Memory.GetMRUSnapshot()
	for _, item := range stmSnapshot {
		if item.Source == MRUSourceAskProgress {
			t.Fatalf("expected STM snapshot to exclude ask-progress MRU, got %+v", stmSnapshot)
		}
	}
}

func TestBuildAskOutcomeMRUItems_StripsToolCallAndScriptDumpFromOutcome(t *testing.T) {
	items := buildAskOutcomeMRUItems(context.Background(), "Find users", "Planning result {\"script\":[{\"op\":\"begin_tx\"}]} <function_call name=\"execute_script\">...</function_call>", nil, nil, nil)
	found := false
	for _, item := range items {
		if item.Category != askOutcomeMRUCategoryResult {
			continue
		}
		found = true
		if strings.Contains(item.Context, "<function_call") || strings.Contains(item.Context, `{"script"}`) || strings.Contains(item.Context, `"script":[`) {
			t.Fatalf("expected ask outcome result to strip tool/script dump, got %q", item.Context)
		}
		if !strings.Contains(item.Context, "Planning result") || !strings.Contains(item.Context, "[tool call/script content stripped]") {
			t.Fatalf("expected ask outcome result to preserve summary and mark stripped tool content, got %q", item.Context)
		}
	}
	if !found {
		t.Fatal("expected ask outcome result item")
	}
}

func TestGetSessionMemoryContext_StripsAssistantToolCallDump(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory = NewShortTermMemory()
	threadID := sop.NewUUID()
	ag.service.session.Memory.AddThread(&ConversationThread{ID: threadID, Exchanges: []Interaction{{Role: RoleUser, Content: "run it"}, {Role: RoleAssistant, Content: "Crafting function call {\"script\":[{\"op\":\"begin_tx\"}]} <function_call name=\"execute_script\">...</function_call>"}}})
	ag.service.session.Memory.CurrentThreadID = threadID

	contextText := ag.getSessionMemoryContext()
	if strings.Contains(contextText, "<function_call") || strings.Contains(contextText, `"script":[`) {
		t.Fatalf("expected session memory context to strip tool/script dump, got %s", contextText)
	}
	if !strings.Contains(contextText, "Crafting function call") || !strings.Contains(contextText, "[tool call/script content stripped]") {
		t.Fatalf("expected session memory context to keep summary while marking stripped content, got %s", contextText)
	}
}

func TestProjectMRUItemsFromSTM_AppliesSourceCapsAndPriority(t *testing.T) {
	projected := projectMRUItemsFromSTM([]MRUItem{
		{Category: "PLAYBOOK_old", Source: MRUSourcePlaybook, LastAccessed: 10},
		{Category: "PLAYBOOK_new", Source: MRUSourcePlaybook, LastAccessed: 20},
		{Category: "PLAYBOOK_extra1", Source: MRUSourcePlaybook, LastAccessed: 30},
		{Category: "PLAYBOOK_extra2", Source: MRUSourcePlaybook, LastAccessed: 40},
		{Category: "PLAYBOOK_extra3", Source: MRUSourcePlaybook, LastAccessed: 50},
		{Category: SYSTEM_TOOLS, Source: MRUSourceSystemTools, Context: "tools-a", LastAccessed: 60},
		{Category: "SYSTEM_TOOLS_OLD", Source: MRUSourceSystemTools, Context: "tools-b", LastAccessed: 10},
		{Category: "PERSONA_a", Source: MRUSourcePersona, LastAccessed: 5},
		{Category: "PERSONA_b", Source: MRUSourcePersona, LastAccessed: 15},
		{Category: "PERSONA_c", Source: MRUSourcePersona, LastAccessed: 25},
		{Category: "UNSCOPED", Source: MRUSourceUnknown, LastAccessed: 100},
	}, "sop,Spaces")

	if len(projected) != 7 {
		t.Fatalf("expected capped projection size of 7, got %d (%+v)", len(projected), projected)
	}
	if projected[0].Source != MRUSourcePersona || projected[1].Source != MRUSourcePersona {
		t.Fatalf("expected persona entries to lead projection order, got %+v", projected)
	}
	if projected[2].Source != MRUSourceSystemTools {
		t.Fatalf("expected system tools to follow persona entries, got %+v", projected)
	}
	playbookCount := 0
	for _, item := range projected {
		if item.Category == "UNSCOPED" {
			t.Fatalf("unexpected unknown-source MRU item in projection: %+v", projected)
		}
		if item.Source == MRUSourcePlaybook {
			playbookCount++
		}
	}
	if playbookCount != maxProjectedPlaybookEntries {
		t.Fatalf("expected playbook entries to be capped at %d, got %d (%+v)", maxProjectedPlaybookEntries, playbookCount, projected)
	}
}

func TestEpilogueAndCleanup_PersistsMRUSnapshotWithoutSleepCycleLogging(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.brain = nil
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.markMRUCategoryWithSource(SYSTEM_TOOLS, "TOOLS_FOR_RESTART", MRUSourceSystemTools)

	ag.epilogueAndCleanup(context.Background(), "List stores", "Omni", "Done", []ai.ToolCall{{Name: "list_stores"}, {Name: "execute_script"}}, []string{"list_stores confirmed users schema=key:string, first_name:string", "list_stores confirmed users relations=[users_orders(key->users.key)]"}, []ai.LearnedRecipe{{
		ID:         "implicit.execute_script.research_then_retry",
		Kind:       RecipeKindImplicit,
		Scope:      RecipeScopeMicro,
		Domain:     StoresDomain,
		Topic:      "Research grounded schema before execute_script retry",
		Trigger:    "execute_script repair requires missing schema or relation facts",
		Protocol:   []string{"Call list_stores first to confirm schema and relations.", "Retry execute_script with corrected grounded arguments."},
		Invariants: []string{"Preserve valid script slices."},
		Confidence: 0.95,
		Source:     "inner_loop_success",
	}}, nil)

	snapshot := ag.service.session.Memory.GetMRUSnapshot()
	if len(snapshot) == 0 {
		t.Fatalf("expected epilogue to persist MRU snapshot even when sleep-cycle logging is disabled")
	}
	recipeSnapshot := ag.service.session.Memory.GetRecipeSnapshot()
	if len(recipeSnapshot) != 1 || recipeSnapshot[0].ID != "implicit.execute_script.research_then_retry" {
		t.Fatalf("expected epilogue to persist learned recipe snapshot, got %+v", recipeSnapshot)
	}
	carryover := ag.service.session.Memory.GetCarryoverState()
	if carryover != nil {
		t.Fatalf("expected epilogue to skip carryover persistence when no generator is configured, got %+v", carryover)
	}
	if snapshot[0].Category != SYSTEM_TOOLS || snapshot[0].Context != "TOOLS_FOR_RESTART" {
		t.Fatalf("unexpected MRU snapshot persisted to STM: %+v", snapshot)
	}
	foundAskOutcomeQuery := false
	foundAskOutcomeResult := false
	foundAskOutcomeSchema := false
	foundAskOutcomeRelations := false
	foundAskOutcomePattern := false
	for _, item := range snapshot {
		if item.Source == MRUSourceAskOutcome {
			if item.Category == askOutcomeMRUCategoryQuery {
				foundAskOutcomeQuery = strings.Contains(item.Context, "List stores")
			}
			if item.Category == askOutcomeMRUCategoryResult {
				foundAskOutcomeResult = strings.Contains(item.Context, "Done")
			}
			if strings.HasPrefix(item.Category, askOutcomeMRUCategoryStoreSchema+"_") {
				foundAskOutcomeSchema = foundAskOutcomeSchema || strings.Contains(item.Context, "list_stores confirmed users schema=key:string, first_name:string")
			}
			if strings.HasPrefix(item.Category, askOutcomeMRUCategoryRelations+"_") {
				foundAskOutcomeRelations = foundAskOutcomeRelations || strings.Contains(item.Context, "list_stores confirmed users relations=[users_orders(key->users.key)]")
			}
			if item.Category == askOutcomeMRUCategoryToolPattern {
				foundAskOutcomePattern = strings.Contains(item.Context, "Tool pattern: list_stores -> execute_script")
			}
		}
	}
	if !foundAskOutcomeQuery || !foundAskOutcomeResult || !foundAskOutcomeSchema || !foundAskOutcomeRelations || !foundAskOutcomePattern {
		t.Fatalf("expected epilogue to persist compact ask outcome into MRU snapshot, got %+v", snapshot)
	}
}

func TestEpilogueAndCleanup_MergesRecipeSnapshotsAcrossAsks(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ag.service = &Service{session: NewRunnerSession(), EnableShortTermMemory: false}
	ag.service.session.Memory.SetRecipeSnapshot([]RecipeItem{{
		ID:         "implicit.execute_script.research_then_retry",
		Kind:       RecipeKindImplicit,
		Scope:      RecipeScopeMicro,
		Domain:     StoresDomain,
		Topic:      "Research grounded schema before execute_script retry",
		Trigger:    "execute_script repair requires missing schema or relation facts",
		Protocol:   []string{"Call list_stores first to confirm schema and relations."},
		Invariants: []string{"Preserve valid script slices."},
		Confidence: 0.75,
		Source:     "inner_loop_success",
	}})

	ag.epilogueAndCleanup(context.Background(), "Find orders", "Omni", "Done", []ai.ToolCall{{Name: "execute_script"}}, []string{"execute_script returned: []"}, []ai.LearnedRecipe{{
		ID:         "implicit.execute_script.repair_in_place",
		Kind:       RecipeKindImplicit,
		Scope:      RecipeScopeMicro,
		Domain:     StoresDomain,
		Topic:      "Repair execute_script in place",
		Trigger:    "execute_script has a recoverable argument-shape error",
		Protocol:   []string{"Retry the same tool instead of abandoning the plan."},
		Invariants: []string{"Do not switch to unrelated tools before the repair attempt."},
		Confidence: 0.9,
		Source:     "inner_loop_success",
	}}, nil)

	recipeSnapshot := ag.service.session.Memory.GetRecipeSnapshot()
	if len(recipeSnapshot) != 2 {
		t.Fatalf("expected epilogue to merge recipe snapshots across asks, got %+v", recipeSnapshot)
	}
	if recipeSnapshot[0].ID != "implicit.execute_script.repair_in_place" || recipeSnapshot[1].ID != "implicit.execute_script.research_then_retry" {
		t.Fatalf("expected merged recipe snapshot ordered by confidence, got %+v", recipeSnapshot)
	}
}

func TestSystemPromptBuilder_BudgetReportTracksTrimmedComponents(t *testing.T) {
	builder := NewSystemPromptBuilder().
		With(ComponentSystemTools, strings.Repeat("TOOL_RULE ", 200)).
		With(ComponentHistory, strings.Repeat("history ", 200)).
		With(ComponentUserQuery, "User: inspect users")

	prompt, report := builder.ToJSONWithBudgetReport(PromptBudgetProfile{
		TotalChars: 1200,
		ComponentCharBudgets: map[PromptComponent]int{
			ComponentSystemTools: 500,
			ComponentHistory:     300,
			ComponentUserQuery:   200,
		},
		TrimPriorityLowToHigh: []PromptComponent{
			ComponentHistory,
			ComponentSystemTools,
			ComponentUserQuery,
		},
	})

	if prompt == "" || prompt == "[]" {
		t.Fatalf("expected rendered prompt output")
	}
	if report.OriginalTotalChars <= report.FinalTotalChars {
		t.Fatalf("expected report to show trimming, got original=%d final=%d", report.OriginalTotalChars, report.FinalTotalChars)
	}
	trimmed := report.TrimmedComponents()
	if len(trimmed) < 2 {
		t.Fatalf("expected multiple trimmed components, got %#v", trimmed)
	}
	if trimmed[0].OriginalChars <= trimmed[0].FinalChars {
		t.Fatalf("expected trimmed stat to shrink chars, got %#v", trimmed[0])
	}
	if !strings.Contains(prompt, "[truncated]") {
		t.Fatalf("expected prompt output to include truncation marker: %s", prompt)
	}
	if summary := summarizePromptBudgetTrim(report); !strings.Contains(summary, "system_tools") || !strings.Contains(summary, "conversation_history") {
		t.Fatalf("expected trim summary to mention reduced components, got %q", summary)
	}
	if present := summarizePromptComponentsPresent(report); !strings.Contains(present, "system_tools") || !strings.Contains(present, "user_query") {
		t.Fatalf("expected present-components summary to mention retained components, got %q", present)
	}
}

func TestSummarizeProjectedMRU_IncludesSourceAndCategory(t *testing.T) {
	summary := summarizeProjectedMRU([]MRUItem{
		{Category: "PERSONA_omni", Source: MRUSourcePersona},
		{Category: SYSTEM_TOOLS, Source: MRUSourceSystemTools},
		{Category: playbookMRUCategory("sop"), Source: MRUSourcePlaybook},
	})
	if !strings.Contains(summary, "persona:PERSONA_omni") || !strings.Contains(summary, "system_tools:System_Tools") || !strings.Contains(summary, "playbook:PLAYBOOK_sop") {
		t.Fatalf("unexpected projected MRU summary: %q", summary)
	}
}
