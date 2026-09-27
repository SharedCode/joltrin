package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common"
	sopdb "github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func (a *CopilotAgent) resolveTransaction(ctx context.Context, db *database.Database, dbName string, mode sop.TransactionMode) (sop.Transaction, bool, error) {
	p := ai.GetSessionPayload(ctx)
	var tx sop.Transaction

	if p != nil {
		// 1. Check Transactions map (Multi-DB support)
		if p.Transactions != nil {
			if tAny, ok := p.Transactions[dbName]; ok {
				if t, ok := tAny.(sop.Transaction); ok {
					tx = t
				}
			}
		}

		// 2. Fallback to legacy Transaction field if not found in map
		// Only use if it matches the target database (or if dbName is empty/default)
		if tx == nil && p.Transaction != nil {
			if dbName == "" || dbName == p.CurrentDB {
				if t, ok := p.Transaction.(sop.Transaction); ok {
					tx = t
				}
			}
		}
	}

	if tx == nil {
		if p == nil {
			if dbName != "" {
				return nil, false, fmt.Errorf("no session payload available to create transaction for database '%s'", dbName)
			}
			return nil, false, fmt.Errorf("no session payload available to create transaction")
		}
		if db == nil {
			if dbName != "" {
				return nil, false, fmt.Errorf("no database available to create transaction for database '%s'", dbName)
			}
			return nil, false, fmt.Errorf("no database available to create transaction")
		}

		var err error
		tx, err = db.BeginTransaction(ctx, mode)
		if err != nil {
			return nil, false, fmt.Errorf("failed to begin transaction: %w", err)
		}

		if p.Transactions == nil {
			p.Transactions = make(map[string]any)
		}
		p.Transactions[dbName] = tx
		if dbName == "" || dbName == p.CurrentDB {
			p.Transaction = tx
		}
	}
	return tx, false, nil
}

func (a *CopilotAgent) openGenericStore(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (btree.BtreeInterface[any, any], btree.ComparerFunc[any], *jsondb.IndexSpecification, error) {
	// Variables to hold state for the closure
	var indexSpec *jsondb.IndexSpecification
	var isPrimitiveKey bool

	// Check if primitive key before opening
	if t2, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := t2.StoreRepository.Get(ctx, storeName)
		if err == nil && len(stores) > 0 {
			// log.Info(fmt.Sprintf("OpenGenericStore: Store %s found. Primitive=%v, SpecLen=%d", storeName, stores[0].IsPrimitiveKey, len(stores[0].MapKeyIndexSpecification)))
			isPrimitiveKey = stores[0].IsPrimitiveKey
			if stores[0].MapKeyIndexSpecification != "" {
				var is jsondb.IndexSpecification
				if err := encoding.DefaultMarshaler.Unmarshal([]byte(stores[0].MapKeyIndexSpecification), &is); err == nil {
					indexSpec = &is
				}
			}
		}
	}

	var comparer btree.ComparerFunc[any]

	if !isPrimitiveKey {
		// Proxy comparer
		comparer = func(a, b any) int {
			mapA, okA := a.(map[string]any)
			mapB, okB := b.(map[string]any)
			if !okA || !okB {
				return btree.Compare(a, b)
			}

			if indexSpec != nil {
				return indexSpec.Comparer(mapA, mapB)
			}

			// Default Map Comparer (Dynamic)
			// Collect all keys, sort them, compare values.
			keys := make([]string, 0, len(mapA)+len(mapB))
			seen := make(map[string]struct{})
			for k := range mapA {
				if _, exists := seen[k]; !exists {
					keys = append(keys, k)
					seen[k] = struct{}{}
				}
			}
			for k := range mapB {
				if _, exists := seen[k]; !exists {
					keys = append(keys, k)
					seen[k] = struct{}{}
				}
			}
			// We need to sort keys to be deterministic
			sort.Strings(keys)

			for _, k := range keys {
				valA, existsA := mapA[k]
				valB, existsB := mapB[k]

				if !existsA && !existsB {
					continue
				}
				if !existsA {
					return -1 // A is missing key, so A < B
				}
				if !existsB {
					return 1 // B is missing key, so A > B
				}

				res := btree.Compare(valA, valB)
				if res != 0 {
					return res
				}
			}
			return 0
		}
	} else {
		// Primitive Key Comparer
		comparer = btree.Compare
	}

	// Open the B-Tree using 'any' for Key and Value to support generic browsing.
	store, err := sopdb.OpenBtreeCursor[any, any](ctx, dbOpts, storeName, tx, comparer)
	return store, comparer, indexSpec, err
}

