package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/generator"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/ai/obfuscation"
	"github.com/sharedcode/joltrin/search"
)

const (
	// KnowledgeStore is the name of the B-tree store used to persist learned rules, vocabulary, and corrections.
	// This store resides in the System Database configured for the agent.
	KnowledgeStore = "memory"
	// MRUKnowledgeStore is the name of the store that tracks "active" or "relevant" knowledge categories/items.
	// This acts as a Working Memory Index, telling the agent what to pre-load from Long Term Memory.
	// Keys are "{Category}/{Name}" or just "{Category}", Values are Timestamps/Scores.
	MRUKnowledgeStore = "mru_knowledge"
	// KnowledgeRefreshDuration is the interval at which the agent refreshes its local view of the persistent knowledge.
	KnowledgeRefreshDuration = 5 * time.Minute
)

// Service is a generic agent service that operates on any Domain.
type Service struct {
	domain            ai.Domain[map[string]any]
	systemDB          *database.Database
	databases         map[string]sop.DatabaseOptions
	generator         ai.Generator // The LLM (Gemini, etc.)
	pipeline          []PipelineStep
	registry          map[string]ai.Agent[map[string]any]
	EnableObfuscation bool
	// Feature Flags
	EnableHistoryInjection bool
	EnableShortTermMemory  bool

	// Session State
	session *RunnerSession

	// Refresh Tracker
	lastKnowledgeRefresh map[string]time.Time
}

// Check that Service implements ScriptRecorder
var _ ai.ScriptRecorder = (*Service)(nil)

// NewService creates a new agent service for a specific domain.
func NewService(domain ai.Domain[map[string]any], systemDB *database.Database, databases map[string]sop.DatabaseOptions, generator ai.Generator, pipeline []PipelineStep, registry map[string]ai.Agent[map[string]any], enableObfuscation bool) *Service {
	return &Service{
		domain:                 domain,
		systemDB:               systemDB,
		databases:              databases,
		generator:              generator,
		pipeline:               pipeline,
		registry:               registry,
		EnableObfuscation:      enableObfuscation,
		EnableHistoryInjection: false, // Default to OFF for simple machine mode (can be enabled via config)
		session:                NewRunnerSession(),
		lastKnowledgeRefresh:   make(map[string]time.Time),
	}
}

// ListTools exposes the registered tool definitions for the service-backed runtime path.
func (s *Service) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	if s == nil {
		return nil, nil
	}
	return (&ServiceToolExecutor{s: s}).ListTools(ctx)
}

// Clone creates a new isolated instance of the agent sharing read-only components.
func (s *Service) RunnerSession() *RunnerSession {
	if s == nil {
		return nil
	}
	return s.session
}

func (s *Service) Clone() ai.Agent[map[string]any] {
	clonedRegistry := make(map[string]ai.Agent[map[string]any])
	for k, v := range s.registry {
		if cloneable, ok := v.(interface {
			Clone() ai.Agent[map[string]any]
		}); ok {
			clonedRegistry[k] = cloneable.Clone()
		} else {
			clonedRegistry[k] = v // shallow fallback
		}
	}

	clone := &Service{
		domain:                 s.domain,
		systemDB:               s.systemDB,
		databases:              s.databases,
		generator:              s.generator,
		pipeline:               s.pipeline,
		registry:               clonedRegistry,
		EnableObfuscation:      s.EnableObfuscation,
		EnableHistoryInjection: s.EnableHistoryInjection,
		session:                NewRunnerSession(),
		lastKnowledgeRefresh:   make(map[string]time.Time),
	}

	// Inject back the service pointer to agents if they rely on it
	for _, v := range clone.registry {
		if da, ok := v.(*CopilotAgent); ok {
			da.service = clone
		}
	}

	return clone
}

// SetFeature allows toggling of agent features at runtime.
func (s *Service) SetFeature(feature string, enabled bool) {
	switch feature {
	case "active_memory":
		s.EnableShortTermMemory = enabled
	case "history_injection":
		s.EnableHistoryInjection = enabled
	case "obfuscation":
		s.EnableObfuscation = enabled
	}
}

// ServiceAskOptions contains explicit, typed parameters for Service.Ask().
// This is the user-facing struct with IDE support, type safety, and clear documentation.
// It gets converted from generic ConfigMap at the Agent interface boundary.
//
// Example usage:
//
//	// Working with the Agent interface (generic):
//	cfg := ai.NewConfigMap()
//	cfg.Set("database", myDB)
//	cfg.Set("payload", sessionPayload)
//	response, err := agent.Ask(ctx, query, cfg)
//
//	// Working with Service directly (explicit, typed):
//	opts := &ServiceAskOptions{
//	    Database: myDB,
//	    Payload:  sessionPayload,
//	    Verbose:  true,
//	}
//	// Convert to ConfigMap for Agent interface
//	cfg := optsToConfigMap(opts)
//	response, err := service.Ask(ctx, query, cfg)
type ServiceAskOptions struct {
	// Database to use for this query (overrides session default)
	Database *database.Database

	// Payload contains session state: current DB, selected KBs, transaction, variables, etc.
	Payload *ai.SessionPayload

	// Executor for running tool calls during ReAct loops (commands, queries, scripts)
	Executor ai.ToolExecutor

	// Writer for streaming output in real-time (optional)
	Writer io.Writer

	// Recorder captures script steps for playback or audit (optional)
	Recorder ai.ScriptRecorder

	// DefaultFormat for query results: "csv", "json", "table", etc.
	DefaultFormat string

	// EventStreamer receives structured events: tool calls, progress, errors (optional)
	EventStreamer func(string, any)

	// ProgressSink receives progress messages during long operations (optional)
	ProgressSink func(string)

	// Generator optionally overrides the default LLM provider
	Generator ai.Generator

	// ProviderDetails for runtime provider configuration (API key, base URL, model)
	ProviderDetails *ProviderDetails

	// IsNewTopic indicates this query starts a new conversation topic
	IsNewTopic bool

	// ForcedDBName overrides database selection logic (internal use)
	ForcedDBName string
}

// FromConfigMap converts generic ConfigMap to typed ServiceAskOptions.
// This is the conversion layer between the Agent interface and implementation.
func (s *Service) optsFromConfigMap(cfg *ai.ConfigMap) *ServiceAskOptions {
	opts := &ServiceAskOptions{}

	if cfg == nil {
		return opts
	}

	// Extract each field with proper type assertions
	if val, ok := cfg.Get("database"); ok {
		if db, ok := val.(*database.Database); ok {
			opts.Database = db
		} else if dbName, ok := val.(string); ok && dbName != "" {
			// Resolve database by name
			if dbOpts, exists := s.databases[dbName]; exists {
				opts.Database = database.NewDatabase(dbOpts)
			}
		}
	}

	if val, ok := cfg.Get("payload"); ok {
		if p, ok := val.(*ai.SessionPayload); ok {
			opts.Payload = p
		}
	}

	if val, ok := cfg.Get("executor"); ok {
		if exec, ok := val.(ai.ToolExecutor); ok {
			opts.Executor = exec
		}
	}

	if val, ok := cfg.Get("writer"); ok {
		if w, ok := val.(io.Writer); ok {
			opts.Writer = w
		}
	}

	if val, ok := cfg.Get("recorder"); ok {
		if rec, ok := val.(ai.ScriptRecorder); ok {
			opts.Recorder = rec
		}
	}

	if val, ok := cfg.Get("default_format"); ok {
		if format, ok := val.(string); ok {
			opts.DefaultFormat = format
		}
	}

	if val, ok := cfg.Get("event_streamer"); ok {
		if streamer, ok := val.(func(string, any)); ok {
			opts.EventStreamer = streamer
		}
	}

	if val, ok := cfg.Get("progress_sink"); ok {
		if sink, ok := val.(func(string)); ok {
			opts.ProgressSink = sink
		}
	}

	if val, ok := cfg.Get("generator"); ok {
		if gen, ok := val.(ai.Generator); ok {
			opts.Generator = gen
		}
	}

	if val, ok := cfg.Get("provider_details"); ok {
		if po, ok := val.(*ProviderDetails); ok {
			opts.ProviderDetails = po
		}
	}

	if val, ok := cfg.Get("is_new_topic"); ok {
		if v, ok := val.(bool); ok {
			opts.IsNewTopic = v
		}
	}

	if val, ok := cfg.Get("forced_db_name"); ok {
		if name, ok := val.(string); ok {
			opts.ForcedDBName = name
		}
	}

	return opts
}

// AskRequest contains all dependencies for an Ask operation, making the data flow explicit.
// This replaces the previous pattern of hiding dependencies in context.Context.
type AskRequest struct {
	// Query is the user's input question or command
	Query string

	// Session holds the current session state including transactions, variables, and database context
	Session *ai.SessionPayload

	// Executor is the tool executor for running tool calls during ReAct loops
	Executor ai.ToolExecutor

	// Generator optionally overrides the default LLM provider
	Generator ai.Generator

	// ProviderOverride optionally provides runtime provider configuration (provider, model, API key, base URL)
	ProviderOverride *ProviderDetails

	// Database optionally overrides the session's current database
	Database *database.Database

	// Writer is the output destination for streaming responses
	Writer io.Writer

	// EventStreamer receives structured events during reasoning (tool calls, progress, etc.)
	EventStreamer func(eventType string, data any)

	// ProgressSink receives progress messages during execution
	ProgressSink func(message string)

	// ScriptRecorder captures executed script steps for playback or audit
	ScriptRecorder ai.ScriptRecorder

	// DefaultFormat sets the default output format for tools (csv, json, etc.)
	DefaultFormat string

	// Options carries additional configuration from ConfigMap
	Options *ai.ConfigMap
}

// AskResponse contains the result and any state changes from an Ask operation.
type AskResponse struct {
	// FinalText is the assistant's answer to the user
	FinalText string

	// UpdatedSession contains any session state changes (updated transactions, variables, etc.)
	UpdatedSession *ai.SessionPayload

	// CarryoverState holds provider-specific continuation state for next Ask
	CarryoverState *ai.CarryoverState

	// ToolCalls lists all tools executed during the Ask for audit/logging
	ToolCalls []ai.ToolCall

	// OutcomeFacts contains compact grounded facts safe to carry into MRU continuity
	OutcomeFacts []string

	// OutcomeRecipes contains learned patterns distilled from this Ask
	OutcomeRecipes []ai.LearnedRecipe
}

