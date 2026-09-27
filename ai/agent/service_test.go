package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/obfuscation"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// MockGenerator implements ai.Generator for testing.
type MockGenerator struct {
	Response string
}

func (m *MockGenerator) Name() string {
	return "mock"
}

func (m *MockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	// For testing native tool calls since we moved away from JSON regex parsing
	var tc []ai.ToolCall

	// Obfuscation test match:
	// dbHash might be anything since it's md5. We just examine m.Response to see if it looks like the obfuscation test responses.
	if strings.Contains(m.Response, `{"tool": "select", "args": {"database": "`) && strings.Contains(m.Response, `"store": "`) {
		// Mock decoding the string back (test logic will check the obfuscated payload behavior)
		// For the purpose of the test, we mock what the LLM *would* have returned as ToolCalls natively:

		// Extract dbhash and storehash manually from m.Response since it's dynamic
		// Very brittle, but it's a test mock.
		dbStart := strings.Index(m.Response, `"database": "`) + 13
		dbEnd := strings.Index(m.Response[dbStart:], `"`) + dbStart
		storeStart := strings.Index(m.Response, `"store": "`) + 10
		storeEnd := strings.Index(m.Response[storeStart:], `"`) + storeStart

		tc = append(tc, ai.ToolCall{
			Name: "select",
			Args: map[string]any{
				"database": strings.ReplaceAll(m.Response[dbStart:dbEnd], `\u00a0`, "\u00a0"),
				"store":    m.Response[storeStart:storeEnd],
			},
		})
	}

	return ai.GenOutput{
		Text:      m.Response,
		ToolCalls: tc,
	}, nil
}

