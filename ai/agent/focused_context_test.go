package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
)

func TestBuildSystemPrompt_IncludesFocusedStoreContext(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{dbDir}}
	appDB := database.NewDatabase(dbOpts)
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

	tx, err := appDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	usersOpts := sop.StoreOptions{
		Name:           "users",
		SlotLength:     10,
		IsPrimitiveKey: true,
		Relations: []sop.Relation{{
			SourceFields: []string{"key"},
			TargetStore:  "orders",
			TargetFields: []string{"user_id"},
		}},
	}
	users, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, usersOpts)
	if err != nil {
		t.Fatalf("create users store: %v", err)
	}
	orders, err := sopdb.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil, sop.StoreOptions{Name: "orders", SlotLength: 10, IsPrimitiveKey: true})
	if err != nil {
		t.Fatalf("create orders store: %v", err)
	}
	if ok, err := users.Add(ctx, "u1", map[string]any{"user_id": "u1", "first_name": "John"}); err != nil || !ok {
		t.Fatalf("add users record: %v", err)
	}
	if ok, err := orders.Add(ctx, "o1", map[string]any{"user_id": "u1", "total_amount": 600}); err != nil || !ok {
		t.Fatalf("add orders record: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{"appdb": dbOpts}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	payload := &ai.SessionPayload{CurrentDB: "appdb", Variables: make(map[string]any)}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, "Find users", TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users"},
		Layers: []LayerInfo{{
			Name: "Single-Domain",
			CRUD: []string{"R"},
		}},
	})

	if !strings.Contains(prompt, "focused_execution_context") {
		t.Fatalf("expected focused execution context component in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Target Stores") || !strings.Contains(prompt, "- users") {
		t.Fatalf("expected focused users store details in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, `stores:[\"users\"]`) {
		t.Fatalf("expected focused prompt to include scoped list_stores research hint: %s", prompt)
	}
	if !strings.Contains(prompt, "Relations:") || !strings.Contains(prompt, "orders([user_id])") {
		t.Fatalf("expected relations metadata for users store: %s", prompt)
	}
	if !strings.Contains(prompt, "R = Read") || !strings.Contains(prompt, "begin_tx(mode=read)") {
		t.Fatalf("expected read-only store operations in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "use the active Current Database name from context") {
		t.Fatalf("expected focused prompt to teach open_db database selection from active context: %s", prompt)
	}
	if strings.Contains(prompt, "Available Stores:") {
		t.Fatalf("expected no generic all-store schema dump when a target artifact is classified: %s", prompt)
	}
}

func TestBuildSystemPrompt_StoresFocusedContext_StaysWithinBudgetGuardrail(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{dbDir}}
	appDB := database.NewDatabase(dbOpts)
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})

	tx, err := appDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	usersOpts := sop.StoreOptions{
		Name:           "users",
		SlotLength:     10,
		IsPrimitiveKey: true,
		Relations: []sop.Relation{{
			SourceFields: []string{"key"},
			TargetStore:  "users_orders",
			TargetFields: []string{"key"},
		}},
	}
	users, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, usersOpts)
	if err != nil {
		t.Fatalf("create users store: %v", err)
	}
	orders, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users_orders", tx, nil, sop.StoreOptions{Name: "users_orders", SlotLength: 10, IsPrimitiveKey: true})
	if err != nil {
		t.Fatalf("create users_orders store: %v", err)
	}
	if ok, err := users.Add(ctx, "u1", map[string]any{"first_name": "John", "user_id": "u1"}); err != nil || !ok {
		t.Fatalf("add users record: %v", err)
	}
	if ok, err := orders.Add(ctx, "u1", map[string]any{"user_id": "u1", "value": "o1"}); err != nil || !ok {
		t.Fatalf("add users_orders record: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup: %v", err)
	}

	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{"appdb": dbOpts}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	payload := &ai.SessionPayload{CurrentDB: "appdb", Variables: make(map[string]any)}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, "Find John and inspect related orders", TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}

	focusedContent := ""
	for _, element := range elements {
		if element.Component == ComponentFocusedContext {
			focusedContent = element.Content
			break
		}
	}
	if focusedContent == "" {
		t.Fatalf("expected focused execution context component in prompt: %s", prompt)
	}
	if len(focusedContent) > 1000 {
		t.Fatalf("expected stores focused execution context to stay within 1000 chars after prompt reductions, got %d\nContent: %s", len(focusedContent), focusedContent)
	}
	if !strings.Contains(focusedContent, "Target Stores") || !strings.Contains(focusedContent, "Relevant Store Operations") {
		t.Fatalf("expected focused execution context to retain core Stores anchors: %s", focusedContent)
	}
	if !strings.Contains(focusedContent, `stores:["users"]`) {
		t.Fatalf("expected focused execution context to retain scoped list_stores hint, got %s", focusedContent)
	}
	for _, element := range elements {
		if element.Component == ComponentSystemTools && strings.Contains(element.Content, "Execution Flow Engine Guardrails") {
			t.Fatalf("expected execution guardrails to live only in focused execution context, got duplicated system tools content: %s", element.Content)
		}
		if element.Component == ComponentSystemTools && strings.Contains(element.Content, "[truncated]") {
			t.Fatalf("expected stores system tools context to fit without truncation after prompt reductions, got: %s", element.Content)
		}
	}
}

