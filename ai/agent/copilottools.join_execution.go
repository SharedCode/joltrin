package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

// ensurePlan selects the execution strategy using simple schema analysis.
// This is the "Planning Phase".
func (jc *JoinRightCursor) ensurePlan() error {
	if jc.planReady {
		return nil
	}
	jc.planReady = true

	// Extract Right Join Fields from the ON clause
	// The ON clause is { "LeftField": "RightField" }
	var rightFields []string
	for _, v := range jc.on {
		rightFields = append(rightFields, fmt.Sprintf("%v", v))
	}

	jc.plan, _ = AnalyzeJoinPlan(jc.right.GetStoreInfo(), rightFields)

	// Post-Analysis Optimization: Bloom Filter
	if jc.plan.Strategy == StrategyIndexSeek {
		count := jc.right.GetStoreInfo().Count
		if count > 100 {
			jc.buildBloomFilter(count)
		}
	}

	return nil
}

// AnalyzeJoinPlan analyzes the store schema and join fields to determine the best execution strategy.
func AnalyzeJoinPlan(info sop.StoreInfo, rightFields []string) (JoinPlan, string) {
	plan := JoinPlan{Strategy: StrategyInMemory}
	var sb strings.Builder

	// 1. Primitive vs Composite Check
	isPrimitive := true
	if info.MapKeyIndexSpecification != "" {
		isPrimitive = false
		var spec struct {
			IndexFields []struct {
				FieldName          string `json:"field_name"`
				AscendingSortOrder *bool  `json:"ascending_sort_order"`
			} `json:"index_fields"`
		}
		if err := json.Unmarshal([]byte(info.MapKeyIndexSpecification), &spec); err == nil {
			for i, f := range spec.IndexFields {
				plan.IndexFields = append(plan.IndexFields, f.FieldName)
				if i == 0 {
					if f.AscendingSortOrder != nil {
						plan.Ascending = *f.AscendingSortOrder
					} else {
						plan.Ascending = true
					}
				}
			}
			plan.IsComposite = true
		}
	}

	sb.WriteString("## Execution Plan Analysis\n")
	sb.WriteString(fmt.Sprintf("- **Store Type**: %s\n", func() string {
		if isPrimitive {
			return "Primitive (Key/Value)"
		}
		return "Composite (Complex Object with Index)"
	}()))

	if !isPrimitive {
		sb.WriteString(fmt.Sprintf("- **Defined Index Fields**: `%v`\n", plan.IndexFields))
	}
	sb.WriteString(fmt.Sprintf("- **Requested Join Fields**: `%v`\n", rightFields))

	// 2. Strategy Selection
	if isPrimitive {
		for _, f := range rightFields {
			if f == "key" {
				plan.Strategy = StrategyIndexSeek
				plan.IsComposite = false
				sb.WriteString("\n### Selected Strategy: **Index Seek (Fast)**\n")
				sb.WriteString("Reasoning: Joining on primary 'key' of a primitive store. This is the optimal path.")
				return plan, sb.String()
			}
		}
		sb.WriteString("\n### Selected Strategy: **Hash Join (High Cost)**\n")
		sb.WriteString("Reasoning: You are joining on a Value field in a primitive store. Only the 'key' is indexed.\n")
		sb.WriteString("Recommendation: If you need fast joins on this value, consider creating a Secondary Index store mapping `value -> key`.")
		return plan, sb.String()
	} else if len(plan.IndexFields) > 0 {
		// Find Longest Common matching Prefix
		for _, idxField := range plan.IndexFields {
			found := false
			for _, rf := range rightFields {
				// Exact match
				if rf == idxField {
					found = true
					break
				}
				// Alias match: "store.field" == "field"
				if strings.HasSuffix(rf, "."+idxField) {
					found = true
					break
				}
			}
			if found {
				plan.PrefixFields = append(plan.PrefixFields, idxField)
			} else {
				break
			}
		}

		if len(plan.PrefixFields) > 0 {
			plan.Strategy = StrategyIndexSeek
			sb.WriteString("\n### Selected Strategy: **Index Seek (Fast)**\n")
			sb.WriteString(fmt.Sprintf("Reasoning: Join fields match the Index Prefix `%v`.\n", plan.PrefixFields))
			sb.WriteString("Performance: $O(M \\log N)$. The system will perform efficient B-Tree lookups.")
			return plan, sb.String()
		} else {
			sb.WriteString("\n### Selected Strategy: **Hash Join (High Cost)**\n")
			sb.WriteString(fmt.Sprintf("Reasoning: The join fields `%v` do NOT match the start of the Index `%v`.\n", rightFields, plan.IndexFields))

			// Diagnosis
			missingPrefix := plan.IndexFields[0]
			sb.WriteString("\n### Diagnosis: Broken Prefix\n")
			sb.WriteString(fmt.Sprintf("The B-Tree index requires you to provide the first field **`%s`** to navigate the tree.\n", missingPrefix))
			sb.WriteString("Without this field, we must scan the entire store into memory ($O(N)$) to find matches.")

			// Recommendation
			sb.WriteString("\n\n### Recommendation\n")
			sb.WriteString(fmt.Sprintf("1. **Adjust Query**: precise your ON clause to include `%s`.\n", missingPrefix))
			sb.WriteString("2. **Change Schema**: If you frequently join by these fields only, create a new Store with a different Index ordering.\n")

			return plan, sb.String()
		}
	} else {
		sb.WriteString("\n### Selected Strategy: **Hash Join (High Cost)**\n")
		sb.WriteString("Reasoning: This Composite store has NO index specification defined in `CheckAttributes`. It behaves like a heap.\n")
		sb.WriteString("Recommendation: Define a `MapKeyIndexSpecification` in the store creation to enable indexing.")
		return plan, sb.String()
	}
}

