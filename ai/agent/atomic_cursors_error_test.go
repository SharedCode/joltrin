package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/jsondb"
)

// erroringSkipStore simulates a store whose underlying Next call fails
// (e.g. a real I/O error) while StoreCursor is skipping a non-matching row.
type erroringSkipStore struct {
	items []MockItem
	idx   int
}

func (s *erroringSkipStore) First(ctx context.Context) (bool, error) {
	s.idx = 0
	return len(s.items) > 0, nil
}

func (s *erroringSkipStore) Last(ctx context.Context) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) Next(ctx context.Context) (bool, error) {
	return false, errors.New("simulated store I/O error")
}

func (s *erroringSkipStore) Previous(ctx context.Context) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) GetCurrentKey() any {
	if s.idx < 0 || s.idx >= len(s.items) {
		return nil
	}
	return s.items[s.idx].Key
}

func (s *erroringSkipStore) GetCurrentValue(ctx context.Context) (any, error) {
	if s.idx < 0 || s.idx >= len(s.items) {
		return nil, nil
	}
	return s.items[s.idx].Value, nil
}

func (s *erroringSkipStore) GetCurrentValueNoLock(ctx context.Context) (any, error) {
	return s.GetCurrentValue(ctx)
}

func (s *erroringSkipStore) RLockCurrentItem(ctx context.Context) error {
	return nil
}

func (s *erroringSkipStore) Add(ctx context.Context, key, value any) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) Update(ctx context.Context, key, value any) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) Remove(ctx context.Context, key any) (bool, error) {
	return false, nil
}

func (s *erroringSkipStore) GetStoreInfo() sop.StoreInfo {
	return sop.StoreInfo{Name: "erroring"}
}

var _ jsondb.StoreAccessor = (*erroringSkipStore)(nil)

// A filtered StoreCursor skips non-matching rows by calling Previous/Next on
// the underlying store directly (bypassing the outer function's own err
// check). If that call fails, the error must reach the caller instead of
// being treated as end-of-iteration.
func TestStoreCursor_Next_PropagatesErrorFromFilteredSkip(t *testing.T) {
	store := &erroringSkipStore{
		items: []MockItem{{Key: "k1", Value: map[string]any{"name": "mismatch"}}},
	}
	ctx := context.Background()
	cursor := &StoreCursor{
		store:  store,
		ctx:    ctx,
		filter: map[string]any{"name": "john"},
		engine: &ScriptEngine{Context: NewScriptContext()},
	}

	item, ok, err := cursor.Next(ctx)
	if err == nil {
		t.Fatalf("cursor.Next() = (%v, %v, nil), want a non-nil error propagated from the failed skip-advance", item, ok)
	}
	if ok {
		t.Errorf("cursor.Next() returned ok=true alongside an error")
	}
}