func TestBuildStoresCRUDOperationsContext_WriteGuidanceUsesGroundedScriptContract(t *testing.T) {
	content := buildStoresCRUDOperationsContext(map[string]bool{"C": true, "U": true, "D": true})

	checks := []string{
		"C = Create. Use write transactions.",
		"research them with list_stores first",
		"generate a concrete write script and run it with execute_script",
		"U = Update. Use write transactions and narrow the target records before update.",
		"D = Delete. Use write transactions and narrow the target records before delete.",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Fatalf("expected write guidance to contain %q, got %s", check, content)
		}
	}
	for _, duplicate := range []string{"Read steps:", "Create steps:", "Update steps:", "Delete steps:"} {
		if strings.Contains(content, duplicate) {
			t.Fatalf("expected focused CRUD guidance to omit duplicated step inventory %q, got %s", duplicate, content)
		}
	}
}

func TestBuildSystemPrompt_IncludesCrossDomainFocusedContext(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	payload := &ai.SessionPayload{Variables: make(map[string]any)}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, "Search release notes and inspect users", TaskContextClassification{
		Domain:          StoresDomain,
		StoresArtifacts: []string{"users"},
		SpacesArtifacts: []string{"release_notes"},
		Layers: []LayerInfo{{
			Name: "Cross-Domain",
			CRUD: []string{"R"},
		}},
	})

	if !strings.Contains(prompt, "Stores Execution Context") {
		t.Fatalf("expected stores cross-domain execution context in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Spaces Execution Context") {
		t.Fatalf("expected spaces cross-domain execution context in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Target Stores") || !strings.Contains(prompt, "users") {
		t.Fatalf("expected stores targets in cross-domain prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Target Spaces: release_notes") {
		t.Fatalf("expected spaces targets in cross-domain prompt: %s", prompt)
	}

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}
	for _, element := range elements {
		if element.Component != ComponentSystemTools {
			continue
		}
		if strings.Contains(element.Content, "Execution Flow Engine Guardrails") {
			t.Fatalf("expected cross-domain system tools to avoid duplicated execution guardrails, got: %s", element.Content)
		}
		if strings.Contains(element.Content, "[truncated]") {
			t.Fatalf("expected cross-domain system tools context to fit without truncation after prompt reductions, got: %s", element.Content)
		}
		if !strings.Contains(element.Content, "Structured Context: Stores Tools") || !strings.Contains(element.Content, "Structured Context: Spaces Tools") {
			t.Fatalf("expected cross-domain system tools to use generated stores and spaces tool context, got: %s", element.Content)
		}
		if !strings.Contains(element.Content, "You are a Stores Database Expert Agent") || !strings.Contains(element.Content, "- mint_to_space:") {
			t.Fatalf("expected compact stores guidance plus generated spaces tool descriptions, got: %s", element.Content)
		}
		if !strings.Contains(element.Content, "defines the durability boundary") {
			t.Fatalf("expected cross-domain system tools to preserve Space transaction boundary guidance, got: %s", element.Content)
		}
		if strings.Contains(element.Content, "# Spaces Manual") {
			t.Fatalf("expected cross-domain system tools to avoid the old spaces manual blob, got: %s", element.Content)
		}
	}
}

func TestBuildSystemPrompt_IncludesScriptAuthoringContextForStores(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	payload := &ai.SessionPayload{CurrentDB: SystemDBName, Variables: make(map[string]any)}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	prompt := ag.buildSystemPrompt(ctx, "Create a script named expensive_orders to find orders over 1000", TaskContextClassification{
		Domain:          StoresDomain,
		DBArtifacts:     []string{"orders"},
		Layers:          []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
		ScriptAuthoring: true,
	})

	if !strings.Contains(prompt, "Relevant Script Authoring Operations") {
		t.Fatalf("expected focused execution context to include script authoring guidance: %s", prompt)
	}
	if !strings.Contains(prompt, "Structured Context: Script Authoring Tools") {
		t.Fatalf("expected system tools to include script authoring manual: %s", prompt)
	}
	if !strings.Contains(prompt, "Use create_script for new reusable scripts") {
		t.Fatalf("expected create_script guidance in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Put reusable steps under the `script` field") {
		t.Fatalf("expected script payload shape guidance in prompt: %s", prompt)
	}

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}
	for _, element := range elements {
		if element.Component != ComponentSystemTools {
			continue
		}
		if strings.Contains(element.Content, "[truncated]") {
			t.Fatalf("expected script-authoring system tools context to fit without truncation after prompt reductions, got: %s", element.Content)
		}
		if !strings.Contains(element.Content, "Structured Context: Script Authoring Tools") {
			t.Fatalf("expected script-authoring manual to remain present in system tools, got: %s", element.Content)
		}
	}
}

func TestBuildSystemPrompt_DedupesFocusedToolSectionsAgainstSystemToolsBaseline(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	payload := &ai.SessionPayload{CurrentDB: SystemDBName, Variables: make(map[string]any)}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	ag.markMRUCategoryWithSource(SYSTEM_TOOLS, buildScriptToolDescriptionContext(StoresDomain, map[string]bool{"R": true}), MRUSourceSystemTools)

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

	systemTools := ""
	for _, element := range elements {
		if element.Component == ComponentSystemTools {
			systemTools = element.Content
			break
		}
	}
	if systemTools == "" {
		t.Fatalf("expected system tools component in prompt: %s", prompt)
	}
	if count := strings.Count(systemTools, "Structured Context: Script Authoring Tools"); count != 1 {
		t.Fatalf("expected script authoring tools section once after baseline dedupe, got %d\n%s", count, systemTools)
	}
	if count := strings.Count(systemTools, "Structured Context: Stores Tools"); count != 1 {
		t.Fatalf("expected stores tools section once after baseline merge, got %d\n%s", count, systemTools)
	}
}

func TestBuildSystemPrompt_StoresSystemTools_PrefersCompactProtocolSlice(t *testing.T) {
	ctx := context.Background()
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{}, sysDB)
	ag.service = &Service{session: &RunnerSession{MRU: []MRUItem{}}}

	prompt := ag.buildSystemPrompt(ctx, "Find John orders over 500", TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: []string{"users", "orders"},
		Layers:      []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}},
	})

	var elements []PromptElement
	if err := json.Unmarshal([]byte(prompt), &elements); err != nil {
		t.Fatalf("expected buildSystemPrompt to return JSON prompt elements: %v\nPrompt: %s", err, prompt)
	}

	systemTools := ""
	for _, element := range elements {
		if element.Component == ComponentSystemTools {
			systemTools = element.Content
			break
		}
	}
	if systemTools == "" {
		t.Fatalf("expected system tools component in prompt: %s", prompt)
	}
	if strings.Contains(systemTools, "<h2> Example</h2>") || strings.Contains(systemTools, "Execution Flow Engine Guardrails") {
		t.Fatalf("expected stores system tools to omit the large execute_script example block, got: %s", systemTools)
	}
	if !strings.Contains(systemTools, "Structured Context: Stores Tools") || !strings.Contains(systemTools, "You are a Stores Database Expert Agent") {
		t.Fatalf("expected stores system tools to use the compact stores tool context, got: %s", systemTools)
	}
	if !strings.Contains(systemTools, "Never guess store names") || !strings.Contains(systemTools, "retry once") {
		t.Fatalf("expected simplified stores tool protocol guidance to remain visible in system tools, got: %s", systemTools)
	}
	if !strings.Contains(systemTools, "multi-step store plan") || !strings.Contains(systemTools, "place those steps inside execute_script.script") {
		t.Fatalf("expected stores system tools to guide multi-step operations to use execute_script, got: %s", systemTools)
	}
}

