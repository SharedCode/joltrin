package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/search"
)

// ContextKey is a type for context keys used in the AI package.
type ContextKey string

// Secret holds a value that must never be written to a log or error message
// in cleartext, such as a transient API key. Its String/GoString/LogValue
// implementations redact the value, so an accidental %v, %+v, or structured
// log.Info("...", "key", value) call can't leak it. Callers that genuinely
// need the raw value (building an HTTP Authorization header, for example)
// must convert explicitly with string(secret).
type Secret string

func (Secret) String() string       { return "[REDACTED]" }
func (Secret) GoString() string     { return "[REDACTED]" }
func (Secret) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

const (
	// CtxKeyProvider is the context key for overriding the AI provider.
	CtxKeyProvider ContextKey = "ai_provider"
	// CtxKeyAPIKey is the context key for passing a transient API key
	CtxKeyAPIKey ContextKey = "ai_api_key"
	// CtxKeyBaseURL is the context key for passing a transient Base URL
	CtxKeyBaseURL ContextKey = "ai_base_url"
	// CtxKeyAppBaseURL is the context key for passing the public app origin used to build absolute viewer links.
	CtxKeyAppBaseURL ContextKey = "ai_app_base_url"
	// CtxKeyExecutor is the context key for passing the ToolExecutor.
	CtxKeyExecutor ContextKey = "ai_executor"
	// CtxKeyDeobfuscator is the context key for passing the Deobfuscator.
	CtxKeyDeobfuscator ContextKey = "ai_deobfuscator"
	// CtxKeyHistory is the context key for passing conversation history.
	CtxKeyHistory ContextKey = "ai_history"
	// CtxKeyWriter is the context key for passing an io.Writer for streaming output.
	CtxKeyWriter ContextKey = "ai_writer"
	// CtxKeyDatabase is the context key for passing the target database for script execution.
	CtxKeyDatabase ContextKey = "ai_database"
	// CtxKeyResultStreamer is the context key for passing the ResultStreamer.
	CtxKeyResultStreamer ContextKey = "ai_result_streamer"
	// CtxKeyScriptRecorder is the context key for passing the ScriptRecorder.
	CtxKeyScriptRecorder ContextKey = "ai_script_recorder"
	// CtxKeyAutoFlush is the context key for enabling/disabling auto-flush (boolean).
	CtxKeyAutoFlush ContextKey = "ai_auto_flush"
	// CtxKeyDefaultFormat is the context key for carrying the requested default output format.
	CtxKeyDefaultFormat ContextKey = "ai_default_format"
	// CtxKeyProgressSink is the context key for passing a structured progress emitter callback.
	CtxKeyProgressSink ContextKey = "ai_progress_sink"
	// CtxKeyEventStreamer is the context key for passing a structured event emitter callback.
	CtxKeyEventStreamer ContextKey = "ai_event_streamer"
	// CtxKeyNativeToolHints marks native Ask-loop tool execution that can consume structured progress hints.
	CtxKeyNativeToolHints ContextKey = "ai_native_tool_hints"
	// CtxKeyIsNewTopic carries the is-new-topic signal from the orchestrating Service down to pipeline agents.
	// When true it indicates the current request starts a fresh conversation thread rather than continuing one.
	CtxKeyIsNewTopic ContextKey = "ai_is_new_topic"
)

// ArtifactType represents the type of a database artifact.
type ArtifactType string

const (
	ArtifactTypeSpace ArtifactType = "Space"
	ArtifactTypeStore ArtifactType = "Store"
)

// ArtifactReference is a unified structure to pass globally referenced DB Artifacts (KB, Space, etc.)
// containing the exact location, avoiding the need to cycle through available databases.
type ArtifactReference struct {
	Name            string              `json:"name"`
	Type            ArtifactType        `json:"type"`
	DatabaseName    string              `json:"db_name"`
	DatabaseOptions sop.DatabaseOptions `json:"db_opts,omitempty"`
}

