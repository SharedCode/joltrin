package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	log "log/slog"
	"path/filepath"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/domain"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/generator"
	"github.com/sharedcode/joltrin/ai/policy"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
)

// Dependencies holds external dependencies required for agent creation.
type Dependencies struct {
	AgentRegistry map[string]ai.Agent[map[string]any]
	SystemDB      *database.Database
	Databases     map[string]sop.DatabaseOptions
}

// HashString generates a deterministic hash for the given string.
func HashString(s string) string {
	h := fnv.New64a()
	io.WriteString(h, s)
	return fmt.Sprintf("%x", h.Sum64())
}

func prewarmGeneratorCache(ctx context.Context, gen ai.Generator, systemPrompt string, tools []ai.ToolDefinition) {
	if gen == nil {
		return
	}

	if systemPrompt == "" && len(tools) == 0 {
		return
	}

	if err := gen.PrewarmCache(ctx, ai.GenOptions{
		SystemPrompt: systemPrompt,
		Tools:        tools,
	}); err != nil {
		log.Warn("generator cache prewarm failed", "generator", gen.Name(), "error", err)
	}
}

// SetupInfrastructure initializes the Embedder and Vector Index based on the configuration.
func SetupInfrastructure(ctx context.Context, cfg Config, deps Dependencies) (ai.Embeddings, *database.Database, string, vector.Config, error) {
	// 1. Initialize Embedder
	var emb ai.Embeddings

	switch cfg.Embedder.Type {
	case "agent":
		agent, ok := deps.AgentRegistry[cfg.Embedder.AgentID]
		if !ok {
			return nil, nil, "", vector.Config{}, fmt.Errorf("embedder agent '%s' not found in registry", cfg.Embedder.AgentID)
		}
		// Use a simple base embedder for the vectors, but the agent will expand the text first
		baseEmb := embed.NewSimple(cfg.ID+"-base-embed", 1024, nil)
		emb = embed.NewAgentEmbedder(agent, baseEmb, cfg.Embedder.Instruction)

	case "ollama":
		baseURL, _ := cfg.Embedder.Options["base_url"].(string)
		model, _ := cfg.Embedder.Options["model"].(string)
		emb = embed.NewOllama(baseURL, model)
	case "gemini":
		apiKey, _ := cfg.Embedder.Options["api_key"].(string)
		model, _ := cfg.Embedder.Options["model"].(string)
		emb = embed.NewGemini(apiKey, model)
	case "local":
		modelPath, _ := cfg.Embedder.Options["model_path"].(string)
		if modelPath == "" {
			modelPath, _ = cfg.Embedder.Options["model"].(string)
		}
		providerName, _ := cfg.Embedder.Options["provider"].(string)
		gpuLayers := 0
		switch v := cfg.Embedder.Options["gpu_layers"].(type) {
		case float64:
			gpuLayers = int(v)
		case int:
			gpuLayers = v
		case int32:
			gpuLayers = int(v)
		case int64:
			gpuLayers = int(v)
		}
		var err error
		emb, err = embed.NewLocalWithProvider(providerName, modelPath, gpuLayers)
		if err != nil {
			return nil, nil, "", vector.Config{}, err
		}
	default:
		// Default: Simple Embedder with domain-specific synonyms
		// We use a higher dimensionality (1024) to reduce collisions in the simple hash embedder
		emb = embed.NewSimple(cfg.ID+"-embed", 1024, cfg.Synonyms)
	}

	// 2. Initialize Vector Database
	storagePath := cfg.StoragePath
	if storagePath != "" {
		// Ensure absolute path to avoid duplication issues with relative paths
		if absPath, err := filepath.Abs(storagePath); err == nil {
			storagePath = absPath
		}
	}

	// Determine Database Type
	var dbType sop.DatabaseType
	switch cfg.DBType {
	case "clustered":
		dbType = sop.Clustered
	case "standalone":
		dbType = sop.Standalone
	default:
		dbType = sop.Standalone
	}

	// Vector database does not support Replication disk structure, ignore error.
	// We append a dummy segment because the underlying SOP library seems to treat the path
	// as a base directory and creates folders alongside it if we don't nest it.
	// By appending "db", we ensure the actual data folders are created inside 'storagePath'.
	dbPath := filepath.Join(storagePath, "db")
	db := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		Type:          dbType,
	})

	vCfg := vector.Config{
		UsageMode:             ai.BuildOnceQueryMany, // Default
		EnableIngestionBuffer: cfg.EnableIngestionBuffer,
		TransactionOptions: sop.TransactionOptions{
			StoresFolders: []string{dbPath},
			CacheType:     db.CacheType(),
		},
		Cache: db.Cache(),
	}

	if cfg.ContentSize != "" {
		switch cfg.ContentSize {
		case "small":
			vCfg.ContentSize = sop.SmallData
		case "medium":
			vCfg.ContentSize = sop.MediumData
		case "big":
			vCfg.ContentSize = sop.BigData
		}
	}

	return emb, db, cfg.ID, vCfg, nil
}

