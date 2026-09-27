//go:build integration

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/domain"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/generator"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// kbItem matches the structure of medical.json and sop_base_knowledge.json
type kbItem struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Text        string `json:"text"`
	Description string `json:"description"`
}

// setupIntegHarness bootstraps a Service hooked up to real/in-memory B-Trees.
// It conditionally loads the provided JSON files into designated Knowledge Bases.
func setupIntegHarness(t *testing.T, ctx context.Context, kbsToLoad map[string]string) *Service {
	// Note: For production code, API keys come from Config only.
	// For tests, we still check environment variables for convenience.
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping integration test: GEMINI_API_KEY is not set. For production code, configure via Config struct, not environment variables.")
	}

	gen, err := generator.New("gemini", map[string]any{
		"api_key": apiKey,
	})
	if err != nil {
		t.Fatalf("Failed to initialize Gemini: %v", err)
	}

	emb := embed.NewGemini(apiKey, "")

	tmpDir := t.TempDir()

	// 1. Setup System DB (for SOP KB and LTM)
	sysDbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(sysDbOpts)

	// 2. Setup domain DBs (simulating custom KBs the user might select)
	dbs := make(map[string]sop.DatabaseOptions)

	for kbName, filePath := range kbsToLoad {
		kbDir := t.TempDir()
		dbs[kbName] = sop.DatabaseOptions{
			Type:          sop.Standalone,
			StoresFolders: []string{kbDir},
			CacheType:     sop.InMemory,
		}

		seedDB := database.NewDatabase(dbs[kbName])

		// If a file is specified, ingest it
		if filePath != "" {
			t.Logf("Ingesting %s into KB '%s'...", filePath, kbName)
			err := ingestKBFile(ctx, seedDB, kbName, filePath, gen, emb)
			if err != nil {
				t.Fatalf("Failed to ingest %s: %v", filePath, err)
			}
		}
	}

	// 3. Setup the main Domain/Workflow
	cfg := domain.Config[map[string]any]{
		DB:        sysDB,
		Generator: gen,
		Embedder:  embed.NewGemini(apiKey, ""),
		StoreName: "user_long_term_memory",
		ID:        "integ_domain_id",
		Name:      "Integration Test Domain",
		Prompts: map[string]string{
			"system": "# The Omni Persona (Master Architect)\n\nYou are the **Omni Persona**, the ultimate Master Architect and overarching guide for the SOP AI platform. You are not just a standard assistant; you hold the highest level of context regarding the system's architecture, capabilities, and active Knowledge Bases (KBs).\n\n## 1. Master Directive\nYour foundational, inescapable directive is twofold:\n1. **SOP Knowledge Expert**: You possess complete expertise on the SOP library and tech stack. You are here to help developers through the entire Software Development Life Cycle (SDLC) by providing deep, preloaded technical knowledge on building applications, databases, and microservices using SOP.\n2. **The Omni Manager (Ultimate Enabler)**: You transcend standard developer assistance. You act as an \"Omni Persona\" that seamlessly differentiates between raw backend \"Technical Tables\" (B-Trees / Stores) and new AI memory subsystems called \"Spaces\" (Knowledge Bases). You hold the meta-knowledge of all isolated Knowledge Bases within the platform. You act as the agentic manager that enables context and KB routing. You are the sole orchestrator capable of managing multiple domains and dynamically shifting the focus among different expertise silos to best serve the organization.",
		},
	}
	activeDomain := domain.NewGenericDomain(cfg)
	if err != nil {
		t.Fatalf("Failed to bootstrap primary domain: %v", err)
	}

	// Create and bind Service (HistoryInjection = true for authentic ReAct turns)
	service := NewService(activeDomain, sysDB, dbs, gen, nil, nil, false)
	service.EnableHistoryInjection = true

	// Initialize the central domain index (long term memory) to assign metadata while we can write
	initTx, initErr := activeDomain.BeginTransaction(ctx, sop.ForWriting)
	if initErr == nil {
		activeDomain.Index(ctx, initTx) // This triggers vector.OpenStore which writes metadata!
		initTx.Commit(ctx)
	}

	return service
}

