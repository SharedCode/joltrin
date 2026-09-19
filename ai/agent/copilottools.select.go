package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func (a *CopilotAgent) toolSelect(ctx context.Context, args map[string]any) (string, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		bytes, _ := json.MarshalIndent(args, "", "  ")
		fmt.Printf("DEBUG: toolSelect called in STUB MODE with:\n%s\n", string(bytes))
		return "Select executed successfully (STUBBED).", nil
	}

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
		if dbName == "system" && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}

	storeName, _ := args["store"].(string)
	if storeName == "" {
		return "", fmt.Errorf("store name is required")
	}

	// Check if storeName is a Script (View)
	if a.systemDB != nil {
		// Try to find script in ai.DefaultScriptCategory category (default)
		// We need a transaction for systemDB
		sysTx, err := a.systemDB.BeginTransaction(ctx, sop.ForReading)
		if err == nil {
			scriptStore, err := a.systemDB.OpenModelStore(ctx, "scripts", sysTx)
			if err == nil {
				var script ai.Script
				if err := scriptStore.Load(ctx, ai.DefaultScriptCategory, storeName, &script); err == nil {
					sysTx.Commit(ctx)
					// Found Script! Execute it.
					return a.executeScriptView(ctx, storeName, script, args)
				}
			}
			sysTx.Rollback(ctx)
		}
	}

	// Parse Fields
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
		} else if fStr, ok := f.(string); ok {
			// Allow comma-separated fields list (e.g. "a.region, b.name as employee")
			parts := strings.Split(fStr, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					fields = append(fields, p)
				}
			}
		}
	}

	// Parse Limit
	limit := 100.0
	if l, ok := args["limit"]; ok {
		limit = coerceToFloat(l)
	}
	if limit <= 0 {
		limit = 100
	}

	// Parse Order By / Direction
	orderBy, _ := args["order_by"].(string)
	if orderBy == "" {
		orderBy, _ = args["direction"].(string)
	}
	isDesc := false
	if orderBy != "" {
		lowerOrder := strings.ToLower(orderBy)
		// Handle commas in "field desc, field2 asc"
		lowerOrder = strings.ReplaceAll(lowerOrder, ",", " ")
		if lowerOrder == "desc" || lowerOrder == "descending" {
			isDesc = true
		} else {
			parts := strings.Fields(lowerOrder)
			if len(parts) >= 2 && strings.HasPrefix(parts[1], "desc") {
				isDesc = true
			}
		}
	}

	// Parse Key Match
	var keyMatch any
	if k, ok := args["key"]; ok {
		keyMatch = k
	} else if k, ok := args["key_match"]; ok {
		keyMatch = k
	}

	// Parse Value Match
	var valueMatch any
	if v, ok := args["value"]; ok {
		valueMatch = v
	} else if v, ok := args["value_match"]; ok {
		valueMatch = v
	} else if v, ok := args["filter"]; ok {
		valueMatch = v
	} else {
		// If "value" is not explicitly provided, check if there are other args that are not reserved
		// This allows "select(store='users', age=30)" style
		valMap := CleanArgs(args, "store", "key", "key_match", "database", "fields", "limit", "action", "update_values", "value_match", "order_by", "direction", "filter")

		if len(valMap) > 0 {
			valueMatch = valMap
		}
	}

	// Parse Action
	action, _ := args["action"].(string)
	isDelete := action == "delete"
	isUpdate := action == "update"
	updateValues, _ := args["update_values"].(map[string]any)

	if isUpdate && len(updateValues) == 0 {
		return "", fmt.Errorf("update_values required for update action")
	}

	mode := sop.ForReading
	if isDelete || isUpdate {
		mode = sop.ForWriting
	}

	tx, autoCommit, err := a.resolveTransaction(ctx, db, dbName, mode)
	if err != nil {
		return "", err
	}
	if autoCommit {
		defer tx.Rollback(ctx)
	}

	store, err := jsondb.OpenStore(ctx, db.Config(), storeName, tx)
	if err != nil {
		return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
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

	emitter := NewResultEmitter(ctx)
	if len(fields) > 0 {
		emitter.SetColumns(fields)
	}
	emitter.Start()

	count := 0

	var startKey any
	// fmt.Printf("keyMatch: %v (type: %T)\n", keyMatch, keyMatch)
	var ok bool
	if keyMatch != nil {
		// Try to extract a start key for optimization
		if indexSpec != nil {
			startKey = extractStartKey(keyMatch, indexSpec)
		} else {
			// Primitive Key
			startKey = extractPrimitiveStartKey(keyMatch)
		}

		// If we found a start key, use FindOne to position cursor
		if startKey != nil {
			// For Ascending: FindOne positions at the first matching item (or start of range).
			// For Descending: FindOne positions at the first matching item.
			// NOTE: For strict prefix match Descending (e.g. Key="A"), FindOne("A") lands on first "A".
			// Previous() would go before "A". This is technically incorrect for "Key=A" Descending.
			// However, for Range queries (e.g. Key < "B"), FindOne("B") lands on "B", and Previous() goes to "A". Correct.
			// We implement as requested: Find/Previous for desc.

			if isDesc {
				// Use the new optimized FindInDescendingOrder
				_, err = store.FindInDescendingOrder(ctx, startKey)
				k := store.GetCurrentKey()
				if k != nil {
					ok = true
				}
			} else {
				_, err = store.FindOne(ctx, startKey, true)
				k := store.GetCurrentKey()
				if k != nil {
					ok = true
				}
			}
		} else {
			if isDesc {
				ok, err = store.Last(ctx)
			} else {
				ok, err = store.First(ctx)
			}
		}
	} else {
		if isDesc {
			ok, _ = store.Last(ctx)
		} else {
			ok, _ = store.First(ctx)
		}
	}

	// Iterate
	for ok {
		k := store.GetCurrentKey()
		if k == nil {
			break
		}

		// Check Key Match
		matched, _ := matchesKey(k, keyMatch)
		if !matched && startKey != nil && !isDesc {
			// Optimization: If we used a startKey (prefix/range start) and we are iterating forward,
			// a mismatch likely means we have moved past the range.
			// We should verify if the mismatch is indeed because we are "after" the range.
			// For simple equality prefix (e.g. Key="A"), if we are at "B", matchesKey is false.
			// Since B-Tree is sorted, we can safely break.
			// This assumes matchesKey returns true for ALL items in the range.
			// If keyMatch has other filters (e.g. Key="A" AND Age>20), matchesKey might be false
			// even if we are still in "A" prefix (e.g. Key="A", Age=10).
			// So we must only break if the PREFIX part mismatches.

			// Check if the current key 'k' still matches the 'startKey' prefix constraints.
			// startKey contains the equality constraints of the prefix.
			prefixMatched, _ := matchesKey(k, startKey)
			if !prefixMatched {
				break
			}
		}

		if matched {
			// Check Value Match
			var v any
			var vLoaded bool
			hasValueMatch := valueMatch != nil

			if hasValueMatch {
				v, err = store.GetCurrentValue(ctx)
				if err != nil {
					break
				}
				vLoaded = true
				matched, _ = matchesKey(v, valueMatch)
			}

			if matched {
				if !vLoaded {
					v, err = store.GetCurrentValue(ctx)
					if err != nil {
						break
					}
				}

				if isUpdate {
					var newVal any
					if vMap, ok := v.(map[string]any); ok {
						newVal = mergeMap(vMap, updateValues)
					} else {
						newVal = updateValues
					}
					if ok, err := store.Update(ctx, k, newVal); err != nil || !ok {
						return "", fmt.Errorf("failed to update item: %v", err)
					}
					count++
				} else if isDelete {
					kToDelete := k
					var hasNext bool
					if isDesc {
						hasNext, _ = store.Previous(ctx)
					} else {
						hasNext, _ = store.Next(ctx)
					}
					if _, err := store.Remove(ctx, kToDelete); err != nil {
						return "", fmt.Errorf("failed to delete item: %w", err)
					}
					count++

					if count >= int(limit) {
						break
					}
					if !hasNext {
						break
					}
					continue
				} else {
					// Select
					item := renderItem(k, v, fields)

					log.Debug(fmt.Sprintf("item: %v", item))

					emitter.Emit(item)

					count++
				}

				if count >= int(limit) {
					break
				}
			}
		}

		if isDesc {
			ok, _ = store.Previous(ctx)
		} else {
			ok, _ = store.Next(ctx)
		}
	}

	if isDelete {
		emitter.Finalize()
		if autoCommit {
			if err := tx.Commit(ctx); err != nil {
				return "", fmt.Errorf("failed to commit delete transaction: %w", err)
			}
		}
		return fmt.Sprintf(`{"deleted_count": %d}`, count), nil
	}

	if isUpdate {
		emitter.Finalize()
		if autoCommit {
			if err := tx.Commit(ctx); err != nil {
				return "", fmt.Errorf("failed to commit update transaction: %w", err)
			}
		}
		return fmt.Sprintf(`{"updated_count": %d}`, count), nil
	}

	return emitter.Finalize(), nil
}

