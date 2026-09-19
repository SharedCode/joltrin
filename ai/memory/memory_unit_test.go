package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/inmemory"
)

type fakeMemoryDB struct {
	mu          sync.Mutex
	stores      map[string]any
	beginCount  atomic.Int32
	commitCount atomic.Int32
}

func newFakeMemoryDB() *fakeMemoryDB {
	return &fakeMemoryDB{stores: make(map[string]any)}
}

func (db *fakeMemoryDB) BeginTransaction(ctx context.Context, mode sop.TransactionMode, maxTime ...time.Duration) (sop.Transaction, error) {
	db.beginCount.Add(1)
	return &fakeMemoryTx{commit: func() { db.commitCount.Add(1) }}, nil
}

func (db *fakeMemoryDB) Config() sop.DatabaseOptions {
	return sop.DatabaseOptions{}
}

func (db *fakeMemoryDB) OpenKnowledgeBase(ctx context.Context, name string, tx sop.Transaction, llm ai.Generator, embedder ai.Embeddings, documentMode bool, enableTextSearch ...bool) (*KnowledgeBase[map[string]any], error) {
	return nil, nil
}

func (db *fakeMemoryDB) NewBtree(ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[string, any], error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if existing, ok := db.stores[name]; ok {
		return existing.(btree.BtreeInterface[string, any]), nil
	}
	tree := inmemory.NewBtree[string, any](true).Btree
	db.stores[name] = tree
	return tree, nil
}

func (db *fakeMemoryDB) Btree(name string) btree.BtreeInterface[string, any] {
	db.mu.Lock()
	defer db.mu.Unlock()
	if tree, ok := db.stores[name]; ok {
		return tree.(btree.BtreeInterface[string, any])
	}
	return nil
}

type fakeMemoryTx struct {
	onCommit []func(context.Context) error
	commit   func()
	begun    bool
}

func (tx *fakeMemoryTx) Begin(ctx context.Context) error {
	tx.begun = true
	return nil
}

func (tx *fakeMemoryTx) Commit(ctx context.Context) error {
	for _, callback := range tx.onCommit {
		if err := callback(ctx); err != nil {
			return err
		}
	}
	if tx.commit != nil {
		tx.commit()
	}
	return nil
}

func (tx *fakeMemoryTx) Rollback(ctx context.Context) error { return nil }
func (tx *fakeMemoryTx) HasBegun() bool                     { return tx.begun }
func (tx *fakeMemoryTx) GetPhasedTransaction() sop.TwoPhaseCommitTransaction {
	return nil
}
func (tx *fakeMemoryTx) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {}
func (tx *fakeMemoryTx) GetStores(ctx context.Context) ([]string, error)                        { return nil, nil }
func (tx *fakeMemoryTx) Close() error                                                           { return nil }
func (tx *fakeMemoryTx) GetID() sop.UUID                                                        { return sop.UUID{} }
func (tx *fakeMemoryTx) CommitMaxDuration() time.Duration                                       { return 0 }
func (tx *fakeMemoryTx) OnCommit(callback func(ctx context.Context) error) {
	tx.onCommit = append(tx.onCommit, callback)
}

func TestMemoryStoreNames_IncludeUserIDWhenBound(t *testing.T) {
	ctx := context.WithValue(context.Background(), ai.CtxKeySessionPayload, &ai.SessionPayload{UserID: "user42"})
	m := NewMemoryUnit("omni")
	m.BindSession(ctx)

	if got := m.ShortTermMemoryName(); got != "stm_user42" {
		t.Fatalf("expected user-scoped STM name, got %q", got)
	}
	if got := m.ShortTermMemoryTimeIndexName(); got != "stm_user42_by_time" {
		t.Fatalf("expected user-scoped STM time index name, got %q", got)
	}
	if got := m.LongTermMemoryName(); got != "ltm_user42" {
		t.Fatalf("expected user-scoped LTM name, got %q", got)
	}
}

