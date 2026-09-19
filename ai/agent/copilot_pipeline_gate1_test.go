package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

type gate1MockGen struct {
	CapturedPrompt string
	Response       string
}

func (m *gate1MockGen) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.CapturedPrompt = opts.SystemPrompt
	return ai.GenOutput{Text: m.Response}, nil
}
func (m *gate1MockGen) Name() string                                 { return "gate1_mock" }
func (m *gate1MockGen) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *gate1MockGen) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func TestGate1_PartialPrefixHandling(t *testing.T) {
	ctx := context.Background()
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)

	// Seed some sample data so the classifier can pull it
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	sysDB.NewBtree(ctx, "TestStoreAlpha", tx)
	sysDB.NewBtree(ctx, "TestStoreBeta", tx)
	tx.Commit(ctx)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{}, sysDB)

	// Create mock string
	expectedResponse := `{"entity": "Omni", "domain": "Stores", "db_artifacts": ["TestStoreAlpha"], "layers": []}`

	tests := []struct {
		name           string
		query          string
		expectedPrompt []string // strings that must be present in the prompt
	}{
		{
			name:  "Missing Domain And Artifact",
			query: "Omni: ", // Empty right side
			expectedPrompt: []string{
				"Available Artifact Samples",
				"Stores:",
				"TestStoreAlpha",
			},
		},
		{
			name:  "Missing Artifact Only",
			query: "Omni:Stores:",
			expectedPrompt: []string{
				"Available Artifact Samples",
				"TestStoreAlpha",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &gate1MockGen{Response: expectedResponse}

			parts := strings.Split(tc.query, ":")
			domain := ""
			if len(parts) > 1 {
				domain = strings.TrimSpace(parts[1])
			}

			_, err := ag.ClassifyFocusedTaskContext(ctx, tc.query, "Omni", domain, "", mock)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, exp := range tc.expectedPrompt {
				if !strings.Contains(mock.CapturedPrompt, exp) {
					t.Errorf("Expected prompt to contain '%s', but got: %s", exp, mock.CapturedPrompt)
				}
			}
		})
	}
}

func TestClassifyFocusedTaskContext_EnforcesExplicitConstraints(t *testing.T) {
	ctx := context.Background()
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, nil)
	mock := &gate1MockGen{Response: `{"entity":"Other","domain":"Spaces","db_artifacts":["kb_docs"],"stores_artifacts":[],"spaces_artifacts":["kb_docs"],"layers":[{"name":"Cross-Domain","crud":["C","R"]}]}`}

	taskCtx, err := ag.ClassifyFocusedTaskContext(ctx, "Omni:Stores:users", "Omni", "Stores", "users", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if taskCtx.Entity != "Omni" {
		t.Fatalf("expected entity Omni, got %q", taskCtx.Entity)
	}
	if taskCtx.Domain != StoresDomain {
		t.Fatalf("expected forced domain %q, got %q", StoresDomain, taskCtx.Domain)
	}
	if len(taskCtx.DBArtifacts) != 1 || taskCtx.DBArtifacts[0] != "users" {
		t.Fatalf("expected forced db artifact users, got %#v", taskCtx.DBArtifacts)
	}
	if len(taskCtx.StoresArtifacts) != 1 || taskCtx.StoresArtifacts[0] != "users" {
		t.Fatalf("expected forced stores artifact users, got %#v", taskCtx.StoresArtifacts)
	}
	if len(taskCtx.SpacesArtifacts) != 0 {
		t.Fatalf("expected spaces artifacts to be cleared by forced Stores constraint, got %#v", taskCtx.SpacesArtifacts)
	}
	if hasLayer(taskCtx.Layers, "Cross-Domain") {
		t.Fatalf("expected Cross-Domain layer to be removed under explicit single-domain constraint, got %#v", taskCtx.Layers)
	}
}

