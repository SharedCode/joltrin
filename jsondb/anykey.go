// Package jsondb provides thin wrappers for storing JSON-serializable values in a SOP B-Tree
// with helpers for paging and convenience CRUD operations.
//
// Note: This package offers similar performance to the native Go implementation because SOP
// efficiently serializes entire B-Tree nodes and value segments, minimizing JSON overhead.
package jsondb

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// Item contains a Key and Value pair, with a generated UUID for reference.
type Item[TK btree.Ordered, TV any] struct {
	Key   TK        `json:"key"`
	Value *TV       `json:"value"`
	ID    uuid.UUID `json:"id"`
}

func (itm *Item[TK, TV]) extract(source *btree.Item[TK, TV]) {
	itm.Key = source.Key
	itm.Value = source.Value
	itm.ID = uuid.UUID(source.ID)
}

// JsonDBAnyKey wraps a B-Tree for storing arbitrary JSON-serializable values under an ordered key.
type JsonDBAnyKey[TK btree.Ordered, TV any] struct {
	btree.BtreeInterface[TK, TV]
}

type PagingDirection int

const (
	// Forward iterates from low to high.
	Forward = iota
	// Backward iterates from high to low.
	Backward
)

// PagingInfo specifies pagination when reading keys or items.
type PagingInfo struct {
	// PageOffset is the page index to move to before fetching (0 to start from current).
	PageOffset int `json:"page_offset"`
	// PageSize is the number of elements per page.
	PageSize int `json:"page_size"`
	// FetchCount overrides PageSize when > 0 to fetch an exact number of elements.
	FetchCount int `json:"fetch_count"`
	// Direction selects forward or backward iteration.
	Direction PagingDirection `json:"direction"`
}