func TestPruneSTMOlderThan_RemovesExpiredEpisodes(t *testing.T) {
	ctx := context.Background()
	stm := inmemory.NewBtree[string, any](true).Btree
	now := time.UnixMilli(2 * MaxSTMEpisodeAge.Milliseconds())

	if _, err := stm.Add(ctx, "root_anchor", map[string]any{"id": "root_anchor", "created_at": now.UnixMilli()}); err != nil {
		t.Fatalf("failed to add root anchor: %v", err)
	}
	if _, err := stm.Add(ctx, "old_episode", map[string]any{"id": "old_episode", "created_at": now.Add(-25 * time.Hour).UnixMilli()}); err != nil {
		t.Fatalf("failed to add old episode: %v", err)
	}
	if _, err := stm.Add(ctx, "fresh_episode", map[string]any{"id": "fresh_episode", "created_at": now.Add(-2 * time.Hour).UnixMilli()}); err != nil {
		t.Fatalf("failed to add fresh episode: %v", err)
	}

	stmByTime := inmemory.NewBtree[string, any](true).Btree
	if _, err := stmByTime.Add(ctx, STMTimeIndexKey(now.Add(-25*time.Hour).UnixMilli(), "old_episode"), "old_episode"); err != nil {
		t.Fatalf("failed to add old time index: %v", err)
	}
	if _, err := stmByTime.Add(ctx, STMTimeIndexKey(now.Add(-2*time.Hour).UnixMilli(), "fresh_episode"), "fresh_episode"); err != nil {
		t.Fatalf("failed to add fresh time index: %v", err)
	}

	removed, err := PruneSTMOlderThan(ctx, stm, stmByTime, now, MaxSTMEpisodeAge)
	if err != nil {
		t.Fatalf("PruneSTMOlderThan failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected one expired episode to be pruned, got %d", removed)
	}

	if found, err := stm.Find(ctx, "old_episode", false); err != nil {
		t.Fatalf("failed finding old episode: %v", err)
	} else if found {
		t.Fatalf("expected old episode to be removed")
	}
	if found, err := stm.Find(ctx, "fresh_episode", false); err != nil {
		t.Fatalf("failed finding fresh episode: %v", err)
	} else if !found {
		t.Fatalf("expected fresh episode to remain")
	}
	if found, err := stm.Find(ctx, "root_anchor", false); err != nil {
		t.Fatalf("failed finding root anchor: %v", err)
	} else if !found {
		t.Fatalf("expected root anchor to remain")
	}
	if found, err := stmByTime.Find(ctx, STMTimeIndexKey(now.Add(-25*time.Hour).UnixMilli(), "old_episode"), false); err != nil {
		t.Fatalf("failed finding old time index: %v", err)
	} else if found {
		t.Fatalf("expected old time index to be removed")
	}
	if found, err := stmByTime.Find(ctx, STMTimeIndexKey(now.Add(-2*time.Hour).UnixMilli(), "fresh_episode"), false); err != nil {
		t.Fatalf("failed finding fresh time index: %v", err)
	} else if !found {
		t.Fatalf("expected fresh time index to remain")
	}
}

func TestShortTermMemoryStore_DedupedUpsertDoesNotRefreshTimeIndex(t *testing.T) {
	ctx := context.Background()
	store := NewShortTermMemoryStore("agent-1", MaxSTMEpisodeAge)
	primary := inmemory.NewBtree[string, any](true).Btree
	byTime := inmemory.NewBtree[string, any](true).Btree
	store.Attach(primary, byTime)

	firstCreatedAt := time.Now().Add(-3 * time.Hour).UnixMilli()
	updatedCreatedAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	payload := map[string]any{"id": "episode-1", "created_at": firstCreatedAt, "thought": "first"}

	if err := store.UpsertEpisode(ctx, payload); err != nil {
		t.Fatalf("UpsertEpisode initial insert failed: %v", err)
	}
	if found, err := primary.Find(ctx, "episode-1", false); err != nil {
		t.Fatalf("failed finding inserted episode: %v", err)
	} else if !found {
		t.Fatalf("expected inserted episode to exist")
	}
	if found, err := byTime.Find(ctx, STMTimeIndexKey(firstCreatedAt, "episode-1"), false); err != nil {
		t.Fatalf("failed finding initial time index: %v", err)
	} else if !found {
		t.Fatalf("expected initial time index to exist")
	}

	updated := map[string]any{"id": "episode-1", "created_at": updatedCreatedAt, "thought": "updated"}
	if err := store.UpsertEpisode(ctx, updated); err != nil {
		t.Fatalf("UpsertEpisode duplicate insert failed: %v", err)
	}
	if found, err := byTime.Find(ctx, STMTimeIndexKey(firstCreatedAt, "episode-1"), false); err != nil {
		t.Fatalf("failed finding original time index: %v", err)
	} else if !found {
		t.Fatalf("expected original time index to remain after dedupe")
	}
	if found, err := byTime.Find(ctx, STMTimeIndexKey(updatedCreatedAt, "episode-1"), false); err != nil {
		t.Fatalf("failed finding duplicate time index: %v", err)
	} else if found {
		t.Fatalf("expected duplicate episode not to create a fresh time index")
	}
	if found, err := primary.Find(ctx, "episode-1", false); err != nil {
		t.Fatalf("failed finding deduped episode: %v", err)
	} else if !found {
		t.Fatalf("expected deduped episode to remain in primary store")
	}
	if found, err := primary.Find(ctx, "episode-1", false); err != nil {
		t.Fatalf("failed locating deduped episode payload: %v", err)
	} else if !found {
		t.Fatalf("expected deduped episode payload to remain readable")
	} else if existing, err := primary.GetCurrentValue(ctx); err != nil {
		t.Fatalf("failed reading deduped episode payload: %v", err)
	} else if storedPayload, ok := existing.(map[string]any); ok {
		if gotCreatedAt, _ := ExtractSTMCreatedAt(storedPayload); gotCreatedAt != firstCreatedAt {
			t.Fatalf("expected deduped episode to preserve original created_at, got %d want %d", gotCreatedAt, firstCreatedAt)
		}
	}

	if err := store.RemoveEpisode(ctx, "episode-1", payload); err != nil {
		t.Fatalf("RemoveEpisode failed: %v", err)
	}
	if found, err := primary.Find(ctx, "episode-1", false); err != nil {
		t.Fatalf("failed finding removed episode: %v", err)
	} else if found {
		t.Fatalf("expected removed episode to be gone from primary store")
	}
	if found, err := byTime.Find(ctx, STMTimeIndexKey(updatedCreatedAt, "episode-1"), false); err != nil {
		t.Fatalf("failed finding removed time index: %v", err)
	} else if found {
		t.Fatalf("expected removed episode time index to be gone")
	}
}