// ingestKBFile reads the JSON file and upserts it into the KB B-Tree
func ingestKBFile(ctx context.Context, db *database.Database, kbName string, filePath string, generator ai.Generator, embedder ai.Embeddings) error {
	data, err := os.ReadFile("../../" + filePath) // Adjust relative to execution dir
	if err != nil {
		// Attempt fallback relative path depending on where test is run
		data, err = os.ReadFile(filePath)
		if err != nil {
			return err
		}
	}

	var items []kbItem
	if err := json.Unmarshal(data, &items); err != nil {
		// Try parsing as an object with "items" field (e.g. sop_base_knowledge.json format)
		var wrapper struct {
			Items []kbItem `json:"items"`
		}
		if err2 := json.Unmarshal(data, &wrapper); err2 == nil && len(wrapper.Items) > 0 {
			items = wrapper.Items
		} else {
			return err
		}
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Open the KB, generator handles real embeddings if configured
	kbStore, err := db.OpenKnowledgeBase(ctx, kbName, tx, generator, embedder, false)
	if err != nil || kbStore == nil {
		return fmt.Errorf("failed to open KB %s: %v", kbName, err)
	}

	for _, item := range items {
		content := fmt.Sprintf("%s\n%s", item.Text, item.Description)
		payload := map[string]any{
			"id":          item.ID,
			"category":    item.Category,
			"description": item.Description,
		}

		// Use real embeddings if the pipeline supports it,
		vecs, embErr := embed.DocumentTexts(ctx, embedder, []string{content})
		if embErr != nil || len(vecs) == 0 {
			return fmt.Errorf("failed generating real vector embedding: %v", embErr)
		}
		realVector := vecs[0]

		err = kbStore.Store.Upsert(ctx, memory.Item[map[string]any]{
			ID:         sop.NewUUID(),
			CategoryID: sop.NewUUID(),
			Summaries:  []string{content},
			Data:       payload,
		}, realVector)
		if err != nil {
			return fmt.Errorf("upsert failed: %v", err)
		}
	}

	return tx.Commit(ctx)
}

func TestHarness_ReAct_MedicalKBSimulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Load 1 KB using the medical.json file
	kbs := map[string]string{
		{Name: "medical_kb", Type: ai.ArtifactTypeSpace}: "medical.json", // Setup looks natively in root or via fallback
	}
	svc := setupIntegHarness(t, ctx, kbs)

	options := ai.NewConfigMap()
	options.Set("payload", &ai.SessionPayload{SelectedKBs: []ai.ArtifactReference{{Name: "medical_kb", Type: ai.ArtifactTypeSpace}}})

	// Query requiring it to fetch from the loaded medical KB
	t.Log("Querying medical condition...")
	res, err := svc.Ask(ctx, "I have continuous sneezing and shivering. Based on the medical KB, what condition might I have?", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	t.Logf("Agent Response:\n%s", res)
}

func TestHarness_ReAct_MultiKBSimulation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Load multiple KBs
	kbs := map[string]string{
		{Name: "medical_kb", Type: ai.ArtifactTypeSpace}: "medical.json",
		{Name: "sop_kb", Type: ai.ArtifactTypeSpace}:     "sop_base_knowledge.json",
		{Name: "empty_kb1", Type: ai.ArtifactTypeSpace}:  "",
		{Name: "empty_kb2", Type: ai.ArtifactTypeSpace}:  "",
	}
	svc := setupIntegHarness(t, ctx, kbs)

	options := ai.NewConfigMap()
	options.Set("payload", &ai.SessionPayload{SelectedKBs: []ai.ArtifactReference{{Name: "medical_kb", Type: ai.ArtifactTypeSpace}, {Name: "sop_kb", Type: ai.ArtifactTypeSpace}, {Name: "empty_kb1", Type: ai.ArtifactTypeSpace}, {Name: "empty_kb2", Type: ai.ArtifactTypeSpace}}})

	t.Log("Asking complex multi-KB query...")
	res, err := svc.Ask(ctx, "First read from the medical KB: Tell me what condition has dischromic patches. Then, read from the sop_kb: explain what the No-LLM Direct Command Interface is. Conclude by storing both answers in your ActiveMemory.", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	t.Logf("Agent Response:\n%s", res)
}

// getKBItemsCount retrieves current total items count across the domain KB
func getKBItemsCount(t *testing.T, ctx context.Context, svc *Service) int {
	tx, err := svc.domain.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return 0
	}
	defer tx.Rollback(ctx)

	memStoreAny, err := svc.domain.Memory(ctx, tx)
	if err != nil {
		return 0
	}

	kb, ok := memStoreAny.(*memory.KnowledgeBase[map[string]any])
	if !ok {
		return 0
	}

	c, _ := kb.Store.Count(ctx)
	return int(c)
}

