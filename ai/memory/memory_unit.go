package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

// MemoryUnit encapsulates the cognitive state and boundaries of an Agent instance.
type MemoryUnit struct {
	AgentID    string
	UserID     string
	AllowedKBs []string // LTM scoping boundaries

	// Tracks the last time an episode was logged to STM for idle sleep cycles
	LastEpisodeTS atomic.Int64
	// Agent-scoped STM batching queue
	episodeQueue chan map[string]any

	// Episodic B-Tree buffer for the cognitive footprint
	stm *ShortTermMemoryStore
}

const MaxSTMEpisodeAge = 24 * time.Hour

func NewMemoryUnit(agentID string) *MemoryUnit {
	return &MemoryUnit{
		AgentID:      agentID,
		AllowedKBs:   []string{},
		episodeQueue: make(chan map[string]any, 100),
		stm:          NewShortTermMemoryStore(agentID, MaxSTMEpisodeAge),
	}
}

func (m *MemoryUnit) BindSession(ctx context.Context) {
	if m == nil {
		return
	}
	if payload := ai.GetSessionPayload(ctx); payload != nil {
		m.UserID = strings.TrimSpace(payload.UserID)
	}
	if m.stm != nil {
		m.stm.SetUserID(m.UserID)
	}
}

func (m *MemoryUnit) ShortTermMemory() any {
	if m.stm == nil {
		return nil
	}
	return m.stm.Primary()
}

func (m *MemoryUnit) STMStore() *ShortTermMemoryStore {
	return m.stm
}

func (m *MemoryUnit) ShortTermMemoryName() string {
	if m.stm != nil {
		return m.stm.StoreName()
	}
	return BuildSTMStoreName(m.AgentID, m.UserID)
}

func (m *MemoryUnit) ShortTermMemoryTimeIndexName() string {
	if m.stm != nil {
		return m.stm.TimeIndexName()
	}
	return BuildSTMTimeIndexName(m.AgentID, m.UserID)
}

func (m *MemoryUnit) LongTermMemoryName() string {
	return BuildLTMStoreName(m.AgentID, m.UserID)
}

func (m *MemoryUnit) OpenShortTermMemory(ctx context.Context, systemDB Database, trans sop.Transaction) (any, error) {
	log.Info("MemoryUnit.openShortTermMemory")
	m.BindSession(ctx)
	if m.stm == nil {
		m.stm = NewShortTermMemoryStore(m.AgentID, MaxSTMEpisodeAge)
	}
	m.stm.SetUserID(m.UserID)
	if err := m.stm.Open(ctx, systemDB, trans); err != nil {
		return nil, err
	}
	return m.stm.Primary(), nil
}

func (m *MemoryUnit) CloseShortTermMemory() {
	if m.stm != nil {
		m.stm.Close()
	}
}

func (m *MemoryUnit) OpenLongTermMemory(ctx context.Context, systemDB Database, trans sop.Transaction, llm ai.Generator, embedder ai.Embeddings) (*KnowledgeBase[map[string]any], error) {
	log.Info("MemoryUnit.OpenLongTermMemory")
	m.BindSession(ctx)

	ltmStoreName := m.LongTermMemoryName()
	ltm, err := systemDB.OpenKnowledgeBase(ctx, ltmStoreName, trans, llm, embedder, false, true)
	if err != nil {
		return nil, fmt.Errorf("failed to open isolated LTM KnowledgeBase: %w", err)
	}
	return ltm, nil
}

// LogEpisodeToSTM directly writes to the Agent's physical STM structure
func (m *MemoryUnit) LogEpisodeToSTM(ctx context.Context, intent string, astPayload any, outcome string, executeErr error) {
	m.BindSession(ctx)
	astBytes, err := json.Marshal(astPayload)
	var astStr string
	if err == nil {
		astStr = string(astBytes)
	} else {
		astStr = fmt.Sprintf("%T", astPayload)
	}

	status := "Success"
	errorDesc := ""
	if executeErr != nil {
		status = "Error"
		errorDesc = executeErr.Error()
	}

	// Combine into a structured representation for embedding and retrieval
	thought := fmt.Sprintf("Intent: %s\nAST: %s\nStatus: %s\n", intent, astStr, status)
	if errorDesc != "" {
		thought += fmt.Sprintf("Error: %s\n", errorDesc)
	}
	if status == "Success" && outcome != "" {
		outLog := outcome
		if len(outLog) > 100 {
			outLog = outLog[:100] + "..."
		}
		thought += fmt.Sprintf("Outcome: %s\n", outLog)
	}

	hash := sha256.Sum256([]byte(thought))
	itemID := fmt.Sprintf("%x", hash)

	payload := map[string]any{
		"id":         itemID,
		"intent":     intent,
		"thought":    thought,
		"status":     status,
		"outcome":    outcome,
		"created_at": time.Now().UnixMilli(),
		"agent_id":   m.AgentID,
		"user_id":    m.UserID,
	}

	m.LastEpisodeTS.Store(time.Now().UnixMilli())

	select {
	case m.episodeQueue <- payload:
		log.Debug("Isolated STM: Buffered thought snippet to queue successfully", "agent_id", m.AgentID)
	default:
		log.Warn("Isolated STM: Episode queue is full, dropping thought snippet", "agent_id", m.AgentID)
	}
}

// StartMemoryWorkers launches the dedicated background worker that
// reads episodes from the channel and flushes them to STM.
func (m *MemoryUnit) StartMemoryWorkers(ctx context.Context, systemDB Database) error {
	if systemDB == nil {
		return fmt.Errorf("systemDB can't be nil")
	}
	m.BindSession(ctx)
	if m.stm == nil {
		m.stm = NewShortTermMemoryStore(m.AgentID, MaxSTMEpisodeAge)
	}
	m.stm.SetUserID(m.UserID)
	return m.stm.StartPeriodicCommitter(ctx, systemDB, m.episodeQueue)
}