func (a *CopilotAgent) toolFind(ctx context.Context, args map[string]any) (string, error) {
	return a.runNavigation(ctx, args, func(ctx context.Context, store jsondb.StoreAccessor) (bool, error) {
		key, ok := args["key"]
		if !ok {
			return false, fmt.Errorf("key is required")
		}
		return store.FindOne(ctx, key, true)
	})
}

func (a *CopilotAgent) toolFindNearest(ctx context.Context, args map[string]any) (string, error) {
	return a.runNavigation(ctx, args, func(ctx context.Context, store jsondb.StoreAccessor) (bool, error) {
		key, ok := args["key"]
		if !ok {
			return false, fmt.Errorf("key is required")
		}
		return store.FindOne(ctx, key, true)
	}, true)
}

func extractStartKey(keyMatch any, indexSpec *jsondb.IndexSpecification) map[string]any {
	m, ok := keyMatch.(map[string]any)
	if !ok {
		return nil
	}

	startKey := make(map[string]any)
	for _, field := range indexSpec.IndexFields {
		val, exists := m[field.FieldName]
		if !exists {
			break
		}

		// Check if value is a map (operator)
		if valMap, ok := val.(map[string]any); ok {
			// Look for $eq, $gte, $gt
			if v, ok := valMap["$eq"]; ok {
				startKey[field.FieldName] = v
				continue
			}
			if v, ok := valMap["$gte"]; ok {
				startKey[field.FieldName] = v
				return startKey
			}
			if v, ok := valMap["$gt"]; ok {
				startKey[field.FieldName] = v
				return startKey
			}
			// $lt, $lte cannot be used for start key
			break
		} else {
			// Simple value (Equality)
			startKey[field.FieldName] = val
		}
	}

	if len(startKey) == 0 {
		return nil
	}
	return startKey
}

func extractPrimitiveStartKey(keyMatch any) any {
	if m, ok := keyMatch.(map[string]any); ok {
		if v, ok := m["$eq"]; ok {
			return v
		}
		if v, ok := m["$gte"]; ok {
			return v
		}
		if v, ok := m["$gt"]; ok {
			return v
		}
		// If it has other operators like $lt, we can't optimize start
		return nil
	}
	// Direct value
	return keyMatch
}