func (m *MockGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func (m *MockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

// MockEmbedder implements ai.Embeddings for testing.
type MockEmbedder struct{}

func (m *MockEmbedder) Name() string { return "mock" }
func (m *MockEmbedder) Dim() int     { return 1536 }
func (m *MockEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

// MockDomain implements ai.Domain for testing.
type MockDomain struct{}

func (m *MockDomain) ID() string              { return "mock" }
func (m *MockDomain) Name() string            { return "Mock Domain" }
func (m *MockDomain) Embedder() ai.Embeddings { return &MockEmbedder{} }
func (m *MockDomain) Index(ctx context.Context, tx sop.Transaction) (ai.VectorStore[map[string]any], error) {
	return &MockVectorStore{}, nil
}
func (m *MockDomain) TextIndex(ctx context.Context, tx sop.Transaction) (ai.TextIndex, error) {
	return nil, nil
}
func (m *MockDomain) BeginTransaction(ctx context.Context, mode sop.TransactionMode) (sop.Transaction, error) {
	return &MockTransaction{}, nil
}
func (m *MockDomain) Policies() ai.PolicyEngine { return nil }
func (m *MockDomain) Classifier() ai.Classifier { return nil }
func (m *MockDomain) Prompt(ctx context.Context, kind string) (string, error) {
	return "System Prompt", nil
}
func (m *MockDomain) Memory(ctx context.Context, tx sop.Transaction) (any, error) { return nil, nil }
func (m *MockDomain) DataPath() string                                            { return "/tmp" }

// MockTransaction implements sop.Transaction for testing.
type MockTransaction struct{}

func (m *MockTransaction) Begin(ctx context.Context) error                                        { return nil }
func (m *MockTransaction) Commit(ctx context.Context) error                                       { return nil }
func (m *MockTransaction) Rollback(ctx context.Context) error                                     { return nil }
func (m *MockTransaction) HasBegun() bool                                                         { return true }
func (m *MockTransaction) GetPhasedTransaction() sop.TwoPhaseCommitTransaction                    { return nil }
func (m *MockTransaction) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {}
func (m *MockTransaction) GetStores(ctx context.Context) ([]string, error)                        { return nil, nil }
func (m *MockTransaction) Close() error                                                           { return nil }
func (m *MockTransaction) GetID() sop.UUID                                                        { return sop.UUID{} }
func (m *MockTransaction) CommitMaxDuration() time.Duration                                       { return 0 }
func (m *MockTransaction) OnCommit(callback func(ctx context.Context) error)                      {}

// MockVectorStore implements ai.VectorStore for testing.
type MockVectorStore struct{}

func (m *MockVectorStore) UpdateEmbedderInfo(ctx context.Context, provider string, model string, dimensions int) error {
	return nil
}

func (m *MockVectorStore) Upsert(ctx context.Context, item ai.Item[map[string]any]) error { return nil }
func (m *MockVectorStore) UpsertBatch(ctx context.Context, items []ai.Item[map[string]any]) error {
	return nil
}
func (m *MockVectorStore) Get(ctx context.Context, id string) (*ai.Item[map[string]any], error) {
	return nil, nil
}
func (m *MockVectorStore) Delete(ctx context.Context, id string) error { return nil }
func (m *MockVectorStore) Query(ctx context.Context, vec []float32, k int, filter func(map[string]any) bool) ([]ai.Hit[map[string]any], error) {
	return nil, nil
}
func (m *MockVectorStore) Count(ctx context.Context) (int64, error)                    { return 0, nil }
func (m *MockVectorStore) AddCentroid(ctx context.Context, vec []float32) (int, error) { return 0, nil }
func (m *MockVectorStore) Optimize(ctx context.Context) error                          { return nil }
func (m *MockVectorStore) Consolidate(ctx context.Context) error                       { return nil }
func (m *MockVectorStore) SetDeduplication(enabled bool)                               {}
func (m *MockVectorStore) Centroids(ctx context.Context) (btree.BtreeInterface[int, ai.Centroid], error) {
	return nil, nil
}
func (m *MockVectorStore) Vectors(ctx context.Context) (btree.BtreeInterface[ai.VectorKey, []float32], error) {
	return nil, nil
}
func (m *MockVectorStore) Content(ctx context.Context) (btree.BtreeInterface[ai.ContentKey, string], error) {
	return nil, nil
}
func (m *MockVectorStore) Lookup(ctx context.Context) (btree.BtreeInterface[int, string], error) {
	return nil, nil
}
func (m *MockVectorStore) Version(ctx context.Context) (int64, error) { return 0, nil }

// MockExecutor implements ai.ToolExecutor for testing.
type MockExecutor struct {
	LastTool string
	LastArgs map[string]any
}

type listToolsTestAgent struct {
	tools []ai.ToolDefinition
}

func (a *listToolsTestAgent) Open(ctx context.Context) error  { return nil }
func (a *listToolsTestAgent) Close(ctx context.Context) error { return nil }
func (a *listToolsTestAgent) Search(ctx context.Context, query string, limit int) ([]ai.Hit[map[string]any], error) {
	return nil, nil
}
func (a *listToolsTestAgent) Ask(ctx context.Context, query string, cfg *ai.ConfigMap) (string, error) {
	return "", nil
}
func (a *listToolsTestAgent) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", nil
}
func (a *listToolsTestAgent) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return a.tools, nil
}

func (e *MockExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	e.LastTool = toolName
	e.LastArgs = args
	return "Executed", nil
}

func (e *MockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return nil, nil
}

func TestServiceToolExecutor_ListTools_ReflectsRegisteredAgents(t *testing.T) {
	expected := []ai.ToolDefinition{{Name: "alpha", Description: "alpha tool", Schema: `{"type":"object"}`}}
	svc := NewService(nil, nil, nil, nil, nil, map[string]ai.Agent[map[string]any]{
		"test": &listToolsTestAgent{tools: expected},
	}, false)

	tools, err := (&ServiceToolExecutor{s: svc}).ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != expected[0].Name {
		t.Fatalf("expected registered tool list to be returned, got %#v", tools)
	}
}

func TestPerformTopicRouting_WithNilSession_DoesNotPanic(t *testing.T) {
	svc := &Service{EnableHistoryInjection: true}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("performTopicRouting panicked with nil session: %v", r)
		}
	}()

	result := svc.performTopicRouting(context.Background(), "hello there")
	if result == nil {
		t.Fatal("performTopicRouting returned nil result")
	}
	if !result.isNewTopic {
		t.Fatal("expected topic routing to fall back to a new topic when session is unavailable")
	}
}

