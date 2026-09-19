package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

type mockEmbeddings struct{}

func (m *mockEmbeddings) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	var results [][]float32
	for range texts {
		results = append(results, []float32{0.1, 0.2})
	}
	return results, nil
}
func (m *mockEmbeddings) Name() string { return "mock" }
func (m *mockEmbeddings) Dim() int     { return 2 }

type mockGenerator struct{}

func (m *mockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	return ai.GenOutput{Text: "TestCategory"}, nil
}
func (m *mockGenerator) Name() string                                 { return "mock" }
func (m *mockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0.0 }

func (m *mockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

type mockDomain struct {
	emb ai.Embeddings
}

func (m *mockDomain) ID() string              { return "mock" }
func (m *mockDomain) Name() string            { return "mock" }
func (m *mockDomain) Embedder() ai.Embeddings { return m.emb }
func (m *mockDomain) Index(ctx context.Context, tx sop.Transaction) (ai.VectorStore[map[string]any], error) {
	return nil, nil
}
func (m *mockDomain) Memory(ctx context.Context, tx sop.Transaction) (any, error) { return nil, nil }
func (m *mockDomain) TextIndex(ctx context.Context, tx sop.Transaction) (ai.TextIndex, error) {
	return nil, nil
}
func (m *mockDomain) BeginTransaction(ctx context.Context, mode sop.TransactionMode) (sop.Transaction, error) {
	return nil, nil
}
func (m *mockDomain) Policies() ai.PolicyEngine                               { return nil }
func (m *mockDomain) Classifier() ai.Classifier                               { return nil }
func (m *mockDomain) Prompt(ctx context.Context, kind string) (string, error) { return "", nil }
func (m *mockDomain) DataPath() string                                        { return "" }

func TestAgentFullMemoryLifeCycleTest(t *testing.T) {
	ctx := context.Background()

	// 1. Setup DB
	os.RemoveAll("./test_data/lifecycle")
	defer os.RemoveAll("./test_data/lifecycle")
	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{"./test_data/lifecycle"}}
	sysDB := database.NewDatabase(sysDBOptions)

	dbs := map[string]sop.DatabaseOptions{
		"test_db": sysDBOptions,
	}

	// Create Agent
	cfg := Config{}
	ag := NewCopilotAgent(cfg, dbs, sysDB)

	// Setup session
	ag.service = &Service{
		session: &RunnerSession{
			MRU: []MRUItem{},
		},
		databases: dbs,
		systemDB:  sysDB,
	}

	// 2. Initialize Memory
	ag.Memory = memory.NewMemoryUnit("agent123")

	llm := &mockGenerator{}
	embedder := &mockEmbeddings{}
	ag.brain = llm
	ag.service.generator = llm

	err := ag.InitializePhysicalMemory(ctx)
	if err != nil {
		t.Fatalf("InitializePhysicalMemory failed: %v", err)
	}

	// 3. Queue an episode via official logger
	ag.Memory.LogEpisodeToSTM(ctx, "test_intent", map[string]any{"action": "learn"}, "I learned something new today", nil)

	time.Sleep(6 * time.Second) // wait for worker to batch and commit

	tx2, _ := sysDB.BeginTransaction(ctx, sop.ForReading)
	stmRead, _ := sysDB.OpenBtree(ctx, "stm_agent123", tx2)
	ok, _ := stmRead.First(ctx)
	if !ok {
		t.Fatalf("Failed to find episode in STM worker")
	}
	var loadedItem map[string]any
	v, _ := stmRead.GetCurrentValue(ctx)
	loadedItem = v.(map[string]any)
	tx2.Rollback(ctx)

	episode := loadedItem

	// 4. Sleep Cycle Consolidation logic (inline trigger)
	tx3, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	kbWrite, _ := sysDB.OpenKnowledgeBase(ctx, "ltm_agent123", tx3, llm, embedder, false)
	stmWrite, _ := sysDB.NewBtree(ctx, "stm_agent123", tx3)

	var thoughts []memory.Thought[map[string]any]
	thoughts = append(thoughts, memory.Thought[map[string]any]{
		Summaries: []string{"I learned something new today"},
		Data:      episode,
	})
	kbWrite.IngestThoughts(ctx, thoughts, "agent123")
	err = kbWrite.TriggerSleepCycle(ctx)
	if err != nil {
		t.Fatalf("TriggerSleepCycle failed: %v", err)
	}
	stmWrite.Remove(ctx, "ep_1")
	tx3.Commit(ctx)
	sysDB.Vectorize(ctx, "ltm_agent123", llm, embedder, 50)

	// Verify LTM count
	tx4, _ := sysDB.BeginTransaction(ctx, sop.ForReading)
	kbRead, _ := sysDB.OpenKnowledgeBase(ctx, "ltm_agent123", tx4, llm, embedder, false)
	count, _ := kbRead.Store.Count(ctx)
	if count == 0 {
		t.Fatalf("LTM did not ingest thought")
	}
	tx4.Rollback(ctx)

	// 5. Query / MRU Injection
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		ActiveDomain: "ltm_agent123",
		CurrentDB:    "test_db",
	})
	ag.service.domain = &mockDomain{emb: embedder}

	db := ag.resolveDBForKB(ctx, "ltm_agent123")
	res, err := ag.searchKnowledgeBase(ctx, db, "ltm_agent123", "learned something", "", "", 5)
	if err != nil {
		t.Fatalf("toolSearchDomainKB failed: %v", err)
	}
	if res == "No results found." {
		t.Fatalf("Search failed to find LTM item")
	}

	mruSnapshot := ag.getMRUSnapshot()

	if len(mruSnapshot) == 0 {
		t.Fatalf("MRU was not updated")
	}

	found := false
	for _, item := range mruSnapshot {
		if item.Category == "ltm_agent123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Category ltm_agent123 not injected into MRU, got: %+v", mruSnapshot)
	}
}