func (a *CopilotAgent) runNavigation(ctx context.Context, args map[string]any, op func(context.Context, jsondb.StoreAccessor) (bool, error), showNearest ...bool) (string, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	// Resolve Database
	var db *database.Database
	dbName, _ := args["database"].(string)
	if dbName == "" {
		dbName = p.CurrentDB
	}

	if dbName != "" {
		if dbName == SystemDBName && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}

	storeName, _ := args["store"].(string)
	if storeName == "" {
		return "", fmt.Errorf("store name is required")
	}

	var fields []string
	if f, ok := args["fields"]; ok {
		if fSlice, ok := f.([]any); ok {
			for _, v := range fSlice {
				if s, ok := v.(string); ok {
					fields = append(fields, s)
				}
			}
		} else if fSlice, ok := f.([]string); ok {
			fields = fSlice
		}
	}

	var tx sop.Transaction
	var localTx bool

	tx, localTx, err := a.resolveTransaction(ctx, db, dbName, sop.ForReading)
	if err != nil {
		return "", err
	}

	// Navigation requires a persistent transaction for stateful cursors.
	// If we started a new transaction, persist it in the session.
	if localTx {
		p.Transaction = tx
		// Update CurrentDB to match the new transaction
		if dbName != "" {
			p.CurrentDB = dbName
		}
	}

	// Check cache for store
	var store jsondb.StoreAccessor
	cacheKey := fmt.Sprintf("store_%s", storeName)
	if p.Variables != nil {
		if s, ok := p.Variables[cacheKey].(jsondb.StoreAccessor); ok {
			store = s
		}
	}

	if store == nil {
		store, err = jsondb.OpenStore(ctx, db.Config(), storeName, tx)
		if err != nil {
			return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
		}
		if p.Variables == nil {
			p.Variables = make(map[string]any)
		}
		p.Variables[cacheKey] = store
	}

	// Check for ResultStreamer
	var streamer ai.ResultStreamer
	if s, ok := ctx.Value(ai.CtxKeyResultStreamer).(ai.ResultStreamer); ok {
		streamer = s
	}

	// Determine if store uses complex keys
	var indexSpec *jsondb.IndexSpecification
	si := store.GetStoreInfo()
	if si.MapKeyIndexSpecification != "" {
		var is jsondb.IndexSpecification
		if err := json.Unmarshal([]byte(si.MapKeyIndexSpecification), &is); err == nil {
			indexSpec = &is
		}
	}

	found, err := op(ctx, store)
	if err != nil {
		return "", fmt.Errorf("navigation failed: %w", err)
	}

	if !found {
		if len(showNearest) > 0 && showNearest[0] {
			var neighbors []map[string]any

			// 1. Check if we are at a valid item (this is the "Current" neighbor, usually >= key)
			k := store.GetCurrentKey()
			if k != nil {
				v, _ := store.GetCurrentValue(ctx)

				// Format key if it's a map and we have an index spec
				var keyFormatted any = k
				if indexSpec != nil {
					if m, ok := k.(map[string]any); ok {
						keyFormatted = OrderedKey{m: m, spec: indexSpec}
					}
				}

				neighbors = append(neighbors, map[string]any{"key": keyFormatted, "value": v, "relation": "next_or_equal"})
			}

			// 2. Check previous item
			if k != nil {
				// We are at some item. Try to peek previous.
				if ok, _ := store.Previous(ctx); ok {
					k2 := store.GetCurrentKey()
					v2, _ := store.GetCurrentValue(ctx)

					// Format key if it's a map and we have an index spec
					var keyFormatted2 any = k2
					if indexSpec != nil {
						if m, ok := k2.(map[string]any); ok {
							keyFormatted2 = OrderedKey{m: m, spec: indexSpec}
						}
					}

					neighbors = append(neighbors, map[string]any{"key": keyFormatted2, "value": v2, "relation": "previous"})

					// Restore
					store.Next(ctx)
				}
			} else {
				// We are at End.
				if ok, _ := store.Previous(ctx); ok {
					// This is the Last item (Previous neighbor)
					k2 := store.GetCurrentKey()
					v2, _ := store.GetCurrentValue(ctx)

					// Format key if it's a map and we have an index spec
					var keyFormatted2 any = k2
					if indexSpec != nil {
						if m, ok := k2.(map[string]any); ok {
							keyFormatted2 = OrderedKey{m: m, spec: indexSpec}
						}
					}

					neighbors = append(neighbors, map[string]any{"key": keyFormatted2, "value": v2, "relation": "previous"})

					// We are now at Last. The original state was "End".
					// Restore to "End"
					store.Next(ctx)
				}
			}

			if len(neighbors) > 0 {
				if streamer != nil {
					streamer.BeginArray()
					for _, n := range neighbors {
						streamer.WriteItem(reorderItem(n, fields, indexSpec))
					}
					streamer.EndArray()
					return "", nil
				}

				// Return JSON array string
				var filteredNeighbors []any
				for _, n := range neighbors {
					filteredNeighbors = append(filteredNeighbors, reorderItem(n, fields, indexSpec))
				}
				b, _ := json.Marshal(filteredNeighbors)
				return string(b), nil
			}
		}

		if streamer != nil {
			streamer.BeginArray()
			streamer.EndArray()
			return "", nil
		}
		return "[]", nil
	}

	k := store.GetCurrentKey()
	v, _ := store.GetCurrentValue(ctx)

	// Format Output using common helper
	item := map[string]any{"key": k, "value": v}
	finalItem := reorderItem(item, fields, indexSpec)

	if streamer != nil {
		streamer.BeginArray()
		streamer.WriteItem(finalItem)
		streamer.EndArray()
		return "", nil
	}

	// Return JSON representation
	b, _ := json.Marshal([]any{finalItem})
	return string(b), nil
}
