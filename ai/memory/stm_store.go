package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

const (
	stmBatchMaxItems = 500
	stmBatchWindow   = 5 * time.Second
)

type ShortTermMemoryStore struct {
	agentID string
	userID  string
	primary btree.BtreeInterface[string, any]
	byTime  btree.BtreeInterface[string, any]
	maxAge  time.Duration
}

func buildMemoryScopeID(agentID string, userID string) string {
	agentID = strings.TrimSpace(agentID)
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return agentID
	}
	if agentID == "" {
		return userID
	}
	return fmt.Sprintf("%s_%s", agentID, userID)
}

func BuildSTMStoreName(agentID string, userID string) string {
	scope := strings.TrimSpace(userID)
	if scope == "" {
		scope = strings.TrimSpace(agentID)
	}
	if scope == "" {
		return "stm_default"
	}
	return fmt.Sprintf("stm_%s", strings.NewReplacer(" ", "_", "/", "_", "\\", "_").Replace(scope))
}

func BuildSTMTimeIndexName(agentID string, userID string) string {
	return fmt.Sprintf("%s_by_time", BuildSTMStoreName(agentID, userID))
}

func BuildLTMStoreName(agentID string, userID string) string {
	scope := strings.TrimSpace(userID)
	if scope == "" {
		scope = strings.TrimSpace(agentID)
	}
	if scope == "" {
		return "ltm_default"
	}
	return fmt.Sprintf("ltm_%s", strings.NewReplacer(" ", "_", "/", "_", "\\", "_").Replace(scope))
}

func STMTimeIndexKey(createdAt int64, itemID string) string {
	return fmt.Sprintf("%020d:%s", createdAt, itemID)
}

func PruneSTMOlderThan(ctx context.Context, stm btree.BtreeInterface[string, any], stmByTime btree.BtreeInterface[string, any], now time.Time, maxAge time.Duration) (int, error) {
	if stm == nil || stmByTime == nil || maxAge <= 0 {
		return 0, nil
	}

	cutoffKey := STMTimeIndexKey(now.Add(-maxAge).UnixMilli(), "")
	var primaryIDs []string
	var indexKeys []string

	ok, err := stmByTime.First(ctx)
	if err != nil {
		return 0, err
	}
	for ok {
		key := stmByTime.GetCurrentKey()
		if key.Key >= cutoffKey {
			break
		}
		if key.Key != "root_anchor" {
			indexKeys = append(indexKeys, key.Key)
			if value, err := stmByTime.GetCurrentValue(ctx); err != nil {
				return 0, err
			} else if id, ok := value.(string); ok && id != "" {
				primaryIDs = append(primaryIDs, id)
			} else if sep := len(key.Key); sep > 21 {
				primaryIDs = append(primaryIDs, key.Key[21:])
			}
		}
		ok, err = stmByTime.Next(ctx)
		if err != nil {
			return 0, err
		}
	}

	for _, id := range primaryIDs {
		if _, err := stm.Remove(ctx, id); err != nil {
			return 0, err
		}
	}
	for _, key := range indexKeys {
		if _, err := stmByTime.Remove(ctx, key); err != nil {
			return 0, err
		}
	}

	return len(indexKeys), nil
}

func ExtractSTMCreatedAt(payload map[string]any) (int64, bool) {
	switch value := payload["created_at"].(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case int32:
		return int64(value), true
	case float64:
		return int64(value), true
	case float32:
		return int64(value), true
	default:
		return 0, false
	}
}

func NewShortTermMemoryStore(agentID string, maxAge time.Duration) *ShortTermMemoryStore {
	return &ShortTermMemoryStore{agentID: agentID, maxAge: maxAge}
}

func (s *ShortTermMemoryStore) SetUserID(userID string) {
	if s == nil {
		return
	}
	s.userID = strings.TrimSpace(userID)
}

func (s *ShortTermMemoryStore) StoreName() string {
	return BuildSTMStoreName(s.agentID, s.userID)
}

func (s *ShortTermMemoryStore) TimeIndexName() string {
	return BuildSTMTimeIndexName(s.agentID, s.userID)
}

func (s *ShortTermMemoryStore) Primary() btree.BtreeInterface[string, any] {
	if s == nil {
		return nil
	}
	return s.primary
}

func (s *ShortTermMemoryStore) Open(ctx context.Context, systemDB Database, tx sop.Transaction) error {
	if s == nil {
		return nil
	}
	if s.primary != nil && s.byTime != nil {
		return nil
	}
	primary, err := systemDB.NewBtree(ctx, s.StoreName(), tx)
	if err != nil {
		return fmt.Errorf("failed to create/open isolated STM BTree: %w", err)
	}
	byTime, err := systemDB.NewBtree(ctx, s.TimeIndexName(), tx)
	if err != nil {
		return fmt.Errorf("failed to create/open isolated STM time index BTree: %w", err)
	}
	s.primary = primary
	s.byTime = byTime
	return nil
}