// NewJsonBtree creates a new JSON-capable B-Tree. Values are marshaled by the caller as needed.
func NewJsonBtree[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, so sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (*JsonDBAnyKey[TK, TV], error) {
	b3, err := database.NewBtree[TK, TV](ctx, config, so.Name, t, comparer, so)
	if err != nil {
		return nil, err
	}
	return &JsonDBAnyKey[TK, TV]{
		BtreeInterface: b3,
	}, nil
}

// OpenJsonBtree opens an existing JSON-capable B-Tree.
func OpenJsonBtree[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (*JsonDBAnyKey[TK, TV], error) {
	b3, err := database.OpenBtree[TK, TV](ctx, config, name, t, comparer)
	if err != nil {
		return nil, err
	}
	return &JsonDBAnyKey[TK, TV]{
		BtreeInterface: b3,
	}, nil
}

// OpenJsonBtreeCursor opens a cursor wrapper for a given Btree. It opens it if it is not yet.
func OpenJsonBtreeCursor[TK btree.Ordered, TV any](ctx context.Context, config sop.DatabaseOptions, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (*JsonDBAnyKey[TK, TV], error) {
	b3, err := database.OpenBtreeCursor[TK, TV](ctx, config, name, t, comparer)
	if err != nil {
		return nil, err
	}
	return &JsonDBAnyKey[TK, TV]{
		BtreeInterface: b3,
	}, nil
}

// Add inserts items without duplicate checks; returns true only if all inserts succeed.
func (j *JsonDBAnyKey[TK, TV]) Add(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	allSucceeded := true
	for i := range items {
		if ok, err := j.BtreeInterface.Add(ctx, items[i].Key, *items[i].Value); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// AddIfNotExist inserts items only when the key does not already exist.
func (j *JsonDBAnyKey[TK, TV]) AddIfNotExist(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	allSucceeded := true
	for i := range items {
		if ok, err := j.BtreeInterface.AddIfNotExist(ctx, items[i].Key, *items[i].Value); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// Update replaces existing values for the provided keys.
func (j *JsonDBAnyKey[TK, TV]) Update(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	allSucceeded := true
	for i := range items {
		if ok, err := j.BtreeInterface.Update(ctx, items[i].Key, *items[i].Value); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// UpdateKey updates the key of the item found by the key in the provided item.
func (j *JsonDBAnyKey[TK, TV]) UpdateKey(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	allSucceeded := true
	for i := range items {
		if ok, err := j.BtreeInterface.UpdateKey(ctx, items[i].Key); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// UpdateCurrentKey updates the key of the currently positioned item.
func (j *JsonDBAnyKey[TK, TV]) UpdateCurrentKey(ctx context.Context, item Item[TK, TV]) (bool, error) {
	return j.BtreeInterface.UpdateCurrentKey(ctx, item.Key)
}

// Upsert adds new items or updates existing ones based on key existence.
func (j *JsonDBAnyKey[TK, TV]) Upsert(ctx context.Context, items []Item[TK, TV]) (bool, error) {
	allSucceeded := true
	for i := range items {
		if ok, err := j.BtreeInterface.Upsert(ctx, items[i].Key, *items[i].Value); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// Remove deletes items by key; returns true only if all deletions succeed.
func (j *JsonDBAnyKey[TK, TV]) Remove(ctx context.Context, keys []TK) (bool, error) {
	allSucceeded := true
	for i := range keys {
		if ok, err := j.BtreeInterface.Remove(ctx, keys[i]); err != nil {
			return false, err
		} else if !ok {
			allSucceeded = false
		}
	}
	return allSucceeded, nil
}

// GetKeys returns a JSON string of keys starting from the current cursor and paginated per PagingInfo.
func (j *JsonDBAnyKey[TK, TV]) GetKeys(ctx context.Context, pagingInfo PagingInfo) (string, error) {
	if j.BtreeInterface.GetCurrentKey().ID == sop.NilUUID {
		if pagingInfo.PageOffset != 0 {
			return "", fmt.Errorf("can't fetch keys, try calling First, Last or Find/FindWithID prior to GetItems")
		}
		// Auto navigate to first item of the B-tree if page offset == 0.
		if ok, err := j.BtreeInterface.First(ctx); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("can't fetch from an empty btree")
		}
	}

	if pagingInfo.PageOffset > 0 {
		for range pagingInfo.PageOffset {
			for range pagingInfo.PageSize {
				if pagingInfo.Direction == Forward {
					if ok, err := j.BtreeInterface.Next(ctx); err != nil {
						return "", err
					} else if !ok {
						return "", fmt.Errorf("reached the end of B-tree, no items fetched")
					}
					continue
				}
				// Walk in backwards direction.
				if ok, err := j.BtreeInterface.Previous(ctx); err != nil {
					return "", err
				} else if !ok {
					return "", fmt.Errorf("reached the top of B-tree, no items fetched")
				}
			}
		}
	}

	// Fetch the data elements starting with current cursor location.
	fetchCount := pagingInfo.FetchCount
	if fetchCount <= 0 {
		fetchCount = pagingInfo.PageSize
	}
	keys := make([]Item[TK, TV], 0, fetchCount)
	for range fetchCount {

		key := j.BtreeInterface.GetCurrentKey()
		itm := Item[TK, TV]{
			Key: key.Key,
			ID:  uuid.UUID(key.ID),
		}
		keys = append(keys, itm)
		if pagingInfo.Direction == Forward {
			if ok, err := j.BtreeInterface.Next(ctx); err != nil {
				p, _ := toJsonString(keys)
				return p, err
			} else if !ok {
				return toJsonString(keys)
			}
			continue
		}
		if ok, err := j.BtreeInterface.Previous(ctx); err != nil {
			p, _ := toJsonString(keys)
			return p, err
		} else if !ok {
			return toJsonString(keys)
		}
	}

	// Package as JSON string the result.
	return toJsonString(keys)
}

// GetItems returns a JSON string of items starting from the current cursor and paginated per PagingInfo.
func (j *JsonDBAnyKey[TK, TV]) GetItems(ctx context.Context, pagingInfo PagingInfo) (string, error) {
	if j.BtreeInterface.GetCurrentKey().ID == sop.NilUUID {
		if pagingInfo.PageOffset != 0 {
			return "", fmt.Errorf("can't fetch items, try calling First, Last or Find/FindWithID prior to GetItems")
		}
		// Auto navigate to first item of the B-tree if page offset == 0.
		if ok, err := j.BtreeInterface.First(ctx); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("can't fetch from an empty btree")
		}
	}

	if pagingInfo.PageOffset > 0 {
		for range pagingInfo.PageOffset {
			for range pagingInfo.PageSize {
				if pagingInfo.Direction == Forward {
					if ok, err := j.BtreeInterface.Next(ctx); err != nil {
						return "", err
					} else if !ok {
						return "", fmt.Errorf("reached the end of B-tree, no items fetched")
					}
					continue
				}
				// Walk in backwards direction.
				if ok, err := j.BtreeInterface.Previous(ctx); err != nil {
					return "", err
				} else if !ok {
					return "", fmt.Errorf("reached the top of B-tree, no items fetched")
				}
			}
		}
	}

	// Fetch the data elements starting with current cursor location.
	fetchCount := pagingInfo.FetchCount
	if fetchCount <= 0 {
		fetchCount = pagingInfo.PageSize
	}
	items := make([]Item[TK, TV], 0, fetchCount)
	for range fetchCount {
		item, err := j.BtreeInterface.GetCurrentItem(ctx)
		if err != nil {
			p, _ := toJsonString(items)
			return p, err
		}
		var itm Item[TK, TV]
		itm.extract(&item)
		items = append(items, itm)
		if pagingInfo.Direction == Forward {
			if ok, err := j.BtreeInterface.Next(ctx); err != nil {
				p, _ := toJsonString(items)
				return p, err
			} else if !ok {
				return toJsonString(items)
			}
			continue
		}
		if ok, err := j.BtreeInterface.Previous(ctx); err != nil {
			p, _ := toJsonString(items)
			return p, err
		} else if !ok {
			return toJsonString(items)
		}
	}

	// Package as JSON string the result.
	return toJsonString(items)
}

// GetValues finds items by key (or id) and returns their values as a JSON string.
func (j *JsonDBAnyKey[TK, TV]) GetValues(ctx context.Context, keys []Item[TK, TV]) (string, error) {
	values := make([]Item[TK, TV], len(keys))
	for i := range keys {
		var err error
		var ok bool
		id := sop.UUID(keys[i].ID)
		if id.IsNil() {
			ok, err = j.BtreeInterface.Find(ctx, keys[i].Key, true)
		} else {
			ok, err = j.BtreeInterface.FindWithID(ctx, keys[i].Key, sop.UUID(keys[i].ID))
		}
		if err != nil {
			p, _ := toJsonString(values)
			return p, err
		} else if !ok {
			// Assign the source key to allow caller to deduce that item was not found, Value field is empty.
			values[i] = keys[i]
			continue
		}
		item, err := j.BtreeInterface.GetCurrentItem(ctx)
		if err != nil {
			p, _ := toJsonString(values)
			return p, err
		}
		values[i] = Item[TK, TV]{}
		values[i].extract(&item)
	}
	return toJsonString(values)
}

// GetCurrentKey returns the current item's key and ID as a JSON string.
func (j *JsonDBAnyKey[TK, TV]) GetCurrentKey() (string, error) {
	item := j.BtreeInterface.GetCurrentKey()
	var itm Item[TK, TV]
	itm.extract(&item)
	return toJsonString([]Item[TK, TV]{itm})
}

// GetCurrentValue returns the current item's value as a JSON string.
func (j *JsonDBAnyKey[TK, TV]) GetCurrentValue(ctx context.Context) (string, error) {
	item, err := j.BtreeInterface.GetCurrentItem(ctx)
	if err != nil {
		return "", err
	}
	var itm Item[TK, TV]
	itm.extract(&item)
	return toJsonString([]Item[TK, TV]{itm})
}

// toJsonString encodes objects to a JSON string or returns an empty string for empty input.
func toJsonString[T any](objects []T) (string, error) {
	if len(objects) == 0 {
		return "", nil
	}
	// Package as JSON string the result.
	ba, err := encoding.DefaultMarshaler.Marshal(objects)
	if err != nil {
		return "", err
	}
	return string(ba), nil
}