// ToolExecutionContext carries all dependencies needed for tool execution.
// This replaces the pattern of hiding tool dependencies in context.Context via multiple context keys.
// Affects Phase 2 context keys: CtxKeyExecutor, CtxKeyScriptRecorder, CtxKeyWriter,
// CtxKeyResultStreamer, CtxKeyNativeToolHints
type ToolExecutionContext struct {
	// Session holds the current session state for tools that need access to variables or state
	Session *ai.SessionPayload

	// Executor is the tool executor (may be nested/chained)
	Executor ai.ToolExecutor

	// Recorder captures script steps during execution for playback/audit
	Recorder ai.ScriptRecorder

	// Writer is the output destination for streaming tool results
	Writer io.Writer

	// ResultStreamer enables structured streaming output for tools (BeginArray, WriteItem, EndArray)
	ResultStreamer ai.ResultStreamer

	// NativeToolHints indicates this is native Ask-loop tool execution that can consume structured hints
	NativeToolHints bool

	// Database is the target database for script/tool execution
	Database *database.Database

	// EventStreamer receives structured events during tool execution
	EventStreamer func(eventType string, data any)

	// ProgressSink receives progress messages
	ProgressSink func(message string)
}

// ScriptRunContext carries all dependencies needed for script execution orchestration.
// This replaces the pattern of hiding script orchestration state in context.Context.
// Affects Phase 3 context keys: CtxKeyJSONStreamer, CtxKeySuppressInternalStepStart,
// "step_index", "verbose", CtxKeyUseNDJSON, CtxKeyCurrentScriptCategory
type ScriptRunContext struct {
	// JSONStreamer handles streaming JSON array elements for script execution output
	JSONStreamer *JSONStreamer

	// SuppressInternalStepStart suppresses step_start events in streamed output
	SuppressInternalStepStart bool

	// StepIndex tracks the current step number in script execution
	StepIndex int

	// Verbose enables detailed logging and progress reporting for script steps
	Verbose bool

	// UseNDJSON indicates whether to use newline-delimited JSON format instead of JSON array
	UseNDJSON bool

	// CurrentScriptCategory tracks the category of the currently executing script
	CurrentScriptCategory string

	// StringBuilderMutex protects concurrent writes to the script output string builder
	StringBuilderMutex *sync.Mutex
}

// ProviderDetails carries runtime provider configuration for generator selection.
type ProviderDetails struct {
	// Provider specifies which LLM provider to use (e.g., "gemini", "chatgpt", "claude")
	Provider string

	// Model optionally specifies a specific model within the provider (e.g., "gemini-2.0-flash-thinking-exp")
	Model string

	// APIKey provides a transient API key override for the provider. Typed as
	// ai.Secret so it can't be logged in cleartext by accident; unwrap with
	// string(...) only at the point it's handed to the actual HTTP client.
	APIKey ai.Secret

	// BaseURL provides a transient base URL override for the provider
	BaseURL string
}

// TopicAssessment is the structure returned by the generic router.
type TopicAssessment struct {
	IsNewTopic    bool   `json:"is_new_topic"`
	TopicUUID     string `json:"topic_uuid,omitempty"` // If not new, the UUID of the existing graph
	NewTopicLabel string `json:"new_topic_label,omitempty"`
	Reasoning     string `json:"reasoning"`
}

// identifyTopic determines if the query belongs to an existing conversation graph or starts a new one.
func (s *Service) resolveTopicRoutingGenerator(ctx context.Context) ai.Generator {
	gen := s.generator

	providerOverride := extractProviderOverrideFromContext(ctx)
	if providerOverride != nil && providerOverride.Provider != "" {
		provider := providerOverride.Provider
		if provider == "openai" {
			provider = "chatgpt"
		}

		configMap := make(map[string]any)
		if providerOverride.APIKey != "" {
			configMap["api_key"] = string(providerOverride.APIKey)
		}
		if providerOverride.BaseURL != "" {
			configMap["base_url"] = providerOverride.BaseURL
		}
		if providerOverride.Model != "" {
			configMap["model"] = providerOverride.Model
		}

		if gen == nil || gen.Name() != provider {
			if overriddenGen, err := generator.New(provider, configMap); err == nil {
				gen = overriddenGen
			} else {
				log.Warn("Failed to initialize topic-routing provider, falling back to default generator",
					"provider", logsafe.V(provider), "error", logsafe.V(err))
			}
		}
	}

	return gen
}

func (s *Service) identifyTopic(ctx context.Context, query string) (*TopicAssessment, error) {
	if s == nil || s.session == nil || s.session.Memory == nil || len(s.session.Memory.Threads) == 0 {
		return &TopicAssessment{IsNewTopic: true, Reasoning: "No history exists."}, nil
	}

	gen := s.resolveTopicRoutingGenerator(ctx)
	if gen == nil {
		log.Warn("Topic routing generator unavailable; defaulting to a new topic", "query", logsafe.V(query))
		return &TopicAssessment{IsNewTopic: true, Reasoning: "No generator available for topic routing"}, nil
	}

	// Prepare list of recent topics
	var summaries []string
	for i := len(s.session.Memory.Order) - 1; i >= 0; i-- {
		id := s.session.Memory.Order[i]
		thread := s.session.Memory.Threads[id]
		// Get last interaction
		lastMsg := ""
		if len(thread.Exchanges) > 0 {
			lastMsg = thread.Exchanges[len(thread.Exchanges)-1].Content
			if len(lastMsg) > 50 {
				lastMsg = lastMsg[:50] + "..."
			}
		}
		statusSuffix := ""
		if thread.Status == "concluded" {
			statusSuffix = " [CONCLUDED]"
		}
		summaries = append(summaries, fmt.Sprintf("- ID: %s | Label: %s%s | Last Msg: %s", thread.ID, thread.Label, statusSuffix, lastMsg))
	}
	topicsBlock := strings.Join(summaries, "\n")

	prompt := fmt.Sprintf(`You are a conversation manager. Analyze the User Query and decide if it is a follow-up to an existing topic or a new topic.
Existing Topics (Most Recent First):
%s

User Query: %q

Instructions:
1. If the query strictly refers to the context of a previous topic (e.g. "change it to blue", "what about the other one?"), select that Topic ID.
2. If the query starts a completely new subject, mark as New Topic.
3. Provide a JSON response.

Format:
{
  "is_new_topic": true/false,
  "topic_uuid": "UUID-STRING",
  "new_topic_label": "Short Label if new",
  "reasoning": "Short explanation"
}
`, topicsBlock, query)

	// Combine instructions into the prompt since GenOptions doesn't support SystemPrompt
	fullPrompt := "Answer in strict JSON.\n" + prompt

	output, err := gen.Generate(ctx, fullPrompt, ai.GenOptions{
		Temperature:   0.1,   // Deterministic
		ThinkingLevel: "low", // Strict JSON schema adherence for topic routing
	})
	if err != nil {
		return nil, err
	}

	// Sanitize JSON
	jsonStr := strings.TrimSpace(output.Text)
	jsonStr = strings.TrimPrefix(jsonStr, "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")

	var assessment TopicAssessment
	if err := json.Unmarshal([]byte(jsonStr), &assessment); err != nil {
		// Fallback if JSON fails
		log.Warn("Failed to parse topic assessment JSON", "error", err, "response", output.Text)
		return &TopicAssessment{IsNewTopic: true, Reasoning: "JSON parse failure"}, nil
	}

	return &assessment, nil
}

