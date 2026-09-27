package model

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// btreeModelStore implements ModelStore using SOP B-Trees.
// It relies on an externally provided transaction.
type btreeModelStore struct {
	name        string
	trans       sop.Transaction
	openedStore btree.BtreeInterface[ModelKey, string]
}

// New creates a new B-Tree based model store bound to a transaction.
func New(name string, trans sop.Transaction) ai.ModelStore {
	return &btreeModelStore{
		name:  name,
		trans: trans,
	}
}

// WithTransaction returns a new instance of the store bound to the provided transaction.
func (s *btreeModelStore) WithTransaction(trans sop.Transaction) ai.ModelStore {
	return &btreeModelStore{
		name:  s.name,
		trans: trans,
	}
}

// ModelKey is the composite key for the Model Store.
type ModelKey struct {
	Category string
	Name     string
}

func (s *btreeModelStore) openStore(ctx context.Context) (btree.BtreeInterface[ModelKey, string], error) {
	if s.openedStore != nil {
		return s.openedStore, nil
	}
	if s.trans == nil {
		return nil, fmt.Errorf("transaction is required")
	}

	// Prefix the store name with the domain name to allow multiple stores in the same folder.
	storeName := s.name
	slotLength := btree.DefaultSlotLength
	so := sop.ConfigureStore(storeName, true, slotLength, "AI Models Registry", sop.MediumData, "")

	// Use jsondb.IndexSpecification to generating the MapKeyIndexSpecification JSON string.
	// This ensures consistency with other components that check or use this specification.
	idxSpec := jsondb.IndexSpecification{
		IndexFields: []jsondb.IndexFieldSpecification{
			{FieldName: "Category", AscendingSortOrder: true},
			{FieldName: "Name", AscendingSortOrder: true},
		},
	}
	if b, err := json.Marshal(idxSpec); err == nil {
		so.MapKeyIndexSpecification = string(b)
	}

	comparer := func(a, b ModelKey) int {
		if a.Category < b.Category {
			return -1
		}
		if a.Category > b.Category {
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	}

	store, err := infs.NewBtree[ModelKey, string](ctx, so, s.trans, comparer)
	if err != nil {
		if err.Error() == "failed in NewBtree as transaction has replication enabled, use NewBtreeWithReplication instead" {
			store, err = infs.NewBtreeWithReplication[ModelKey, string](ctx, so, s.trans, comparer)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	s.openedStore = store
	return store, nil
}

// Save persists a model with the given name and category.
func (s *btreeModelStore) Save(ctx context.Context, category string, name string, model any) error {
	store, err := s.openStore(ctx)
	if err != nil {
		return err
	}

	data, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("failed to marshal model: %w", err)
	}

	key := ModelKey{Category: category, Name: name}
	_, err = store.Upsert(ctx, key, string(data))
	return err
}

// Load retrieves a model by name and category.
func (s *btreeModelStore) Load(ctx context.Context, category string, name string, target any) error {
	store, err := s.openStore(ctx)
	if err != nil {
		return err
	}

	key := ModelKey{Category: category, Name: name}
	found, err := store.Find(ctx, key, false)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("model not found: %s/%s", category, name)
	}

	data, err := store.GetCurrentValue(ctx)
	if err != nil {
		return err
	}

	return json.Unmarshal([]byte(data), target)
}

// List returns the names of all stored models in a given category.
func (s *btreeModelStore) List(ctx context.Context, category string) ([]string, error) {
	store, err := s.openStore(ctx)
	if err != nil {
		return nil, err
	}

	var names []string
	// Start search at the beginning of the category
	startKey := ModelKey{Category: category, Name: ""}
	found, err := store.Find(ctx, startKey, true)
	if err != nil {
		return nil, err
	}

	if !found {
		// If not found, check if we are at a valid position
		if store.GetCurrentKey().Key.Category != category {
			return nil, nil
		}
	}

	// Iterate while category matches
	for {
		key := store.GetCurrentKey().Key
		if key.Category != category {
			break
		}
		names = append(names, key.Name)

		if ok, err := store.Next(ctx); err != nil {
			return nil, err
		} else if !ok {
			break
		}
	}

	return names, nil
}

// Delete removes a model from the store.
func (s *btreeModelStore) Delete(ctx context.Context, category string, name string) error {
	store, err := s.openStore(ctx)
	if err != nil {
		return err
	}

	key := ModelKey{Category: category, Name: name}
	found, err := store.Find(ctx, key, false)
	if err != nil {
		return err
	}
	if found {
		_, err := store.RemoveCurrentItem(ctx)
		return err
	}
	return nil
}