func TestLooksLikeSpecializedRoutingQuery_RecognizesSOPPrefixes(t *testing.T) {
	if !looksLikeSpecializedRoutingQuery("omni:sop") {
		t.Fatal("expected omni:sop (root KB query) to be recognized as specialized")
	}
	if !looksLikeSpecializedRoutingQuery("omni:sop:language:c# tutorial") {
		t.Fatal("expected SOP-style query to be recognized as specialized")
	}
	if !looksLikeSpecializedRoutingQuery("omni:medical:skin diseases") {
		t.Fatal("expected Omni custom-KB query to be recognized as specialized")
	}
	if !looksLikeSpecializedRoutingQuery("sop") {
		t.Fatal("expected sop (KB name alone) to be recognized as specialized")
	}
	if !looksLikeSpecializedRoutingQuery("sop:category") {
		t.Fatal("expected sop:category to be recognized as specialized")
	}
	if !looksLikeSpecializedRoutingQuery("sop:cat1:cat2") {
		t.Fatal("expected sop:cat1:cat2 to be recognized as specialized")
	}
	if looksLikeSpecializedRoutingQuery("just a regular ask") {
		t.Fatal("expected plain ask to stay outside specialized routing")
	}
}

func TestRoutingKBName_UsesOmniTargetKB(t *testing.T) {
	if got := routingKBName("omni:medical:skin diseases"); got != "medical" {
		t.Fatalf("routingKBName() = %q, want %q", got, "medical")
	}
}

func TestShouldRouteToOmniInsteadOfAvatar_ForKBStyleShorthand(t *testing.T) {
	if !shouldRouteToOmniInsteadOfAvatar("sop:language bindings:c#") {
		t.Fatal("expected shorthand KB query to be treated as Omni-focused routing")
	}
	if !shouldRouteToOmniInsteadOfAvatar("omni:sop:language bindings:c#") {
		t.Fatal("expected omni-prefixed SOP routing to be treated as Omni-focused routing")
	}
	if shouldRouteToOmniInsteadOfAvatar("omni:medical:skin diseases") {
		t.Fatal("expected non-SOP KB routing to remain on the normal path")
	}
	if shouldRouteToOmniInsteadOfAvatar("Please summarize the architecture") {
		t.Fatal("expected a general ask to remain on the normal routing path")
	}
}