// NewFromConfig creates and initializes a new Agent Service based on the provided configuration.
// It handles infrastructure setup (Embedder, VectorDB).
func NewFromConfig(ctx context.Context, cfg Config, deps Dependencies) (ai.Agent[map[string]any], error) {
	// Handle specialized agent types
	switch cfg.Type {
	case ai.AgentTypeCopilot:
		return NewCopilotAgent(cfg, deps.Databases, deps.SystemDB), nil
	// Add other types here
	case "standard", "":
		// Fallthrough to standard service creation
	default:
		// For now, default to standard service if unknown, or could return error
		// return nil, fmt.Errorf("unknown agent type: %s", cfg.Type)
	}

	// 1. Initialize Infrastructure
	emb, db, storeName, vCfg, err := SetupInfrastructure(ctx, cfg, deps)
	if err != nil {
		return nil, err
	}

	// Initialize Generator (LLM)
	var gen ai.Generator
	if cfg.Generator.Type != "" {
		var err error
		// Pass global obfuscation setting to generator options
		if enableObf, _ := cfg.Params["enable_obfuscation"].(bool); enableObf {
			if cfg.Generator.Options == nil {
				cfg.Generator.Options = make(map[string]any)
			}
			cfg.Generator.Options["enable_obfuscation"] = true
		}
		gen, err = generator.New(cfg.Generator.Type, cfg.Generator.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize generator: %w", err)
		}
	}

	// Policies & Classifier
	var pol ai.PolicyEngine
	var class ai.Classifier

	// Create a local registry for policy agents and sub-agents
	policyRegistry := make(map[string]ai.Agent[map[string]any])

	// Initialize Sub-Agents defined in Config
	for _, subCfg := range cfg.Agents {
		// Recursively create sub-agent
		// We pass the current dependencies, but we might need to handle circular deps if any
		subAgent, err := NewFromConfig(ctx, subCfg, deps)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize sub-agent '%s': %w", subCfg.ID, err)
		}
		policyRegistry[subCfg.ID] = subAgent
	}

	for _, pCfg := range cfg.Policies {
		if pCfg.Type == "profanity" {
			// Use configured max strikes, default to 3 if 0
			strikes := pCfg.MaxStrikes
			if strikes <= 0 {
				strikes = 3
			}
			// Create the policy engine and classifier
			pEngine, pClass := policy.NewProfanityGuardrail(strikes)

			// If this is the "main" policy (no ID or first one), use it for the domain
			if pol == nil {
				pol = pEngine
				class = pClass
			}

			// If an ID is provided, register it as a PolicyAgent
			if pCfg.ID != "" {
				policyAgent := NewPolicyAgent(pCfg.ID, pEngine, pClass)
				policyRegistry[pCfg.ID] = policyAgent
			}
		}
	}

	// Merge policy registry into the main registry
	// We create a new map to avoid modifying the passed dependencies
	fullRegistry := make(map[string]ai.Agent[map[string]any])
	for k, v := range deps.AgentRegistry {
		fullRegistry[k] = v
	}
	for k, v := range policyRegistry {
		fullRegistry[k] = v
	}

	// 2. Create Domain
	dom := domain.NewGenericDomain(domain.Config[map[string]any]{
		ID:         cfg.ID,
		Name:       cfg.Name,
		DB:         db,
		StoreName:  storeName,
		StoreCfg:   vCfg,
		Embedder:   emb,
		Policy:     pol,
		Classifier: class,
		Prompts: map[string]string{
			"system": cfg.SystemPrompt,
		},
	})

	// 3. Ingest Data (if present)
	if len(cfg.Data) > 0 {
		texts := make([]string, len(cfg.Data))
		for i, item := range cfg.Data {
			// Combine text and description for richer embedding context
			texts[i] = fmt.Sprintf("%s %s", item.Text, item.Description)
		}

		vecs, err := embed.DocumentTexts(ctx, emb, texts)
		if err != nil {
			return nil, fmt.Errorf("failed to embed seed data: %w", err)
		}

		items := make([]ai.Item[map[string]any], len(cfg.Data))
		for i, item := range cfg.Data {
			items[i] = ai.Item[map[string]any]{
				ID:     item.ID,
				Vector: vecs[i],
				Payload: map[string]any{
					"text":        item.Text,
					"description": item.Description,
				},
			}
		}

		// Start Transaction for Ingestion
		tx, err := dom.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return nil, fmt.Errorf("failed to begin transaction: %w", err)
		}
		// Ensure rollback on error, but commit on success
		defer tx.Rollback(ctx)

		idx, err := dom.Index(ctx, tx)
		if err != nil {
			return nil, fmt.Errorf("failed to open index: %w", err)
		}

		if err := idx.UpsertBatch(ctx, items); err != nil {
			return nil, fmt.Errorf("failed to ingest seed data: %w", err)
		}

		// Text Indexing
		textIdx, err := dom.TextIndex(ctx, tx)
		if err != nil {
			return nil, fmt.Errorf("failed to open text index: %w", err)
		}
		for i, item := range cfg.Data {
			// Index the combined text
			if err := textIdx.Add(ctx, item.ID, texts[i]); err != nil {
				return nil, fmt.Errorf("failed to index text for item %s: %w", item.ID, err)
			}
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("failed to commit ingestion: %w", err)
		}
	}

	// 4. Create Agent Service
	// If the generator is "copilot" and obfuscation is enabled, the generator handles it internally.
	// Therefore, we disable Service-level obfuscation to avoid double-obfuscation.
	enableObf, _ := cfg.Params["enable_obfuscation"].(bool)
	serviceObfuscation := enableObf
	if cfg.Generator.Type == ai.AgentTypeCopilot && enableObf {
		serviceObfuscation = false
	}

	svc := NewService(dom, deps.SystemDB, deps.Databases, gen, cfg.Pipeline, fullRegistry, serviceObfuscation)
	prewarmGeneratorCache(ctx, gen, cfg.SystemPrompt, nil)

	return svc, nil
}