func TestBuildSpacesCRUDOperationsContext_DoesNotRequireVectorizationByDefault(t *testing.T) {
	ctx := buildSpacesCRUDOperationsContext(map[string]bool{"C": true, "U": true})

	if !strings.Contains(ctx, "Use mint_to_space to add generated content") {
		t.Fatalf("expected create guidance to route generated space writes through mint_to_space: %s", ctx)
	}
	if !strings.Contains(ctx, "do not attempt an external import workflow") {
		t.Fatalf("expected create guidance to avoid import-style tool calls for generated content: %s", ctx)
	}
	if !strings.Contains(ctx, "pass that exact name in mint_to_space.kb_name") {
		t.Fatalf("expected create guidance to require explicit kb_name for user-named Spaces: %s", ctx)
	}
	if !strings.Contains(ctx, "Do NOT call vectorize_space_items") {
		t.Fatalf("expected create guidance to avoid default vectorization: %s", ctx)
	}
	if !strings.Contains(ctx, "mint_to_space manages its own transaction internally") {
		t.Fatalf("expected update guidance to describe mint_to_space transaction handling: %s", ctx)
	}
	if !strings.Contains(ctx, "Do NOT call vectorization APIs unless the user explicitly asks") {
		t.Fatalf("expected update guidance to avoid default vectorization: %s", ctx)
	}
}