func TestTrySpecializedFocusedRouting_ShortCircuitsSOPStyleQuery(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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
	if err := kb.IngestThought(ctx, "C# lambda expressions tutorial", "language/c#/tutorial", "Omni", nil, map[string]any{"description": "lambda tutorial"}); err != nil {
		t.Fatalf("IngestThought failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, "omni:sop:language:c# tutorial", "Omni", "Spaces", "")
	if err != nil {
		t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
	}
	if !handled {
		t.Fatal("expected specialized focused routing to handle the SOP-style query")
	}
	if taskCtx == nil {
		t.Fatal("expected a focused task context")
	}
	if taskCtx.RoutingGate != RoutingGateFocused {
		t.Fatalf("expected focused routing gate, got %q", taskCtx.RoutingGate)
	}
	if !hasLayer(taskCtx.Layers, "KBRoute") {
		t.Fatalf("expected KBRoute layer to be attached, got %+v", taskCtx.Layers)
	}
}

func TestEvaluateRoutingGates_HandlesBareSopQuery(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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
	if err := kb.IngestThought(ctx, "Getting Started", "installation/setup", "Omni", nil, map[string]any{"description": "setup guide"}); err != nil {
		t.Fatalf("IngestThought failed: %v", err)
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

	tests := []struct {
		name                string
		query               string
		expectDirectDisplay bool
	}{
		{"bare sop", "sop", true},
		{"sop with colon", "sop:", true},
		{"sop with category", "sop:installation", false}, // No results, so not direct display
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			taskCtx, err := ag.evaluateRoutingGates(ctx, tc.query, nil)
			if err != nil {
				t.Fatalf("evaluateRoutingGates failed: %v", err)
			}
			if taskCtx == nil {
				t.Fatal("expected a routing classification for sop query")
			}
			if taskCtx.RoutingGate != RoutingGateFocused {
				t.Fatalf("expected specialized focused routing gate, got %q", taskCtx.RoutingGate)
			}
			if !hasLayer(taskCtx.Layers, "KBRoute") {
				t.Fatalf("expected KBRoute layer to be attached, got %+v", taskCtx.Layers)
			}
			if taskCtx.DirectDisplay != tc.expectDirectDisplay {
				t.Fatalf("expected direct display=%v, got %v", tc.expectDirectDisplay, taskCtx.DirectDisplay)
			}
		})
	}
}

func TestTrySpecializedFocusedRouting_SupportsWhitespaceAroundLLM(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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
	if err := kb.IngestThought(ctx, "C# lambda expressions tutorial", "language/c#/tutorial", "Omni", nil, map[string]any{"description": "lambda tutorial"}); err != nil {
		t.Fatalf("IngestThought failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, "omni:sop:language/c#/tutorial: llm summarize", "Omni", "Spaces", "")
	if err != nil {
		t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
	}
	if !handled {
		t.Fatal("expected specialized focused routing to handle the llm-suffixed SOP-style query")
	}
	if taskCtx == nil {
		t.Fatal("expected a focused task context")
	}
	if !hasLayer(taskCtx.Layers, "LLMFilter") {
		t.Fatalf("expected LLMFilter layer to be attached, got %+v", taskCtx.Layers)
	}
	if len(taskCtx.SpacesArtifacts) == 0 || taskCtx.SpacesArtifacts[0] != "language/c#/tutorial" {
		t.Fatalf("expected category path to be preserved in spaces artifacts, got %+v", taskCtx.SpacesArtifacts)
	}
	// Verify CleanQuery field is set (without :llm instruction)
	if taskCtx.CleanQuery != "language/c#/tutorial" {
		t.Fatalf("expected CleanQuery to be 'language/c#/tutorial', got %q", taskCtx.CleanQuery)
	}
	// Verify LLMInstruction field is set
	if taskCtx.LLMInstruction != "summarize" {
		t.Fatalf("expected LLMInstruction to be 'summarize', got %q", taskCtx.LLMInstruction)
	}
}

func TestTrySpecializedFocusedRouting_StripsLLMInstructionWithoutCategoryPath(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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
	taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, "omni:sop:performance tips:llm summarize the top 5", "Omni", "Spaces", "")
	if err != nil {
		t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
	}
	if !handled {
		t.Fatal("expected specialized focused routing to handle the query with :llm")
	}
	if taskCtx == nil {
		t.Fatal("expected a focused task context")
	}
	// Verify CleanQuery has :llm instruction stripped out
	if taskCtx.CleanQuery != "performance tips" {
		t.Fatalf("expected CleanQuery to be 'performance tips' (without :llm), got %q", taskCtx.CleanQuery)
	}
	// Verify LLMInstruction field is set
	if taskCtx.LLMInstruction != "summarize the top 5" {
		t.Fatalf("expected LLMInstruction to be 'summarize the top 5', got %q", taskCtx.LLMInstruction)
	}
	// Should be in LLM mode
	if !hasLayer(taskCtx.Layers, "LLMFilter") {
		t.Fatalf("expected LLMFilter layer for :llm instruction, got %+v", taskCtx.Layers)
	}
}

func TestExtractPageNumber_SupportsColonAndSlashSeparators(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantQuery string
		wantPage  int
	}{
		{name: "colon separator", query: "omni:sop:page:3", wantQuery: "omni:sop", wantPage: 3},
		{name: "slash separator", query: "omni:sop/page/2", wantQuery: "omni:sop", wantPage: 2},
		{name: "mixed path with slash page", query: "omni:sop:language/page/4", wantQuery: "omni:sop:language", wantPage: 4},
		{name: "defaults when no page suffix", query: "omni:sop:language", wantQuery: "omni:sop:language", wantPage: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotQuery, gotPage := extractPageNumber(tc.query)
			if gotQuery != tc.wantQuery {
				t.Fatalf("extractPageNumber(%q) query = %q, want %q", tc.query, gotQuery, tc.wantQuery)
			}
			if gotPage != tc.wantPage {
				t.Fatalf("extractPageNumber(%q) page = %d, want %d", tc.query, gotPage, tc.wantPage)
			}
		})
	}
}