func TestHarness_ReAct_LongConversation_50Turns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	kbs := map[string]string{
		{Name: "medical_kb", Type: ai.ArtifactTypeSpace}: "medical.json",
		{Name: "sop_kb", Type: ai.ArtifactTypeSpace}:     "sop_base_knowledge.json",
	}
	svc := setupIntegHarness(t, ctx, kbs)
	svc.EnableShortTermMemory = true
	if err := svc.InitializeShortTermMemory(ctx); err != nil {
		t.Fatalf("STM init failed: %v", err)
	}

	options := ai.NewConfigMap()
	options.Set("payload", &ai.SessionPayload{SelectedKBs: []ai.ArtifactReference{{Name: "medical_kb", Type: ai.ArtifactTypeSpace}, {Name: "sop_kb", Type: ai.ArtifactTypeSpace}}})

	questions := []string{
		"How do I use slash commands in the SOP No-LLM mode?",
		"What are the symptoms of Allergy in the medical KB?",
		"Are there any side effects to the AI copilot feature? Just give a general common sense answer.",
		"What's 2+2? Keep it short.",
		"If I have a cough and stomach pain, what condition is that?",
	}

	for i := 0; i < 50; i++ {
		q := questions[i%len(questions)]

		// Periodically ask the agent to store things explicitly so STM triggers
		if i == 10 || i == 30 || i == 45 {
			q = "Please store a summary of our entire conversation so far into ActiveMemory."
		}

		res, err := svc.Ask(ctx, fmt.Sprintf("[Turn %d] %s", i+1, q), options)
		if err != nil {
			t.Fatalf("Ask failed on turn %d: %v", i+1, err)
		}
		t.Logf("[Turn %d] Agent Response: %s", i+1, res)
	}

	initialLTMCount := getKBItemsCount(t, ctx, svc)
	t.Logf("Before SleepCycle: LTM Vector Count = %d", initialLTMCount)

	// Fire sleep cycle
	svc.StartShortTermMemorySleepCycle(ctx, 1*time.Second)
	t.Log("Background sleep cycle initiated. Waiting 4 seconds for DB sweeps...")
	time.Sleep(4 * time.Second)

	finalLTMCount := getKBItemsCount(t, ctx, svc)
	t.Logf("After SleepCycle: LTM Vector Count = %d", finalLTMCount)

	if finalLTMCount <= initialLTMCount {
		t.Logf("Warning: LTM count did not increase (Initial: %d, Final: %d). This might be due to lack of distinct explicit records.", initialLTMCount, finalLTMCount)
	}
}

