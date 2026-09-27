package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

// MockGenerator is a placeholder.
type MockGenerator struct{}

func (m *MockGenerator) Name() string { return "mock" }

func (m *MockGenerator) Generate(ctx context.Context, prompt string, options ai.GenOptions) (ai.GenOutput, error) {
	return ai.GenOutput{Text: "Mock AI Response"}, nil
}

func (m *MockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *MockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error { return nil }

// MockAgent implements ai.Agent and ToolProvider
type MockAgent struct{}

func (a *MockAgent) Open(ctx context.Context) error  { return nil }
func (a *MockAgent) Close(ctx context.Context) error { return nil }
func (a *MockAgent) Search(ctx context.Context, query string, limit int) ([]ai.Hit[map[string]any], error) {
	return nil, nil
}
func (a *MockAgent) Ask(ctx context.Context, query string, cfg *ai.ConfigMap) (string, error) {
	return "", nil
}
func (a *MockAgent) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if toolName == "echo" {
		msg, _ := args["msg"].(string)
		return "Echo: " + msg, nil
	}
	return "", fmt.Errorf("unknown tool: %s", toolName)
}

func (a *MockAgent) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{
		{
			Name:        "echo",
			Description: "Echoes the message",
			Schema:      `{"type": "object", "properties": {"msg": {"type": "string"}}, "required": ["msg"]}`,
		},
	}, nil
}

// GenericDomain is a placeholder.
type GenericDomain struct{}

func (d *GenericDomain) ID() string                                                  { return "generic" }
func (d *GenericDomain) Name() string                                                { return "generic" }
func (d *GenericDomain) Embedder() ai.Embeddings                                     { return nil }
func (d *GenericDomain) Memory(ctx context.Context, tx sop.Transaction) (any, error) { return nil, nil }
func (d *GenericDomain) Index(ctx context.Context, tx sop.Transaction) (ai.VectorStore[map[string]any], error) {
	return nil, nil
}
func (d *GenericDomain) TextIndex(ctx context.Context, tx sop.Transaction) (ai.TextIndex, error) {
	return nil, nil
}
func (d *GenericDomain) BeginTransaction(ctx context.Context, mode sop.TransactionMode) (sop.Transaction, error) {
	return nil, nil
}
func (d *GenericDomain) Policies() ai.PolicyEngine                               { return nil }
func (d *GenericDomain) Classifier() ai.Classifier                               { return nil }
func (d *GenericDomain) Prompt(ctx context.Context, kind string) (string, error) { return "", nil }
func (d *GenericDomain) DataPath() string                                        { return "" }

func TestHandleExecuteScript(t *testing.T) {
	// 1. Setup Agent Service
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.NoCache,
	}
	sysDB := database.NewDatabase(dbOpts)

	// Create registry with MockAgent to handle "echo" tool
	registry := map[string]ai.Agent[map[string]any]{
		"mock": &MockAgent{},
	}

	svc := agent.NewService(&GenericDomain{}, sysDB, map[string]sop.DatabaseOptions{"system": dbOpts}, &MockGenerator{}, nil, registry, false)

	// 2. Register Agent in global map
	loadedAgents = make(map[string]ai.Agent[map[string]any])
	loadedAgents["copilot"] = svc

	// 3. Create a Test Script
	ctx := context.Background()
	script := ai.Script{
		Description: "A test script",
		Parameters:  []string{"name"},
		Steps: []ai.ScriptStep{
			{
				Type: "command",
				// We use a dummy command. The execution might fail if the command handler isn't registered,
				// but the goal is to verify the endpoint invokes the service and streams JSON.
				Command: "echo",
				Args:    map[string]any{"msg": "Hello {{.name}}"},
			},
		},
	}

	// Save script to system DB
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, "general", "test_script", script)
	tx.Commit(ctx)

	// 4. Create Request
	reqBody := `{"name": "test_script", "args": {"name": "World"}, "verbose": true}`
	req, _ := http.NewRequest("POST", "/api/scripts/execute", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// 5. Execute Handler
	// We test the handler directly, bypassing the auth middleware for this basic test
	// (or we could wrap it if we wanted to test auth)
	handleExecuteScript(w, req)

	// 6. Verify
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Check headers
	if w.Header().Get("Transfer-Encoding") != "chunked" {
		t.Log("Warning: Transfer-Encoding chunked not set in recorder (expected for streaming)")
	}

	// Check Body
	body := w.Body.String()
	// Verify output contains the expected result (NDJSON format or JSON Array)
	// We relax the check to allow for whitespace differences (e.g. "record": "Echo..." vs "record":"Echo...")
	if !strings.Contains(body, "Echo: Hello World") {
		t.Errorf("Expected output to contain 'Echo: Hello World', got: %s", body)
	}
}