func TestIdentifyTopic_NilGeneratorFallsBackWithoutPanic(t *testing.T) {
	svc := &Service{session: NewRunnerSession()}
	svc.session.Memory = NewShortTermMemory()
	svc.session.Memory.AddThread(&ConversationThread{
		ID:     sop.NewUUID(),
		Label:  "Existing Topic",
		Status: "open",
		Exchanges: []Interaction{{
			Role:    RoleUser,
			Content: "previous question",
		}},
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("identifyTopic panicked with nil generator: %v", r)
		}
	}()

	assessment, err := svc.identifyTopic(context.Background(), "what about the other one?")
	if err != nil {
		t.Fatalf("identifyTopic returned error: %v", err)
	}
	if assessment == nil {
		t.Fatal("identifyTopic returned nil assessment")
	}
	if !assessment.IsNewTopic {
		t.Fatal("expected fallback classification to choose a new topic when no generator is available")
	}
}

func TestServiceToolExecutor_DoesNotInjectLegacyVerboseFallback(t *testing.T) {
	executor := &ServiceToolExecutor{}
	ctx := executor.injectToolContextToLegacyContext(context.Background(), &ToolExecutionContext{
		Session: &ai.SessionPayload{},
	})

	if v := ctx.Value("verbose"); v != nil {
		t.Fatalf("expected no legacy verbose fallback to be injected, got %#v", v)
	}
}

type eventAwareAgent struct{}

func (a *eventAwareAgent) Open(ctx context.Context) error  { return nil }
func (a *eventAwareAgent) Close(ctx context.Context) error { return nil }
func (a *eventAwareAgent) Search(ctx context.Context, query string, limit int) ([]ai.Hit[map[string]any], error) {
	return nil, nil
}
func (a *eventAwareAgent) Ask(ctx context.Context, query string, cfg *ai.ConfigMap) (string, error) {
	return "", nil
}
func (a *eventAwareAgent) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if _, ok := ctx.Value(ai.CtxKeyEventStreamer).(func(string, any)); !ok {
		return "", fmt.Errorf("expected event streamer to be preserved in context")
	}
	return "ok", nil
}

func TestServiceToolExecutor_Execute_PreservesEventStreamer(t *testing.T) {

	eventStreamer := func(eventType string, payload any) {}
	ctx := context.WithValue(context.Background(), ai.CtxKeyEventStreamer, eventStreamer)
	ctx = context.WithValue(ctx, ai.CtxKeyExecutor, &MockExecutor{})

	svc := &Service{registry: map[string]ai.Agent[map[string]any]{"event_agent": &eventAwareAgent{}}}
	_, err := (&ServiceToolExecutor{s: svc}).Execute(ctx, "demo", map[string]any{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
}

func TestIsVerboseEnabled_UsesRunnerSessionPointer(t *testing.T) {
	ctx := context.WithValue(context.Background(), RunnerSessionKey, &RunnerSession{Verbose: true})

	if !isVerboseEnabled(ctx) {
		t.Fatal("expected verbose to resolve from the runner session pointer")
	}
}

type verboseRuntimeGenerator struct {
	calls int
}

func (m *verboseRuntimeGenerator) Name() string { return "verbose_runtime_mock" }

func (m *verboseRuntimeGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *verboseRuntimeGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *verboseRuntimeGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{Name: "echo_verbose", Args: map[string]any{}}}}, nil
	}
	return ai.GenOutput{Text: "Final answer: captured verbose state."}, nil
}

type verboseRuntimeExecutor struct {
	seen []bool
}