func TestBuildSpacesCRUDOperationsContext_ReadToolsManageOwnTransactions(t *testing.T) {
	ctx := buildSpacesCRUDOperationsContext(map[string]bool{"R": true})

	if !strings.Contains(ctx, "list_space_categories, list_space_items, search_space, and read_space_config") {
		t.Fatalf("expected read guidance to mention Space discovery tools: %s", ctx)
	}
	if !strings.Contains(ctx, "manage their own read transactions") {
		t.Fatalf("expected read guidance to describe direct Space API transaction ownership: %s", ctx)
	}
	if !strings.Contains(ctx, "Do NOT wrap") {
		t.Fatalf("expected read guidance to forbid begin_tx wrapping for direct Space API tools: %s", ctx)
	}
}

func TestBuildSpacesCRUDOperationsContext_DeleteUsesDeleteSpace(t *testing.T) {
	ctx := buildSpacesCRUDOperationsContext(map[string]bool{"D": true})

	if !strings.Contains(ctx, "Use delete_space for full Space deletion") {
		t.Fatalf("expected delete guidance to expose delete_space: %s", ctx)
	}
	if !strings.Contains(ctx, "Prefer a confirmation step with the user") {
		t.Fatalf("expected delete guidance to require confirmation wording: %s", ctx)
	}
}