func TestSourceLinkPopupTemplateUsesEscapedAttributes(t *testing.T) {
	templatePath := filepath.Join("templates", "scripts_part01.html")
	content, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("failed to read template: %v", err)
	}

	text := string(content)
	if !strings.Contains(text, `data-doc-ids="${docIDsAttr}"`) {
		t.Fatalf("expected escaped doc source attributes in template")
	}
	if !strings.Contains(text, `onclick="window.openDocSourcePopup(event, this, ${docIDsAttr}, ${textAttr}); return false;"`) {
		t.Fatalf("expected popup handler to use escaped attribute payloads")
	}
	if strings.Contains(text, `onclick="window.openDocSourcePopup(event, this, ${JSON.stringify(docIDs)}, ${JSON.stringify(t.text || '')});`) {
		t.Fatalf("template must not inject raw JSON into inline onclick attributes")
	}
}

func TestHandleExecuteScript_DefaultsVerboseTrue(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.NoCache,
	}
	sysDB := database.NewDatabase(dbOpts)
	registry := map[string]ai.Agent[map[string]any]{
		"mock": &MockAgent{},
	}
	svc := agent.NewService(&GenericDomain{}, sysDB, map[string]sop.DatabaseOptions{"system": dbOpts}, &MockGenerator{}, nil, registry, false)
	loadedAgents = map[string]ai.Agent[map[string]any]{"copilot": svc}

	ctx := context.Background()
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, "general", "test_script", ai.Script{
		Description: "A test script",
		Parameters:  []string{"name"},
		Steps: []ai.ScriptStep{{
			Type:    "command",
			Command: "echo",
			Args:    map[string]any{"msg": "Hello {{.name}}"},
		}},
	})
	tx.Commit(ctx)

	reqBody := `{"name": "test_script", "args": {"name": "World"}}`
	req, _ := http.NewRequest("POST", "/api/scripts/execute", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleExecuteScript(w, req)

	body := w.Body.String()
	if strings.Contains(body, `"type": "step_start"`) {
		t.Fatalf("expected single-step execution to suppress step output, got: %s", body)
	}
	if !strings.Contains(body, "Echo: Hello World") {
		t.Fatalf("expected script output, got: %s", body)
	}
}

func TestWithAuth_RejectsExpiredSessionToken(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	defer func() { config = Config{} }()

	ctx := context.Background()
	store := NewSessionStore(time.Nanosecond)
	access, _, err := store.CreateSession(ctx, "root", "admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	st, tx, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() error = %v", err)
	}
	defer tx.Rollback(ctx)
	if found, err := st.Find(ctx, access, false); err != nil || !found {
		t.Fatalf("Find() = (%v, %v), want found session", err, found)
	}
	record, err := st.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValue() error = %v", err)
	}
	record.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if ok, err := st.UpdateCurrentValue(ctx, record); err != nil || !ok {
		t.Fatalf("UpdateCurrentValue() = (%v, %v), want success", ok, err)
	}

	oldFacade := tokenFacade
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not run for expired token")
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/store/items", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired session token, got %d", w.Code)
	}
}

func TestWithAuth_AllowsRequestWhenConfigFileIsMissing(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	defer func() { config = Config{} }()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer os.Chdir(oldWd)

	config.ConfigFile = filepath.Join(tmpDir, "config.json")

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// First-run setup is reachable without a token, but only from this
	// machine: the unconfigured window is for local bootstrap, not for
	// anyone who can route to the port.
	req, _ := http.NewRequest(http.MethodGet, "/api/config/environments", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a loopback caller when config.json is missing, got %d", w.Code)
	}
}

// TestWithAuth_DeniesRemoteRequestWhenConfigFileIsMissing is the regression
// test for the fail-open auth finding: an unconfigured server used to serve
// every /api/* route to anybody, including store deletion and script
// execution, because the missing-config branch called the handler directly.
func TestWithAuth_DeniesRemoteRequestWhenConfigFileIsMissing(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	defer func() { config = Config{} }()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer os.Chdir(oldWd)

	config.ConfigFile = filepath.Join(tmpDir, "config.json")

	called := false
	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, remote := range []string{"203.0.113.7:44321", "192.168.1.50:44321", ""} {
		req, _ := http.NewRequest(http.MethodGet, "/api/config/environments", nil)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		handler(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("remote %q: expected 401 on an unconfigured server, got %d", remote, w.Code)
		}
	}
	if called {
		t.Fatal("the wrapped handler must never run for a non-loopback caller on an unconfigured server")
	}
}

func TestHandleExecuteScript_Auth(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	defer func() { config = Config{} }()

	// Setup Config
	if err := config.SetUser("root", "secret_password", "admin"); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	// Handler with Auth
	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Case 1: No Header
	req, _ := http.NewRequest("POST", "/api/scripts/execute", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for missing header, got %d", w.Code)
	}

	// Case 2: Wrong Token
	req, _ = http.NewRequest("POST", "/api/scripts/execute", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong token, got %d", w.Code)
	}

	// Case 3: Correct Session Token
	accessToken, _, err := currentTokenFacade().CreateSession(context.Background(), "root", "admin")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	req, _ = http.NewRequest("POST", "/api/scripts/execute", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for valid session token, got %d", w.Code)
	}

	// Reset Config
}