// KnowledgeDocument represents a single unit of embeddable contextual information
// commonly used to standardize preloading Vector DBs and RAG pipelines.
type KnowledgeDocument struct {
	ID          string                 `json:"id,omitempty"`
	Text        string                 `json:"text,omitempty"`
	PageContent string                 `json:"page_content,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// KnowledgeBasePayload represents the standard JSON payload structure
// used for streaming or batch-loading documents into a Vector DB.
type KnowledgeBasePayload struct {
	DatasetName string              `json:"dataset_name,omitempty"`
	Documents   []KnowledgeDocument `json:"documents"`
}

// ResultStreamer defines the interface for streaming tool results.
type ResultStreamer interface {
	// BeginArray starts a JSON array output.
	BeginArray()
	// SetMetadata sets metadata for the result (e.g. headers, record counts).
	// Should be called before the first WriteItem.
	SetMetadata(meta map[string]any)
	// WriteItem writes a single item to the output (e.g. an element of an array).
	WriteItem(item any)
	// EndArray ends the JSON array output.
	EndArray()
}

// Deobfuscator defines the interface for de-obfuscating text.
type Deobfuscator interface {
	Deobfuscate(text string) string
}

// Embeddings defines the interface for generating vector embeddings from text.
type Embeddings interface {
	// Name returns the name of the embedding model.
	Name() string
	// Dim returns the dimension of the embeddings.
	Dim() int
	// EmbedTexts generates embeddings for a batch of texts.
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbeddingModeSupport adds explicit storage/search/category embedding modes.
// Implementers may use different prefixes or truncation depths for each path.
type EmbeddingModeSupport interface {
	EmbedCategoryTexts(ctx context.Context, texts []string) ([][]float32, error)
	EmbedDocumentTexts(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQueryTexts(ctx context.Context, texts []string) ([][]float32, error)
}

// Centroid represents a cluster center and its metadata.
type Centroid struct {
	Vector      []float32
	VectorCount int
}

// VectorKey is the key for the Vectors B-Tree.
type VectorKey struct {
	CentroidID         int
	DistanceToCentroid float32
	ItemID             string
	// IsDeleted marks this key as a Tombstone.
	// It ensures the Optimize process can find this entry and physically delete
	// the corresponding data from the Content store.
	IsDeleted bool
}

// ContentKey is the key for the Content B-Tree.
// It includes metadata to allow updates without modifying the Value segment (Payload).
//
// SOP's key structure handling allows apps to ride data in it that acts as persistent space
// as well without affecting the overall key/value pair behaviour and BTree ness.
// This means critical metadata (like Version, CentroidID, Deleted flag) is available
// during key traversal (e.g. Range scans) without needing to fetch the potentially large Value payload.
type ContentKey struct {
	ItemID         string  `json:"id"`
	CentroidID     int     `json:"cid"`
	Distance       float32 `json:"dist"`
	Version        int64   `json:"ver"`
	Deleted        bool    `json:"del"`
	NextCentroidID int     `json:"ncid"`
	NextDistance   float32 `json:"ndist"`
	NextVersion    int64   `json:"nver"`
}

// VectorStore defines the interface for a vector database domain (like a table).
type VectorStore[T any] interface {
	// Upsert adds or updates a single item in the store.
	// It now accepts an Item[T] which includes the Vector, Payload, and optional CentroidID.
	Upsert(ctx context.Context, item Item[T]) error
	// UpsertBatch adds or updates multiple items in the store efficiently.
	UpsertBatch(ctx context.Context, items []Item[T]) error
	// Get retrieves an item by its ID.
	Get(ctx context.Context, id string) (*Item[T], error)
	// Delete removes an item by its ID.
	Delete(ctx context.Context, id string) error
	// Query searches for the nearest neighbors to the given vector.
	// filters is a function that returns true if the item should be included.
	Query(ctx context.Context, vec []float32, k int, filter func(T) bool) ([]Hit[T], error)
	// Count returns the total number of items in the store.
	Count(ctx context.Context) (int64, error)
	// AddCentroid adds a new centroid to the store dynamically.
	// This allows for runtime expansion of the concept space without full rebalancing.
	AddCentroid(ctx context.Context, vec []float32) (int, error)

	// SplitCentroid reorganizes an overloaded centroid by running localized 2-Means
	// clustering, creating two new centroids, and reassigning its vectors.
	SplitCentroid(ctx context.Context, centroidID int) error

	// Optimize reorganizes the index to improve query performance.
	// It re-calculates centroids based on the full dataset and re-distributes vectors.
	// This is recommended after a large batch ingestion (BuildOnceQueryMany mode) to "Seal" the index.
	Optimize(ctx context.Context) error

	// Consolidate reads accumulated vectors from short-term memory (TempVectors),
	// dynamically routes them into existing Centroids using AssignAndIndex logic,
	// and clears them from short-term memory.
	Consolidate(ctx context.Context) error

	// UpdateEmbedderInfo updates the configuration defining which embedder was used
	// to index the vectors, persisting it in the system configuration of the store.
	UpdateEmbedderInfo(ctx context.Context, provider string, model string, dimensions int) error

	// SetDeduplication enables or disables the internal deduplication check during Upsert.
	// Disabling this can speed up ingestion for pristine data but may lead to ghost vectors if duplicates exist.
	SetDeduplication(enabled bool)

	// Centroids returns the Centroids B-Tree for advanced manipulation.
	Centroids(ctx context.Context) (btree.BtreeInterface[int, Centroid], error)
	// Vectors returns the Vectors B-Tree for advanced manipulation.
	Vectors(ctx context.Context) (btree.BtreeInterface[VectorKey, []float32], error)
	// Content returns the Content B-Tree for advanced manipulation.
	Content(ctx context.Context) (btree.BtreeInterface[ContentKey, string], error)
	// Lookup returns the Sequence Lookup B-Tree for advanced manipulation (e.g. random sampling).
	Lookup(ctx context.Context) (btree.BtreeInterface[int, string], error)

	// Version returns the Vector store's version number, which is a unix elapsed time.
	Version(ctx context.Context) (int64, error)
}

// UsageMode defines how the vector database is intended to be used.
type UsageMode int

const (
	// BuildOnceQueryMany optimizes for a single ingestion phase followed by read-only queries.
	// Temporary structures (TempVectors, Lookup) are deleted after indexing to save space.
	BuildOnceQueryMany UsageMode = iota

	// DynamicWithVectorCountTracking optimizes for continuous updates (CRUD).
	// It maintains necessary metadata (like vector counts per centroid) to support
	// future rebalancing and structural adjustments.
	DynamicWithVectorCountTracking

	// Dynamic optimizes for continuous updates (CRUD). Does not maintain metadata like vector counts.
	// Useful for scenarios where the Agent explicitly manages the Centroids & Vector assignments.
	Dynamic
)

// Item represents a vector item returned to the user.
type Item[T any] struct {
	ID         string
	Vector     []float32
	Payload    T
	CentroidID int // Optional: Explicitly assign to a centroid (0 = auto)
}

// Hit represents a search result.
type Hit[T any] struct {
	ID      string
	DocID   []string
	Score   float32
	Payload T
}

// Generator defines the interface for an LLM or text generation model.
type Generator interface {
	// Name returns the name of the generator.
	Name() string
	// Generate produces text based on the provided prompt and options.
	Generate(ctx context.Context, prompt string, opts GenOptions) (GenOutput, error)
	// PrewarmCache sends a request to the provider to cache the provided
	// options (e.g., SystemPrompt, Tools) without generating a response.
	// This is used for just-in-time, predictive caching.
	PrewarmCache(ctx context.Context, opts GenOptions) error
	// EstimateCost calculates the estimated cost of the generation.
	EstimateCost(inTokens, outTokens int) float64
}

type AI struct {
	// Provider is the default AI provider to use.
	Provider string `json:"Provider,omitempty"`
	// CacheTTL is the cache duration for AI providers that support it (e.g., "5m", "1h").
	CacheTTL string `json:"CacheTTL,omitempty"`
	// EndPoint is the AI provider's endpoint.
	EndPoint string `json:"Endpoint,omitempty"`
}

// GenOptions configures the generation process.
type GenOptions struct {
	SystemPrompt     string
	MaxTokens        int
	Temperature      float32
	ForceTemperature bool
	TopP             float32
	Stop             []string
	Tools            []ToolDefinition // NEW: Pass the schemas via Native API
	// ToolCallContinuations carries provider-neutral tool-call/result turns that a
	// generator can translate into its provider-specific continuation format.
	ToolCallContinuations []ToolCallContinuation
	// ThinkingLevel controls internal reasoning intensity for Gemini 3.1 Pro (low/medium/high).
	// Use "low" for strict structured outputs, "high" for creative tasks.
	ThinkingLevel string
	// ResponseSchema enforces strict output structure by physically constraining token generation.
	// Pass a JSON schema to prevent hallucinated fields/keys.
	ResponseSchema map[string]any
}

// ToolCallContinuation preserves a completed tool-call turn so generators can
// continue a provider-native function-calling exchange without flattening the
// tool response back into plain prompt text.
type ToolCallContinuation struct {
	ToolCall ToolCall
	Response any
}

// ToolCall represents a native tool call requested by the LLM.
type ToolCall struct {
	Name string
	Args map[string]any
	// NativeID carries the provider-native tool/function call identifier when one exists.
	// It is intentionally transport-agnostic so engines can round-trip tool continuity
	// across Gemini, OpenAI, Anthropic, or future providers without hard-coding one API shape.
	NativeID string
	// TransportMeta preserves provider-specific fields that may be needed to continue a
	// native tool-calling session without flattening everything into prompt text.
	TransportMeta map[string]any
}

// GenOutput represents the result of a generation.
type GenOutput struct {
	Text       string
	TokensUsed int
	Raw        any
	ToolCalls  []ToolCall // NEW: Struct for natively parsed tool requests
}

type CarryoverMode string

const (
	CarryoverModeOff     CarryoverMode = "off"
	CarryoverModeCompact CarryoverMode = "compact"
	CarryoverModeLive    CarryoverMode = "live"
)

type CarryoverResetReason string

const (
	CarryoverResetNone                CarryoverResetReason = ""
	CarryoverResetDisabled            CarryoverResetReason = "disabled"
	CarryoverResetUnsupportedProvider CarryoverResetReason = "unsupported_provider"
	CarryoverResetMissingState        CarryoverResetReason = "missing_state"
	CarryoverResetLiveContinuation    CarryoverResetReason = "live_continuation"
	CarryoverResetTopicSwitch         CarryoverResetReason = "topic_switch"
	CarryoverResetProviderChanged     CarryoverResetReason = "provider_changed"
	CarryoverResetModelChanged        CarryoverResetReason = "model_changed"
	CarryoverResetKBChanged           CarryoverResetReason = "kb_changed"
	CarryoverResetExpired             CarryoverResetReason = "expired"
	CarryoverResetBudgetExceeded      CarryoverResetReason = "budget_exceeded"
	CarryoverResetCompactContinuation CarryoverResetReason = "compact_continuation"
)

type CarryoverCapability struct {
	Provider        string
	Model           string
	SupportsCompact bool
	SupportsLive    bool
}

// CarryoverCapabilityProvider is an optional generator extension that lets a
// provider declare whether it can participate in compact or live carryover
// across adjacent asks.
type CarryoverCapabilityProvider interface {
	CarryoverCapability() CarryoverCapability
}

type CarryoverPolicy struct {
	DefaultMode             CarryoverMode
	LiveCarryoverMaxAsks    int
	LiveCarryoverIdleTTL    time.Duration
	LiveCarryoverSoftTokens int
	LiveCarryoverHardTokens int
	MaxRawToolCarryTokens   int
}

func DefaultCarryoverPolicy() CarryoverPolicy {
	return CarryoverPolicy{
		DefaultMode:             CarryoverModeCompact,
		LiveCarryoverMaxAsks:    4,
		LiveCarryoverIdleTTL:    15 * time.Minute,
		LiveCarryoverSoftTokens: 8000,
		LiveCarryoverHardTokens: 12000,
		MaxRawToolCarryTokens:   2000,
	}
}

type CarryoverState struct {
	Mode                   CarryoverMode
	Provider               string
	Model                  string
	ConversationHandle     string
	ConversationID         string
	TopicFingerprint       string
	KBFingerprint          string
	StartedAtUnixMilli     int64
	LastUsedAtUnixMilli    int64
	AskCount               int
	EstimatedCarryTokens   int
	EstimatedRawToolTokens int
	LastOutcomeFacts       []string
	LastRecipeIDs          []string
	LastToolNames          []string
	LastUserQuery          string
	LastAssistantSummary   string
	LastResetReason        CarryoverResetReason
}

// ReasoningEngine isolates the orchestration loop from the service configuration.
type ReasoningEngine interface {
	Run(ctx context.Context, req ReasoningRequest) (ReasoningResponse, error)
}

// ReActLoop owns a full multi-turn ReAct loop lifecycle rather than just a
// single-turn prompt adaptation.
type ReActLoop interface {
	Run(ctx context.Context, req ReasoningRequest) (ReasoningResponse, error)
}

type ReasoningRequest struct {
	SystemPrompt   string
	ContextText    string
	HistoryText    string
	UserQuery      string
	Executor       ToolExecutor
	Generator      Generator
	CarryoverMode  CarryoverMode
	CarryoverState *CarryoverState
	HydrationSink  MemoryHydrationSink
	Streamer       func(eventType string, data any) // Optional NDJSON callback
	// Verbose enables structured progress/tool-result streaming during the reasoning loop.
	Verbose bool
	// ForceToolCall instructs provider-owned loops to set tool_choice=required so
	// the model must emit a tool call rather than a conversational text response.
	ForceToolCall bool
}

type MemoryHydrationUpdate struct {
	FinalText      string
	ToolCalls      []ToolCall
	OutcomeFacts   []string
	OutcomeRecipes []LearnedRecipe
	CarryoverState *CarryoverState
}

type MemoryHydrationSink func(update MemoryHydrationUpdate)

// ReActToolResult is the provider-neutral per-step state that the ReAct
// engine can expose to provider-specific loop adapters.
type ReActToolResult struct {
	Name   string
	Args   map[string]any
	Result string
	Hint   *ToolProgressHint
}

// ReActPendingRepair is a provider-neutral snapshot of a pending repair path.
type ReActPendingRepair struct {
	ToolName       string
	Strategy       string
	ResearchTool   string
	ResearchReason string
}

type ReActLoopPhase string

const (
	ReActLoopPhaseActive            ReActLoopPhase = "active"
	ReActLoopPhaseRepair            ReActLoopPhase = "repair"
	ReActLoopPhaseClarification     ReActLoopPhase = "clarification"
	ReActLoopPhaseMalformedToolCall ReActLoopPhase = "malformed_tool_call"
)

type ReActNextAction string

const (
	ReActNextActionCallTool         ReActNextAction = "call_tool"
	ReActNextActionRetrySameTool    ReActNextAction = "retry_same_tool"
	ReActNextActionAskClarification ReActNextAction = "ask_clarification"
	ReActNextActionAnswerUser       ReActNextAction = "answer_user"
)

// ReActLoopState captures the current loop-owned state so provider-owned loops
// can observe or eventually own the same transition surface.
type ReActLoopState struct {
	Iteration                int
	AllowedIterations        int
	MaxIterations            int
	RemainingToolCalls       int
	RepairAttempts           int
	Phase                    ReActLoopPhase
	AllowedNextActions       []ReActNextAction
	PendingRepair            *ReActPendingRepair
	PendingMalformedToolCall bool
	ToolResults              []ReActToolResult
	ToolCallContinuations    []ToolCallContinuation
}

// ReActTurn captures a single ReAct generation turn before it reaches a
// provider-specific generator. Providers can adapt the prompt/options while the
// engine retains ownership of retry policy and tool execution.
type ReActTurn struct {
	Iteration       int
	UserQuery       string
	Prompt          string
	RepairDirective string
	LoopState       ReActLoopState
	Options         GenOptions
	ToolResults     []ReActToolResult
	FinalTurn       bool
}

// ReActTurnStrategy is the shared loop-facing contract for shaping a single
// ReAct generation turn before it reaches the provider transport.
type ReActTurnStrategy interface {
	PrepareTurn(ctx context.Context, turn ReActTurn) ReActTurn
}

// ReActPromptBypassStrategy is an optional strategy hook that lets a provider
// skip generic synthetic prompt construction when carried tool-call state is
// sufficient to continue the interaction.
type ReActPromptBypassStrategy interface {
	ShouldBypassPrompt(turn ReActTurn) bool
}

// ReActTurnStrategyProvider lets generators return a provider-specific ReAct
// loop implementation while the engine keeps ownership of retry and execution policy.
type ReActTurnStrategyProvider interface {
	ReActTurnStrategy() ReActTurnStrategy
}

// ReActLoopProvider lets generators return a provider-owned ReAct loop that can
// take over retry, repair, continuation, and termination policy end-to-end.
type ReActLoopProvider interface {
	ReActLoop() ReActLoop
}

// DefaultReActTurnStrategy preserves the generic synthetic-prompt behavior.
// It exists as an explicit implementation so providers can extend or replace it.
type DefaultReActTurnStrategy struct{}

func (DefaultReActTurnStrategy) PrepareTurn(ctx context.Context, turn ReActTurn) ReActTurn {
	_ = ctx
	return turn
}

// LearnedRecipe captures a reusable protocol distilled from successful reasoning behavior.
// It is intentionally separate from grounded facts.
type LearnedRecipe struct {
	ID         string
	Kind       string
	Scope      string
	Domain     string
	Topic      string
	Trigger    string
	Protocol   []string
	Invariants []string
	Confidence float64
	Source     string
}

type ReasoningResponse struct {
	FinalText      string
	ToolCalls      []ToolCall // For audit/logging
	OutcomeFacts   []string   // Compact grounded facts safe to carry into outer MRU continuity
	OutcomeRecipes []LearnedRecipe
	// CarryoverState lets provider-owned loops return updated continuity metadata,
	// such as a reusable live conversation handle, without exposing transport
	// details to the service layer.
	CarryoverState *CarryoverState
}

// PolicyEngine defines the interface for evaluating content against safety policies.
type PolicyEngine interface {
	// Evaluate checks the content against the policy rules.
	Evaluate(ctx context.Context, stage string, sample ContentSample, labels []Label) (PolicyDecision, error)
}

// Classifier defines the interface for content classification models.
type Classifier interface {
	// Name returns the name of the classifier.
	Name() string
	// Classify analyzes the content and returns a list of labels.
	Classify(ctx context.Context, sample ContentSample) ([]Label, error)
}

// ContentSample represents the content being evaluated for safety.
type ContentSample struct {
	Text string
	Meta map[string]any
}

// Label represents a classification result (e.g., "profanity", "hate_speech").
type Label struct {
	Name   string
	Score  float32
	Source string
}

// PolicyDecision represents the outcome of a policy evaluation.
type PolicyDecision struct {
	Action   string   // "allow", "block", "flag"
	Reasons  []string // Why the action was taken
	PolicyID string
}

// Domain represents a vertical or specific AI application domain.
type Domain[T any] interface {
	ID() string
	Name() string
	Embedder() Embeddings
	// Index returns the vector index used for retrieval.
	// It requires a transaction to be passed in.
	Index(ctx context.Context, tx sop.Transaction) (VectorStore[T], error)
	// Memory returns the new Cognitive Knowledge Base for retrieval and episodic reasoning.
	// Returns any to prevent circular dependencies with the memory package.
	Memory(ctx context.Context, tx sop.Transaction) (any, error)
	// TextIndex returns the text search index used for retrieval.
	TextIndex(ctx context.Context, tx sop.Transaction) (TextIndex, error)
	// BeginTransaction starts a new transaction for the domain's underlying storage.
	BeginTransaction(ctx context.Context, mode sop.TransactionMode) (sop.Transaction, error)
	Policies() PolicyEngine
	Classifier() Classifier
	Prompt(ctx context.Context, kind string) (string, error)
	DataPath() string
}

// ConfigMap holds configuration parameters for Agent.Ask calls.
// This is the "goo" type used at the Agent interface boundary for pipeline flexibility.
// Individual agent implementations should define their own typed structs and convert from ConfigMap.
type ConfigMap struct {
	values map[string]any
}

// NewConfigMap creates a new empty ConfigMap.
func NewConfigMap() *ConfigMap {
	return &ConfigMap{values: make(map[string]any)}
}

func (c *ConfigMap) Get(key string) (any, bool) {
	if c == nil || c.values == nil {
		return nil, false
	}
	value, exists := c.values[key]
	return value, exists
}

func (c *ConfigMap) Set(key string, value any) {
	if c == nil {
		return
	}
	if c.values == nil {
		c.values = make(map[string]any)
	}
	c.values[key] = value
}

// ToMap returns the underlying map for marshaling/conversion.
func (c *ConfigMap) ToMap() map[string]any {
	if c == nil || c.values == nil {
		return make(map[string]any)
	}
	return c.values
}

// IsNewTopicFromContext returns the is-new-topic flag stamped into the context by applyAskOptions.
// Returns false if the flag was not set (treat as topic continuation).
func IsNewTopicFromContext(ctx context.Context) bool {
	if v, ok := ctx.Value(CtxKeyIsNewTopic).(bool); ok {
		return v
	}
	return false
}

// Agent defines the interface for an AI agent service.
type Agent[T any] interface {
	// Lifecycle methods
	Open(ctx context.Context) error
	Close(ctx context.Context) error

	Search(ctx context.Context, query string, limit int) ([]Hit[T], error)
	Ask(ctx context.Context, query string, cfg *ConfigMap) (string, error)
}

// SessionPayload represents the context and state for an agent session.
// It carries domain-specific artifacts like database connections.
type SessionPayload struct {
	// CurrentDB is the active database name for the session.
	CurrentDB string
	// CurrentUserQuery stores the raw user request for the current Ask interaction.
	// It is used sparingly for narrow tool fallbacks when the model omits a required arg.
	CurrentUserQuery string
	// UserID identifies the user for privacy and memory isolation.
	UserID string
	// ClientID tags the origin client device or application.
	ClientID string
	// SessionID tracks the specific transient connection/session.
	SessionID string
	// AvatarScope tags the session with the active KBContextID.
	// Used by Omni-Protocol to know which sandbox the conversation occurred within.
	AvatarScope string
	// AgentID represents whether Omni or a specific Avatar generated this payload/memory
	AgentID string
	// ActiveDomain is the knowledge domain selected by the user.
	ActiveDomain string
	// SelectedKBs are the explicit Knowledge Bases selected by the user from the dropdown.
	SelectedKBs []ArtifactReference
	// Transaction holds the active transaction for the session.
	// Deprecated: Migrate usage to TransactionPool.
	Transaction any
	// TransactionPool manages active transactions, their configurations, and pooling across the session.
	TransactionPool *TransactionPool
	// Transactions holds active transactions keyed by database name.
	// Deprecated: Migrate usage to TransactionPool.
	Transactions map[string]any
	// Variables holds session-scoped variables (e.g. cached store instances).
	Variables map[string]any
	// ExplicitTransaction marks when language bindings (Python, Rust, Java, .NET) are managing
	// their own transaction lifecycle via manage_transaction API.
	//
	// When ExplicitTransaction = true:
	//   - Transaction is managed EXTERNALLY by language binding code
	//   - SessionPayload.Close() will NOT auto-commit or auto-rollback
	//   - Language bindings must explicitly call commit/rollback via manage_transaction
	//   - This allows transactions to span multiple API calls (e.g., HTTP requests)
	//
	// When ExplicitTransaction = false (default):
	//   - Transaction is managed INTERNALLY by the agent session
	//   - SessionPayload.Close() will auto-commit the transaction
	//   - Transaction lifecycle is scoped to a single Ask/Execute call
	//
	// WARNING: ExplicitTransaction = true is prone to transaction leaks if language bindings
	// don't properly commit/rollback. Only use when you need multi-request transaction scope.
	ExplicitTransaction bool
	// RetryRewriteState tracks an explicit retry-the-same-ask rewrite.
	RetryRewriteState *RetryRewriteState
	// ClarificationState tracks an explicit pending clarification round before execution resumes.
	ClarificationState *ClarificationState
	// LastInteractionSteps tracks the number of steps added/executed in the last user interaction.
	LastInteractionSteps int
	// ConversationHistory stores the active memory/transcript for the session.
	ConversationHistory string
}

// ClarificationState tracks a pending assistant clarification before the target ask resumes.
type ClarificationState struct {
	TargetQuery        string
	AssistantQuestion  string
	OriginalUserQuery  string
	UserClarification  string
	EffectiveResumeAsk string
	Status             string
}

// RetryRewriteState tracks a retry-the-same-ask rewrite through Gate 0.
type RetryRewriteState struct {
	OriginalQuery string
	ResolvedQuery string
	Status        string
}

// Manages Payload's cleanup, e.g. Transaction Commit/Rollback.
func (sp *SessionPayload) Close(ctx context.Context) error {
	if sp.Transaction == nil {
		return nil
	}
	defer func() {
		sp.Transaction = nil
	}()

	if sp.Transactions != nil {
		// Iterate to remove if any global dbName holds this tx
		for k, v := range sp.Transactions {
			if v == sp.Transaction {
				delete(sp.Transactions, k)
			}
		}
	}

	if sp.ExplicitTransaction {
		// CRITICAL: Explicit transactions are managed externally by language bindings
		// via the manage_transaction API. They must NOT be auto-committed or auto-rolled back
		// by SessionPayload.Close() because:
		//   1. The transaction may span multiple API calls (multi-request scope)
		//   2. Language binding code is responsible for commit/rollback
		//   3. Auto-managing would break the external transaction lifecycle
		// See TestTransactionLifecycle_ExplicitTransaction for validation.
		return nil
	}

	// Auto-commit implicit (session-managed) transactions
	if tx, ok := sp.Transaction.(sop.Transaction); ok {
		if tx.HasBegun() {
			return tx.Commit(ctx)
		}
	}
	return nil
}

func (s *SessionPayload) GetDatabase() string {
	return s.CurrentDB
}

// GetSessionPayload retrieves the session payload from the context.
func GetSessionPayload(ctx context.Context) *SessionPayload {
	if val := ctx.Value("session_payload"); val != nil {
		if p, ok := val.(*SessionPayload); ok {
			return p
		}
	}
	return nil
}

// ToolExecutor defines the interface for the application to expose capabilities to the Agent.
// This allows the Agent to "act" on the application (e.g. query DB, send email).
type ToolExecutor interface {
	// Execute runs a named tool with the provided arguments.
	Execute(ctx context.Context, toolName string, args map[string]any) (string, error)
	// ListTools returns the list of available tools.
	ListTools(ctx context.Context) ([]ToolDefinition, error)
}

// ToolProgressHint is an optional progress envelope that a tool can return to guide
// the native Ask loop toward the next narrowing step without directly mutating scratchpad state.
type ToolProgressHint struct {
	Status             string   `json:"status,omitempty"`
	CompletionDelta    float64  `json:"completion_delta,omitempty"`
	Confidence         float64  `json:"confidence,omitempty"`
	Missing            []string `json:"missing,omitempty"`
	Tips               []string `json:"tips,omitempty"`
	Clues              []string `json:"clues,omitempty"`
	SuggestedNextTools []string `json:"suggested_next_tools,omitempty"`
}

// ToolResultEnvelope is an optional JSON result shape that tools can return.
// If present, `tool_result` remains the user-visible payload while `progress_hint`
// feeds the engine's internal progress and retry budgeting.
type ToolResultEnvelope struct {
	ToolResult   json.RawMessage   `json:"tool_result,omitempty"`
	ProgressHint *ToolProgressHint `json:"progress_hint,omitempty"`
}

// ToolDefinition describes a tool available to the agent.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Schema is a JSON schema string describing the arguments.
	Schema string `json:"schema"`
}

// AgentControl defines methods to manage the agent's lifecycle.
// This allows the Application to control the Agent (e.g. Stop/Pause).
type AgentControl interface {
	// Stop aborts the current operation.
	Stop() error
	// Pause suspends the agent's activities (if supported).
	Pause() error
	// Resume resumes the agent's activities.
	Resume() error
}

// TextIndex defines the interface for a text search index.
type TextIndex interface {
	// Add indexes a document.
	Add(ctx context.Context, docID string, text string) error
	// Search performs a text search.
	Search(ctx context.Context, query string) ([]search.TextSearchResult, error)
}

// ModelStore defines the interface for persisting and retrieving AI models.
// It allows for the management of "Skills" (small models) and "Brains" (large models).
type ModelStore interface {
	// Save persists a model with the given name and category.
	// The model can be any serializable object (e.g., Perceptron, NeuralNet).
	Save(ctx context.Context, category string, name string, model any) error

	// Load retrieves a model by name and category and populates the provided object.
	// The target parameter must be a pointer to the model struct.
	Load(ctx context.Context, category string, name string, target any) error

	// List returns the names of all stored models in a given category.
	List(ctx context.Context, category string) ([]string, error)

	// Delete removes a model from the store.
	Delete(ctx context.Context, category string, name string) error
}

// Script represents a recorded sequence of user interactions or a programmed script.
type Script struct {
	Description string   `json:"description"`
	Parameters  []string `json:"parameters"`         // Input parameters for the script
	Database    string   `json:"database,omitempty"` // Database to run the script against
	Portable    bool     `json:"portable,omitempty"` // If true, allows running on any database
	// TransactionMode specifies how transactions are handled for this script.
	// Values: "none" (default, manual), "single" (one tx for all steps), "per_step" (auto-commit each step)
	TransactionMode string       `json:"transaction_mode,omitempty"`
	Steps           []ScriptStep `json:"steps"`
}

// ScriptStep represents a single instruction in a script.
// It follows a stabilized schema for "Natural Language Programming".
type ScriptStep struct {
	// Type of the step.
	// Valid values: "ask", "set", "if", "loop", "fetch", "say", "command", "call_script" (or "script"), "block"
	Type string `json:"type"`

	// Name acts as a label or identifier for the step.
	// It can be used for UI display or referenced in complex flows.
	Name string `json:"name,omitempty"`

	// --- Fields for "ask" (LLM Interaction) ---
	// Prompt is the question or command to send to the LLM. Supports templating.
	Prompt string `json:"prompt,omitempty"`
	// OutputVariable is where the LLM's response will be stored.
	// If empty, the response is just printed.
	OutputVariable string `json:"output_variable,omitempty"`
	// InputVariable is the variable to use as input for this step.
	InputVariable string `json:"input_variable,omitempty"`

	// --- Fields for "set" (Variable Assignment) ---
	// Variable is the name of the variable to set.
	Variable string `json:"variable,omitempty"`
	// Value is the value to assign. Supports templating.
	Value string `json:"value,omitempty"`

	// --- Fields for "if" (Flow Branching) ---
	// Condition is a Go template expression that must evaluate to "true".
	// Example: "{{ gt .count 5 }}"
	Condition string `json:"condition,omitempty"`
	// Then is the list of steps to execute if the condition is true.
	Then []ScriptStep `json:"then,omitempty"`
	// Else is the list of steps to execute if the condition is false.
	Else []ScriptStep `json:"else,omitempty"`

	// --- Fields for "loop" (Iteration) ---
	// List is the variable name or expression evaluating to a list/slice to iterate over.
	List string `json:"list,omitempty"`
	// Iterator is the variable name for the current item in the loop.
	Iterator string `json:"iterator,omitempty"`
	// Steps is the list of steps to execute for each item.
	// Also used for "block" type to hold the sequence of steps.
	Steps []ScriptStep `json:"steps,omitempty"`

	// --- Fields for "fetch" (Data Retrieval) ---
	// Database specifies the database name (optional). If empty, uses the current context database.
	Database string `json:"database,omitempty"`
	// Source specifies the data source type (e.g., "btree", "vector_store").
	Source string `json:"source,omitempty"`
	// Resource specifies the name of the resource (e.g., table name).
	Resource string `json:"resource,omitempty"`
	// Filter is an optional filter expression (not yet fully implemented).
	Filter string `json:"filter,omitempty"`
	// (Uses Variable field to store the result)

	// --- Fields for "say" (Output) ---
	// Message is the text to output to the user. Supports templating.
	Message string `json:"message,omitempty"`

	// --- Fields for "command" (Direct Tool Execution) ---
	// Command is the name of the tool/command to execute.
	Command string `json:"command,omitempty"`
	// Args are the arguments for the command.
	Args map[string]any `json:"args,omitempty"`

	// --- Fields for "call_script" (Nested Script Execution) ---
	// ScriptName is the name of the script to execute.
	ScriptName string `json:"script_name,omitempty"`
	// ScriptArgs are the arguments to pass to the script.
	ScriptArgs map[string]string `json:"script_args,omitempty"`

	// --- Async Execution ---
	// IsAsync specifies if the step should be executed asynchronously.
	// If true, the step runs in a goroutine and the script continues to the next step.
	// All async steps are gathered (waited for) at the end of the script execution.
	IsAsync bool `json:"is_async,omitempty"`

	// ContinueOnError specifies if the script should continue executing if this step fails.
	// If false (default), the script stops and returns the error.
	// For async steps, if this is false and the step fails, it cancels the execution of other steps.
	ContinueOnError bool `json:"continue_on_error,omitempty"`

	// --- Documentation ---
	// Description provides a human-readable explanation of what this step does.
	// It is used for documentation and self-explanation of the script.
	Description string `json:"description,omitempty"`
}

// ScriptRecorder is an interface for recording script steps.
type ScriptRecorder interface {
	RecordStep(ctx context.Context, step ScriptStep)
	// RefactorLastSteps refactors the last N steps into a new structure (script or block).
	// count: number of steps to refactor.
	// mode: "script" (extract to new named script) or "block" (group into block step).
	// name: name of the new script (if mode is "script").
	RefactorLastSteps(count int, mode string, name string) error
}
