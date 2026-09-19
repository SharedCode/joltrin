//go:build llm
// +build llm

package agent_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/generator"
)

func TestFakeAgentGeneration(t *testing.T) {
	// 1. Setup LLM
	// Note: For production code, API keys come from Config only.
	// For tests, we still check environment variables for convenience.
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set. For production code, configure via Config struct, not environment variables.")
	}

	// Try known valid models.
	// We loop because model availability depends on the API key and region.
	// We prioritize the configured default.
	envModel := os.Getenv("GEMINI_MODEL")
	models := []string{envModel, ai.DefaultModelGemini, "gemini-2.5-pro", "gemini-2.0-flash-exp", "gemini-1.5-flash", "gemini-1.5-pro"}
	var gen ai.Generator
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, model := range models {
		if model == "" {
			continue
		}
		fmt.Printf("Probing model: %s\n", model)
		gen, err = generator.New("gemini", map[string]any{
			"api_key": apiKey,
			"model":   model,
		})
		if err != nil {
			fmt.Printf("Failed to create generator for %s: %v\n", model, err)
			continue
		}

		// Simple probe to check if model works
		probeCtx, probeCancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = gen.Generate(probeCtx, "Hi", ai.GenOptions{MaxTokens: 1})
		probeCancel()

		if err == nil {
			fmt.Printf("Using model: %s\n", model)
			break
		}
		fmt.Printf("Model %s failed: %v\n", model, err)
		gen = nil
	}

	if gen == nil {
		t.Fatalf("All models failed. Last error: %v", err)
	}

	// 2. Setup CopilotAgent
	// We need a dummy config and database
	cfg := agent.Config{
		EnableObfuscation: false, // Disable for now to isolate
		Verbose:           true,
		StubMode:          true, // Enable this to verify LLM generation without executing DB ops
	}
	databases := make(map[string]sop.DatabaseOptions)
	// We need at least one DB to satisfy the agent
	databases["mydb"] = sop.DatabaseOptions{
		StoresFolders: []string{"/tmp/mydb"},
	}

	// Setup System DB for scripts
	sysDB := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{"/tmp/sysdb_test"},
	})

	// Create Agent
	adminAgent := agent.NewCopilotAgent(cfg, databases, sysDB)
	adminAgent.SetGenerator(gen)

	// Create a dummy script manually to test execution
	ctx = context.WithValue(ctx, ai.CtxKeyWriter, os.Stdout)
	// We need to mock the session payload
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, &ai.SessionPayload{
		CurrentDB: "mydb",
	})
	adminAgent.Open(ctx)

	// Initialize script store
	tx, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
	store, _ := sysDB.OpenModelStore(ctx, "scripts", tx)
	store.Save(ctx, ai.DefaultScriptCategory, "test_script", ai.Script{Name: "test_script", Steps: []ai.ScriptStep{}})
	tx.Commit(ctx)

	// 3. User Query
	userQuery := "select from department a inner join employees b on a.region=b.region, a.department=b.department order by key limit 7"

	// 4. Invoke Agent
	fmt.Println("Sending request to Agent...")
	response, err := adminAgent.Ask(ctx, userQuery, nil)
	if err != nil {
		t.Fatalf("Agent Ask failed: %v", err)
	}

	// 5. Print Result
	fmt.Printf("Agent Response:\n%s\n", response)

	// 6. Verify Last Tool Call
	fmt.Println("Verifying Last Tool Call...")

	res, err := adminAgent.Execute(ctx, "save_last_step", map[string]any{
		"script": "test_script",
	})
	if err != nil {
		t.Fatalf("Failed to add step from last: %v", err)
	}
	fmt.Println(res)

	// Check if script is present
	tx, _ = sysDB.BeginTransaction(ctx, sop.ForReading)
	store, _ = sysDB.OpenModelStore(ctx, "scripts", tx)
	var m ai.Script
	store.Load(ctx, ai.DefaultScriptCategory, "test_script", &m)
	tx.Commit(ctx)

	if len(m.Steps) == 0 {
		t.Fatalf("Function has no steps")
	}
	lastStep := m.Steps[len(m.Steps)-1]
	if lastStep.Command != "execute_script" {
		t.Fatalf("Expected execute_script, got %s", lastStep.Command)
	}
	if _, ok := lastStep.Args["script"]; !ok {
		t.Fatalf("Script argument missing from recorded step! Args: %+v", lastStep.Args)
	}
	fmt.Printf("Recorded Script: %+v\n", lastStep.Args["script"])
}