// Open initializes the agent service.
func (s *Service) Open(ctx context.Context) error {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return nil
	}

	// Delegate Open to all registered sub-agents so they can initialize (e.g. CopilotAgent memory/tools)
	openRegistryAgents := func() error {
		for _, subAgent := range s.registry {
			if err := subAgent.Open(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	// If we have an active session transaction (e.g. from a previous step in a stateful session), use it.
	// BUT ONLY if it matches the requested database.
	if s.session.CurrentDB == "" || s.session.CurrentDB == p.CurrentDB {
		p.Transaction = s.session.Transaction
		p.Variables = s.session.Variables
		return openRegistryAgents()
	}
	// If DB mismatch, we commit the previous transaction as we are switching context.
	if s.session.CurrentDB != "" && s.session.CurrentDB != p.CurrentDB {
		if p.ExplicitTransaction {
			log.Warn("Switching databases with an explicit transaction is not recommended. Committing the previous transaction before switching.", "from_db", s.session.CurrentDB, "to_db", logsafe.V(p.CurrentDB))
			// Since we are switching, we clear the explicit transaction flag & onto implicit.
			p.ExplicitTransaction = false
		}
		if s.session.Transaction != nil {
			// Commit the old transaction to persist changes
			if err := s.session.Transaction.Commit(ctx); err != nil {
				return fmt.Errorf("failed to commit previous transaction on database '%s' before switching to '%s': %w", s.session.CurrentDB, p.CurrentDB, err)
			}
		}
		// Clear the session transaction as we've committed it
		s.session.Transaction = nil
		s.session.Variables = nil
	}

	if p.CurrentDB == "" {
		return openRegistryAgents()
	}

	// Check if configured System DB matches
	var dbToOpen *database.Database
	if p.CurrentDB == SystemDBName && s.systemDB != nil {
		dbToOpen = s.systemDB
	} else if dbOpts, ok := s.databases[p.CurrentDB]; ok {
		dbToOpen = database.NewDatabase(dbOpts)
	}

	if dbToOpen != nil {
		// Start transaction
		tx, err := dbToOpen.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Errorf("failed to begin transaction on database '%s': %w", p.CurrentDB, err)
		}
		p.Transaction = tx
	} else {
		return fmt.Errorf("database '%s' not found in agent configuration", p.CurrentDB)
	}
	return openRegistryAgents()
}

// Close cleans up the agent service.
func (s *Service) Close(ctx context.Context) error {
	// Delegate Close to all registered sub-agents
	closeRegistryAgents := func() error {
		for _, subAgent := range s.registry {
			if err := subAgent.Close(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	// We no longer clear the Memory here to support cross-request short-term memory (Conversation Graphs).
	// The LRU limit (20 items) prevents unbounded growth.
	if s.session != nil {
		s.session.Variables = nil
		// s.session.CurrentScript = nil // Preserved for drafting across interactions
		// s.session.LastStep = nil // Preserved for /last-tool
		// s.session.LastInteractionToolCalls = nil // Preserved for /last-tool
		// s.session.PendingRefinement = nil // Preserved for /script refine
	}

	p := ai.GetSessionPayload(ctx)
	if p == nil || p.Transaction == nil {
		return closeRegistryAgents()
	}
	if tx, ok := p.Transaction.(sop.Transaction); ok {
		// Check if context was canceled - if so, rollback any uncommitted transaction
		if ctx.Err() != nil {
			log.Warn("Context canceled during Close - rolling back transaction", "error", ctx.Err())
			if tx.HasBegun() {
				// Use background context for rollback since original context is canceled
				tx.Rollback(context.Background())
			}
			p.Transaction = nil
			s.session.Transaction = nil
			s.session.Variables = nil
			return closeRegistryAgents()
		}

		// CRITICAL: ExplicitTransaction = true means language bindings manage the transaction.
		// Do NOT auto-commit or auto-rollback explicit transactions. They may span multiple requests.
		// Language bindings will call manage_transaction to commit/rollback explicitly.
		if p.ExplicitTransaction {
			log.Debug("Explicit transaction left open for external management", "db", logsafe.V(p.CurrentDB))
			// Do NOT clear p.Transaction - language bindings may reuse it across requests
			return closeRegistryAgents()
		}

		// Auto-commit implicit (session-managed) transactions
		if tx.HasBegun() {
			if err := tx.Commit(ctx); err != nil {
				p.Transaction = nil
				return fmt.Errorf("failed to commit implicit transaction: %w", err)
			}
		}
		p.Transaction = nil
		// Clear session state
		s.session.Transaction = nil
		s.session.Variables = nil
		return closeRegistryAgents()
	}
	return closeRegistryAgents()
}

// Domain returns the underlying domain of the service.
func (s *Service) Domain() ai.Domain[map[string]any] {
	return s.domain
}

func (s *Service) getScriptDB() *database.Database {
	return s.systemDB
}

// evaluateInputPolicy checks the input against the domain's policies.
func (s *Service) evaluateInputPolicy(ctx context.Context, input string) error {
	if s.domain == nil {
		return nil
	}
	if pol := s.domain.Policies(); pol != nil {
		classifier := s.domain.Classifier()
		if classifier != nil {
			sample := ai.ContentSample{Text: input}
			labels, err := classifier.Classify(ctx, sample)
			if err != nil {
				return fmt.Errorf("classification failed: %w", err)
			}
			decision, err := pol.Evaluate(ctx, "input", sample, labels)
			if err != nil {
				return fmt.Errorf("policy evaluation failed: %w", err)
			}
			if decision.Action == "block" {
				return fmt.Errorf("request blocked by policy: %v", decision.Reasons)
			}
		}
	}
	return nil
}

// extractCategoryPath detects and extracts explicit category paths from queries.
// Supports patterns like:
//   - "[Category/Subcategory] query text" - bracketed format
//   - "path:Category/Subcategory query text" - prefixed format
//   - "in Category/Subcategory: query text" - inline format
//
// Returns (categoryPath, cleanQuery) where cleanQuery has the path removed.
func extractCategoryPath(query string) (string, string) {
	query = strings.TrimSpace(query)

	// Pattern 1: [Category/Path] at start
	if strings.HasPrefix(query, "[") {
		if endIdx := strings.Index(query, "]"); endIdx > 1 {
			path := strings.TrimSpace(query[1:endIdx])
			if strings.Contains(path, "/") || strings.Contains(path, ">") {
				cleanQuery := strings.TrimSpace(query[endIdx+1:])
				return normalizeCategoryPath(path), cleanQuery
			}
		}
	}

	// Pattern 2: path:Category/Path
	if strings.HasPrefix(strings.ToLower(query), "path:") {
		parts := strings.SplitN(query[5:], " ", 2)
		if len(parts) > 0 {
			path := strings.TrimSpace(parts[0])
			if strings.Contains(path, "/") || strings.Contains(path, ">") {
				cleanQuery := ""
				if len(parts) > 1 {
					cleanQuery = strings.TrimSpace(parts[1])
				}
				return normalizeCategoryPath(path), cleanQuery
			}
		}
	}

	// Pattern 3: in Category/Path:
	if strings.HasPrefix(strings.ToLower(query), "in ") {
		remaining := query[3:]
		if colonIdx := strings.Index(remaining, ":"); colonIdx > 0 {
			path := strings.TrimSpace(remaining[:colonIdx])
			if strings.Contains(path, "/") || strings.Contains(path, ">") {
				cleanQuery := strings.TrimSpace(remaining[colonIdx+1:])
				return normalizeCategoryPath(path), cleanQuery
			}
		}
	}

	return "", query
}

// normalizeCategoryPath standardizes category path format.
// Converts "Category > Subcategory" to "Category/Subcategory"
func normalizeCategoryPath(path string) string {
	path = strings.TrimSpace(path)
	// Replace > separator with /
	path = strings.ReplaceAll(path, " > ", "/")
	path = strings.ReplaceAll(path, ">", "/")
	// Clean up multiple slashes
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	return path
}

// Search performs a semantic search in the domain's knowledge base.
// It enforces policies and uses the domain's embedder.
// This implements HYBRID SEARCH:
//   - Semantic/Vector search (embedding similarity)
//   - Text/BM25 search (keyword matching) when text index is configured
//   - Results merged using Reciprocal Rank Fusion (RRF)
//   - CategoryPath search (when explicit path detected in query)
//
// Used by retrieveKnowledge to automatically enrich LLM context in Ask flow.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]ai.Hit[map[string]any], error) {
	// 1. Policy Check (Input)
	if err := s.evaluateInputPolicy(ctx, query); err != nil {
		return nil, err
	}

	if s.domain == nil {
		return nil, nil
	}

	// 2. Embed
	emb := s.domain.Embedder()
	if emb == nil {
		// If no embedder is configured, we cannot perform vector search.
		// Return empty results instead of error, allowing the agent to proceed without context.
		return nil, nil
	}

	// 2a. Check for explicit CategoryPath in query (rare but supported)
	categoryPath, cleanQuery := extractCategoryPath(query)
	if categoryPath != "" {
		log.Debug("CategoryPath detected in query", "path", logsafe.V(categoryPath), "clean_query", logsafe.V(cleanQuery))
	}
	queryToEmbed := cleanQuery
	if queryToEmbed == "" {
		queryToEmbed = query
	}

	vecs, err := embed.QueryTexts(ctx, emb, []string{queryToEmbed})
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	// 3. Query Index
	tx, err := s.domain.BeginTransaction(ctx, sop.NoCheck)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	idx, err := s.domain.Index(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("domain %s has no index configured: %w", s.domain.ID(), err)
	}

	var vectorHits []ai.Hit[map[string]any]
	var vecErr error
	var textHits []search.TextSearchResult
	var textErr error

	// Vector Search (Semantic similarity via embeddings)
	// If CategoryPath specified, use filter to scope results to that category
	var queryFilter func(map[string]any) bool
	if categoryPath != "" {
		// CategoryPath filter: match items in the specified category hierarchy
		queryFilter = func(payload map[string]any) bool {
			if cat, ok := payload["category"].(string); ok {
				// Match exact path or sub-paths
				return cat == categoryPath || strings.HasPrefix(cat, categoryPath+"/")
			}
			return false
		}
		log.Debug("Scoping search to CategoryPath", "path", logsafe.V(categoryPath))
	}
	vectorHits, vecErr = idx.Query(ctx, vecs[0], limit, queryFilter)

	// Text Search (BM25 keyword matching) - runs in parallel if text index exists
	textIdx, err := s.domain.TextIndex(ctx, tx)
	if err == nil && textIdx != nil {
		textHits, textErr = textIdx.Search(ctx, queryToEmbed)
	}

	if vecErr != nil {
		return nil, fmt.Errorf("vector query failed: %w", vecErr)
	}
	if textErr != nil {
		log.Warn("Text search failed", "error", textErr)
	}

	k := 60.0
	scores := make(map[string]float64)
	payloads := make(map[string]map[string]any)

	// Identify Active Memory concepts from conversation to boost relevance
	activeContext := make(map[string]bool)
	if s.session != nil && s.session.Memory != nil {
		for _, threadID := range s.session.Memory.Order {
			if thread, ok := s.session.Memory.Threads[threadID]; ok {
				// Add topic or concept IDs to active memory map
				activeContext[thread.Category] = true
			}
		}
	}

	// Process Vector Hits
	// CategoryPath hits get a boost since they match explicit user intent
	for rank, hit := range vectorHits {
		boost := 1.0
		if categoryPath != "" {
			// Boost hits from the specified category path
			if cat, ok := hit.Payload["category"].(string); ok {
				if cat == categoryPath || strings.HasPrefix(cat, categoryPath+"/") {
					boost = 1.5 // 50% boost for category path matches
				}
			}
		}
		scores[hit.ID] += boost / (k + float64(rank+1))
		payloads[hit.ID] = hit.Payload
	}

	// Process Vector Hits
	for rank, hit := range vectorHits {
		scores[hit.ID] += 1.0 / (k + float64(rank+1))
		payloads[hit.ID] = hit.Payload
	}

	// Process Text Hits
	for rank, hit := range textHits {
		docID := hit.DocID
		scores[docID] += 1.0 / (k + float64(rank+1))
		// If payload missing, we need to fetch it
		if _, ok := payloads[docID]; !ok {
			item, err := idx.Get(ctx, docID)
			if err == nil && item != nil {
				payloads[docID] = item.Payload
			}
		}
	}

	// Construct Final Results
	var results []ai.Hit[map[string]any]
	for id, score := range scores {
		if payload, ok := payloads[id]; ok {
			results = append(results, ai.Hit[map[string]any]{
				ID:      id,
				Score:   float32(score),
				Payload: payload,
			})
		}
	}

	// Sort by Score Descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// RunPipeline executes the configured chain of agents.
func (s *Service) RunPipeline(ctx context.Context, input string, cfg *ai.ConfigMap) (string, error) {
	// Note: We do NOT call evaluateInputPolicy here anymore.
	// Policies should be explicitly added as steps in the pipeline if desired.
	// This allows for more flexible policy application (e.g. input, output, intermediate).

	currentInput := input

	for _, step := range s.pipeline {
		agent, ok := s.registry[step.Agent.ID]
		if !ok {
			return "", fmt.Errorf("pipeline agent '%s' not found in registry", step.Agent.ID)
		}

		output, err := agent.Ask(ctx, currentInput, cfg)
		if err != nil {
			return "", fmt.Errorf("pipeline step '%s' failed: %w", step.Agent.ID, err)
		}

		if step.OutputTo == "context" {
			// Append context to the input for the next step so it's available
			currentInput = fmt.Sprintf("%s\n\nContext from %s:\n%s", currentInput, step.Agent.ID, output)
		} else {
			// Default or "next_step": The output becomes the input for the next agent
			currentInput = output
		}
	}
	return currentInput, nil
}

func (s *Service) handlePendingUserConfirmation(ctx context.Context, query string) (bool, string, error) {
	if s.session == nil {
		return false, "", nil
	}

	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false, "", nil
	}

	var pending *PendingUserConfirmation
	isAffirmative := isAffirmativeConfirmation(trimmed)
	isNegative := isNegativeConfirmation(trimmed)

	func() {
		s.session.mu.Lock()
		defer s.session.mu.Unlock()
		if p := s.session.PendingConfirmation; p != nil {
			if isAffirmative || isNegative {
				s.session.PendingConfirmation = nil
			}
			pending = p
		}
	}()

	if pending != nil {
		if isAffirmative {
			res, err := s.executeDeleteSpaceDirect(ctx, pending.SpaceName, pending.DatabaseName)
			if err == nil {
				res = "[[CLEAR_PENDING_CONFIRMATION]]\n" + res
			}
			return true, res, err
		}
		if isNegative {
			return true, fmt.Sprintf("[[CLEAR_PENDING_CONFIRMATION]]\nCancelled deletion of Space '%s'.", pending.SpaceName), nil
		}
		return true, fmt.Sprintf("Pending deletion confirmation for Space '%s'. Reply 'yes' to confirm or 'no' to cancel.", pending.SpaceName), nil
	}

	spaceName, ok := parseDeleteSpaceRequest(trimmed)
	if !ok {
		return false, "", nil
	}

	dbName := ""
	if p := ai.GetSessionPayload(ctx); p != nil {
		dbName = p.CurrentDB
	}

	func() {
		s.session.mu.Lock()
		defer s.session.mu.Unlock()
		s.session.PendingConfirmation = &PendingUserConfirmation{
			Kind:         "delete_space",
			SpaceName:    spaceName,
			DatabaseName: dbName,
		}
	}()

	if dbName != "" {
		return true, fmt.Sprintf("Delete Space %q from database %q? Reply 'yes' to confirm or 'no' to cancel.", spaceName, dbName), nil
	}
	return true, fmt.Sprintf("Delete Space %q? Reply 'yes' to confirm or 'no' to cancel.", spaceName), nil
}

func (s *Service) executeDeleteSpaceDirect(ctx context.Context, spaceName string, databaseName string) (string, error) {
	args := map[string]any{"kb_name": spaceName}
	if databaseName != "" {
		args["database"] = databaseName
	}

	for _, agent := range s.registry {
		if copilotAgent, ok := agent.(*CopilotAgent); ok {
			copilotAgent.registerTools(ctx)
			return copilotAgent.Execute(ctx, "delete_space", args)
		}
		if provider, ok := agent.(ToolProvider); ok {
			res, err := provider.Execute(ctx, "delete_space", args)
			if err == nil {
				return res, nil
			}
			if strings.Contains(strings.ToLower(err.Error()), "unknown tool") {
				continue
			}
			return "", err
		}
	}

	return "", fmt.Errorf("delete_space tool is unavailable")
}

// GetLastToolInstructions returns the JSON instructions of the last executed tool.
func (s *Service) GetLastToolInstructions() string {
	if s.session == nil {
		return ""
	}

	// Try to get the last command from LastInteractionToolCalls if available,
	// as this is the most reliable source for the *last interaction's* tools.
	var targetStep *ai.ScriptStep

	if len(s.session.LastInteractionToolCalls) > 0 {
		// Use the last one in the buffer
		targetStep = &s.session.LastInteractionToolCalls[len(s.session.LastInteractionToolCalls)-1]
	} else if s.session.LastStep != nil && s.session.LastStep.Type == "command" {
		// Fallback to LastStep
		targetStep = s.session.LastStep
	}

	if targetStep == nil || targetStep.Type != "command" {
		return ""
	}

	// Debug: Log what we are retrieving
	if script, ok := targetStep.Args["script"]; ok {
		// Try to unmarshal if it's a string to log it as a JSON object instead of a string
		if scriptStr, ok := script.(string); ok {
			var scriptJSON interface{}
			if err := json.Unmarshal([]byte(scriptStr), &scriptJSON); err == nil {
				log.Debug("Service.GetLastToolInstructions: Retrieving script", "script", scriptJSON)
			} else {
				log.Debug("Service.GetLastToolInstructions: Retrieving script", "script", script)
			}
		} else {
			log.Debug("Service.GetLastToolInstructions: Retrieving script", "script", script)
		}
	} else {
		keys := make([]string, 0, len(targetStep.Args))
		for k := range targetStep.Args {
			keys = append(keys, k)
		}
		log.Debug(fmt.Sprintf("Service.GetLastToolInstructions: Retrieving command '%s' without script. Args keys: %v", targetStep.Command, keys))
	}

	// Reconstruct the tool call structure with unmarshaled script if present
	args := make(map[string]any)
	for k, v := range targetStep.Args {
		args[k] = v
	}

	if script, ok := args["script"]; ok {
		if scriptStr, ok := script.(string); ok {
			var scriptJSON interface{}
			if err := json.Unmarshal([]byte(scriptStr), &scriptJSON); err == nil {
				args["script"] = scriptJSON
			}
		}
	}

	toolCall := map[string]any{
		"tool": targetStep.Command,
		"args": args,
	}

	b, _ := json.MarshalIndent(toolCall, "", "  ")
	return string(b)
}

// RecordStep implements the ScriptRecorder interface.
func (s *Service) RecordStep(ctx context.Context, step ai.ScriptStep) {
	// Debug: Log what we are recording
	if step.Type == "command" {
		if script, ok := step.Args["script"]; ok {
			log.Debug(logsafe.V(fmt.Sprintf("Service.RecordStep: Drafting script. Type: %T, Value: %+v", script, script)))
		} else {
			keys := make([]string, 0, len(step.Args))
			for k := range step.Args {
				keys = append(keys, k)
			}
			log.Debug(fmt.Sprintf("Service.RecordStep: Drafting command %q without script. Args keys: %v", step.Command, keys))
		}
	}

	// Deep copy args to ensure we persist the exact state at this moment
	// and protect against future mutations of the map by the caller.
	if step.Args != nil {
		step.Args = deepCopyMap(step.Args)
	}

	// Always capture the last step for potential manual addition
	s.session.LastStep = &step

	// Note: Auto-recording to CurrentScript is disabled to prevent noise.
	// Users must explicitly add steps using /step.

	// Buffer tool calls for potential refactoring
	if step.Type == "command" {
		s.session.LastInteractionToolCalls = append(s.session.LastInteractionToolCalls, step)
	}
}

// RefactorLastSteps implements the ScriptRecorder interface
func (s *Service) RefactorLastSteps(count int, mode string, name string) error {
	// TODO: Implement script refactoring logic
	return fmt.Errorf("not implemented")
}

func (s *Service) LastInteractionToolCallsSnapshot() []ai.ScriptStep {
	if s == nil || s.session == nil {
		return nil
	}
	steps := make([]ai.ScriptStep, len(s.session.LastInteractionToolCalls))
	copy(steps, s.session.LastInteractionToolCalls)
	return steps
}

func (s *Service) saveScript(ctx context.Context, name string, script ai.Script) error {
	scriptDB := s.getScriptDB()
	if scriptDB == nil {
		return fmt.Errorf("script database not available")
	}
	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	if err := store.Save(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("failed to save script: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) getToolInfo(ctx context.Context, toolName string) (string, error) {
	if s.systemDB == nil {
		return "", fmt.Errorf("system DB not available")
	}
	tx, err := s.systemDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	kb, err := s.systemDB.OpenKnowledgeBase(ctx, "sop", tx, s.generator, nil, false, true)
	if err != nil {
		return "", fmt.Errorf("failed to open sop KB: %w", err)
	}

	searchQuery := fmt.Sprintf("%s Tool Operations", toolName)
	batch, err := kb.Search(ctx, []memory.SearchRequest[map[string]any]{{
		Text:  searchQuery,
		Limit: 1,
	}})
	if err == nil && len(batch) > 0 && len(batch[0]) > 0 {
		hits := batch[0]
		if content, ok := hits[0].Payload["Content"].(string); ok {
			return content, nil
		}
		// Fallback if Content isn't formatted that way
		if txt, ok := hits[0].Payload["text"].(string); ok {
			return txt, nil
		}
	}
	return "", fmt.Errorf("tool info for '%s' not found", toolName)
}

// registerTools sets up the tools available to the LLM.
func (s *Service) registerTools() {
	if s.registry == nil {
		s.registry = make(map[string]ai.Agent[map[string]any])
	}
	// We inject `conclude_topic` by directly defining it in the interface.
	// Since s.registry is map[string]ai.Agent (where ai.Agent is an interface),
	// this approach (editing registry from service) assumes Service *owns* the orchestration.
	// However, `ai.Agent` interface expects `Execute` method.
	// We need a wrapper.

	// Create a wrapper agent for ad-hoc service tools
	s.registry["conclude_topic"] = &AdHocAgent{
		Name: "conclude_topic",
		Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.handleConcludeTopic(ctx, args)
		},
	}
}

// AdHocAgent implements ai.Agent for simple function wrappers
type AdHocAgent struct {
	Name    string
	Handler func(ctx context.Context, args map[string]interface{}) (string, error)
}

// Implement ai.Agent[map[string]any] interface
func (a *AdHocAgent) Open(ctx context.Context) error  { return nil }
func (a *AdHocAgent) Close(ctx context.Context) error { return nil }
func (a *AdHocAgent) Search(ctx context.Context, query string, limit int) ([]ai.Hit[map[string]any], error) {
	return nil, nil
}
func (a *AdHocAgent) Ask(ctx context.Context, query string, cfg *ai.ConfigMap) (string, error) {
	return "", nil
}

// Implement ToolProvider interface
func (a *AdHocAgent) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if toolName == a.Name {
		return a.Handler(ctx, args)
	}
	return "", fmt.Errorf("unknown tool: %s", toolName)
}

// handleConcludeTopic implements the session-aware topic conclusion.
func (s *Service) handleConcludeTopic(ctx context.Context, args map[string]interface{}) (string, error) {
	summary, _ := args["summary"].(string)
	label, _ := args["topic_label"].(string)

	if summary == "" {
		return "", fmt.Errorf("summary is required")
	}

	if s.session.Memory == nil || len(s.session.Memory.CurrentThreadID) == 0 {
		return "No active topic to conclude.", nil
	}

	thread := s.session.Memory.GetCurrentThread()
	if thread == nil {
		return "Current topic not found.", nil
	}

	thread.Conclusion = summary
	thread.Status = "concluded"
	if label != "" {
		thread.Label = label
	}

	return fmt.Sprintf("Topic '%s' concluded. Summary saved: %s", thread.Label, summary), nil
}

// InitializeUserSession explicitly creates the DDL components required for a user's workflow.
// This creates the User's Long-Term Memory (LTM) Vector Knowledge Base and any other
// dependencies that must exist before the ReAct loop starts querying with ForReading transactions.
func (s *Service) InitializeUserSession(ctx context.Context, userID string) error {
	// 1. STM Initialization (if enabled)
	if s.EnableShortTermMemory {
		if err := s.InitializeShortTermMemory(ctx); err != nil {
			return fmt.Errorf("failed to init STM: %w", err)
		}
	}

	if userID == "" {
		return nil // Nothing to init for a systemic or session-less user
	}

	// 2. LTM Initialization (User's Private Knowledge Base DB Tables)
	tx, err := s.systemDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("failed to begin session init tx: %w", err)
	}
	defer tx.Rollback(ctx)

	agentID := ai.AgentIDOmni
	if p := ai.GetSessionPayload(ctx); p != nil && p.AgentID != "" {
		agentID = p.AgentID
	}
	ltmName := memory.BuildLTMStoreName(agentID, userID)

	// OpenKnowledgeBase safely ensures DDL (creates B-Trees if they don't exist)
	// Because this is a ForWriting transaction, the NewBtree calls deep inside will succeed.
	ltmKB, err := s.systemDB.OpenKnowledgeBase(ctx, ltmName, tx, s.generator, nil, false, true)
	if err != nil {
		return fmt.Errorf("failed to initialize user LTM '%s': %w", ltmName, err)
	}

	// Sync SystemPrompt from the SOP KB config to the LTM KB config
	sopKB, err := s.systemDB.OpenKnowledgeBase(ctx, "sop", tx, s.generator, nil, false)
	if err == nil && sopKB != nil {
		sopCfg, _ := sopKB.GetConfig(ctx)
		if sopCfg != nil && sopCfg.SystemPrompt != "" {
			ltmCfg, _ := ltmKB.GetConfig(ctx)
			if ltmCfg == nil {
				ltmCfg = &memory.KnowledgeBaseConfig{}
			}
			if ltmCfg.SystemPrompt != sopCfg.SystemPrompt {
				ltmCfg.SystemPrompt = sopCfg.SystemPrompt
				_ = ltmKB.SetConfig(ctx, ltmCfg)
			}
		}
	}

	return tx.Commit(ctx)
}

// topicRoutingResult holds the outcome of topic identification and routing.
type topicRoutingResult struct {
	assessment *TopicAssessment
	isNewTopic bool
}

// promptInputs holds the constructed prompt components for the reasoning engine.
type promptInputs struct {
	systemPrompt string
	contextText  string
	historyText  string
}

// generatorConfig holds the resolved generator and carryover strategy.
type generatorConfig struct {
	generator ai.Generator
	carryover carryoverDecision
}

// resetInteractionBuffer clears the tool call buffer unless this is a last-tool introspection request.
func (s *Service) resetInteractionBuffer(query string) {
	trimQ := strings.TrimSpace(query)
	if trimQ != "last-tool" && trimQ != "/last-tool" && trimQ != "last_tool" && trimQ != "/last_tool" {
		s.session.LastInteractionToolCalls = []ai.ScriptStep{}
	}
}

// // parseAskConfig extracts database and format from ConfigMap and updates context.
// func (s *Service) parseAskConfig(ctx context.Context, cfg *ai.ConfigMap) (context.Context, *database.Database) {
// 	if cfg == nil {
// 		return ctx, nil
// 	}

// 	if val, ok := cfg.Get("default_format"); ok {
// 		if format, ok := val.(string); ok && strings.TrimSpace(format) != "" {
// 			ctx = context.WithValue(ctx, ai.CtxKeyDefaultFormat, strings.TrimSpace(format))
// 		}
// 	}

// 	var db *database.Database
// 	if val, ok := cfg.Get("database"); ok {
// 		if d, ok := val.(*database.Database); ok {
// 			db = d
// 		} else if dName, ok := val.(string); ok && dName != "" {
// 			if dbOpts, ok := s.databases[dName]; ok {
// 				db = database.NewDatabase(dbOpts)
// 			}
// 		}
// 	}

// 	return ctx, db
// }

// // ensureToolExecutor attaches a default tool executor if none is present in context.
// func (s *Service) ensureToolExecutor(ctx context.Context) context.Context {
// 	if ctx.Value(ai.CtxKeyExecutor) == nil {
// 		executor := &ServiceToolExecutor{s: s}
// 		ctx = context.WithValue(ctx, ai.CtxKeyExecutor, executor)
// 	}
// 	return ctx
// }

// // resolveSessionPayload establishes or enriches the session payload in context.
// func (s *Service) resolveSessionPayload(ctx context.Context, query string, cfg *ai.ConfigMap, db *database.Database) (context.Context, *database.Database) {
// 	if val, ok := cfg.Get("payload"); ok {
// 		if p, ok := val.(*ai.SessionPayload); ok {
// 			if strings.TrimSpace(p.CurrentUserQuery) == "" {
// 				p.CurrentUserQuery = query
// 			}
// 			ctx = context.WithValue(ctx, SessionPayloadKey, p)
// 			if db == nil && p.CurrentDB != "" {
// 				if dbOpts, ok := s.databases[p.CurrentDB]; ok {
// 					db = database.NewDatabase(dbOpts)
// 				}
// 			}
// 		}
// 	} else if p := ai.GetSessionPayload(ctx); p != nil {
// 		if db == nil && p.CurrentDB != "" {
// 			if dbOpts, ok := s.databases[p.CurrentDB]; ok {
// 				db = database.NewDatabase(dbOpts)
// 			}
// 		}
// 	} else {
// 		targetDB := s.session.CurrentDB
// 		if forcedDBName, ok := cfg.Get("forced_db_name"); ok {
// 			if dbName, ok := forcedDBName.(string); ok && dbName != "" {
// 				targetDB = dbName
// 			}
// 		}

// 		p := &ai.SessionPayload{
// 			CurrentDB:        targetDB,
// 			CurrentUserQuery: query,
// 		}
// 		if s.session.Transaction != nil {
// 			p.Transaction = s.session.Transaction
// 			p.Variables = s.session.Variables
// 			p.ExplicitTransaction = true
// 		}
// 		ctx = context.WithValue(ctx, SessionPayloadKey, p)

// 		if db == nil && p.CurrentDB != "" {
// 			if dbOpts, ok := s.databases[p.CurrentDB]; ok {
// 				db = database.NewDatabase(dbOpts)
// 			}
// 		}
// 	}

// 	return ctx, db
// }

// // attachScriptRecorder injects the script recorder and captures the ask step if applicable.
// func (s *Service) attachScriptRecorder(ctx context.Context, query string) context.Context {
// 	ctx = context.WithValue(ctx, ai.CtxKeyScriptRecorder, s)

// 	if !strings.HasPrefix(query, "/") && query != "last-tool" {
// 		s.RecordStep(ctx, ai.ScriptStep{
// 			Type:   "ask",
// 			Prompt: query,
// 		})
// 	}

// 	return ctx
// }

// applyObfuscation obfuscates known resource names in the query if enabled.
func (s *Service) applyObfuscation(query string) string {
	if s.EnableObfuscation {
		return obfuscation.GlobalObfuscator.ObfuscateText(query)
	}
	return query
}

// performTopicRouting identifies the conversation topic and manages thread continuity.
func (s *Service) performTopicRouting(ctx context.Context, query string) *topicRoutingResult {
	if s == nil {
		return &topicRoutingResult{assessment: &TopicAssessment{IsNewTopic: true}, isNewTopic: true}
	}
	if s.session == nil {
		s.session = NewRunnerSession()
	}

	var topicAssessment *TopicAssessment

	if s.EnableHistoryInjection && s.session.Memory != nil {
		assessment, err := s.identifyTopic(ctx, query)
		if err != nil {
			log.Warn("Topic identification failed, defaulting to new topic", "error", err)
			topicAssessment = &TopicAssessment{IsNewTopic: true}
		} else {
			topicAssessment = assessment
		}

		if !topicAssessment.IsNewTopic && topicAssessment.TopicUUID != "" {
			topicID, err := sop.ParseUUID(topicAssessment.TopicUUID)
			if err == nil {
				s.session.Memory.PromoteThread(topicID)
			}
		}
	} else {
		s.session.Memory = NewShortTermMemory()
		topicAssessment = &TopicAssessment{IsNewTopic: true}
	}

	result := &topicRoutingResult{assessment: topicAssessment}
	if topicAssessment != nil && topicAssessment.IsNewTopic {
		resetTopicSwitchProjection(ctx, s.session)
		result.isNewTopic = true
	}

	return result
}

// retrieveKnowledge performs vector/text search against the domain knowledge base.
// This is the core implementation of the "Agent searches its KB" pattern.
// Called automatically during Ask flow to enrich LLM context with domain expertise.
func (s *Service) retrieveKnowledge(ctx context.Context, query string) ([]ai.Hit[map[string]any], error) {
	return s.Search(ctx, query, 10)
}

// buildPromptInputs constructs system prompt, context, and history for the reasoning engine.
func (s *Service) buildPromptInputs(ctx context.Context, query string, hits []ai.Hit[map[string]any]) *promptInputs {
	result := &promptInputs{}

	result.contextText = s.formatContext(hits)

	if s.domain != nil {
		result.systemPrompt, _ = s.domain.Prompt(ctx, "system")
	}

	// Inject user preferences from long-term memory
	if p := ai.GetSessionPayload(ctx); p != nil && p.UserID != "" && s.systemDB != nil {
		agentID := p.AgentID
		if agentID == "" {
			agentID = ai.AgentIDOmni
		}
		kbName := memory.BuildLTMStoreName(agentID, p.UserID)
		if tx, err := s.systemDB.BeginTransaction(ctx, sop.ForReading); err == nil {
			if kb, err := s.systemDB.OpenKnowledgeBase(ctx, kbName, tx, s.generator, nil, false, true); err == nil {
				if batch, err := kb.Search(ctx, []memory.SearchRequest[map[string]any]{{Text: query, Limit: 5}}); err == nil && len(batch) > 0 && len(batch[0]) > 0 {
					userHits := batch[0]
					var prefText strings.Builder
					prefText.WriteString("\n\n[User Preferences & Active Memory Ledger for this query]\n")
					for _, hit := range userHits {
						if txt, ok := hit.Payload["thought"].(string); ok {
							prefText.WriteString(fmt.Sprintf("- %s\n", txt))
						} else if txt, ok := hit.Payload["text"].(string); ok {
							prefText.WriteString(fmt.Sprintf("- %s\n", txt))
						} else {
							b, _ := json.Marshal(hit.Payload)
							prefText.WriteString(fmt.Sprintf("- %s\n", string(b)))
						}
					}
					result.systemPrompt = fmt.Sprintf("%s%s", result.systemPrompt, prefText.String())
				}
			}
			tx.Rollback(ctx)
		}
	}

	// Build history text if injection is enabled
	if s.EnableHistoryInjection && s.session.Memory != nil && len(s.session.Memory.Order) > 0 {
		var historyBuilder strings.Builder
		for _, threadID := range s.session.Memory.Order {
			thread, ok := s.session.Memory.Threads[threadID]
			if !ok {
				continue
			}

			historyBuilder.WriteString(fmt.Sprintf("\n--- Conversation Thread: %s ---\n", thread.Label))
			if thread.Category != "" {
				historyBuilder.WriteString(fmt.Sprintf("Category: %s\n", thread.Category))
			}
			if thread.ContextNotes != "" {
				historyBuilder.WriteString(fmt.Sprintf("Context: %s\n", thread.ContextNotes))
			}

			historyBuilder.WriteString(fmt.Sprintf("Root: %s\n", thread.RootPrompt))
			for _, interaction := range thread.Exchanges {
				roleName := "User"
				if interaction.Role == RoleAssistant {
					roleName = "Copilot"
				} else if interaction.Role == RoleSystem {
					roleName = "System"
				}
				historyBuilder.WriteString(fmt.Sprintf("%s: %s\n", roleName, interaction.Content))
			}

			if thread.Conclusion != "" {
				historyBuilder.WriteString(fmt.Sprintf("Conclusion: %s\n", thread.Conclusion))
			}
			historyBuilder.WriteString("--------------------------------\n")
		}

		result.historyText = historyBuilder.String()
		if result.historyText != "" {
			result.historyText = "\n\n[Existing Conversation Threads]\n" + result.historyText
		}
	}

	return result
}

// resolveGeneratorAndCarryover selects the generator and computes the carryover strategy.
func (s *Service) resolveGeneratorAndCarryover(ctx context.Context, topicAssessment *TopicAssessment, historyText string) *generatorConfig {
	gen := s.generator

	// Extract provider override from context for backward compatibility
	providerOverride := extractProviderOverrideFromContext(ctx)

	if providerOverride != nil && providerOverride.Provider != "" {
		provider := providerOverride.Provider
		if provider == "openai" {
			provider = "chatgpt" // Normalize openai to chatgpt
		}

		// Build config map for generator initialization
		configMap := make(map[string]any)
		if providerOverride.APIKey != "" {
			configMap["api_key"] = string(providerOverride.APIKey)
		}
		if providerOverride.BaseURL != "" {
			configMap["base_url"] = providerOverride.BaseURL
		}
		if providerOverride.Model != "" {
			configMap["model"] = providerOverride.Model
		}

		// Create generator with config
		if gen == nil || gen.Name() != provider {
			if overriddenGen, err := generator.New(provider, configMap); err == nil {
				gen = overriddenGen
			} else {
				log.Warn("Failed to initialize requested provider, falling back to default", "provider", provider, "error", err)
			}
		}
	}

	carryover := decideCarryover(ctx, s.session, gen, topicAssessment, historyText)

	log.Info("Carryover Decision",
		"provider", providerName(gen),
		"mode", carryover.Mode,
		"reason", carryover.Reason,
		"history_suppressed", carryover.SuppressHistory,
		"estimated_carry_tokens", carryover.EstimatedCarryTokens,
		"estimated_history_tokens", carryover.EstimatedHistoryTokens,
	)

	return &generatorConfig{
		generator: gen,
		carryover: carryover,
	}
}

// // executeReasoningEngine runs the ReAct loop with the configured inputs and executor.
// func (s *Service) executeReasoningEngine(ctx context.Context, query string, prompts *promptInputs, genCfg *generatorConfig, db *database.Database) (ai.ReasoningResponse, error) {
// 	executor, _ := ctx.Value(ai.CtxKeyExecutor).(ai.ToolExecutor)

// 	engine := &NativeReActEngine{
// 		EnableObfuscation: s.EnableObfuscation,
// 	}

// 	contextText := prompts.contextText
// 	historyText := prompts.historyText

// 	if strings.TrimSpace(genCfg.carryover.Summary) != "" {
// 		contextText = appendCarryoverToContext(contextText, genCfg.carryover.Summary)
// 	}
// 	if genCfg.carryover.Mode == ai.CarryoverModeCompact && genCfg.carryover.SuppressHistory {
// 		historyText = ""
// 	}

// 	req := ai.ReasoningRequest{
// 		SystemPrompt:   prompts.systemPrompt,
// 		ContextText:    contextText,
// 		HistoryText:    historyText,
// 		UserQuery:      query,
// 		Executor:       &autoTxExecutor{original: executor, s: s, db: db},
// 		Generator:      genCfg.generator,
// 		CarryoverMode:  genCfg.carryover.Mode,
// 		CarryoverState: genCfg.carryover.State,
// 	}

// 	if streamer, ok := ctx.Value(ai.CtxKeyEventStreamer).(func(string, any)); ok && streamer != nil {
// 		req.Streamer = streamer
// 	}

// 	return engine.Run(ctx, req)
// }

// updateSessionMemory persists the interaction outcomes into conversation threads and carryover state.
func (s *Service) updateSessionMemory(ctx context.Context, query string, finalText string, topicAssessment *TopicAssessment, engineResp ai.ReasoningResponse, gen ai.Generator) {
	toolRecorded := len(engineResp.ToolCalls) > 0

	if s.session.CurrentScript != nil && !toolRecorded {
		s.session.LastStep = &ai.ScriptStep{
			Type:   "ask",
			Prompt: query,
		}
		s.session.CurrentScript.Steps = append(s.session.CurrentScript.Steps, *s.session.LastStep)
	}

	var currentThread *ConversationThread

	if !topicAssessment.IsNewTopic && topicAssessment.TopicUUID != "" {
		currentThread = s.session.Memory.GetCurrentThread()
	}

	if currentThread == nil {
		newThreadID := sop.NewUUID()
		label := "New Topic"
		if topicAssessment.NewTopicLabel != "" {
			label = topicAssessment.NewTopicLabel
		} else if len(query) > 0 {
			label = query[:min(len(query), 20)] + "..."
		}

		newThread := &ConversationThread{
			ID:         newThreadID,
			RootPrompt: query,
			Label:      label,
			Category:   "General",
			Exchanges:  make([]Interaction, 0),
			Status:     "active",
		}
		s.session.Memory.AddThread(newThread)
		currentThread = newThread
	}

	currentThread.Exchanges = append(currentThread.Exchanges, Interaction{
		Role:      RoleUser,
		Content:   query,
		Timestamp: time.Now().Unix(),
	})

	assistantContent := finalText
	if toolRecorded {
		assistantContent = fmt.Sprintf("(Tool Execution) %s", finalText)
	}
	currentThread.Exchanges = append(currentThread.Exchanges, Interaction{
		Role:      RoleAssistant,
		Content:   assistantContent,
		Timestamp: time.Now().Unix(),
	})

	// MRU PUSH: Write ask outcome to session MRU (infrastructure-level, benefits all provider loops)
	persistSessionAskOutcomeMRU(ctx, s.session, query, finalText, engineResp.ToolCalls, engineResp.OutcomeFacts, engineResp.CarryoverState)

	persistCarryoverState(s.session.Memory, buildCarryoverState(ctx, s.session, gen, currentThread, query, finalText, engineResp.ToolCalls, engineResp.OutcomeFacts, engineResp.OutcomeRecipes, engineResp.CarryoverState))
}

// Ask is the public interface method that maintains backward compatibility with ai.Agent interface.
// It extracts configuration from ConfigMap (not context!), constructs an AskRequest, and delegates to ask().
//
// Proper separation of concerns:
// - context.Context: Only for cancellation, deadlines, request-scoped tracing
// - cfg *ConfigMap: For all parameter passing (session, executor, writer, etc.)
func (s *Service) Ask(ctx context.Context, query string, cfg *ai.ConfigMap) (string, error) {
	// Convert generic ConfigMap to explicit typed struct
	opts := s.optsFromConfigMap(cfg)

	// Backwards compatibility: fall back to context if executor not in ConfigMap
	if opts.Executor == nil {
		if ctxExec, ok := ctx.Value(ai.CtxKeyExecutor).(ai.ToolExecutor); ok {
			opts.Executor = ctxExec
		}
	}

	// Initialize session if not provided
	session := opts.Payload
	if session == nil {
		// Backwards compatibility: fall back to context-based session payload
		if ctxPayload, ok := ctx.Value(SessionPayloadKey).(*ai.SessionPayload); ok && ctxPayload != nil {
			session = ctxPayload
		} else {
			session = &ai.SessionPayload{
				CurrentDB:        s.session.CurrentDB,
				CurrentUserQuery: query,
			}
			if s.session.Transaction != nil {
				session.Transaction = s.session.Transaction
				session.Variables = s.session.Variables
				//session.ExplicitTransaction = true
			}
		}
	} else if strings.TrimSpace(session.CurrentUserQuery) == "" {
		session.CurrentUserQuery = query
	}

	if session != nil {
		s.hydrateSessionPreferences(ctx, session)
	}

	// Build AskRequest from typed options
	req := AskRequest{
		Query:            query,
		Session:          session,
		Executor:         opts.Executor,
		Generator:        opts.Generator,
		ProviderOverride: opts.ProviderDetails,
		Database:         opts.Database,
		Writer:           opts.Writer,
		EventStreamer:    opts.EventStreamer,
		ProgressSink:     opts.ProgressSink,
		ScriptRecorder:   opts.Recorder,
		DefaultFormat:    opts.DefaultFormat,
		Options:          cfg,
	}

	// Delegate to explicit-parameter ask method
	resp, err := s.ask(ctx, req)
	if err != nil {
		return "", err
	}
	// TODO: Remove this bridge once verbose and similar preferences are hydrated from
	// LTM -> MRU -> SessionPayload at ask entry. SessionPayload is a runtime carrier,
	// not durable storage.
	s.syncSessionFromPayload(resp.UpdatedSession)

	return resp.FinalText, nil
}

func (s *Service) hydrateSessionPreferences(ctx context.Context, session *ai.SessionPayload) {
	if s == nil || session == nil || s.systemDB == nil {
		return
	}

	pref, ok, err := loadPreference(ctx, s.systemDB, session, memory.PreferenceKeyVerbose)
	if err != nil {
		log.Warn("Failed to hydrate verbose preference", "error", logsafe.V(err))
		return
	}
	if !ok {
		return
	}
	if value, found := pref.Bool(); found {
		s.session.SetVerbose(value)
	}
}

func (s *Service) syncSessionFromPayload(payload *ai.SessionPayload) {
	if s == nil || s.session == nil || payload == nil {
		return
	}

	// TODO: Replace this cross-ask mirroring with memory-backed preference hydration.
	// This helper exists only to bridge ephemeral SessionPayload state until the
	// LTM -> MRU -> SessionPayload preference flow is fully implemented.
	if payload.CurrentDB != "" {
		s.session.CurrentDB = payload.CurrentDB
	}
	if payload.Transaction == nil {
		s.session.Transaction = nil
	} else if tx, ok := payload.Transaction.(sop.Transaction); ok {
		s.session.Transaction = tx
	}
	s.session.Variables = payload.Variables
}

// ask handles a user request using explicit parameters instead of context-based state passing.
// This method provides better readability and type safety by making all dependencies explicit
// in the AskRequest struct rather than hiding them in context.Context.
//
// Functional flow:
// 1. Guard transaction/session state cleanup for non-playback sessions.
// 2. Reset per-interaction tool call buffer (except last-tool introspection requests).
// 3. Handle session commands early (drafting/playback/script commands).
// 4. Apply optional obfuscation and pending user confirmation handling.
// 5. Short-circuit to configured pipeline when present.
// 6. Perform topic routing and short-term memory thread promotion/initialization.
// 7. Run retrieval against the domain knowledge index.
// 8. Build system/context/history prompt inputs, including user preference enrichment.
// 9. Resolve generator override and compute carryover strategy.
// 10. Execute the native ReAct loop with tool execution and auto-transaction wrapping.
// 11. Persist response outcomes into script/session memory and carryover state.
func (s *Service) ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	// Check for context cancellation early
	select {
	case <-ctx.Done():
		return AskResponse{}, fmt.Errorf("request canceled before execution: %w", ctx.Err())
	default:
	}

	// Initialize session if not provided
	if req.Session == nil {
		req.Session = &ai.SessionPayload{
			CurrentDB:        s.session.CurrentDB,
			CurrentUserQuery: req.Query,
		}
		if s.session.Transaction != nil {
			req.Session.Transaction = s.session.Transaction
			req.Session.Variables = s.session.Variables
			//req.Session.ExplicitTransaction = true
		}
	} else if strings.TrimSpace(req.Session.CurrentUserQuery) == "" {
		req.Session.CurrentUserQuery = req.Query
	}

	// Initialize executor if not provided
	if req.Executor == nil {
		// Build tool execution context with all dependencies explicit
		toolCtx := &ToolExecutionContext{
			Session:         req.Session,
			Executor:        nil, // will be set to self after creation
			Recorder:        req.ScriptRecorder,
			Writer:          req.Writer,
			ResultStreamer:  nil,  // not typically needed in Ask flow, tools extract from context if needed
			NativeToolHints: true, // Ask flow uses native tool hints
			Database:        req.Database,
			EventStreamer:   req.EventStreamer,
			ProgressSink:    req.ProgressSink,
		}
		executor := &ServiceToolExecutor{s: s, toolCtx: toolCtx}
		toolCtx.Executor = executor // self-reference
		req.Executor = executor
	}

	// Initialize script recorder if not provided
	if req.ScriptRecorder == nil {
		req.ScriptRecorder = s
	}

	// 1. Reset per-interaction tool call buffer
	s.resetInteractionBuffer(req.Query)

	// 2. Capture the top-level ask step
	if !strings.HasPrefix(req.Query, "/") && req.Query != "last-tool" {
		req.ScriptRecorder.RecordStep(ctx, ai.ScriptStep{
			Type:   "ask",
			Prompt: req.Query,
		})
	}

	// 3. Handle session commands early (drafting/playback/script commands)
	db := req.Database
	if db == nil && req.Session.CurrentDB != "" {
		if dbOpts, ok := s.databases[req.Session.CurrentDB]; ok {
			db = database.NewDatabase(dbOpts)
		}
	}

	if resp, handled, err := s.handleSessionCommandWithRequest(ctx, req.Query, db, req.Session); handled {
		return AskResponse{FinalText: resp, UpdatedSession: req.Session}, err
	}

	// 4. Apply optional obfuscation and pending user confirmation handling
	query := s.applyObfuscation(req.Query)

	if handled, resp, err := s.handlePendingUserConfirmationWithRequest(ctx, query, req.Session); handled {
		return AskResponse{FinalText: resp, UpdatedSession: req.Session}, err
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, req.Session)
	ctx = context.WithValue(ctx, RunnerSessionKey, s.session)

	// 5. Perform topic routing and short-term memory thread promotion/initialization
	topicResult := s.performTopicRoutingWithRequest(ctx, query, req.Session)

	// 6. Short-circuit to configured pipeline when present
	if len(s.pipeline) > 0 {
		// TODO: Refactor RunPipeline to accept explicit parameters
		// For now, fall back to context-based approach for pipeline
		ctx = context.WithValue(ctx, SessionPayloadKey, req.Session)
		if req.Executor != nil {
			ctx = context.WithValue(ctx, ai.CtxKeyExecutor, req.Executor)
		}
		if req.ScriptRecorder != nil {
			ctx = context.WithValue(ctx, ai.CtxKeyScriptRecorder, req.ScriptRecorder)
		}

		pipelineCfg := req.Options
		if pipelineCfg == nil {
			pipelineCfg = ai.NewConfigMap()
		}
		if topicResult.isNewTopic {
			pipelineCfg.Set("is_new_topic", true)
		}

		finalText, err := s.RunPipeline(ctx, query, pipelineCfg)
		return AskResponse{FinalText: finalText, UpdatedSession: req.Session}, err
	}

	// Ensure tools are registered (session specific registration)
	s.registerTools()

	// 7. CRITICAL PATTERN: Agent looks up its domain-specific Knowledge Base (KB)
	// This implements the universal pattern: "When user asks Agent X about Topic Y, Agent X searches its KB first"
	// Examples:
	//   - Omni (system agent) → searches SOP KB (database documentation)
	//   - Avatar agents → search their domain-specific expertise KBs
	//   - Custom agents → search their configured knowledge stores
	// The Search performs BOTH:
	//   a) Semantic/Vector search (embeddings-based similarity)
	//   b) Text/BM25 search (keyword matching) if text index is enabled
	// Results are merged using Reciprocal Rank Fusion and injected into LLM context
	hits, err := s.retrieveKnowledge(ctx, query)
	if err != nil {
		return AskResponse{}, fmt.Errorf("retrieval failed: %w", err)
	}
	if len(hits) > 0 {
		log.Info("KB retrieval enriched context", "domain", s.domain.ID(), "hits", len(hits), "top_score", hits[0].Score)
	} else if s.domain != nil && s.domain.Embedder() != nil {
		log.Debug("KB retrieval returned no results", "domain", s.domain.ID(), "query", logsafe.V(query))
	}

	// 8. Build system/context/history prompt inputs
	prompts := s.buildPromptInputsWithRequest(ctx, query, hits, req.Session)

	// 9. Resolve generator override and compute carryover strategy
	gen := req.Generator
	if gen == nil {
		gen = s.generator
	}

	// Use explicit ProviderOverride if provided, otherwise extract from context for backward compatibility
	providerOverride := req.ProviderOverride
	if providerOverride == nil {
		providerOverride = extractProviderOverrideFromContext(ctx)
	}

	genCfg := s.resolveGeneratorAndCarryoverWithRequest(ctx, gen, providerOverride, topicResult.assessment, prompts.historyText)

	// 10. Execute the native ReAct loop with tool execution
	engineResp, err := s.executeReasoningEngineWithRequest(ctx, query, prompts, genCfg, db, req.Executor, req.EventStreamer, s.session.IsVerbose())
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() != nil {
			return AskResponse{}, fmt.Errorf("request canceled during execution: %w", ctx.Err())
		}
		return AskResponse{}, err
	}

	// 11. Persist response outcomes into script/session memory
	s.updateSessionMemoryWithRequest(ctx, query, engineResp.FinalText, topicResult.assessment, engineResp, genCfg.generator, req.Session)

	return AskResponse{
		FinalText:      engineResp.FinalText,
		UpdatedSession: req.Session,
		CarryoverState: engineResp.CarryoverState,
		ToolCalls:      engineResp.ToolCalls,
		OutcomeFacts:   engineResp.OutcomeFacts,
		OutcomeRecipes: engineResp.OutcomeRecipes,
	}, nil
}