func TestHarness_ReAct_MultiSleepCycles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	kbs := map[string]string{
		{Name: "sop_kb", Type: ai.ArtifactTypeSpace}: "sop_base_knowledge.json",
	}
	svc := setupIntegHarness(t, ctx, kbs)
	svc.EnableShortTermMemory = true
	if err := svc.InitializeShortTermMemory(ctx); err != nil {
		t.Fatalf("STM init failed: %v", err)
	}

	options := ai.NewConfigMap()
	options.Set("payload", &ai.SessionPayload{SelectedKBs: []ai.ArtifactReference{{Name: "sop_kb", Type: ai.ArtifactTypeSpace}}})

	// Manually initiate constant background sleep cycle
	svc.StartShortTermMemorySleepCycle(ctx, 2*time.Second)

	// Cycle 1: Inject fact
	t.Log("--- Cycle 1: Initial Injection ---")
	_, err := svc.Ask(ctx, "My favorite color is Blue. Please remember this in ActiveMemory. You MUST use the 'conclude_topic' tool to ensure it is recorded. Output the tool call wrapped exactly in a ```json block.", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	t.Log("Waiting for SleepCycle 1 to run...")
	time.Sleep(4 * time.Second)

	// Cycle 2: Verify and Mutate
	t.Log("--- Cycle 2: Mutation ---")
	res, err := svc.Ask(ctx, "What was my favorite color? Also, I've changed my mind, my favorite color is now Green. Please store this update in your ActiveMemory. You MUST use the 'conclude_topic' tool to record it. Output the tool call wrapped exactly in a ```json block.", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	t.Logf("Response to mutation: %s", res)

	t.Log("Waiting for SleepCycle 2 to run...")
	time.Sleep(4 * time.Second)

	// Cycle 3: Verify the mutated state
	t.Log("--- Cycle 3: Verification ---")
	resFinal, err := svc.Ask(ctx, "What is my favorite color currently? Respond extremely concisely.", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	t.Log("Waiting for SleepCycle 3 (for good measure) to run...")
	time.Sleep(3 * time.Second)

	t.Logf("Final Verification Response: %s", resFinal)

	finalCount := getKBItemsCount(t, ctx, svc)
	t.Logf("LTM Count at end of 3 cycles: %d", finalCount)
}

func TestHarness_ReAct_OmniSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	testKBFile := "omni_test_kb.json"
	kbData := []byte("[{\"id\": \"1\", \"category\": \"fruits\", \"text\": \"apple\", \"description\": \"a sweet red or green fruit\"}, {\"id\": \"2\", \"category\": \"fruits\", \"text\": \"b\", \"description\": \"banana is a yellow fruit\"}, {\"id\": \"3\", \"category\": \"fruits\", \"text\": \"c\", \"description\": \"cherry is a small red fruit\"}, {\"id\": \"4\", \"category\": \"smart phones\", \"text\": \"apple\", \"description\": \"apple iPhones are popular smart phones\"}, {\"id\": \"5\", \"category\": \"smart phones\", \"text\": \"y\", \"description\": \"device y\"}, {\"id\": \"6\", \"category\": \"smart phones\", \"text\": \"z\", \"description\": \"device z\"}, {\"id\": \"7\", \"category\": \"computer\", \"text\": \"apple\", \"description\": \"apple macbooks are powerful computers\"}, {\"id\": \"8\", \"category\": \"computer\", \"text\": \"h\", \"description\": \"device h\"}, {\"id\": \"9\", \"category\": \"computer\", \"text\": \"i\", \"description\": \"device i\"}]")

	err := os.WriteFile("../../"+testKBFile, kbData, 0644)
	if err != nil {
		err = os.WriteFile(testKBFile, kbData, 0644)
		if err != nil {
			t.Fatalf("Failed to create test kb file: %v", err)
		}
	}
	defer os.Remove("../../" + testKBFile)
	defer os.Remove(testKBFile)

	kbs := map[string]string{
		{Name: "omni_kb", Type: ai.ArtifactTypeSpace}: testKBFile,
		// {Name: "sop_kb", Type: ai.ArtifactTypeSpace}:  "sop_base_knowledge.json", // Omitted to avoid long vectorization during test
	}
	svc := setupIntegHarness(t, ctx, kbs)
	svc.EnableShortTermMemory = false // Avoid write conflicts

	options := ai.NewConfigMap()
	options.Set("payload", &ai.SessionPayload{SelectedKBs: []ai.ArtifactReference{{Name: "omni_kb", Type: ai.ArtifactTypeSpace}}})

	t.Log("Asking question about apple iPhones...")
	res, err := svc.Ask(ctx, "what details do you know about apple iPhones? You MUST use the 'search_custom_kbs' tool to search for knowledge. Output the tool call wrapped exactly in a ```json block.", options)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	t.Logf("Agent Response:\n%s", res)
}