func (jc *JoinRightCursor) buildBloomFilter(count int64) {
	// 1. Create Bloom Filter
	jc.bloomFilter = NewBloomFilter(uint(count), 0.01)

	// 2. Scan Right Store Keys
	// We use a new context or same? Iterate without "Value" payload if possible to be fast.
	// But StoreAccessor doesn't support "KeysOnly" iteration easily without GetCurrentKey.
	// We use First/Next.

	ok, err := jc.right.First(jc.ctx)
	if err != nil {
		jc.bloomFilter = nil // Failed
		return
	}

	for ok {
		k := jc.right.GetCurrentKey() // Only need Key

		// 3. Extract Join Key(s) from Right Record Key/Value
		// Challenge: The join condition might be on Value fields.
		// If jc.plan.Strategy == StrategyIndexSeek, it implies we are matching on Index Fields.
		// If MapKeyIndex (IsComposite), the Key IS the map.
		// If Primitive, Key is string.

		var joinKeyStr string

		if jc.plan.IsComposite {
			// Composite Key (Map)
			if kMap, ok := k.(map[string]any); ok {
				// We construct the "Seek Key" string representation.
				// We must match the construction in NextOptimized (Seek block).
				// In Seek block:
				/*
					compKey := make(map[string]any)
					for _, pField := range jc.plan.PrefixFields {
						... compKey[pField] = ...
					}
				*/
				// The seeking works by passing `compKey` (map) to `FindOne`.
				// FindOne uses B-Tree comparer.
				// Bloom Filter needs a string.
				// We must canonicalize the map subset that corresponds to the join condition?
				// Actually, B-Tree `FindOne` takes the whole key map? Or a subset?
				// It likely takes a subset (prefix) if specific support exists, OR exact match.
				// `FindOne` vs `Find`?
				// In `copilottools.join_execution.go`: `jc.right.FindOne(ctx, seekKey, true)`.

				// Simplified approach: If we are using IndexSeek, we are matching on *Indexed Prefix*.
				// We should add the Value of the *first* index field (or concatenation of prefix fields) to Bloom?
				// But we need exact match logic.

				// Let's assume we use the string representation of the JOIN values.
				// This mirrors `scan` loop where we check `if fmt.Sprintf("%v", lVal) != fmt.Sprintf("%v", rVal)`

				// We need to extract the Right side values for the ON fields.
				// Since we are Scanning Right, we can check Key first. If not in Key, check Value.
				// But wait, if we are doing IndexSeek, the ON fields MUST be in the Index (Key).
				// So we only need to look at 'k'.

				// Construct a composite string key? "val1|val2|..."
				// Or just add each column individually? No, that's wrong (Cross matching).
				// We need a composite hash.

				// For now, let's stick to Single Key optimization or handle the first field only?
				// Bloom Filter acts as a "Fast Reject".
				// If we have multi-column join A=1 AND B=2.
				// If we just check Bloom(1) for A... if A=1 exists but B=2 doesn't... we pass Bloom, then fail Seek.
				// That is still valid (False Postive).
				// So for Composite keys, we can just Bloom the PRIMARY (first) join field.

				primaryField := jc.plan.PrefixFields[0]
				// Find value in kMap (use getField for case-insensitivity)
				if val := getField(kMap, primaryField); val != nil {
					joinKeyStr = fmt.Sprintf("%v", val)
				}
			}
		} else {
			// Primitive Key
			joinKeyStr = fmt.Sprintf("%v", k)
		}

		if joinKeyStr != "" {
			jc.bloomFilter.Add(joinKeyStr)
		}

		ok, err = jc.right.Next(jc.ctx)
		if err != nil {
			break
		}
	}

	// Reset Position?
	// The cursor is now at the end.
	// The NextOptimized logic calls `FindOne` (Seek) anyway, so position doesn't matter.
}