func TestGetSubcategories_PaginatesAndReportsPageCount(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	kb, err := sysDB.OpenKnowledgeBase(ctx, "demo", tx, nil, nil, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	for i := 1; i <= 25; i++ {
		cat := &memory.Category{
			ID:        sop.NewUUID(),
			Name:      fmt.Sprintf("Cat%02d", i),
			Path:      fmt.Sprintf("cat%02d", i),
			ItemCount: i,
		}
		if _, err := kb.Store.AddCategory(ctx, cat); err != nil {
			t.Fatalf("AddCategory(%d) failed: %v", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	text, err := ag.getSubcategories(ctx, sysDB, "demo", "", 2)
	if err != nil {
		t.Fatalf("getSubcategories failed: %v", err)
	}

	if !strings.Contains(text, "Page 2 of 2") {
		t.Fatalf("expected pagination header to report total pages, got: %s", text)
	}
	if !strings.Contains(text, "showing 21-25 of 25") {
		t.Fatalf("expected page range to show 21-25 of 25, got: %s", text)
	}
	if !strings.Contains(text, "Previous: omni:demo:page:1") {
		t.Fatalf("expected previous page hint, got: %s", text)
	}
}

func TestTrySpecializedFocusedRouting_ShowsRootCategoriesForOmniKBQuery(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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

	// Add some root categories
	for _, name := range []string{"Language", "Operations", "Architecture"} {
		cat := &memory.Category{
			ID:        sop.NewUUID(),
			Name:      name,
			Path:      strings.ToLower(name),
			ItemCount: 10,
		}
		if _, err := kb.Store.AddCategory(ctx, cat); err != nil {
			t.Fatalf("AddCategory(%s) failed: %v", name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)

	testCases := []struct {
		name  string
		query string
	}{
		{name: "omni:KB pattern", query: "omni:sop"},
		{name: "direct KB name", query: "sop"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, tc.query, "Omni", "Spaces", "")
			if err != nil {
				t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
			}
			if !handled {
				t.Fatalf("expected specialized focused routing to handle %q query", tc.query)
			}
			if taskCtx == nil {
				t.Fatal("expected a focused task context")
			}
			if !hasLayer(taskCtx.Layers, "KBRoute") {
				t.Fatalf("expected KBRoute layer to be attached, got %+v", taskCtx.Layers)
			}
			// Should be direct display (subcategories/root categories)
			if !taskCtx.DirectDisplay {
				t.Fatal("expected DirectDisplay to be true for root category navigation")
			}
		})
	}
}

func TestTrySpecializedFocusedRouting_SupportsDirectSOPPathQueries(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

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

	testCases := []struct {
		name         string
		query        string
		expectedPath string
	}{
		{name: "single level", query: "sop:language", expectedPath: "language"},
		{name: "multi level", query: "sop:language:c#", expectedPath: "language/c#"},
		{name: "deep path", query: "sop:operations:performance:caching", expectedPath: "operations/performance/caching"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, tc.query, "Omni", "Spaces", "")
			if err != nil {
				t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
			}
			if !handled {
				t.Fatalf("expected specialized focused routing to handle %q query", tc.query)
			}
			if taskCtx == nil {
				t.Fatal("expected a focused task context")
			}
			if !hasLayer(taskCtx.Layers, "KBRoute") {
				t.Fatalf("expected KBRoute layer to be attached, got %+v", taskCtx.Layers)
			}
		})
	}
}

func TestFormatKBSourceLinks_RendersEveryDocIDAsClickableLink(t *testing.T) {
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, nil)

	ctx := context.WithValue(context.Background(), ai.CtxKeyAppBaseURL, "https://example.test")
	links := ag.formatKBSourceLinks(ctx, memory.DocIDs{"doc-1", "https://example.test/doc-2"})

	if !strings.Contains(links, "https://example.test/viewer?docID=doc-1") {
		t.Fatalf("expected first doc ID to be rendered as an absolute link, got: %s", links)
	}
	if !strings.Contains(links, "https://example.test/viewer?docID=https%3A%2F%2Fexample.test%2Fdoc-2") {
		t.Fatalf("expected encoded external doc ID to be rendered as an absolute link, got: %s", links)
	}
	if strings.Count(links, "[") != 2 {
		t.Fatalf("expected two clickable links, got: %s", links)
	}
}

func TestFormatKBSearchResultsForDisplay_HandlesCategories(t *testing.T) {
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, nil)

	// Test category navigation output (matchCount = 0)
	categoryResults := "Available Categories: (3 total)\n\n• Language (10 items)\n  Navigate: omni:sop:language"
	output := ag.formatKBSearchResultsForDisplay(categoryResults, 0)

	// Should NOT have "Found 0 knowledge base matches"
	if strings.Contains(output, "Found 0 knowledge base matches") {
		t.Errorf("expected category navigation to not show 'Found 0 matches', got: %s", output)
	}

	// Should contain the category results
	if !strings.Contains(output, "Available Categories:") {
		t.Errorf("expected output to contain category results, got: %s", output)
	}

	// Should use real newlines, not escaped ones
	if strings.Contains(output, "\\n") {
		t.Errorf("expected real newlines, not escaped ones, got: %s", output)
	}

	// Test regular search results output (matchCount > 0)
	searchResults := "CategoryPath: language/go\nScore: 0.95"
	output2 := ag.formatKBSearchResultsForDisplay(searchResults, 3)

	// Should have "Found 3 knowledge base matches"
	if !strings.Contains(output2, "Found 3 knowledge base matches") {
		t.Errorf("expected search results to show match count, got: %s", output2)
	}
}