func (s *ShortTermMemoryStore) Attach(primary btree.BtreeInterface[string, any], byTime btree.BtreeInterface[string, any]) {
	if s == nil {
		return
	}
	s.primary = primary
	s.byTime = byTime
}

func (s *ShortTermMemoryStore) Close() {
	if s == nil {
		return
	}
	s.primary = nil
	s.byTime = nil
}

func (s *ShortTermMemoryStore) PruneExpired(ctx context.Context, now time.Time) (int, error) {
	if s == nil {
		return 0, nil
	}
	return PruneSTMOlderThan(ctx, s.primary, s.byTime, now, s.maxAge)
}

func (s *ShortTermMemoryStore) UpsertEpisode(ctx context.Context, payload map[string]any) error {
	if s == nil || s.primary == nil || s.byTime == nil {
		return nil
	}
	itemID, _ := payload["id"].(string)
	if itemID == "" {
		return fmt.Errorf("stm episode payload missing id")
	}
	createdAt, _ := ExtractSTMCreatedAt(payload)

	ok, err := s.primary.Add(ctx, itemID, payload)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, _ = s.byTime.Add(ctx, STMTimeIndexKey(createdAt, itemID), itemID)
	return nil
}

func (s *ShortTermMemoryStore) RemoveEpisode(ctx context.Context, itemID string, payload map[string]any) error {
	if s == nil || s.primary == nil || s.byTime == nil || itemID == "" {
		return nil
	}
	if _, err := s.primary.Remove(ctx, itemID); err != nil {
		return err
	}
	if createdAt, ok := ExtractSTMCreatedAt(payload); ok {
		if _, err := s.byTime.Remove(ctx, STMTimeIndexKey(createdAt, itemID)); err != nil {
			return err
		}
	}
	return nil
}

func (s *ShortTermMemoryStore) StartPeriodicCommitter(ctx context.Context, systemDB Database, queue <-chan map[string]any) error {
	if s == nil {
		return nil
	}
	if systemDB == nil {
		return fmt.Errorf("systemDB can't be nil")
	}
	if queue == nil {
		return fmt.Errorf("stm episode queue can't be nil")
	}

	go func() {
		log.Info("ShortTermMemoryStore: Periodic committer started", "agent_id", s.agentID)

		for {
			select {
			case <-ctx.Done():
				log.Info("ShortTermMemoryStore: Periodic committer interrupted", "agent_id", s.agentID)
				return
			case firstPayload := <-queue:
				if firstPayload == nil {
					continue
				}

				tx, err := systemDB.BeginTransaction(ctx, sop.ForWriting)
				if err != nil {
					log.Warn("ShortTermMemoryStore: tx begin failed", "agent_id", s.agentID, "error", err)
					continue
				}
				if err := s.Open(ctx, systemDB, tx); err != nil {
					log.Warn("ShortTermMemoryStore: open failed", "agent_id", s.agentID, "error", err)
					tx.Rollback(ctx)
					continue
				}

				if removed, err := s.PruneExpired(ctx, time.Now()); err != nil {
					log.Warn("ShortTermMemoryStore: prune failed", "agent_id", s.agentID, "error", err)
				} else if removed > 0 {
					log.Debug("ShortTermMemoryStore: pruned stale STM episodes", "agent_id", s.agentID, "count", removed)
				}

				batchCount := 0
				if err := s.UpsertEpisode(ctx, firstPayload); err != nil {
					log.Warn("ShortTermMemoryStore: failed to add item", "agent_id", s.agentID, "error", err)
				}
				batchCount++

				timeout := time.After(stmBatchWindow)
				batching := true
				for batching && batchCount < stmBatchMaxItems {
					select {
					case nextPayload := <-queue:
						if nextPayload == nil {
							continue
						}
						if err := s.UpsertEpisode(ctx, nextPayload); err != nil {
							log.Warn("ShortTermMemoryStore: failed to add item", "agent_id", s.agentID, "error", err)
							continue
						}
						batchCount++
					case <-timeout:
						batching = false
					case <-ctx.Done():
						batching = false
					}
				}

				if err := tx.Commit(ctx); err != nil {
					log.Warn("ShortTermMemoryStore: commit failed", "agent_id", s.agentID, "count", batchCount, "error", err)
				} else {
					log.Debug("ShortTermMemoryStore: batch committed to STM successfully", "agent_id", s.agentID, "count", batchCount)
				}
				s.Close()
			}
		}
	}()

	return nil
}