// NextOptimized is the "Execution Phase".
func (jc *JoinRightCursor) NextOptimized(ctx context.Context) (any, bool, error) {
	if err := jc.ensurePlan(); err != nil {
		return nil, false, err
	}

	// Set default max consecutive rows without match if not configured
	if jc.maxRowsWithoutMatch == 0 {
		jc.maxRowsWithoutMatch = MaxConsecutiveRowsWithoutMatch
	}

	// STRATEGY: Fallback (Memory Hash Join)
	if jc.plan.Strategy == StrategyInMemory {
		if !jc.useFallback {
			jc.useFallback = true
			// Materialize
			// ERROR HANDLING: If First() or Next() fails, return error immediately.
			scanIter, err := jc.right.First(ctx)
			if err != nil {
				return nil, false, fmt.Errorf("join materialization error (First): %w", err)
			}

			// Hard Logic Limit to prevent OOM on massive unrelated joins
			// If we cross this, we spill to a temporary B-Tree
			limit := 10000
			count := 0

			for scanIter && count < limit {
				// Check context cancellation
				select {
				case <-ctx.Done():
					return nil, false, fmt.Errorf("context cancelled during join materialization: %w", ctx.Err())
				default:
				}

				k := jc.right.GetCurrentKey()
				v, err := jc.right.GetCurrentValue(ctx)
				if err != nil {
					return nil, false, fmt.Errorf("join materialization error (Value): %w", err)
				}

				item := renderItem(k, v, nil)
				jc.fallbackList = append(jc.fallbackList, item)

				scanIter, err = jc.right.Next(ctx)
				if err != nil {
					return nil, false, fmt.Errorf("join materialization error (Next): %w", err)
				}
				count++
			}

			if count >= limit && scanIter {
				// Spill to Temp Store
				// 1. Resolve Transaction (We need a transaction to create temp store)
				// We assume the current engine context has one, or we use the right store's transaction if exposed?
				// Limitiation: We can't easily valid transaction from StoreAccessor.
				// We try to grab "system" db transaction or "current" from context.
				// For now, we reuse the "first" available write transaction or create one on system DB.

				// Identify Database and Transaction
				// This is heuristic. If we can't find a transaction, we fail.
				var tx sop.Transaction
				// Try to find a transaction in script context?
				// jc.engine.Context.Transactions
				for _, t := range jc.engine.Context.Transactions {
					tx = t
					break
				}
				if tx == nil {
					// Fallback: Check if we have a system DB and can begin a tx?
					// Complexity: Adding a TX might break script logic.
					return nil, false, fmt.Errorf("join error: right store too large (>%d) and no active write transaction available for spill", limit)
				}

				// 2. Spill to Temp Store preparation
				tempName := fmt.Sprintf("temp_join_%s", sop.NewUUID().String())

				// Resolve Database Config from Transaction
				// We need the database config to create a new B-Tree.
				var dbOpts sop.DatabaseOptions
				if db, found := jc.engine.Context.TxToDB[tx]; found {
					dbOpts = db.Config()
				} else {
					// Fallback or Error?
					return nil, false, fmt.Errorf("failed to resolve database from transaction for spill")
				}

				// Create Temp Store (String Key for simplicity and speed)
				// We serialize the composite key into a string.
				storeOpts := sop.StoreOptions{
					Name:                     tempName,
					IsUnique:                 false, // Allow duplicates for join keys
					IsValueDataInNodeSegment: true,
				}

				// Use core_database.NewBtree with atomic string keys
				tempBtree, err := core_database.NewBtree[string, any](ctx, dbOpts, tempName, tx, nil, storeOpts)
				if err != nil {
					return nil, false, fmt.Errorf("failed to create temp spill store: %w", err)
				}

				// 3. Dump existing fallbackList
				for _, item := range jc.fallbackList {
					if m, ok := item.(map[string]any); ok {
						// Generate Composite Key using common logic
						kStr := jc.generateTempKey(m)
						tempBtree.Add(ctx, kStr, m)
					}
				}
				jc.fallbackList = nil // Clear memory

				// 4. Stream the rest
				for scanIter {
					// Check context cancellation
					select {
					case <-ctx.Done():
						return nil, false, fmt.Errorf("context cancelled during temp store spill: %w", ctx.Err())
					default:
					}

					// Get Current
					k := jc.right.GetCurrentKey()
					v, _ := jc.right.GetCurrentValue(ctx)
					item := renderItem(k, v, nil)

					if m, ok := item.(map[string]any); ok {
						kStr := jc.generateTempKey(m)
						tempBtree.Add(ctx, kStr, m)
					}

					scanIter, err = jc.right.Next(ctx)
					if err != nil {
						return nil, false, err
					}
				}

				// 5. Switch Strategy (Wrapper adapts BTree to StoreAccessor)
				// Note: tempStoreWrapper must embed btree.BtreeInterface
				jc.right = &tempStoreWrapper{BtreeInterface: tempBtree, jc: jc}
				jc.planReady = false         // Force replan to detect StrategyIndexSeek on new store
				jc.useFallback = false       // Disable fallback logic
				return jc.NextOptimized(ctx) // Recurse with new strategy
			}
		}

		for {
			// Check context cancellation
			select {
			case <-ctx.Done():
				return nil, false, fmt.Errorf("context cancelled during fallback join: %w", ctx.Err())
			default:
			}

			if jc.currentL == nil {
				var ok bool
				var err error
				jc.currentL, ok, err = jc.left.Next(ctx)
				if err != nil {
					return nil, false, err
				}
				if !ok {
					return nil, false, nil
				}
				jc.fallbackIdx = 0
				jc.matched = false
			}

			for jc.fallbackIdx < len(jc.fallbackList) {
				rItem := jc.fallbackList[jc.fallbackIdx]
				jc.fallbackIdx++

				match := true
				for lField, rFieldRaw := range jc.on {
					rField := fmt.Sprintf("%v", rFieldRaw)
					lVal := getField(jc.currentL, lField)
					rVal := getField(rItem, rField)

					// Robust Equality Check aligned with copilottools.utils.go matchesKey capabilities
					// But implemented inline for Join speed
					if fmt.Sprintf("%v", lVal) != fmt.Sprintf("%v", rVal) {
						// Double check numeric formats (e.g. 1.0 vs 1)
						fL, okL := coerceToFloatFull(lVal)
						fR, okR := coerceToFloatFull(rVal)
						if okL && okR && fL == fR {
							continue // Match!
						}

						match = false
						break
					}
				}

				if match {
					jc.matched = true
					merged := jc.mergeResult(jc.currentL, rItem, getField(rItem, "key"))
					return merged, true, nil
				}
			}

			if !jc.matched && jc.joinType == "left" {
				res := jc.currentL
				jc.currentL = nil
				return res, true, nil
			}
			jc.currentL = nil
		}
	}

	// STRATEGY: Index Seek
	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("context cancelled during index seek join: %w", ctx.Err())
		default:
		}

		// If we're not currently scanning, get the next left row
		if jc.currentL == nil {
			var ok bool
			var err error
			jc.currentL, ok, err = jc.left.Next(ctx)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil
			}
			jc.matched = false
			jc.scanning = false // Reset scanning flag for new left row

			// Seek
			var seekKey any
			var bloomCheckKey string // For Bloom Filter Check

			if jc.plan.IsComposite {
				compKey := make(map[string]any)
				for i, pField := range jc.plan.PrefixFields {
					for lField, rFieldRaw := range jc.on {
						if fmt.Sprintf("%v", rFieldRaw) == pField {
							val := getField(jc.currentL, lField)
							compKey[pField] = val

							// Prepare Key for Bloom Filter (match buildBloomFilter logic: First Field only)
							if i == 0 {
								bloomCheckKey = fmt.Sprintf("%v", val)
							}
							break
						}
					}
				}
				seekKey = compKey
			} else {
				for lField, rFieldRaw := range jc.on {
					if fmt.Sprintf("%v", rFieldRaw) == "key" {
						val := getField(jc.currentL, lField)
						seekKey = val
						bloomCheckKey = fmt.Sprintf("%v", val)
						break
					}
				}
			}

			// OPTIMIZATION: Check Bloom Filter
			if jc.bloomFilter != nil {
				if !jc.bloomFilter.Test(bloomCheckKey) {
					// Definitely not in Right Store. Skip Seek.
					if jc.joinType == "left" {
						res := jc.currentL
						jc.currentL = nil
						return res, true, nil
					}
					jc.currentL = nil
					continue
				}
			}

			var found bool
			found, err = jc.right.FindOne(ctx, seekKey, true)
			if err != nil {
				return nil, false, fmt.Errorf("join seek error: %w", err)
			}

			if !found && !jc.plan.IsComposite {
				// No match for this key (primitive key case)
				log.Debug("JoinRightCursor: IndexSeek found no match",
					"seek_key", seekKey,
					"join_type", jc.joinType,
					"strategy", "primitive")
				if jc.joinType == "left" {
					// Emit (LHS, nil)
					res := jc.currentL
					jc.currentL = nil // Consumed
					return res, true, nil
				}

				jc.currentL = nil // Consumed (Inner Join: discard LHS)
				continue
			}

			// For composite keys or when found=true, we fall through to scan
			// This handles cases where index seek positions us, then we scan for matches
			if !found && jc.plan.IsComposite {
				log.Debug("JoinRightCursor: IndexSeek returned false for composite key, will attempt scan",
					"seek_key", seekKey,
					"composite_fields", jc.plan.PrefixFields)
			}

			// Mark that we're now scanning the right store
			jc.scanning = true
		}

		// Continue scanning right store for current left row
		if jc.scanning {
			for {
				// Check context cancellation
				select {
				case <-ctx.Done():
					return nil, false, fmt.Errorf("context cancelled during join scan: %w", ctx.Err())
				default:
				}

				k := jc.right.GetCurrentKey()
				if k == nil {
					break
				}
				v, err := jc.right.GetCurrentValue(ctx)
				if err != nil {
					return nil, false, fmt.Errorf("join scan error (Value): %w", err)
				}

				// Stop Check (B-Tree Order Awareness)
				stop := false
				if jc.plan.IsComposite {
					if kMap, ok := k.(map[string]any); ok {
						for _, pField := range jc.plan.PrefixFields {
							var targetVal any
							for lField, rFieldRaw := range jc.on {
								if fmt.Sprintf("%v", rFieldRaw) == pField {
									targetVal = getField(jc.currentL, lField)
									break
								}
							}
							// Use Compare helper if available, or string fallback logic
							// We use the same comparison logic as B-Tree for consistency
							sCurr := fmt.Sprintf("%v", kMap[pField])
							sTarget := fmt.Sprintf("%v", targetVal)

							if jc.plan.Ascending {
								if sCurr > sTarget {
									stop = true
									break
								} else if sCurr < sTarget {
									stop = false
								}
							} else {
								// Descending: Stop if Current < Target (Passed it)
								// e.g. Target=EU. Scan=US, EU, CN.
								// Start US. US > EU. Continue.
								// Next EU. Match.
								// Next CN. CN < EU. Stop.
								// Wait. "US" > "EU".
								// If order is US, EU, CN.
								// Scan US. "US" > "EU". Continue.
								// Scan EU. "EU" == "EU". Match.
								// Scan CN. "CN" < "EU". Stop.
								if sCurr < sTarget {
									stop = true
									break
								} else if sCurr > sTarget {
									stop = false
								}
							}
							// If equal, check next field
						}
					}
				} else {
					// Primitive Key Comparison
					// Updated to use CompareLoose which handles mixed numeric types (int/float).
					var targetVal any
					for lField, rFieldRaw := range jc.on {
						if fmt.Sprintf("%v", rFieldRaw) == "key" {
							targetVal = getField(jc.currentL, lField)
							break
						}
					}

					// Check "Current > Target" to see if we passed the match range.
					// (Assuming Ascending Order for Primitives)
					if CompareLoose(k, targetVal) > 0 {
						stop = true
					}
				}

				if stop {
					break
				}

				jc.totalRowsScanned++
				jc.rowsSinceLastMatch++

				// Safety: Check if we've scanned too many consecutive rows without matches
				if jc.rowsSinceLastMatch > jc.maxRowsWithoutMatch {
					return nil, false, fmt.Errorf("join scanned %d consecutive rows with no matches (total %d rows) - possible cartesian product or incorrect join condition", jc.rowsSinceLastMatch, jc.totalRowsScanned)
				}

				// Log progress every 10k consecutive non-matches
				if jc.rowsSinceLastMatch%10000 == 0 {
					log.Info("JoinRightCursor: Scanning large dataset without matches",
						"consecutive_non_matches", jc.rowsSinceLastMatch,
						"total_scanned", jc.totalRowsScanned,
						"join_type", jc.joinType)
				}

				// Match Check (Filter)
				match := true
				for lField, rFieldRaw := range jc.on {
					rField := fmt.Sprintf("%v", rFieldRaw)
					lVal := getField(jc.currentL, lField)
					var rVal any
					if rField == "key" {
						rVal = k
					} else {
						if vMap, ok := v.(map[string]any); ok {
							if val, found := vMap[rField]; found {
								rVal = val
							}
						}
						// If not found in Value, check the Key (Composite Key support)
						if rVal == nil {
							if kMap, ok := k.(map[string]any); ok {
								if val, found := kMap[rField]; found {
									rVal = val
								}
							}
						}
					}
					if fmt.Sprintf("%v", lVal) != fmt.Sprintf("%v", rVal) {
						match = false
						break
					}
				}

				if match {
					// Reset counter on match - join is working, allow unlimited streaming
					jc.rowsSinceLastMatch = 0
					jc.matched = true
					merged := jc.mergeResult(jc.currentL, v, k)
					jc.right.Next(ctx)
					return merged, true, nil
				}

				ok, err := jc.right.Next(ctx)
				if err != nil {
					return nil, false, fmt.Errorf("join scan error (Next): %w", err)
				}
				if !ok {
					break
				}
			}

			// Scan completed for this left row
			jc.scanning = false // Reset scanning flag

			if !jc.matched {
				// No matches found during scan
				if jc.rowsSinceLastMatch > 1000 {
					// Scanned many rows without match - possibly malformed join condition
					log.Warn("JoinRightCursor: Scan completed with no matches after many attempts",
						"rows_scanned_for_this_left_row", jc.rowsSinceLastMatch,
						"join_type", jc.joinType,
						"join_condition", jc.on)
				}
			}

			if !jc.matched && jc.joinType == "left" {
				// Reset counter when emitting left join unmatched row
				jc.rowsSinceLastMatch = 0
				res := jc.currentL
				jc.currentL = nil
				return res, true, nil
			}
			jc.currentL = nil
		}
	}
}

