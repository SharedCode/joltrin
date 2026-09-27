package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func main() {
	configPath := flag.String("config", "", "Path to the agent configuration JSON file")
	stubMode := flag.Bool("stub", false, "Enable stub mode for all agents")
	flag.Parse()

	if *configPath == "" {
		fmt.Println("Usage: go run ai/cmd/agent/main.go -config <path_to_config.json>")
		os.Exit(1)
	}

	// 1. Load Configuration
	cfg, err := agent.LoadConfigFromFile(*configPath)
	if err != nil {
		panic(fmt.Errorf("failed to load config: %w", err))
	}

	// Apply Stub Mode override
	if *stubMode {
		fmt.Println("WARNING: Stub Mode enabled via flag. Database operations will be simulated.")
		cfg.StubMode = true
		for i := range cfg.Agents {
			cfg.Agents[i].StubMode = true
		}
	}

	// Ensure absolute path for storage
	if cfg.StoragePath != "" {
		if absPath, err := filepath.Abs(cfg.StoragePath); err == nil {
			cfg.StoragePath = absPath
		}
	}

	fmt.Printf("Initializing AI Agent: %s (%s)...\n", cfg.Name, cfg.ID)

	// 2. Initialize Agent Service
	registry := make(map[string]ai.Agent[map[string]any])

	// Helper to initialize an agent from a config
	initAgent := func(agentCfg agent.Config) (ai.Agent[map[string]any], error) {
		// Ensure absolute path for storage
		if agentCfg.StoragePath != "" {
			if absPath, err := filepath.Abs(agentCfg.StoragePath); err == nil {
				agentCfg.StoragePath = absPath
			}
		}

		// Initialize the agent with the shared registry
		return agent.NewFromConfig(context.Background(), agentCfg, agent.Dependencies{
			AgentRegistry: registry,
		})
	}

	// Pre-register internal policy agents so they aren't treated as external dependencies
	for _, pCfg := range cfg.Policies {
		if pCfg.ID != "" {
			// We register a placeholder here. The actual agent will be created in NewFromConfig.
			// This prevents the dependency loader from trying to find a file for it.
			// We use NewPolicyAgent with nil to create a valid placeholder
			registry[pCfg.ID] = agent.NewPolicyAgent(pCfg.ID, nil, nil)
		}
	}

	// Register locally defined agents (from "agents" block)
	for _, localAgentCfg := range cfg.Agents {
		if localAgentCfg.ID == "" {
			continue
		}
		if _, exists := registry[localAgentCfg.ID]; exists {
			continue
		}
		fmt.Printf("Initializing local agent: %s...\n", localAgentCfg.ID)
		svc, err := initAgent(localAgentCfg)
		if err != nil {
			panic(fmt.Errorf("failed to initialize local agent %s: %w", localAgentCfg.ID, err))
		}
		registry[localAgentCfg.ID] = svc
	}

	// Check if this agent needs an embedder agent
	if cfg.Embedder.Type == "agent" && cfg.Embedder.AgentID != "" {
		agentID := cfg.Embedder.AgentID
		if _, exists := registry[agentID]; !exists {
			fmt.Printf("Loading dependency agent: %s...\n", agentID)

			// Try to find the dependency config relative to the current config
			configDir := filepath.Dir(*configPath)
			depConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.json", agentID))

			// Fallback to default location if not found
			if _, err := os.Stat(depConfigPath); os.IsNotExist(err) {
				depConfigPath = fmt.Sprintf("ai/data/%s.json", agentID)
			}

			depCfg, err := agent.LoadConfigFromFile(depConfigPath)
			if err != nil {
				panic(fmt.Errorf("failed to load dependency agent %s: %w", agentID, err))
			}

			// Apply Stub Mode override
			if *stubMode {
				depCfg.StubMode = true
			}

			svc, err := initAgent(*depCfg)
			if err != nil {
				panic(fmt.Errorf("failed to initialize dependency agent %s: %w", agentID, err))
			}
			registry[agentID] = svc
		}
	}

	// Check for pipeline dependencies
	for _, step := range cfg.Pipeline {
		agentID := step.Agent.ID
		if _, exists := registry[agentID]; exists {
			continue
		}

		// Case 1: Inline Config
		if step.Agent.Config != nil {
			fmt.Printf("Initializing inline pipeline agent: %s...\n", agentID)
			// Apply Stub Mode override
			if *stubMode {
				step.Agent.Config.StubMode = true
			}
			svc, err := initAgent(*step.Agent.Config)
			if err != nil {
				panic(fmt.Errorf("failed to initialize inline pipeline agent %s: %w", agentID, err))
			}
			registry[agentID] = svc
			continue
		}

		// Case 2: Load from file
		fmt.Printf("Loading pipeline agent: %s...\n", agentID)

		// Try to find the dependency config relative to the current config
		configDir := filepath.Dir(*configPath)
		depConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.json", agentID))

		// Fallback to default location if not found
		if _, err := os.Stat(depConfigPath); os.IsNotExist(err) {
			depConfigPath = fmt.Sprintf("ai/data/%s.json", agentID)
		}

		depCfg, err := agent.LoadConfigFromFile(depConfigPath)
		if err != nil {
			panic(fmt.Errorf("failed to load pipeline agent %s: %w", agentID, err))
		}

		// Apply Stub Mode override
		if *stubMode {
			depCfg.StubMode = true
		}

		svc, err := initAgent(*depCfg)
		if err != nil {
			panic(fmt.Errorf("failed to initialize pipeline agent %s: %w", agentID, err))
		}
		registry[agentID] = svc
	}

	deps := agent.Dependencies{
		AgentRegistry: registry,
	}

	if cfg.StoragePath != "" {
		dbPath := filepath.Join(cfg.StoragePath, "db")
		dbType := sop.Standalone
		if cfg.DBType == "clustered" {
			dbType = sop.Clustered
		}
		dbOpts := sop.DatabaseOptions{
			StoresFolders: []string{dbPath},
			Type:          dbType,
		}
		sysDB := database.NewDatabase(dbOpts)
		deps.SystemDB = sysDB
		deps.Databases = map[string]sop.DatabaseOptions{
			"default": dbOpts,
		}
	}

	svc, err := agent.NewFromConfig(context.Background(), *cfg, deps)
	if err != nil {
		panic(fmt.Errorf("failed to initialize agent: %w", err))
	}

	// 3. Interactive Loop
	assistantName := cfg.AssistantName
	if assistantName == "" {
		assistantName = "AI Copilot"
	}
	fmt.Printf("\n%s:\n", assistantName)
	fmt.Println(cfg.Description)
	fmt.Println("Type 'exit' to quit.")

	if err := agent.RunLoop(context.Background(), svc, os.Stdin, os.Stdout, cfg.UserPrompt, cfg.AssistantName, ai.NewConfigMap()); err != nil {
		fmt.Printf("Error during session: %v\n", err)
	}
}