func TestBuildKBEnrichedQuery_UsesCleanQueryWithoutLLMToken(t *testing.T) {
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, nil)

	// Simulate what happens in the Ask flow
	cleanQuery := "performance tips" // This is what CleanQuery would be set to (without :llm)
	kbResults := "CategoryPath: operations/performance\nScore: 0.95\nDescription: Performance optimization guide"
	llmInstruction := "summarize the top 5"
	matchCount := 1

	enrichedQuery := ag.buildKBEnrichedQuery(cleanQuery, kbResults, llmInstruction, matchCount)

	// Verify the enriched query contains the clean query (without :llm token)
	if !strings.Contains(enrichedQuery, "performance tips") {
		t.Errorf("expected enriched query to contain clean query 'performance tips', got: %s", enrichedQuery)
	}
	// Verify it does NOT contain the :llm token
	if strings.Contains(enrichedQuery, ":llm") {
		t.Errorf("expected enriched query to NOT contain ':llm' token, got: %s", enrichedQuery)
	}
	// Verify the instruction is included separately
	if !strings.Contains(enrichedQuery, "summarize the top 5") {
		t.Errorf("expected enriched query to contain instruction 'summarize the top 5', got: %s", enrichedQuery)
	}
	// Verify KB results are included
	if !strings.Contains(enrichedQuery, "operations/performance") {
		t.Errorf("expected enriched query to contain KB results, got: %s", enrichedQuery)
	}
	// Verify the prompt explicitly tells the model to preserve absolute source links
	if !strings.Contains(enrichedQuery, "Preserve the exact markdown source links") {
		t.Errorf("expected enriched query to preserve source links, got: %s", enrichedQuery)
	}
}

