package jsondb

import (
	"context"
	"encoding/json"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// JsonDBStructKey wraps JsonDBMapKey to support Go struct keys with configurable index specifications.
// It automatically converts struct keys to map[string]any for storage and indexing.
type JsonDBStructKey[TK any, TV any] struct {
	*JsonDBMapKey
}

// NewJsonBtreeStructKey creates a new B-Tree that uses a struct as the Key.
// The struct is converted to a map internally to support the IndexSpecification.
func NewJsonBtreeStructKey[TK any, TV any](ctx context.Context, config sop.DatabaseOptions, so sop.StoreOptions, t sop.Transaction, indexSpecification *IndexSpecification) (*JsonDBStructKey[TK, TV], error) {
	specStr := ""
	if indexSpecification != nil {
		b, err := encoding.DefaultMarshaler.Marshal(indexSpecification)
		if err != nil {
			return nil, err
		}
		specStr = string(b)
	}

	mk, err := NewJsonBtreeMapKey(ctx, config, so, t, specStr)
	if err != nil {
		return nil, err
	}
	return &JsonDBStructKey[TK, TV]{JsonDBMapKey: mk}, nil
}

// OpenJsonBtreeStructKey opens an existing B-Tree that uses a struct as the Key.
func OpenJsonBtreeStructKey[TK any, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction) (*JsonDBStructKey[TK, TV], error) {
	mk, err := OpenJsonBtreeMapKey(ctx, config, name, t)
	if err != nil {
		return nil, err
	}
	return &JsonDBStructKey[TK, TV]{JsonDBMapKey: mk}, nil
}

// Add adds items to the store.
func (j *JsonDBStructKey[TK, TV]) Add(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	mapItems := make([]Item[map[string]any, any], len(items))
	for i, item := range items {
		// Convert Key (Struct) -> Map
		keyMap, err := structToMap(item.Key)
		if err != nil {
			return false, err
		}
		// Convert Value (Struct) -> Any
		var valAny any
		if item.Value != nil {
			valAny = *item.Value
		}

		mapItems[i] = Item[map[string]any, any]{
			Key:   keyMap,
			Value: &valAny,
			ID:    item.ID,
		}
	}
	return j.JsonDBMapKey.Add(ctx, mapItems)
}

// Update updates items in the store.
func (j *JsonDBStructKey[TK, TV]) Update(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	mapItems := make([]Item[map[string]any, any], len(items))
	for i, item := range items {
		keyMap, err := structToMap(item.Key)
		if err != nil {
			return false, err
		}
		var valAny any
		if item.Value != nil {
			valAny = *item.Value
		}
		mapItems[i] = Item[map[string]any, any]{
			Key:   keyMap,
			Value: &valAny,
			ID:    item.ID,
		}
	}
	return j.JsonDBMapKey.Update(ctx, mapItems)
}

// Remove removes items by key.
func (j *JsonDBStructKey[TK, TV]) Remove(ctx context.Context, keys []TK) (bool, error) {
	mapKeys := make([]map[string]any, len(keys))
	for i, key := range keys {
		keyMap, err := structToMap(key)
		if err != nil {
			return false, err
		}
		mapKeys[i] = keyMap
	}
	return j.JsonDBMapKey.Remove(ctx, mapKeys)
}

// Find finds an item by key.
func (j *JsonDBStructKey[TK, TV]) Find(ctx context.Context, key TK, autoScroll bool) (bool, error) {
	keyMap, err := structToMap(key)
	if err != nil {
		return false, err
	}
	return j.JsonDBMapKey.Find(ctx, keyMap, autoScroll)
}

// GetCurrentKey returns the current key as the struct type TK.
func (j *JsonDBStructKey[TK, TV]) GetCurrentKey() TK {
	// Access the underlying B-Tree item directly to get the map key
	item := j.JsonDBMapKey.BtreeInterface.GetCurrentKey()
	var target TK
	// Best effort conversion.
	_ = mapToStruct(item.Key, &target)
	return target
}

// GetCurrentValue returns the current value as the struct type TV.
func (j *JsonDBStructKey[TK, TV]) GetCurrentValue(ctx context.Context) (TV, error) {
	var target TV
	// Access the underlying B-Tree value directly (which is 'any')
	val, err := j.JsonDBMapKey.BtreeInterface.GetCurrentValue(ctx)
	if err != nil {
		return target, err
	}

	// Try direct assertion first
	if v, ok := val.(TV); ok {
		return v, nil
	}

	// Fallback to JSON roundtrip
	b, err := json.Marshal(val)
	if err != nil {
		return target, err
	}
	err = json.Unmarshal(b, &target)
	return target, err
}

// Helper functions
func structToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}

func mapToStruct(m any, target any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