func (jc *JoinRightCursor) mergeResult(l any, rAny any, rKey any) any {
	// Determine keys for L
	var lKeys []string
	var lMap map[string]any

	if om, ok := l.(*OrderedMap); ok && om != nil {
		lKeys = om.keys
		lMap = om.m
	} else if om, ok := l.(OrderedMap); ok {
		lKeys = om.keys
		lMap = om.m
	} else if m, ok := l.(map[string]any); ok && m != nil {
		lMap = m
		for k := range m {
			lKeys = append(lKeys, k)
		}
		sort.Strings(lKeys)
	} else {
		lMap = make(map[string]any)
	}

	// Flatten Right Item
	rObj := renderItem(rKey, rAny, nil)

	var rMap map[string]any
	if m, ok := rObj.(map[string]any); ok {
		rMap = m
	} else if om, ok := rObj.(*OrderedMap); ok && om != nil {
		rMap = om.m
	} else if om, ok := rObj.(OrderedMap); ok {
		rMap = om.m
	}

	// Merge
	// STRICT Strategy: Use Aliases if available. No hybrid.
	newKeys := make([]string, 0, len(lKeys))
	newMap := make(map[string]any)

	// Add Left Keys (Prefixed)
	cleanLeftAlias := strings.TrimSpace(jc.leftStoreName)
	if cleanLeftAlias != "" {
		for _, k := range lKeys {
			key := cleanLeftAlias + "." + strings.TrimSpace(k)
			newMap[key] = lMap[k]
			newKeys = append(newKeys, key)
		}
	} else {
		// No alias -> Naked
		for _, k := range lKeys {
			newMap[k] = lMap[k]
			newKeys = append(newKeys, k)
		}
	}

	if rMap != nil {
		var rKeys []string
		if om, ok := rObj.(*OrderedMap); ok && om != nil {
			rKeys = om.keys
		} else if om, ok := rObj.(OrderedMap); ok {
			rKeys = om.keys
		} else {
			for k := range rMap {
				rKeys = append(rKeys, k)
			}
			sort.Strings(rKeys)
		}

		for _, k := range rKeys {
			val := rMap[k]

			cleanRightAlias := strings.TrimSpace(jc.rightStoreName)
			if cleanRightAlias != "" {
				key := cleanRightAlias + "." + strings.TrimSpace(k)
				newMap[key] = val
				newKeys = append(newKeys, key)
			} else {
				// No alias -> Naked
				newMap[k] = val
				newKeys = append(newKeys, k)
			}
		}
	}
	return &OrderedMap{m: newMap, keys: newKeys, isImplicit: true}
}