func TestShortTermMemoryStore_StartPeriodicCommitterFlushesAndPrunes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := newFakeMemoryDB()
	store := NewShortTermMemoryStore("agent-committer", MaxSTMEpisodeAge)
	queue := make(chan map[string]any, 4)

	oldCreatedAt := time.Now().Add(-25 * time.Hour).UnixMilli()
	freshCreatedAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	primary, err := db.NewBtree(ctx, store.StoreName(), tx)
	if err != nil {
		t.Fatalf("NewBtree primary failed: %v", err)
	}
	byTime, err := db.NewBtree(ctx, store.TimeIndexName(), tx)
	if err != nil {
		t.Fatalf("NewBtree time index failed: %v", err)
	}
	store.Attach(primary, byTime)
	if err := store.UpsertEpisode(ctx, map[string]any{"id": "old-episode", "created_at": oldCreatedAt, "thought": "old"}); err != nil {
		t.Fatalf("failed seeding old episode: %v", err)
	}
	store.Close()

	queue <- map[string]any{"id": "fresh-episode", "created_at": freshCreatedAt, "thought": "fresh"}

	if err := store.StartPeriodicCommitter(ctx, db, queue); err != nil {
		t.Fatalf("StartPeriodicCommitter failed: %v", err)
	}

	baselineBegins := db.beginCount.Load()
	baselineCommits := db.commitCount.Load()

	deadline := time.Now().Add(2 * time.Second)
	for db.beginCount.Load() <= baselineBegins && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if db.beginCount.Load() <= baselineBegins {
		t.Fatalf("expected periodic committer to begin a transaction")
	}

	cancel()

	deadline = time.Now().Add(2 * time.Second)
	for db.commitCount.Load() <= baselineCommits && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if db.commitCount.Load() <= baselineCommits {
		t.Fatalf("expected periodic committer to commit a batch")
	}

	stm := db.Btree(store.StoreName())
	if stm == nil {
		t.Fatalf("expected STM primary btree to be created")
	}
	indexTree := db.Btree(store.TimeIndexName())
	if indexTree == nil {
		t.Fatalf("expected STM time index btree to be created")
	}

	if found, err := stm.Find(context.Background(), "old-episode", false); err != nil {
		t.Fatalf("failed finding old episode: %v", err)
	} else if found {
		t.Fatalf("expected old episode to be pruned during commit")
	}
	if found, err := stm.Find(context.Background(), "fresh-episode", false); err != nil {
		t.Fatalf("failed finding fresh episode: %v", err)
	} else if !found {
		t.Fatalf("expected fresh episode to remain after commit")
	}
	if found, err := indexTree.Find(context.Background(), STMTimeIndexKey(oldCreatedAt, "old-episode"), false); err != nil {
		t.Fatalf("failed finding old time index: %v", err)
	} else if found {
		t.Fatalf("expected old time index to be pruned during commit")
	}
	if found, err := indexTree.Find(context.Background(), STMTimeIndexKey(freshCreatedAt, "fresh-episode"), false); err != nil {
		t.Fatalf("failed finding fresh time index: %v", err)
	} else if !found {
		t.Fatalf("expected fresh time index to remain after commit")
	}
}