func (s *Service) formatContext(hits []ai.Hit[map[string]any]) string {
	var sb strings.Builder
	for i, hit := range hits {
		// Generic handling of metadata
		sb.WriteString(fmt.Sprintf("[%d] ", i+1))

		text, hasText := hit.Payload["text"].(string)
		desc, hasDesc := hit.Payload["description"].(string)

		if hasText && hasDesc {
			sb.WriteString(fmt.Sprintf("%s: %s (Score: %.2f)", text, desc, hit.Score))
		} else if hasDesc {
			sb.WriteString(fmt.Sprintf("%s (Score: %.2f)", desc, hit.Score))
		} else if hasText {
			sb.WriteString(fmt.Sprintf("%s (Score: %.2f)", text, hit.Score))
		} else {
			sb.WriteString(fmt.Sprintf("%v (Score: %.2f)", hit.Payload, hit.Score))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ----------------------------------------------------------------------------
// MRU Infrastructure Functions (Session-Level)
// ----------------------------------------------------------------------------

// clearSessionMRUBySourceAndScope removes MRU items matching source and scope.
func clearSessionMRUBySourceAndScope(session *RunnerSession, source string, scope string) {
	if session == nil {
		return
	}
	session.MRUMu.Lock()
	defer session.MRUMu.Unlock()

	scope = normalizeMRUScope(scope)
	filtered := session.MRU[:0]
	for _, item := range session.MRU {
		if item.Source == source && normalizeMRUScope(item.Scope) == scope {
			continue
		}
		filtered = append(filtered, item)
	}
	session.MRU = filtered
}

// markSessionMRUCategory writes or updates a single MRU item in the session.
func markSessionMRUCategory(session *RunnerSession, category string, context string, source string, scope string) {
	if session == nil {
		return
	}
	session.MRUMu.Lock()
	defer session.MRUMu.Unlock()

	ts := time.Now().UnixMilli()
	scope = normalizeMRUScope(scope)

	// Update if exists
	for i, item := range session.MRU {
		if item.Category == category {
			session.MRU[i].LastAccessed = ts
			if context != "" {
				session.MRU[i].Context = context
			}
			if source != "" {
				session.MRU[i].Source = source
			}
			session.MRU[i].Scope = scope
			return
		}
	}

	// Add new
	session.MRU = append(session.MRU, MRUItem{
		Category:     category,
		LastAccessed: ts,
		Context:      context,
		Source:       source,
		Scope:        scope,
	})
	// Sort by newest and shrink if > MaxMRUSize
	if len(session.MRU) > MaxMRUSize {
		sort.Slice(session.MRU, func(i, j int) bool {
			return session.MRU[i].LastAccessed > session.MRU[j].LastAccessed
		})
		session.MRU = session.MRU[:MaxMRUSize]
	}
}

// persistSessionAskOutcomeMRU writes ask outcome to session MRU (PUSH model).
// This is the infrastructure-level MRU PUSH that benefits all provider loops.
func persistSessionAskOutcomeMRU(ctx context.Context, session *RunnerSession, query string, finalText string, toolCalls []ai.ToolCall, outcomeFacts []string, carryoverState *ai.CarryoverState) {
	if session == nil {
		return
	}
	clearSessionMRUBySourceAndScope(session, MRUSourceAskOutcome, MRUScopeSession)
	for _, item := range buildAskOutcomeMRUItems(ctx, query, finalText, toolCalls, outcomeFacts, carryoverState) {
		markSessionMRUCategory(session, item.Category, item.Context, item.Source, item.Scope)
	}
}

// Helper methods for AskWithRequest - these use explicit parameters instead of context lookups

// extractProviderOverrideFromContext extracts ProviderOverride from context for backward compatibility
func extractProviderOverrideFromContext(ctx context.Context) *ProviderDetails {
	config := &ProviderDetails{}

	if provider, ok := ctx.Value(ai.CtxKeyProvider).(string); ok && provider != "" {
		// Split provider:model if present
		if providerPart, modelPart, ok := strings.Cut(provider, ":"); ok {
			config.Provider = providerPart
			config.Model = modelPart
		} else {
			config.Provider = provider
		}
	}

	if apiKey, ok := ctx.Value(ai.CtxKeyAPIKey).(string); ok && apiKey != "" {
		config.APIKey = ai.Secret(apiKey)
	}

	if baseURL, ok := ctx.Value(ai.CtxKeyBaseURL).(string); ok && baseURL != "" {
		config.BaseURL = baseURL
	}

	// Return nil if config is empty
	if config.Provider == "" && config.APIKey == "" && config.BaseURL == "" {
		return nil
	}

	return config
}

// handleSessionCommandWithRequest is the explicit-parameter version of handleSessionCommand
func (s *Service) handleSessionCommandWithRequest(ctx context.Context, query string, db *database.Database, session *ai.SessionPayload) (string, bool, error) {
	// Temporarily inject session into context for legacy compatibility
	ctx = context.WithValue(ctx, SessionPayloadKey, session)
	return s.handleSessionCommand(ctx, query, db)
}

// handlePendingUserConfirmationWithRequest is the explicit-parameter version of handlePendingUserConfirmation
func (s *Service) handlePendingUserConfirmationWithRequest(ctx context.Context, query string, session *ai.SessionPayload) (bool, string, error) {
	ctx = context.WithValue(ctx, SessionPayloadKey, session)
	return s.handlePendingUserConfirmation(ctx, query)
}

// performTopicRoutingWithRequest is the explicit-parameter version of performTopicRouting
func (s *Service) performTopicRoutingWithRequest(ctx context.Context, query string, session *ai.SessionPayload) *topicRoutingResult {
	ctx = context.WithValue(ctx, SessionPayloadKey, session)
	return s.performTopicRouting(ctx, query)
}

// buildPromptInputsWithRequest is the explicit-parameter version of buildPromptInputs
func (s *Service) buildPromptInputsWithRequest(ctx context.Context, query string, hits []ai.Hit[map[string]any], session *ai.SessionPayload) *promptInputs {
	ctx = context.WithValue(ctx, SessionPayloadKey, session)
	return s.buildPromptInputs(ctx, query, hits)
}

// resolveGeneratorAndCarryoverWithRequest is the explicit-parameter version that accepts ProviderOverride explicitly
func (s *Service) resolveGeneratorAndCarryoverWithRequest(ctx context.Context, gen ai.Generator, providerOverride *ProviderDetails, topicAssessment *TopicAssessment, historyText string) *generatorConfig {
	// Apply provider override if provided
	if providerOverride != nil && providerOverride.Provider != "" {
		provider := providerOverride.Provider
		if provider == "openai" {
			provider = "chatgpt" // Normalize openai to chatgpt
		}

		// Build config map for generator initialization
		configMap := make(map[string]any)
		if providerOverride.APIKey != "" {
			configMap["api_key"] = string(providerOverride.APIKey)
		}
		if providerOverride.BaseURL != "" {
			configMap["base_url"] = providerOverride.BaseURL
		}
		if providerOverride.Model != "" {
			configMap["model"] = providerOverride.Model
		}

		// Create generator with config
		if overriddenGen, err := generator.New(provider, configMap); err == nil {
			gen = overriddenGen
		} else {
			log.Warn("Failed to initialize requested provider, falling back to provided generator",
				"provider", logsafe.V(provider), "error", logsafe.V(err))
		}
	}

	// Use provided generator if still nil
	if gen == nil {
		gen = s.generator
	}

	carryover := decideCarryover(ctx, s.session, gen, topicAssessment, historyText)

	log.Info("Carryover Decision",
		"provider", providerName(gen),
		"mode", carryover.Mode,
		"reason", carryover.Reason,
		"history_suppressed", carryover.SuppressHistory,
		"estimated_carry_tokens", carryover.EstimatedCarryTokens,
		"estimated_history_tokens", carryover.EstimatedHistoryTokens,
	)

	return &generatorConfig{
		generator: gen,
		carryover: carryover,
	}
}

// executeReasoningEngineWithRequest is the explicit-parameter version with explicit executor and streamer
func (s *Service) executeReasoningEngineWithRequest(ctx context.Context, query string, prompts *promptInputs, genCfg *generatorConfig, db *database.Database, executor ai.ToolExecutor, eventStreamer func(string, any), verbose bool) (ai.ReasoningResponse, error) {
	engine := &NativeReActEngine{
		EnableObfuscation: s.EnableObfuscation,
	}

	contextText := prompts.contextText
	historyText := prompts.historyText

	if strings.TrimSpace(genCfg.carryover.Summary) != "" {
		contextText = appendCarryoverToContext(contextText, genCfg.carryover.Summary)
	}
	if genCfg.carryover.Mode == ai.CarryoverModeCompact && genCfg.carryover.SuppressHistory {
		historyText = ""
	}

	req := ai.ReasoningRequest{
		SystemPrompt:   prompts.systemPrompt,
		ContextText:    contextText,
		HistoryText:    historyText,
		UserQuery:      query,
		Executor:       &autoTxExecutor{original: executor, s: s, db: db},
		Generator:      genCfg.generator,
		CarryoverMode:  genCfg.carryover.Mode,
		CarryoverState: genCfg.carryover.State,
		Streamer:       eventStreamer,
		Verbose:        verbose,
	}

	return engine.Run(ctx, req)
}

// updateSessionMemoryWithRequest is the explicit-parameter version with explicit session
func (s *Service) updateSessionMemoryWithRequest(ctx context.Context, query string, finalText string, topicAssessment *TopicAssessment, engineResp ai.ReasoningResponse, gen ai.Generator, session *ai.SessionPayload) {
	// Temporarily inject session into context for legacy compatibility
	ctx = context.WithValue(ctx, SessionPayloadKey, session)
	s.updateSessionMemory(ctx, query, finalText, topicAssessment, engineResp, gen)
}