func TestTrySpecializedFocusedRouting_FlexibleHierarchyDepth(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

	tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	kb, err := sysDB.OpenKnowledgeBase(ctx, "myapp", tx, nil, nil, false)
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

	testCases := []struct {
		name          string
		query         string
		expectedClean string
		expectedInstr string
		expectLLMMode bool
	}{
		{
			name:          "deep hierarchy with llm",
			query:         "omni:myapp:cat1:subcat1.1:subsubcat1.1.1:llm summarize performance tips",
			expectedClean: "cat1:subcat1.1:subsubcat1.1.1",
			expectedInstr: "summarize performance tips",
			expectLLMMode: true,
		},
		{
			name:          "medium hierarchy with llm",
			query:         "omni:myapp:cat1:subcat1.1:llm explain in detail",
			expectedClean: "cat1:subcat1.1",
			expectedInstr: "explain in detail",
			expectLLMMode: true,
		},
		{
			name:          "shallow hierarchy with llm",
			query:         "omni:myapp:cat1:llm list the top 5",
			expectedClean: "cat1",
			expectedInstr: "list the top 5",
			expectLLMMode: true,
		},
		{
			name:          "operations path with llm",
			query:         "omni:myapp:operations:performance:caching:llm summarize",
			expectedClean: "operations:performance:caching",
			expectedInstr: "summarize",
			expectLLMMode: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			taskCtx, handled, err := ag.trySpecializedFocusedRouting(ctx, tc.query, "Omni", "Spaces", "")
			if err != nil {
				t.Fatalf("trySpecializedFocusedRouting failed: %v", err)
			}
			if !handled {
				t.Fatal("expected specialized focused routing to handle the query")
			}
			if taskCtx == nil {
				t.Fatal("expected a focused task context")
			}

			// Verify CleanQuery has :llm instruction stripped out
			if taskCtx.CleanQuery != tc.expectedClean {
				t.Errorf("expected CleanQuery to be %q, got %q", tc.expectedClean, taskCtx.CleanQuery)
			}

			// Verify LLMInstruction field is set
			if taskCtx.LLMInstruction != tc.expectedInstr {
				t.Errorf("expected LLMInstruction to be %q, got %q", tc.expectedInstr, taskCtx.LLMInstruction)
			}

			// Verify LLM mode
			if tc.expectLLMMode && !hasLayer(taskCtx.Layers, "LLMFilter") {
				t.Errorf("expected LLMFilter layer for :llm instruction, got %+v", taskCtx.Layers)
			}
		})
	}
}

func TestClassifyFocusedTaskContext_PromptEncouragesStoreNameParsing(t *testing.T) {
	ctx := context.Background()
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}}
	sysDB := database.NewDatabase(sysDBOptions)
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	sysDB.NewBtree(ctx, "users", tx)
	sysDB.NewBtree(ctx, "orders", tx)
	tx.Commit(ctx)

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	mock := &gate1MockGen{Response: `{"entity":"Omni","domain":"Stores","db_artifacts":["orders"],"stores_artifacts":["orders"],"spaces_artifacts":[],"layers":[]}`}

	if _, err := ag.ClassifyFocusedTaskContext(ctx, "Omni:Stores:", "Omni", "Stores", "", mock); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(mock.CapturedPrompt, "parse likely store names from the user's query") {
		t.Fatalf("expected focused classifier prompt to encourage store-name parsing, got: %s", mock.CapturedPrompt)
	}
	if !strings.Contains(mock.CapturedPrompt, "singular/plural variants") {
		t.Fatalf("expected focused classifier prompt to mention singular/plural matching, got: %s", mock.CapturedPrompt)
	}
}
