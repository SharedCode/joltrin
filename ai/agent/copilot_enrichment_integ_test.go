package agent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

func TestExplicitMinting_MintToSpaceTool(t *testing.T) {
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{UserID: "test", SessionID: "sess", CurrentDB: "sop"})
	testDir := "./test_data/minting_test"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{testDir}}
	sysDB := database.NewDatabase(sysDBOptions)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{"sop": sysDBOptions}, sysDB)

	mockGen := &mockGenerator{}
	mockEmb := &mockEmbeddings{}

	ag.brain = mockGen
	ag.service = &Service{
		domain:    &mockDomain{emb: mockEmb},
		generator: mockGen,
	}

	args := map[string]any{
		"kb_name":  "DevOps_Playbook",
		"content":  "To bypass coverage mounts during CI builds, utilize the Dockerfile.nocov configuration.",
		"category": "CI_Pipelines",
	}

	res, err := ag.toolMintToSpace(ctx, args)
	if err != nil {
		t.Fatalf("toolMintToSpace failed: %v", err)
	}
	t.Logf("Tool Result: %s", res)

	tx, err := sysDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to open tx: %v", err)
	}
	defer tx.Rollback(ctx)

	kb, err := sysDB.OpenKnowledgeBase(ctx, "DevOps_Playbook", tx, mockGen, mockEmb, false)
	if err != nil {
		t.Fatalf("Failed to open DevOps_Playbook KB: %v", err)
	}

	items, _ := kb.Store.Items(ctx)
	ok, _ := items.First(ctx)
	count := 0
	for ok {
		item, _ := items.GetCurrentValue(ctx)
		fmt.Printf("Item in DB: %+v\n", item)
		count++

		contentStr := ""
		if str, ok := item.Data["content"].(string); ok {
			contentStr = str
		}
		if contentStr != args["content"] {
			t.Fatalf("Expected content %q, got %q", args["content"], contentStr)
		}

		t.Logf("Success! Found minted item: %+v", item.Data)
		ok, _ = items.Next(ctx)
	}

	if count == 0 {
		t.Fatalf("Minted item was not found in the Knowledge Base!")
	}
}

func TestExplicitMinting_MintToSpaceTool_InfersKBNameFromCurrentUserQuery(t *testing.T) {
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{
		UserID:           "test",
		SessionID:        "sess",
		CurrentDB:        "sop",
		CurrentUserQuery: "Generate sample tasks and add them to my Tasks2 space",
	})
	testDir := "./test_data/minting_infer_kb_test"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{testDir}}
	sysDB := database.NewDatabase(sysDBOptions)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{"sop": sysDBOptions}, sysDB)

	mockGen := &mockGenerator{}
	mockEmb := &mockEmbeddings{}

	ag.brain = mockGen
	ag.service = &Service{
		domain:    &mockDomain{emb: mockEmb},
		generator: mockGen,
	}

	args := map[string]any{
		"content":  "Task 1: Define project scope\nTask 2: Break work into milestones\nTask 3: Assign owners and due dates",
		"category": "Tasks",
	}

	if _, err := ag.toolMintToSpace(ctx, args); err != nil {
		t.Fatalf("toolMintToSpace should infer kb_name from the user query, got error: %v", err)
	}

	tx, err := sysDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to open tx: %v", err)
	}
	defer tx.Rollback(ctx)

	if _, err := sysDB.OpenKnowledgeBase(ctx, "Tasks2", tx, mockGen, mockEmb, false); err != nil {
		t.Fatalf("Expected inferred KB 'Tasks2' to be created/opened, got: %v", err)
	}
}

func TestDeleteSpaceTool_InfersKBNameFromCurrentUserQuery(t *testing.T) {
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{
		UserID:           "test",
		SessionID:        "sess",
		CurrentDB:        "sop",
		CurrentUserQuery: "delete Tasks2 space",
	})
	testDir := "./test_data/delete_infer_kb_test"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{testDir}}
	sysDB := database.NewDatabase(sysDBOptions)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{"sop": sysDBOptions}, sysDB)

	mockGen := &mockGenerator{}
	mockEmb := &mockEmbeddings{}

	ag.brain = mockGen
	ag.service = &Service{
		domain:    &mockDomain{emb: mockEmb},
		generator: mockGen,
	}

	func() {
		tx, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			t.Fatalf("failed to begin transaction: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := sysDB.OpenKnowledgeBase(ctx, "Tasks2", tx, mockGen, mockEmb, false); err != nil {
			t.Fatalf("failed to create Tasks2 KB: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("failed to commit Tasks2 creation: %v", err)
		}
	}()

	if _, err := ag.toolDeleteSpace(ctx, map[string]any{}); err != nil {
		t.Fatalf("toolDeleteSpace should infer kb_name from delete request, got error: %v", err)
	}

	domains, err := sysDB.GetDomains(ctx)
	if err != nil {
		t.Fatalf("failed to list domains after delete: %v", err)
	}
	for _, domain := range domains {
		if domain == "Tasks2" {
			t.Fatalf("expected Tasks2 to be deleted, domains=%v", domains)
		}
	}
}

func TestImplicitEnrichment_TriggerSleepCycle(t *testing.T) {
	ctx := context.WithValue(context.Background(), SessionPayloadKey, &ai.SessionPayload{UserID: "test", SessionID: "sess", CurrentDB: "sop"})
	testDir := "./test_data/sleepcycle_test"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	sysDBOptions := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{testDir}}
	sysDB := database.NewDatabase(sysDBOptions)

	cfg := Config{}
	ag := NewCopilotAgent(cfg, map[string]sop.DatabaseOptions{"sop": sysDBOptions}, sysDB)

	mockGen := &mockGenerator{}
	mockEmb := &mockEmbeddings{}

	ag.brain = mockGen
	ag.service = &Service{
		domain:    &mockDomain{emb: mockEmb},
		generator: mockGen,
	}

	kbName := "ltm_agent123"

	func() {
		tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
		defer tx.Rollback(ctx)

		kb, _ := sysDB.OpenKnowledgeBase(ctx, kbName, tx, mockGen, mockEmb, false)
		kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{AllowAutoEnrichment: true})
		kb.IngestThought(ctx, "Why is my docker build failing to find coverage.out? Let's use Dockerfile.nocov instead.", "System Thoughts", "", nil, map[string]any{
			"raw_thought": "Why is my docker build failing to find coverage.out? Let's use Dockerfile.nocov instead.",
		})
		tx.Commit(ctx)
	}()

	err := ag.service.enrichSingleKB(ctx, sysDB, kbName)
	if err != nil {
		t.Fatalf("enrichSingleKB failed: %v", err)
	}

	tx, _ := sysDB.BeginTransaction(ctx, sop.ForReading)
	defer tx.Rollback(ctx)

	kb, _ := sysDB.OpenKnowledgeBase(ctx, kbName, tx, mockGen, mockEmb, false)

	items, _ := kb.Store.Items(ctx)
	ok, _ := items.First(ctx)
	foundVec := false
	for ok {
		item, _ := items.GetCurrentValue(ctx)
		if len(item.Positions) > 0 {
			foundVec = true
			t.Logf("Success! Found implicitly enriched and vectorized item: %+v", item.Data)
		}
		ok, _ = items.Next(ctx)
	}

	if !foundVec {
		t.Fatalf("SleepCycle failed to vectorize the raw item. No vectors found.")
	}
}