func (e *verboseRuntimeExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	verbose := effectiveVerbose(ctx)
	e.seen = append(e.seen, verbose)
	if verbose {
		return "verbose_on", nil
	}
	return "verbose_off", nil
}

func (e *verboseRuntimeExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "echo_verbose"}}, nil
}

func TestServiceAsk_SeedsNewSessionPayloadFromPersistedVerbose(t *testing.T) {
	svc := NewService(&MockDomain{}, nil, nil, nil, nil, nil, false)
	svc.session.Verbose = true
	gen := &verboseRuntimeGenerator{}
	exec := &verboseRuntimeExecutor{}

	_, err := svc.ask(context.Background(), AskRequest{
		Query:     "check verbose runtime",
		Generator: gen,
		Executor:  exec,
	})
	if err != nil {
		t.Fatalf("ask failed: %v", err)
	}
	if !svc.session.IsVerbose() {
		t.Fatalf("expected ask-created session to inherit persisted verbose, got %#v", svc.session)
	}
	if len(exec.seen) != 1 || !exec.seen[0] {
		t.Fatalf("expected tool runtime to see persisted verbose=true, got %#v", exec.seen)
	}
}

func TestServiceAsk_ReusesRunnerSessionVerboseForNextRequest(t *testing.T) {
	svc := NewService(&MockDomain{}, nil, nil, nil, nil, nil, false)
	svc.session.SetVerbose(true)
	firstGen := &verboseRuntimeGenerator{}
	firstExec := &verboseRuntimeExecutor{}
	firstCfg := ai.NewConfigMap()
	firstCfg.Set("payload", &ai.SessionPayload{})
	firstCfg.Set("generator", firstGen)
	firstCfg.Set("executor", firstExec)

	if _, err := svc.Ask(context.Background(), "turn verbose on", firstCfg); err != nil {
		t.Fatalf("first Ask failed: %v", err)
	}
	if !svc.session.IsVerbose() {
		t.Fatal("expected service session to keep verbose=true for the next runtime step")
	}

	secondGen := &verboseRuntimeGenerator{}
	secondExec := &verboseRuntimeExecutor{}
	secondCfg := ai.NewConfigMap()
	secondCfg.Set("generator", secondGen)
	secondCfg.Set("executor", secondExec)

	if _, err := svc.Ask(context.Background(), "reuse verbose runtime", secondCfg); err != nil {
		t.Fatalf("second Ask failed: %v", err)
	}
	if len(secondExec.seen) != 1 || !secondExec.seen[0] {
		t.Fatalf("expected follow-up Ask to reuse persisted verbose=true, got %#v", secondExec.seen)
	}
}

