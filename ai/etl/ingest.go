package etl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/search"
	"github.com/sharedcode/joltrin/v5"
)

// IngestAgent performs the ETL process for a specific agent configuration.
func IngestAgent(ctx context.Context, configPath, dataFile, targetAgentID string) error {
	// 1. Load Configuration
	rootCfg, err := agent.LoadConfigFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Determine which config to use
	var cfg *agent.Config
	if targetAgentID != "" {
		// Look for the agent in the local agents list
		for _, a := range rootCfg.Agents {
			if a.ID == targetAgentID {
				cfg = &a
				break
			}
		}
		if cfg == nil {
			// Check if the root config itself matches
			if rootCfg.ID == targetAgentID {
				cfg = rootCfg
			} else {
				return fmt.Errorf("agent '%s' not found in configuration file", targetAgentID)
			}
		}
	} else {
		cfg = rootCfg
	}

	// Ensure absolute path for storage
	if cfg.StoragePath != "" {
		if absPath, err := filepath.Abs(cfg.StoragePath); err == nil {
			cfg.StoragePath = absPath
		}
	}

	fmt.Printf("Starting ETL for Agent: %s (%s)...\n", cfg.Name, cfg.ID)
	fmt.Printf("Target Storage: %s\n", cfg.StoragePath)

	// 2. Initialize Infrastructure (Embedder & Vector DB)
	deps := agent.Dependencies{
		AgentRegistry: make(map[string]ai.Agent[map[string]any]),
	}

	// If the embedder is an agent, we need to load it.
	if cfg.Embedder.Type == "agent" {
		targetID := cfg.Embedder.AgentID
		fmt.Printf("Loading embedder agent: %s...\n", targetID)

		var depSvc ai.Agent[map[string]any]
		var depErr error

		// 1. Check if the dependency is defined inline in the root config
		var depCfg *agent.Config
		for _, a := range rootCfg.Agents {
			if a.ID == targetID {
				depCfg = &a
				break
			}
		}

		if depCfg != nil {
			// Found inline, initialize it
			depSvc, depErr = agent.NewFromConfig(ctx, *depCfg, agent.Dependencies{AgentRegistry: make(map[string]ai.Agent[map[string]any])})
		} else {
			// 2. Fallback to looking for a separate file
			configDir := filepath.Dir(configPath)
			depConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.json", targetID))
			if _, err := os.Stat(depConfigPath); os.IsNotExist(err) {
				depConfigPath = fmt.Sprintf("ai/data/%s.json", targetID)
			}

			if _, err := os.Stat(depConfigPath); err == nil {
				depCfgFromFile, err := agent.LoadConfigFromFile(depConfigPath)
				if err != nil {
					return fmt.Errorf("failed to load dependency agent config: %w", err)
				}
				depSvc, depErr = agent.NewFromConfig(ctx, *depCfgFromFile, agent.Dependencies{AgentRegistry: make(map[string]ai.Agent[map[string]any])})
			} else {
				// 3. Check if it's the root config itself
				if rootCfg.ID == targetID {
					depSvc, depErr = agent.NewFromConfig(ctx, *rootCfg, agent.Dependencies{AgentRegistry: make(map[string]ai.Agent[map[string]any])})
				} else {
					return fmt.Errorf("dependency agent '%s' not found in inline agents or as a file", targetID)
				}
			}
		}

		if depErr != nil {
			return fmt.Errorf("failed to initialize dependency agent: %w", depErr)
		}
		deps.AgentRegistry[targetID] = depSvc
	}

	_, db, storeName, vCfg, err := agent.SetupInfrastructure(ctx, *cfg, deps)
	if err != nil {
		return fmt.Errorf("failed to setup infrastructure: %w", err)
	}

	// Start Transaction
	if db == nil {
		return fmt.Errorf("database is nil (requirements not met for agent '%s'?)", cfg.ID)
	}
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	idx, err := vector.Open[map[string]any](ctx, tx, storeName, vCfg)
	if err != nil {
		return fmt.Errorf("failed to open index: %w", err)
	}

	// Open Text Index (if required)
	var textIdx *search.Index
	// Text Search is implicitly enabled if we are doing data ingestion here
	searchEnabled := true
	if val, ok := cfg.Params["search"]; ok {
		if b, ok := val.(bool); ok {
			searchEnabled = b
		}
	}

	if searchEnabled {
		textIdx, err = search.NewIndex(ctx, db.Options(), tx, storeName)
		if err != nil {
			return fmt.Errorf("failed to open text index: %w", err)
		}
	}

	// 3. Load Data & Process in Batches
	batchSize := 200
	totalProcessed := 0

	processBatch := func(batch []agent.DataItem) error {
		if len(batch) == 0 {
			return nil
		}
		fmt.Printf("Processing batch of %d items (Total: %d)...\n", len(batch), totalProcessed+len(batch))

		items := make([]ai.Item[map[string]any], len(batch))
		for i, item := range batch {
			// Generate deterministic ID based on content if not provided or if we want to enforce it
			// We use the same hashing logic as in agent/ingest.go to ensure consistency
			contentToEmbed := fmt.Sprintf("%s %s", item.Text, item.Description)
			id := agent.HashString(contentToEmbed)

			items[i] = ai.Item[map[string]any]{
				ID:     id,
				Vector: nil, // We do not auto vectorize on import or Ingest
				Payload: map[string]any{
					"text":        item.Text,
					"description": item.Description,
					"original_id": item.ID, // Keep original ID in payload
				},
			}

			// Index Text
			if textIdx != nil {
				if err := textIdx.Add(ctx, id, contentToEmbed); err != nil {
					return fmt.Errorf("failed to index text for item %s: %w", id, err)
				}
			}
		}

		if err := idx.UpsertBatch(ctx, items); err != nil {
			return fmt.Errorf("failed to upsert batch: %w", err)
		}
		totalProcessed += len(batch)
		return nil
	}

	if dataFile != "" {
		fmt.Printf("Streaming data from %s...\n", dataFile)
		f, err := os.Open(dataFile)
		if err != nil {
			return fmt.Errorf("failed to open data file: %w", err)
		}
		defer f.Close()

		dec := json.NewDecoder(f)

		// Read opening bracket
		t, err := dec.Token()
		if err != nil {
			return fmt.Errorf("failed to read opening token: %w", err)
		}
		if delim, ok := t.(json.Delim); !ok || delim != '[' {
			return fmt.Errorf("expected JSON array of items")
		}

		var batch []agent.DataItem
		for dec.More() {
			var item agent.DataItem
			if err := dec.Decode(&item); err != nil {
				return fmt.Errorf("failed to decode item: %w", err)
			}
			batch = append(batch, item)

			if len(batch) >= batchSize {
				if err := processBatch(batch); err != nil {
					return err
				}
				batch = batch[:0] // Clear batch
			}
		}

		// Process remaining
		if len(batch) > 0 {
			if err := processBatch(batch); err != nil {
				return err
			}
		}

		// Read closing bracket
		_, err = dec.Token()
		if err != nil {
			return fmt.Errorf("failed to read closing token: %w", err)
		}

	} else {
		fmt.Println("Using data from configuration file...")
		if len(cfg.Data) == 0 {
			fmt.Println("No data to ingest.")
			// We still commit if we opened a transaction, though it's empty.
		} else {
			// Process config data in batches too
			for i := 0; i < len(cfg.Data); i += batchSize {
				end := i + batchSize
				if end > len(cfg.Data) {
					end = len(cfg.Data)
				}
				if err := processBatch(cfg.Data[i:end]); err != nil {
					return err
				}
			}
		}
	}

	// Commit
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 5. Verify (New Transaction)
	tx2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		fmt.Printf("Warning: Failed to verify count (transaction error): %v\n", err)
		return nil
	}
	defer tx2.Rollback(ctx)

	idx2, err := vector.Open[map[string]any](ctx, tx2, storeName, vCfg)
	if err != nil {
		fmt.Printf("Warning: Failed to verify count (open error): %v\n", err)
		return nil
	}

	count, _ := idx2.Count(ctx)
	fmt.Printf("ETL Complete. Total items in DB: %d\n", count)
	return nil
}