// Helper for Join Numeric Coercion
// Moved to copilottools.utils.go as coerceToFloatFull to be shared

// generateTempKey creates a comparable string key for the temp store
func (jc *JoinRightCursor) generateTempKey(item map[string]any) string {
	// We iterate the ON keys in deterministic order?
	// jc.on is a map, order is random. We need stable order.
	// But `jc.on` doesn't change during execution.
	// We can sort keys of On.
	var keys []string
	for k := range jc.on {
		// keys are Left Fields? Or Right Fields?
		// jc.on is {LeftField: RightField}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb string
	for _, lKey := range keys {
		rKeyRaw := jc.on[lKey]
		rKey := fmt.Sprintf("%v", rKeyRaw)

		val := getField(item, rKey)
		// Use simple string representation
		if sb != "" {
			sb += "|"
		}
		sb += fmt.Sprintf("%v", val)
	}
	return sb
}

// tempStoreWrapper adapts core_database.Btree to jsondb.StoreAccessor
type tempStoreWrapper struct {
	btree.BtreeInterface[string, any]
	jc *JoinRightCursor
}

func (t *tempStoreWrapper) GetStoreInfo() sop.StoreInfo {
	return t.BtreeInterface.GetStoreInfo()
}
func (t *tempStoreWrapper) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	// Key comes from JoinRightCursor logic (Seek)
	// it might be a map (Composite Key) or primitive.
	var kStr string
	if m, ok := key.(map[string]any); ok {
		// We can't use jc.generateTempKey directy if the map keys are different
		// JoinRightCursor constructs `seekKey` as `compKey[pField] = ...`
		// where pField matches the ON clause right field.
		// generateTempKey expects `item[rKey]`. It should match!
		kStr = t.jc.generateTempKey(m)
	} else {
		// Primitive
		kStr = fmt.Sprintf("%v", key)
	}
	return t.BtreeInterface.Find(ctx, kStr, first)
}
func (t *tempStoreWrapper) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	var kStr string
	if m, ok := key.(map[string]any); ok {
		kStr = t.jc.generateTempKey(m)
	} else {
		kStr = fmt.Sprintf("%v", key)
	}
	return t.BtreeInterface.FindInDescendingOrder(ctx, kStr)
}
func (t *tempStoreWrapper) GetCurrentKey() any {
	// Return the actual B-Tree key (generated string)
	return t.BtreeInterface.GetCurrentKey().Key
}
func (t *tempStoreWrapper) GetCurrentValue(ctx context.Context) (any, error) {
	return t.BtreeInterface.GetCurrentValue(ctx)
}
func (t *tempStoreWrapper) First(ctx context.Context) (bool, error) {
	return t.BtreeInterface.First(ctx)
}
func (t *tempStoreWrapper) Last(ctx context.Context) (bool, error) { return t.BtreeInterface.Last(ctx) }
func (t *tempStoreWrapper) Next(ctx context.Context) (bool, error) { return t.BtreeInterface.Next(ctx) }
func (t *tempStoreWrapper) Previous(ctx context.Context) (bool, error) {
	return t.BtreeInterface.Previous(ctx)
}
func (t *tempStoreWrapper) Add(ctx context.Context, key any, value any) (bool, error) {
	// Not used via wrapper
	return false, fmt.Errorf("read only wrapper")
}
func (t *tempStoreWrapper) Update(ctx context.Context, key any, value any) (bool, error) {
	return false, fmt.Errorf("read only wrapper")
}
func (t *tempStoreWrapper) Remove(ctx context.Context, key any) (bool, error) {
	return false, fmt.Errorf("read only wrapper")
}