func TestService_Ask_Obfuscation(t *testing.T) {
	// Setup Obfuscator
	obfuscation.GlobalObfuscator = obfuscation.NewMetadataObfuscator()
	obfuscation.GlobalObfuscator.RegisterResource("Python Complex DB", "DB")
	obfuscation.GlobalObfuscator.RegisterResource("My Store", "STORE")

	// Get the hashes
	dbHash := obfuscation.GlobalObfuscator.Obfuscate("Python Complex DB", "DB")
	storeHash := obfuscation.GlobalObfuscator.Obfuscate("My Store", "STORE")

	tests := []struct {
		name           string
		llmResponse    string
		expectedResult string // For text response
		expectedDB     string // For tool call
		expectedStore  string // For tool call
		isTool         bool
	}{
		{
			name:           "Normal Text Response",
			llmResponse:    "I found " + dbHash + " and " + storeHash,
			expectedResult: "I found Python Complex DB and My Store",
			isTool:         false,
		},
		{
			name:           "Markdown Bold Response",
			llmResponse:    "I found **" + dbHash + "** and _" + storeHash + "_",
			expectedResult: "I found **Python Complex DB** and _My Store_",
			isTool:         false,
		},
		{
			name:          "Tool Call Clean",
			llmResponse:   `{"tool": "select", "args": {"database": "` + dbHash + `", "store": "` + storeHash + `"}}`,
			expectedDB:    "Python Complex DB",
			expectedStore: "My Store",
			isTool:        true,
		},
		{
			name:          "Tool Call with Markdown Artifacts",
			llmResponse:   `{"tool": "select", "args": {"database": "**` + dbHash + `**", "store": "_` + storeHash + `_"}}`,
			expectedDB:    "Python Complex DB",
			expectedStore: "My Store",
			isTool:        true,
		},
		{
			name:          "Tool Call with NBSP",
			llmResponse:   `{"tool": "select", "args": {"database": "` + dbHash + `\u00a0", "store": "` + storeHash + `"}}`,
			expectedDB:    "Python Complex DB",
			expectedStore: "My Store",
			isTool:        true,
		},
		{
			name:          "Tool Call with Real Name (Leak) + Artifacts",
			llmResponse:   `{"tool": "select", "args": {"database": "**Python Complex DB**", "store": "My Store"}}`,
			expectedDB:    "Python Complex DB",
			expectedStore: "My Store",
			isTool:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen := &MockGenerator{Response: tt.llmResponse}
			// Enable obfuscation for tests
			svc := NewService(&MockDomain{}, nil, nil, gen, nil, nil, true)
			executor := &MockExecutor{}

			ctx := context.WithValue(context.Background(), ai.CtxKeyExecutor, executor)

			result, err := svc.Ask(ctx, "query", nil)
			if err != nil {
				t.Fatalf("Ask failed: %v", err)
			}

			if tt.isTool {
				// For tool calls, Ask returns the JSON string.
				// We need to parse it to verify the args were cleaned.
				// OR, if we move execution into Ask, we verify the executor was called.
				// Currently Ask returns the JSON string.

				// But wait, the current implementation in Service.Ask just does string replacement on the JSON.
				// It does NOT parse the JSON to clean specific fields.
				// So if the LLM returns `**DB_1**`, string replacement turns it into `**Python Complex DB**`.
				// It does NOT remove the `**`.
				// The User wants Copilot (Service) to handle this "woe".
				// So we expect the JSON returned by Ask to contain CLEAN names, without artifacts.

				// Let's see what we get currently.
				t.Logf("Result: %s", result)

				// If we want to verify the "robustness", we should check if the result contains the CLEAN name
				// AND if it's free of artifacts if we implement that logic.

				// Check captured args
				dbArg, ok := executor.LastArgs["database"].(string)
				if !ok {
					// Some tests might not have database arg, check if we expect it
					if tt.expectedDB != "" {
						t.Errorf("Expected 'database' arg to be string, got %T", executor.LastArgs["database"])
					}
				} else {
					if dbArg != tt.expectedDB {
						t.Errorf("Expected database arg '%s', got '%s'", tt.expectedDB, dbArg)
					}
					if strings.Contains(dbArg, "**") || strings.Contains(dbArg, "`") {
						t.Errorf("Database arg still contains artifacts: %s", dbArg)
					}
				}
			} else {
				if result != tt.expectedResult {
					t.Errorf("Expected '%s', got '%s'", tt.expectedResult, result)
				}
			}
		})
	}
}

func TestService_Ask_NoObfuscation(t *testing.T) {
	// Setup
	mockGen := &MockGenerator{Response: "Some response with DB_123"}
	mockDomain := &MockDomain{}

	// Create Service with Obfuscation DISABLED
	svc := NewService(mockDomain, nil, nil, mockGen, nil, nil, false)

	ctx := context.Background()

	// Execute
	result, err := svc.Ask(ctx, "query", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	// Verify
	// Since obfuscation is disabled, the output should be exactly what the generator returned
	if result != "Some response with DB_123" {
		t.Errorf("Expected raw response 'Some response with DB_123', got '%s'", result)
	}
}

// SplitCentroid mocks the base method.
func (m *MockVectorStore) SplitCentroid(ctx context.Context, centroidID int) error {
	return nil
}
