package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/jsondb"
)

func (a *CopilotAgent) toolJoin(ctx context.Context, args map[string]any) (any, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		// We need to import "encoding/json" first, but it's not imported in this file.
		// I'll just print the args map directly for now or add the import.
		// Since I can't easily add imports with replace_string, I'll just use fmt.Printf("%+v", args)
		fmt.Printf("DEBUG: toolJoin called in STUB MODE with:\n%+v\n", args)
		return "Join executed successfully (STUBBED).", nil
	}

	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	// Resolve Left Database
	var leftDb *database.Database
	leftDbName, _ := args["database"].(string)
	if leftDbName == "" {
		leftDbName = p.CurrentDB
	}
	if leftDbName != "" {
		if leftDbName == "system" && a.systemDB != nil {
			leftDb = a.systemDB
		} else if opts, ok := a.databases[leftDbName]; ok {
			leftDb = database.NewDatabase(opts)
		}
	}
	if leftDb == nil {
		return "", fmt.Errorf("left database not found or not selected")
	}

	// Resolve Right Database
	var rightDb *database.Database
	rightDbName, _ := args["right_database"].(string)
	if rightDbName == "" {
		rightDbName = leftDbName
	}
	if rightDbName != "" {
		if rightDbName == "system" && a.systemDB != nil {
			rightDb = a.systemDB
		} else if opts, ok := a.databases[rightDbName]; ok {
			rightDb = database.NewDatabase(opts)
		}
	}
	if rightDb == nil {
		return "", fmt.Errorf("right database not found")
	}

	leftStoreName, _ := args["left_store"].(string)
	rightStoreName, _ := args["right_store"].(string)

	// Parse Join Fields (support both single string and array)
	var leftFields []string
	if args["left_join_fields"] != nil {
		leftFields = coerceToStringSlice(args["left_join_fields"])
	}

	var rightFields []string
	if args["right_join_fields"] != nil {
		rightFields = coerceToStringSlice(args["right_join_fields"])
	}

	// Parse Projection Fields
	var fields []string
	if args["fields"] != nil {
		fields = coerceToStringSlice(args["fields"])
	}

	joinType, _ := args["join_type"].(string)
	if joinType == "" {
		joinType = "inner"
	} else {
		joinType = strings.ToLower(strings.TrimSpace(joinType))
		if strings.Contains(joinType, "outer") {
			joinType = strings.ReplaceAll(joinType, " outer", "")
		}
		if strings.Contains(joinType, "join") {
			joinType = strings.ReplaceAll(joinType, " join", "")
		}
		joinType = strings.TrimSpace(joinType)
	}
	limit := 10.0
	if l, ok := args["limit"]; ok {
		limit = coerceToFloat(l)
	}
	if limit <= 0 {
		limit = 10
	}

	action, _ := args["action"].(string)
	isDeleteLeft := action == "delete_left"
	isUpdateLeft := action == "update_left"
	updateValues, _ := args["update_values"].(map[string]any)

	// Consistency with toolUpdate: Allow loose arguments to define update values
	if isUpdateLeft && len(updateValues) == 0 {
		updateValues = CleanArgs(args,
			"database", "right_database",
			"left_store", "right_store",
			"left_join_fields", "right_join_fields",
			"fields",
			"join_type", "limit", "action",
			"update_values", "order_by", "direction",
		)
	}

	if leftStoreName == "" || rightStoreName == "" {
		// Detect if this is an Atomic Pipeline operation mis-routed to the Legacy Tool
		// The Atomic Engine uses "with" instead of "right_store", and implies "input" from the pipeline.
		_, hasWith := args["with"]
		lastRes := getLastAtomicResult(ctx)
		if hasWith && lastRes != nil {
			log.Info("Delegating ambiguous 'join' to Atomic ScriptEngine (Pipeline Mode)", "with", args["with"])

			// 1. Initialize Engine (reusing session context)
			scriptCtx := getOrInitScriptContext(ctx)

			resolver := func(name string) (Database, error) {
				if name == "" || name == "@db" || name == "current" {
					if p := ai.GetSessionPayload(ctx); p != nil && p.CurrentDB != "" {
						name = p.CurrentDB
					}
				}
				return a.resolveDatabase(name)
			}
			engine := NewScriptEngine(scriptCtx, resolver)
			engine.StoreOpener = a.StoreOpener
			engine.LastResult = lastRes

			// 2. Execute Atomic Join
			result, err := engine.Join(ctx, engine.LastResult, args)
			if err != nil {
				return "", fmt.Errorf("atomic join failed: %w", err)
			}

			// 3. Update State
			setLastAtomicResult(ctx, result)
			engine.LastResult = result

			// 4. Handle result_var
			resVar, _ := args["result_var"].(string)
			if resVar != "" {
				if engine.Context.Variables == nil {
					engine.Context.Variables = make(map[string]any)
				}
				engine.Context.Variables[resVar] = result
			}

			return result, nil
		}

		return "", fmt.Errorf("both left_store and right_store are required")
	}
	if len(leftFields) == 0 {
		return "", fmt.Errorf("left_join_fields (or left_join_field) is required")
	}
	if len(rightFields) == 0 {
		rightFields = []string{"key"}
	}
	if len(leftFields) != len(rightFields) {
		return "", fmt.Errorf("number of left join fields (%d) must match number of right join fields (%d)", len(leftFields), len(rightFields))
	}

	// Parse Order By
	orderBy, _ := args["order_by"].(string)
	if orderBy == "" {
		orderBy, _ = args["direction"].(string)
	}
	isDesc := false
	var rightSortFields []string

	if orderBy != "" {
		// Split by comma for composite sort
		parts := strings.Split(orderBy, ",")

		// The first part determines the iteration order of the LEFT store (since we drive the join from the left)
		// UNLESS the query optimizer (us) decides to swap left/right, which we don't do dynamically yet here.
		// We assume the first field mentioned corresponds to the Left Store if it matches a Left Field,
		// or if the alias matches.
		// For simplicity in this tool: The primary sort direction (Asc/Desc) applies to Left Loop.
		// Detailed sort specs (e.g. "a.f1 ASC, b.f2 DESC") might require buffering Right matches.

		firstPart := strings.TrimSpace(parts[0])
		lowerFirst := strings.ToLower(firstPart)

		// Check direction of first part
		if strings.HasSuffix(lowerFirst, " desc") {
			isDesc = true
		}

		// Handle secondary sort fields (Right Store sorting)
		// We only support secondary sort on Right Store fields for now.
		if len(parts) > 1 {
			for _, p := range parts[1:] {
				p = strings.TrimSpace(p)
				if p != "" {
					// We only care about the field name and direction for the Right Store
					// We store the full string "field DESC" or "field"
					rightSortFields = append(rightSortFields, p)
				}
			}
		} else {
			// Even if single order by, if it refers to the Right store...
			// But Single Order By usually implies Left store iteration order if possible.
			// Currently we only support controlling Left Store iteration direction.
		}
	}

	leftMode := sop.ForReading
	if isDeleteLeft || isUpdateLeft {
		leftMode = sop.ForWriting
	}

	leftTx, leftAutoCommit, err := a.resolveTransaction(ctx, leftDb, leftDbName, leftMode)
	if err != nil {
		return "", err
	}
	if leftAutoCommit {
		defer leftTx.Rollback(ctx)
	}

	var rightTx sop.Transaction
	var rightAutoCommit bool

	if rightDbName == leftDbName {
		rightTx = leftTx
	} else {
		rightTx, rightAutoCommit, err = a.resolveTransaction(ctx, rightDb, rightDbName, sop.ForReading)
		if err != nil {
			return "", err
		}
		if rightAutoCommit {
			defer rightTx.Rollback(ctx)
		}
	}

	// Open Stores
	leftStore, _, leftIndexSpec, err := a.openGenericStore(ctx, leftDb.Options(), leftStoreName, leftTx)
	if err != nil {
		return "", fmt.Errorf("failed to open left store: %w", err)
	}
	rightStore, rightComparer, rightIndexSpec, err := a.openGenericStore(ctx, rightDb.Options(), rightStoreName, rightTx)
	if err != nil {
		return "", fmt.Errorf("failed to open right store: %w", err)
	}

	// ---------------------------------------------------------
	// WILDCARD EXPANSION LOGIC
	// ---------------------------------------------------------
	hasWildcard := false
	for _, f := range fields {
		if strings.Contains(f, "*") {
			hasWildcard = true
			break
		}
	}

	if hasWildcard {
		expandedFields := make([]string, 0)

		// Helper to fetch sample keys/values
		var leftSample map[string]any
		var rightSample map[string]any

		fetchSample := func(store btree.BtreeInterface[any, any]) map[string]any {
			if ok, _ := store.First(ctx); ok {
				k := store.GetCurrentKey()
				v, _ := store.GetCurrentValue(ctx)
				flat := flattenItem(k.Key, v)
				return flat
			}
			return nil
		}

		leftSample = fetchSample(leftStore)
		rightSample = fetchSample(rightStore)

		for _, f := range fields {
			fTrim := strings.TrimSpace(f)
			fLower := strings.ToLower(fTrim)

			// 1. "SELECT *" (Global Wildcard)
			if fTrim == "*" {
				if leftSample != nil {
					var keys []string
					for k := range leftSample {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						expandedFields = append(expandedFields, fmt.Sprintf("a.%s", k))
					}
				}
				if rightSample != nil {
					var keys []string
					for k := range rightSample {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						expandedFields = append(expandedFields, fmt.Sprintf("b.%s", k))
					}
				}
				continue
			}

			// 2. "a.*" or "left.*"
			if fLower == "a.*" || fLower == "left.*" || (leftStoreName != "" && fLower == strings.ToLower(leftStoreName)+".*") {
				if leftSample != nil {
					var keys []string
					for k := range leftSample {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						expandedFields = append(expandedFields, fmt.Sprintf("a.%s", k))
					}
				}
				continue
			}

			// 3. "b.*" or "right.*"
			if fLower == "b.*" || fLower == "right.*" || (rightStoreName != "" && fLower == strings.ToLower(rightStoreName)+".*") {
				if rightSample != nil {
					var keys []string
					for k := range rightSample {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						expandedFields = append(expandedFields, fmt.Sprintf("b.%s", k))
					}
				}
				continue
			}

			// 4. Regular Field
			expandedFields = append(expandedFields, fTrim)
		}

		if len(expandedFields) > 0 {
			fields = expandedFields
			log.Info(fmt.Sprintf("Join: Expanded wildcards to %v", fields))
		}
	}

	emitter := NewResultEmitter(ctx)
	// For Join, columns depends on projection.
	if len(fields) > 0 {
		emitter.SetColumns(fields)
	}
	emitter.Start()

	jp := &JoinProcessor{
		ctx:             ctx,
		leftStore:       leftStore,
		rightStore:      rightStore,
		leftFields:      leftFields,
		rightFields:     rightFields,
		fields:          fields,
		joinType:        joinType,
		limit:           limit,
		isDeleteLeft:    isDeleteLeft,
		isUpdateLeft:    isUpdateLeft,
		updateValues:    updateValues,
		emitter:         emitter,
		rightComparer:   rightComparer,
		leftTx:          leftTx,
		leftAutoCommit:  leftAutoCommit,
		leftIndexSpec:   leftIndexSpec,
		rightIndexSpec:  rightIndexSpec,
		leftStoreName:   leftStoreName,
		rightStoreName:  rightStoreName,
		isDesc:          isDesc,
		rightSortFields: rightSortFields,
	}

	return jp.Execute()
}

// JoinProcessor handles the execution of a join operation between two stores.
type JoinProcessor struct {
	ctx             context.Context
	leftStore       btree.BtreeInterface[any, any]
	rightStore      btree.BtreeInterface[any, any]
	rightComparer   btree.ComparerFunc[any]
	leftFields      []string
	rightFields     []string
	fields          []string
	joinType        string
	limit           float64
	isDeleteLeft    bool
	isUpdateLeft    bool
	updateValues    map[string]any
	emitter         *ResultEmitter
	leftTx          sop.Transaction
	leftAutoCommit  bool
	count           int
	displayKeys     []string
	leftIndexSpec   *jsondb.IndexSpecification
	leftStoreName   string
	rightStoreName  string
	isDesc          bool
	rightIndexSpec  *jsondb.IndexSpecification
	rightSortFields []string

	// Internal State
	rightKeyFields       []string
	rightValueFields     []string
	rightKeyFieldMap     map[string]string
	rightKeyIsJSON       bool
	rightSampleKey       any
	canUseLookup         bool
	rightCache           map[string][]cachedItem
	lastLookupKey        any
	isRightSortOptimized bool
	bloomFilter          *BloomFilter

	// Planner State
	strategy string // "lookup", "merge", "hash_left", "hash_right"
}

type cachedItem struct {
	Key   any
	Value any
}

// Constants for Join Strategies
const (
	StrategyLookup    = "lookup"
	StrategyMerge     = "merge"
	StrategyHashRight = "hash_right" // Scan Left, Probe Right (Buffered)
	StrategyHashLeft  = "hash_left"  // Scan Right, Probe Left (Buffered)
)

func (jp *JoinProcessor) analyzeRightStore() error {
	rOk, _ := jp.rightStore.First(jp.ctx)
	jp.rightKeyFieldMap = make(map[string]string)
	rightKeySampleVals := make(map[string]any)

	if rOk {
		rKey := jp.rightStore.GetCurrentKey()
		jp.rightSampleKey = rKey.Key
		// Check if key is JSON string
		if s, ok := rKey.Key.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
			jp.rightKeyIsJSON = true
			log.Info("Join: Right Store Key detected as JSON.")
		}

		for _, field := range jp.rightFields {
			isKey, actualName := isKeyField(rKey.Key, field)
			if !isKey && jp.rightIndexSpec != nil {
				// If the store has an Index Specification, check if this field is part of it.
				// If so, we treat it as a Key field because the B-Tree is ordered/indexed by it.
				for _, idxField := range jp.rightIndexSpec.IndexFields {
					if strings.EqualFold(idxField.FieldName, field) {
						isKey = true
						actualName = idxField.FieldName
						break
					}
				}
			}

			// Check Relations (Relational Intelligence)
			if !isKey {
				leftInfo := jp.leftStore.GetStoreInfo()
				// Scan relations on the Left Store Source
				for _, rel := range leftInfo.Relations {
					if rel.TargetStore == jp.rightStoreName {
						for _, tf := range rel.TargetFields {
							if strings.EqualFold(tf, field) {
								// Found a relation pointing to this field on the Right Store.
								// If the Right Store is primitive (which we imply by !isKey for this field), we treat it as the Key.
								// We map the user's field (e.g. "id") to the internal "key".
								isKey = true
								actualName = "key"
								log.Info(fmt.Sprintf("Join: Relational Intelligence mapped right field '%s' to 'key' via metadata.", field))
								break
							}
						}
					}
					if isKey {
						break
					} // Found
				}
			}

			if isKey {
				jp.rightKeyFields = append(jp.rightKeyFields, field)

				jp.rightKeyFieldMap[field] = actualName
				rightKeySampleVals[field] = extractVal(rKey.Key, nil, actualName)
			} else {
				jp.rightValueFields = append(jp.rightValueFields, field)
			}
		}
	} else {
		// Empty right store, treat all as value fields (fallback to scan)
		jp.rightValueFields = jp.rightFields
	}

	// We can use Lookup if we have at least one Key field to search on
	// AND we are not trying to lookup sub-fields of a JSON string key (unless we have the full key string)
	// We use FindOne(..., false) to seek to the first item >= key, which supports partial keys (prefix search).
	jp.canUseLookup = len(jp.rightKeyFields) > 0
	log.Info(fmt.Sprintf("Join: Initial canUseLookup=%v (RightKeyFields=%v)", jp.canUseLookup, jp.rightKeyFields))

	if jp.rightKeyIsJSON && len(jp.rightKeyFields) > 0 {
		// If we are joining on "key" explicitly, we can lookup (assuming left side provides the full JSON string)
		// If we are joining on sub-fields (e.g. "region"), we cannot construct the JSON string key reliably -> Disable Lookup
		jp.canUseLookup = false
		for _, f := range jp.rightKeyFields {
			if f == "key" {
				jp.canUseLookup = true
				break
			}
		}
		if !jp.canUseLookup {
			log.Info("Join: Disabled Lookup because Key is JSON and not joining on full 'key'.")
		}
	}

	// If IndexSpecification is present, verify that the join fields match the index prefix.
	if jp.rightIndexSpec != nil && len(jp.rightIndexSpec.IndexFields) > 0 {
		log.Info(fmt.Sprintf("Join: Right has IndexSpec with %d fields.", len(jp.rightIndexSpec.IndexFields)))
		if !jp.validateIndexPrefix() {
			log.Info("Join: Disabling Lookup because join fields do not match IndexSpecification prefix.")
			jp.canUseLookup = false
		}
	}

	return nil
}

// validateIndexPrefix checks if the join fields form a valid prefix of the index fields.
// Returns true if at least the first index field is present in the join keys.
func (jp *JoinProcessor) validateIndexPrefix() bool {
	if jp.rightIndexSpec == nil || len(jp.rightIndexSpec.IndexFields) == 0 {
		return true // No index spec, rely on existing key fields check
	}

	matchesPrefix := false
	for i, idxField := range jp.rightIndexSpec.IndexFields {
		// Check if idxField.FieldName is in jp.rightKeyFields
		// We must map user join fields to actual store field names first.
		found := false
		for _, joinField := range jp.rightKeyFields {
			// FIXED: Use the mapped actual name, not the user alias/input name
			actualName := jp.rightKeyFieldMap[joinField]
			// Fallback if map entry missing (shouldn't happen if setup correctly)
			if actualName == "" {
				actualName = joinField
			}

			if strings.EqualFold(actualName, idxField.FieldName) {
				found = true
				break
			}
		}

		if !found {
			// Missing a leading index field.
			if i == 0 {
				// If the very first index field is missing, we cannot use Lookup at all.
				matchesPrefix = false
			}
			// If i > 0, we have a partial prefix match (valid).
			// We stop checking further index fields.
			break
		}
		// Found this field, so prefix is valid up to here.
		matchesPrefix = true
	}
	return matchesPrefix
}

// checkRightSortOptimization determines if the B-Tree index order naturally satisfies the requested rightSortFields.
func (jp *JoinProcessor) checkRightSortOptimization() {
	if len(jp.rightSortFields) == 0 {
		jp.isRightSortOptimized = true
		return
	}
	if jp.rightIndexSpec == nil || len(jp.rightIndexSpec.IndexFields) == 0 {
		return
	}

	// 1. Identify which IndexFields constitute the Join Prefix.
	// We assume validateIndexPrefix has effectively run or that we only start matching sort AFTER the join keys.
	// But `validateIndexPrefix` logic might be permissive (partial prefix).
	// We need to know EXACTLY how many IndexFields are "consumed" by the Join Equality condition.

	// Count consumed index fields (must be a contiguous prefix starting at 0)
	consumedCount := 0
	for _, idxField := range jp.rightIndexSpec.IndexFields {
		found := false
		for _, joinField := range jp.rightKeyFields {
			actualName := jp.rightKeyFieldMap[joinField]
			if actualName == "" {
				actualName = joinField
			}
			if strings.EqualFold(actualName, idxField.FieldName) {
				found = true
				break
			}
		}
		if found {
			consumedCount++
		} else {
			break // Stop at first gap
		}
	}

	// 2. Match `rightSortFields` against the SUBSEQUENT IndexFields
	// If the Query asks to sort by fields that are NOT the next fields in the index, optimization fails.

	if consumedCount+len(jp.rightSortFields) > len(jp.rightIndexSpec.IndexFields) {
		// Index doesn't have enough fields to cover the sort request
		jp.isRightSortOptimized = false
		return
	}

	for i, sortSpec := range jp.rightSortFields {
		indexField := jp.rightIndexSpec.IndexFields[consumedCount+i]

		// Parse Sort Spec
		specName := sortSpec
		specDesc := false
		lowerSpec := strings.ToLower(sortSpec)
		if strings.HasSuffix(lowerSpec, " desc") {
			specName = sortSpec[:len(sortSpec)-5]
			specDesc = true
		} else if strings.HasSuffix(lowerSpec, " asc") {
			specName = sortSpec[:len(sortSpec)-4]
		}
		specName = strings.TrimSpace(specName)

		// Check Name Match
		// Note: specName might be an alias like "b.age". We need to support that if mapped?
		// But usually we stripped aliases in parser or expecting simple names.
		// However, in processLeftItem buffer sort, we use extractVal which handles mapped names or structs.
		// Here we need to match strict Store Field Names index definition.
		// Assuming specName is the simple field name for now (as `toolJoin` parser logic is simple).
		// If user typed "b.age", we need to strip prefix maybe?
		// In `toolJoin` we stripped prefixes for display keys, but `rightSortFields` are raw inputs?
		// Let's check `rightSortFields` parsing in the code I just added.
		// It just trims space. So "b.f2 DESC".
		// We should strip "b." or store name.

		cleanSpecName := specName
		if dotIndex := strings.LastIndex(specName, "."); dotIndex != -1 {
			cleanSpecName = specName[dotIndex+1:]
		}

		if !strings.EqualFold(cleanSpecName, indexField.FieldName) {
			jp.isRightSortOptimized = false
			log.Info(fmt.Sprintf("Join Optimization Failed: Sort field '%s' does not match Index field '%s' at pos %d", cleanSpecName, indexField.FieldName, consumedCount+i))
			return
		}

		// Check Direction Match
		// Index.Asc (true) matches !specDesc (true if ASC)
		// Index.Asc (false) matches specDesc (true if DESC)
		// So: Index.Asc == !specDesc
		if indexField.AscendingSortOrder != !specDesc {
			jp.isRightSortOptimized = false
			log.Info(fmt.Sprintf("Join Optimization Failed: Sort direction mismatch for '%s'. IndexAsc=%v, ReqDesc=%v", cleanSpecName, indexField.AscendingSortOrder, specDesc))
			return
		}
	}

	log.Info("Join: Right Sort Optimization ENABLED. B-Tree index matches sort request.")
	jp.isRightSortOptimized = true
}

func (jp *JoinProcessor) buildHashCache() error {
	if !jp.canUseLookup {
		log.Info("Join: Building Hash Cache for Right Store (Hash Join Optimization)...")
		jp.rightCache = make(map[string][]cachedItem)

		// Scan Right Store Once
		rFound, _ := jp.rightStore.First(jp.ctx)
		for rFound {
			rKey := jp.rightStore.GetCurrentKey()
			rVal, _ := jp.rightStore.GetCurrentValue(jp.ctx)

			cacheKey := generateJoinKey(rKey.Key, rVal, jp.rightFields)
			jp.rightCache[cacheKey] = append(jp.rightCache[cacheKey], cachedItem{Key: rKey.Key, Value: rVal})

			rFound, _ = jp.rightStore.Next(jp.ctx)
		}
		log.Info(fmt.Sprintf("Join: Hash Cache built with %d unique keys.", len(jp.rightCache)))
	}
	return nil
}

// buildBloomFilter builds a Bloom Filter for the Right Store keys to optimize 'StrategyLookup'.
func (jp *JoinProcessor) buildBloomFilter() error {
	count := jp.rightStore.Count()
	if count == 0 {
		return nil
	}
	// False positive rate 1%
	jp.bloomFilter = NewBloomFilter(uint(count), 0.01)

	log.Info(fmt.Sprintf("Join: Building Bloom Filter for Right Store (Size: %d)...", count))

	// Scan Right Store
	ok, err := jp.rightStore.First(jp.ctx)
	if err != nil {
		return err
	}
	for ok {
		k := jp.rightStore.GetCurrentKey()
		var v any
		allKeyFields := true
		for _, f := range jp.rightFields {
			isK, _ := isKeyField(k.Key, f)
			if !isK {
				allKeyFields = false
				break
			}
		}
		if !allKeyFields {
			v, err = jp.rightStore.GetCurrentValue(jp.ctx)
			if err != nil {
				return err
			}
		}

		probeKey := generateJoinKey(k.Key, v, jp.rightFields)
		jp.bloomFilter.Add(probeKey)

		ok, _ = jp.rightStore.Next(jp.ctx)
	}
	log.Info("Join: Bloom Filter built.")
	return nil
}

// processLeftItem handles a single Left item for Lookups or Hash Probes
// vProvider is a callback to fetch the value lazily if needed.
func (jp *JoinProcessor) processLeftItem(k any, vProvider func() (any, error)) (bool, error) {
	// 1. Extract Join Values (Try from Key First)
	// We optimize by trying to extract purely from Key.
	// If extractVal needs Value (returns nil), we might need to invoke vProvider.

	// Helper to extract with lazy value
	var cachedVal any
	var valFetched bool

	getVal := func() (any, error) {
		if valFetched {
			return cachedVal, nil
		}
		var err error
		cachedVal, err = vProvider()
		valFetched = true
		return cachedVal, err
	}

	leftJoinVals := make(map[string]any)
	for i, field := range jp.leftFields {
		rightFieldName := jp.rightFields[i]

		// Attempt extract from Key (pass nil value)
		val := extractVal(k, nil, field)
		if val == nil {
			// Check if it's really missing or just in Value
			// We can check isKeyField logic or just try fetching value
			// Optimization: We check if field IS "key" explicitly or mapped?
			// Simpler: Just fetch value if nil returned (and field isn't "key").
			if field == "key" {
				// It was successfully extracted as nil? No, extractVal handles "key" specially.
				// If val is nil, it means it's not found in Key.
			}

			// Must fetch value
			v, err := getVal()
			if err != nil {
				return false, err
			}
			val = extractVal(k, v, field)
		}
		leftJoinVals[rightFieldName] = val
	}

	// Bloom Filter Optimization
	// If the Bloom Filter is active, we check it before proceeding to Lookup or Probe.
	if jp.bloomFilter != nil {
		var parts []string
		for _, f := range jp.rightFields {
			v := leftJoinVals[f]
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		probeKey := strings.Join(parts, "|")

		if !jp.bloomFilter.Test(probeKey) {
			// Definite miss. Skip this item.
			return false, nil
		}
	}

	if !jp.canUseLookup {
		// Hash Join Probe. rightCache was built with generateJoinKey(rKey, rVal,
		// jp.rightFields), so the probe key must be generated the same way. If V was
		// already fetched above (during Bloom filter / leftJoinVals extraction),
		// cachedVal holds it and generateJoinKey won't need to fetch it again.
		vToPass := cachedVal // nil if not fetched
		probeKey := generateJoinKey(k, vToPass, jp.leftFields)

		matches, ok := jp.rightCache[probeKey]

		if ok {
			// We have a match! Now we definitely need V for `emitMatch`.
			v, err := getVal()
			if err != nil {
				return false, err
			}

			if jp.isDesc {
				for i := len(matches) - 1; i >= 0; i-- {
					matchItem := matches[i]
					stop, err := jp.emitMatch(k, v, matchItem.Key, matchItem.Value)
					if err != nil {
						return false, err
					}
					if stop {
						return true, nil
					}
				}
			} else {
				for _, matchItem := range matches {
					stop, err := jp.emitMatch(k, v, matchItem.Key, matchItem.Value)
					if err != nil {
						return false, err
					}
					if stop {
						return true, nil
					}
				}
			}
		}
		return false, nil
	}

	// ... Lookup Logic ...
	// Requires leftJoinVals. We already computed them.
	// We only need V if emitMatch is called.

	var rightFound bool

	if jp.canUseLookup {
		var lookupKey any

		// Check if we are doing a simple Key lookup (either explicit "key" field or mapped to "key")
		isSimpleKeyLookup := false
		if len(jp.rightKeyFields) == 1 {
			// Check if the single key field maps to "key" (effectively the Primary Key)
			if actualName, ok := jp.rightKeyFieldMap[jp.rightKeyFields[0]]; ok && actualName == "key" {
				isSimpleKeyLookup = true
			}
		}

		if isSimpleKeyLookup {
			lookupKey = leftJoinVals[jp.rightKeyFields[0]]
			if jp.rightSampleKey != nil {
				lookupKey = coerce(lookupKey, jp.rightSampleKey)
			}
		} else {
			lkMap := make(map[string]any)
			for _, field := range jp.rightKeyFields {
				actualName := jp.rightKeyFieldMap[field]
				v := leftJoinVals[field]
				lkMap[actualName] = v
			}
			lookupKey = lkMap
		}

		if lookupKey != nil {
			var err error
			needFind := true

			// Check if input is sorted (optimization safety)
			isSortedInput := true
			if jp.lastLookupKey != nil {
				if jp.rightComparer(lookupKey, jp.lastLookupKey) < 0 {
					isSortedInput = false
				}
			}
			jp.lastLookupKey = lookupKey

			// Optimization: Check current position
			currKey := jp.rightStore.GetCurrentKey()
			if currKey.Key != nil {
				cmp := jp.rightComparer(currKey.Key, lookupKey)
				if cmp == 0 {
					needFind = false
					rightFound = true
				} else if cmp < 0 {
					if ok, _ := jp.rightStore.Next(jp.ctx); ok {
						nextKey := jp.rightStore.GetCurrentKey()
						cmpNext := jp.rightComparer(nextKey.Key, lookupKey)
						if cmpNext == 0 {
							needFind = false
							rightFound = true
						} else if cmpNext > 0 {
							needFind = false
							rightFound = true
						}
					} else {
						needFind = false
						rightFound = false
					}
				} else {
					if isSortedInput {
						needFind = false
						rightFound = true
					} else {
						needFind = true
					}
				}
			}

			if needFind {
				_, err = jp.rightStore.Find(jp.ctx, lookupKey, false)
				if err != nil {
					rightFound = false
				} else {
					rightFound = (jp.rightStore.GetCurrentKey().Key != nil)
				}
			}
		}
	} else {
		rightFound, _ = jp.rightStore.First(jp.ctx)
	}

	// Buffer or Emit matches?
	// If rightSortFields are present we MUST buffer matches for this left item, sort them, then emit.
	// UNLESS the B-Tree is already sorted optimally.
	var buffer []struct {
		k, v any
	}
	useBuffer := len(jp.rightSortFields) > 0 && !jp.isRightSortOptimized

	for rightFound {
		rKey := jp.rightStore.GetCurrentKey()
		rVal, _ := jp.rightStore.GetCurrentValue(jp.ctx)

		match := true
		for _, field := range jp.rightKeyFields {
			rFieldVal := extractVal(rKey.Key, rVal, field)
			lVal := leftJoinVals[field]
			if !valuesMatch(lVal, rFieldVal) {
				match = false
				break
			}
		}

		if !match && jp.canUseLookup {
			// We can only break if the mismatch occurred on a field that is part of the Index Sort Order.
			// If the mismatch occurred on a non-indexed field, we must continue scanning (as the record might be valid but out of order relative to non-indexed fields).
			shouldBreak := false
			if jp.rightIndexSpec != nil {
				for _, idxField := range jp.rightIndexSpec.IndexFields {
					// Find if this index field is part of the join criteria
					var constraintVal any
					isJoinField := false

					for _, kf := range jp.rightKeyFields {
						// Map the join field to the actual store field name
						if jp.rightKeyFieldMap[kf] == idxField.FieldName {
							isJoinField = true
							constraintVal = leftJoinVals[kf]
							break
						}
					}

					if !isJoinField {
						// Index field not in join criteria?
						// We can stop checking index fields (subsequent sort order doesn't matter for breaking)
						break
					}

					// It IS a join field. Check if current record matches constraint.
					rFieldVal := extractVal(rKey.Key, rVal, idxField.FieldName)
					if !valuesMatch(constraintVal, rFieldVal) {
						// Index field mismatch!
						// Since B-Tree is sorted by this field, and we found a diff, we left the matching block.
						shouldBreak = true
						break
					}
				}
			} else {
				// Should not happen if canUseLookup is true, but safe fallback
				shouldBreak = true
			}

			if shouldBreak {
				break
			}
		}

		if match {
			for _, field := range jp.rightValueFields {
				rFieldVal := extractVal(rKey.Key, rVal, field)
				lVal := leftJoinVals[field]
				if !valuesMatch(lVal, rFieldVal) {
					match = false
					break
				}
			}
		}

		if match {
			// Match confirmed. We need V for buffer/emit.
			v, err := getVal()
			if err != nil {
				return false, err
			}

			if useBuffer {
				buffer = append(buffer, struct{ k, v any }{rKey.Key, rVal})
			} else {
				stop, err := jp.emitMatch(k, v, rKey.Key, rVal)
				if err != nil {
					return false, err
				}
				if stop {
					return true, nil
				}
			}
		}

		rightFound, _ = jp.rightStore.Next(jp.ctx)
	}

	if useBuffer && len(buffer) > 0 {
		// We need V for sorting buffer keys extraction? No, buffer stores Right Store values.
		// So we only need V when we EMIT.
		v, err := getVal()
		if err != nil {
			return false, err
		}

		// Sort the buffer
		sort.Slice(buffer, func(i, j int) bool {
			// Compare each sort field
			for _, sortField := range jp.rightSortFields {
				// Parse "field DESC" vs "field"
				fieldName := sortField
				desc := false
				lower := strings.ToLower(sortField)
				if strings.HasSuffix(lower, " desc") {
					fieldName = sortField[:len(sortField)-5] // remove " desc"
					desc = true
				} else if strings.HasSuffix(lower, " asc") {
					fieldName = sortField[:len(sortField)-4] // remove " asc"
				}
				fieldName = strings.TrimSpace(fieldName)

				// Extract values
				// We need to support "b.field" alias stripping if necessary, but rightSortFields parsing usually stripped it?
				// ToolJoin parsing usually strips prefixes. Let's assume fieldName is clean Key/Value field name.

				// Handle "b." prefix or store name prefix if present in the sort spec
				// Actually, in ToolJoin we usually extract pure field names or keep aliases.
				// Here we are comparing Right Store items. The fields must exist in Right Store Key or Value.

				// We try to extract from Key or Value
				valI := extractVal(buffer[i].k, buffer[i].v, fieldName)
				valJ := extractVal(buffer[j].k, buffer[j].v, fieldName)

				res := btree.Compare(valI, valJ)
				if res == 0 {
					continue
				}

				if desc {
					return res > 0 // i > j -> i comes first
				}
				return res < 0 // i < j -> i comes first
			}
			return false // Equal
		})

		// Emit sorted buffer
		for _, item := range buffer {
			stop, err := jp.emitMatch(k, v, item.k, item.v)
			if err != nil {
				return false, err
			}
			if stop {
				return true, nil
			}
		}
	}

	return false, nil
}

func (jp *JoinProcessor) computeDisplayKeys() {
	jp.displayKeys = make([]string, len(jp.fields))
	candidates := make([]string, len(jp.fields))
	counts := make(map[string]int)

	// Regex for " AS " (case insensitive, handles multiple spaces/tabs)
	aliasRe := regexp.MustCompile(`(?i)\s+as\s+`)

	for i, f := range jp.fields {
		// Handle "AS" alias
		if loc := aliasRe.FindStringIndex(f); loc != nil {
			// loc[0] is start of match, loc[1] is end of match
			alias := strings.TrimSpace(f[loc[1]:])
			jp.fields[i] = strings.TrimSpace(f[:loc[0]]) // Update field to source name
			candidates[i] = alias
			counts[alias]++
			continue
		}

		clean := f
		lowerF := strings.ToLower(f)
		// Strip prefixes
		// We DO NOT strip "a." and "b." because they are used as explicit references in 'project' steps.
		// If we strip them, downstream tools looking for "a.field" will fail.
		prefixes := []string{"left.", "right.", "left_", "right_"}
		if jp.leftStoreName != "" {
			prefixes = append(prefixes, strings.ToLower(jp.leftStoreName)+".")
		}
		if jp.rightStoreName != "" {
			prefixes = append(prefixes, strings.ToLower(jp.rightStoreName)+".")
		}

		for _, prefix := range prefixes {
			if strings.HasPrefix(lowerF, prefix) {
				clean = f[len(prefix):]
				break
			}
		}
		// Strip numeric suffix if it looks like _1
		lastUnderscore := strings.LastIndex(clean, "_")
		if lastUnderscore > 0 && lastUnderscore < len(clean)-1 {
			suffix := clean[lastUnderscore+1:]
			if _, err := strconv.Atoi(suffix); err == nil {
				clean = clean[:lastUnderscore]
			}
		}

		// Title Case (Simple) - DISABLED per ANSI SQL preference
		// if len(clean) > 0 {
		// 	clean = strings.ToUpper(clean[:1]) + clean[1:]
		// }
		candidates[i] = clean
		counts[clean]++
	}

	for i := range jp.fields {
		clean := candidates[i]
		if counts[clean] > 1 {
			// Collision: Fallback to formatted original
			// But if it was an explicit alias, we should probably respect it?
			// If user aliased two fields to the same name, that's their problem/intent.
			// But let's keep collision handling for auto-generated names.

			// Check if this candidate came from an explicit alias
			// We can check if the original field string had " as ".
			// But we lost the original string in the first loop if we overwrote jp.fields[i].
			// Let's assume explicit aliases are intentional and don't dedupe them aggressively,
			// or we just let them collide.

			jp.displayKeys[i] = clean
		} else {
			jp.displayKeys[i] = clean
		}
	}
}

func (jp *JoinProcessor) emitMatch(k, v, rKey, rVal any) (bool, error) {
	include := false
	if jp.joinType == "inner" || jp.joinType == "left" {
		include = true
	}

	if include {
		if jp.isDeleteLeft {
			jp.leftStore.Remove(jp.ctx, k)
			jp.count++
			return false, nil
		}

		if jp.isUpdateLeft {
			var newVal any
			if vMap, ok := v.(map[string]any); ok {
				newVal = mergeMap(vMap, jp.updateValues)
			} else {
				newVal = jp.updateValues
			}
			if ok, err := jp.leftStore.Update(jp.ctx, k, newVal); err != nil || !ok {
				return false, fmt.Errorf("failed to update left item: %v", err)
			}
			jp.count++
			return false, nil
		} else {
			if len(jp.fields) > 0 {
				resultMap := OrderedMap{m: make(map[string]any), keys: make([]string, 0)}

				for i, f := range jp.fields {
					var val any

					// Note: f has been stripped of " AS alias" in computeDisplayKeys
					// But it might still have prefixes like "a.", "b." etc.

					lowerF := strings.ToLower(f)

					if strings.HasPrefix(lowerF, "left.") {
						fieldName := f[5:]
						val = extractVal(k, v, fieldName)
					} else if strings.HasPrefix(lowerF, "right.") {
						fieldName := f[6:]
						val = extractVal(rKey, rVal, fieldName)
					} else {
						// Try Left
						val = extractVal(k, v, f)
						if val == nil {
							// Try Right
							val = extractVal(rKey, rVal, f)
						}

						// Heuristic for aliases (a., b., left_, right_)
						if val == nil {
							var fieldName string
							var tryLeft, tryRight bool

							if strings.HasPrefix(lowerF, "a.") {
								fieldName = f[2:]
								tryLeft = true
							} else if strings.HasPrefix(lowerF, "b.") {
								fieldName = f[2:]
								tryRight = true
							} else if strings.HasPrefix(lowerF, "left_") {
								fieldName = f[5:]
								tryLeft = true
							} else if strings.HasPrefix(lowerF, "right_") {
								fieldName = f[6:]
								tryRight = true
							} else if jp.leftStoreName != "" && strings.HasPrefix(lowerF, strings.ToLower(jp.leftStoreName)+".") {
								fieldName = f[len(jp.leftStoreName)+1:]
								tryLeft = true
							} else if jp.rightStoreName != "" && strings.HasPrefix(lowerF, strings.ToLower(jp.rightStoreName)+".") {
								fieldName = f[len(jp.rightStoreName)+1:]
								tryRight = true
							}

							if tryLeft {
								val = extractVal(k, v, fieldName)
							} else if tryRight {
								val = extractVal(rKey, rVal, fieldName)
							}
						}

						// Heuristic for numeric suffixes (e.g. department_1 -> department)
						if val == nil {
							lastUnderscore := strings.LastIndex(f, "_")
							if lastUnderscore > 0 && lastUnderscore < len(f)-1 {
								suffix := f[lastUnderscore+1:]
								if _, err := strconv.Atoi(suffix); err == nil {
									baseName := f[:lastUnderscore]
									val = extractVal(k, v, baseName)
									if val != nil {
									} else {
										val = extractVal(rKey, rVal, baseName)
									}
								}
							}
						}
					}

					// Normalize key for display
					displayKey := jp.displayKeys[i]

					resultMap.m[displayKey] = val
					resultMap.keys = append(resultMap.keys, displayKey)
				}

				log.Debug(fmt.Sprintf("resultMap: %v\n", resultMap))

				jp.emitter.Emit(resultMap)
			} else {
				// Merge left and right values
				merged := make(map[string]any)
				if vm, ok := v.(map[string]any); ok {
					for k, val := range vm {
						merged[k] = val
					}
				} else {
					merged["left"] = v
				}

				if rm, ok := rVal.(map[string]any); ok {
					for k, val := range rm {
						merged[k] = val
					}
				} else {
					merged["right"] = rVal
				}

				// If Key is a JSON object map, assume it's part of the data and merge it too!
				// This avoids "key" column floating around disjointed in Join results.
				// (Unless key conflicts with existing value field, then we keep it as 'key')
				if keyMap, ok := k.(map[string]any); ok {
					for kField, kVal := range keyMap {
						if _, exists := merged[kField]; !exists {
							merged[kField] = kVal
						}
					}
				}

				// Format output using standard helper (preserves Index Spec)
				// Use renderItem to handle projection/aliasing consistently
				finalItem := renderItem(merged, nil, jp.fields)

				log.Debug(fmt.Sprintf("finalItem: %v\n", finalItem))

				jp.emitter.Emit(finalItem)
			}
			jp.count++
			if jp.count >= int(jp.limit) {
				return true, nil
			}
		}
	}
	return false, nil
}

// Execute runs the join operation.
func (jp *JoinProcessor) Execute() (string, error) {
	jp.computeDisplayKeys() // ensure display keys computed

	if err := jp.analyzeRightStore(); err != nil {
		return "", err
	}
	jp.checkRightSortOptimization()

	// ---------------------------------------------------------
	// PLANNER: Select Join Strategy
	// ---------------------------------------------------------
	jp.chooseStrategy()

	// Bloom Filter Pushdown (Optimization for StrategyLookup)
	if jp.strategy == StrategyLookup && jp.rightStore.Count() > 100 {
		if err := jp.buildBloomFilter(); err != nil {
			return "", err
		}
	}

	log.Info(fmt.Sprintf("Join Planner: Selected Strategy='%s' (CanLookup=%v, RightSortOpt=%v)", jp.strategy, jp.canUseLookup, jp.isRightSortOptimized))

	if jp.strategy == StrategyMerge {
		return jp.executeMergeJoin()
	}

	if jp.strategy == StrategyHashLeft {
		// Adaptive Hash Join: Buffer Left, Scan Right
		// This requires "Swapped" logic.
		return jp.executeAdaptiveHashJoin()
	}

	// Default/Fallback: Drive Left (Scan Left, Probe Right)
	// Supports: Lookup Join (Index Nested Loop) OR Hash Join (Hash Right)
	// Logic is consolidated in executeStandardJoin
	return jp.executeStandardJoin()
}

func (jp *JoinProcessor) chooseStrategy() {
	// 1. Check for Merge Join Suitability
	// Requirements:
	// - Both sides participating in Join via their Primary Keys (or sorted Indices).
	// - Both sides are sorted in the SAME direction (Asc/Asc or Desc/Desc).
	// - "canUseLookup" is true (implies Right is joinable by Key).
	// - Left is also joining on its Key? (Need to check).

	isLeftSortedByKey := false
	if len(jp.leftFields) == 1 && jp.leftFields[0] == "key" {
		isLeftSortedByKey = true
	}
	// TODO: Support complex Left keys or IndexSpec sorting on Left.

	if isLeftSortedByKey && jp.canUseLookup && jp.isRightSortOptimized {
		// Both sides sorted by join key.
		// We can use Merge Join (Galloping).
		jp.strategy = StrategyMerge
		return
	}

	// 2. If no Merge, check for Adaptive Hash Join (side swapping).
	// Only for Inner Joins (symmetric).
	// Only if Read-Only (no delete/update actions).
	// Only if Lookup is NOT available (i.e. we are doing a Scan+Probe).

	if !jp.canUseLookup && jp.joinType == "inner" && !jp.isDeleteLeft && !jp.isUpdateLeft {
		// We are forced to Scan + Probe.
		// Compare counts to decide which to buffer.
		lCount := jp.leftStore.Count()
		rCount := jp.rightStore.Count()

		if lCount < rCount {
			jp.strategy = StrategyHashLeft // Buffer Left (Small), Scan Right (Large)
			return
		}
	}

	// 3. Fallback
	if jp.canUseLookup {
		jp.strategy = StrategyLookup
	} else {
		jp.strategy = StrategyHashRight
	}
}

// executeStandardJoin implements the original Left Scan -> Right Probe logic
// supporting both Lookup (Index Join) and Hash-Right (Buffered Right)
func (jp *JoinProcessor) executeStandardJoin() (string, error) {
	if err := jp.buildHashCache(); err != nil {
		return "", err
	}

	// Iterate Left Store
	var lOk bool
	var err error
	if jp.isDesc {
		lOk, err = jp.leftStore.Last(jp.ctx)
	} else {
		lOk, err = jp.leftStore.First(jp.ctx)
	}

	if err != nil {
		return "", fmt.Errorf("failed to iterate left store: %w", err)
	}

	// Optimization: Determine if we can skip fetching Value (Deferred Fetch)
	// We check this on the first item.
	leftJoinOnKeyOnly := false
	checkedLeftKeyOpt := false

	for lOk {
		if jp.count >= int(jp.limit) {
			break
		}

		k := jp.leftStore.GetCurrentKey()

		// One-time check for key-only optimization
		if !checkedLeftKeyOpt {
			leftJoinOnKeyOnly = true
			for _, f := range jp.leftFields {
				if isK, _ := isKeyField(k.Key, f); !isK {
					leftJoinOnKeyOnly = false
					break
				}
			}
			checkedLeftKeyOpt = true
			if leftJoinOnKeyOnly {
				log.Debug("Join: Optimization Enabled - Left Store Join fields are in Key. Deferring Value fetch.")
			}
		}

		var v any
		if !leftJoinOnKeyOnly {
			v, err = jp.leftStore.GetCurrentValue(jp.ctx)
			if err != nil {
				return "", err
			}
		}

		// If optimized, we pass v=nil. processLeftItem must handle nil value if fields are in Key.
		// processLeftItem calls extractVal. extractVal handles nil value if field is found in Key.
		// However, processLeftItem might need Value for *projection*?
		// No, processLeftItem emits match. emitMatch extracts projection fields.
		// If we emit, we MUST have the Value available for projection/result.
		// processLeftItem logic:
		// 1. extract leftJoinVals (using k, v).
		// 2. probe/lookup.
		// 3. if match -> emitMatch(k, v, ...).

		// If we pass v=nil:
		// 1. extract works (since keys are in Key).
		// 2. probe works.
		// 3. emitMatch needs V if projection uses columns from V.

		// So we need to fetch V *inside* processLeftItem if a match occurs?
		// Or fetch V here if processLeftItem returns true (match emitted)?
		// processLeftItem returns (stop, error).
		// But emitMatch is called INSIDE processLeftItem.
		// So passing v=nil to processLeftItem is dangerous if emitMatch needs it.

		// Solution:
		// We can't easily change processLeftItem signature.
		// But processLeftItem performs the probe.
		// Optimization: Check probe locally here?
		// No, that duplicates logic.

		// Helper: "Peek" probe?
		// "Do we have a match?"
		// If Hash Join (leftJoinVals -> probe key -> cache lookup).
		// If Lookup Join (leftJoinVals -> lookup key -> BTree find).

		// Given the complexity of refactoring processLeftItem, we can only safely optimize
		// if we modify processLeftItem to accept a "fetchValue" callback or similar.
		// OR we inline the probe logic for the common Hash Case optimization (which is the critical one).

		// But wait, the user asked for "Bloom Filter Pushdown" / Optimization.
		// If I implement it only for Adaptive Hash Join (which I did), is that enough?
		// Standard Join (Left Scan, Right Probe) uses processLeftItem.
		// processLeftItem handles both Hash and Lookup.

		// Let's modify processLeftItem to support Lazy Value Fetching.
		// But it receives `v any`.
		// If I pass `nil`, it assumes Value is nil.
		// If I modify `processLeftItem` to take a `getValue func() (any, error)`, that works.

		// Refactoring `processLeftItem` is best.
		// args: (k any, vProvider func() (any, error))

		// But that affects call sites.
		// Call sites: executeStandardJoin.
		// That's the only call site! (Lines 1294).

		// Let's do this refactor.

		stop, err := jp.processLeftItem(k.Key, func() (any, error) {
			if v != nil || leftJoinOnKeyOnly == false {
				return v, nil
			}
			// Fetch on demand
			return jp.leftStore.GetCurrentValue(jp.ctx)
		})

		if err != nil {
			return "", err
		}
		if stop {
			break
		}

		if jp.isDesc {
			lOk, err = jp.leftStore.Previous(jp.ctx)
		} else {
			lOk, err = jp.leftStore.Next(jp.ctx)
		}
		if err != nil {
			return "", err
		}
	}

	return jp.emitter.Finalize(), nil
}

// executeMergeJoin implements the "Galloping" / Lockstep join.
// It assumes both stores are sorted by the join key.
func (jp *JoinProcessor) executeMergeJoin() (string, error) {
	// Initialize both cursors
	// Assuming Ascending for now unless isDesc is supported for Merge?
	// Merge Join logic is directional.
	// If isDesc, we should iterate Last/Previous on both?

	var lOk, rOk bool

	if jp.isDesc {
		lOk, _ = jp.leftStore.Last(jp.ctx)
		rOk, _ = jp.rightStore.Last(jp.ctx)
	} else {
		lOk, _ = jp.leftStore.First(jp.ctx)
		rOk, _ = jp.rightStore.First(jp.ctx)
	}

	for lOk { // Loop is driven by Left for Outer Join compatibility, but we optimize traversal.
		if jp.count >= int(jp.limit) {
			break
		}

		// Left Item
		lKey := jp.leftStore.GetCurrentKey()
		// Wait, extraction of Left Value.
		// If using Key-optimized fields, we can defer fetching lVal.
		var lVal any
		var lValFetched bool
		var err error

		getLVal := func() (any, error) {
			if lValFetched {
				return lVal, nil
			}
			lVal, err = jp.leftStore.GetCurrentValue(jp.ctx)
			lValFetched = true
			return lVal, err
		}

		// Extract Left Join Key (we know it's the "key" / ID because of chooseStrategy)
		// We try extracting from Key first (passing nil).
		lJoinVal := extractVal(lKey.Key, nil, jp.leftFields[0])
		if lJoinVal == nil {
			// Must fetch value
			_, err = getLVal()
			if err != nil {
				return "", err
			}
			lJoinVal = extractVal(lKey.Key, lVal, jp.leftFields[0])
		}

		var rKeyStruct btree.Item[any, any]
		var matchFound bool

		if rOk {
			rKeyStruct = jp.rightStore.GetCurrentKey()
			rJoinVal := extractVal(rKeyStruct.Key, nil, jp.rightKeyFields[0])

			// Compare
			cmp := btree.Compare(lJoinVal, rJoinVal)

			if cmp == 0 {
				matchFound = true
			} else {
				// Mismatch.
				// Logic depends on Direction (Asc vs Desc).

				// Case ASC: l=5, r=3. l > r. r is behind.
				// Case ASC: l=3, r=5. l < r. r is ahead (l is behind).

				// Case DESC: l=5, r=3. l > r. r is ahead (l is behind - assuming we are going down).
				// Case DESC: l=3, r=5. l < r. r is behind.

				behind := false
				if !jp.isDesc {
					if cmp > 0 {
						behind = true
					} // l > r (5 > 3)
				} else {
					if cmp < 0 {
						behind = true
					} // l < r (3 < 5)
				}

				if behind {
					// Right is behind Left. Advance Right.
					// Galloping Logic: Next vs Find.

					advanceOk := false
					if jp.isDesc {
						advanceOk, _ = jp.rightStore.Previous(jp.ctx)
					} else {
						advanceOk, _ = jp.rightStore.Next(jp.ctx)
					}

					if advanceOk {
						rKeyStruct = jp.rightStore.GetCurrentKey()
						rJoinVal = extractVal(rKeyStruct.Key, nil, jp.rightKeyFields[0])
						cmp = btree.Compare(lJoinVal, rJoinVal)

						if cmp == 0 {
							matchFound = true
						} else {
							// Check if we are STILL behind (Gap)
							stillBehind := false
							if !jp.isDesc {
								if cmp > 0 {
									stillBehind = true
								}
							} else {
								if cmp < 0 {
									stillBehind = true
								}
							}

							if stillBehind {
								// Jump!
								lookupKey := lJoinVal
								if jp.rightSampleKey != nil {
									lookupKey = coerce(lookupKey, jp.rightSampleKey)
								}
								// Use Find to skip gap.
								// Find moves cursor to >= Key (Asc/Desc doesn't change B-Tree structure).
								// Logic works for both directions as verified.
								found, _ := jp.rightStore.Find(jp.ctx, lookupKey, false)
								rOk = found || jp.rightStore.GetCurrentKey().Key != nil

								if rOk {
									rKeyStruct = jp.rightStore.GetCurrentKey()
									rJoinVal = extractVal(rKeyStruct.Key, nil, jp.rightKeyFields[0])
									if btree.Compare(lJoinVal, rJoinVal) == 0 {
										matchFound = true
									}
									// If Find lands us ahead of Left...
									// e.g. Left=10. Right jumps to 11.
									// That's fine, we missed 10. MatchFound=false.
								}
							}
						}
					} else {
						rOk = false
					}
				} else {
					// Right is ahead of Left. Left needs to catch up.
					matchFound = false
				}
			}
		}

		if matchFound {
			// Now we need lVal for emitting
			_, err = getLVal()
			if err != nil {
				return "", err
			}

			rVal, _ := jp.rightStore.GetCurrentValue(jp.ctx)
			stop, err := jp.emitMatch(lKey.Key, lVal, rKeyStruct.Key, rVal)
			if err != nil {
				return "", err
			}
			if stop {
				break
			}
		} else {
			if jp.joinType == "left" {
				_, err = getLVal()
				if err != nil {
					return "", err
				}

				jp.emitMatch(lKey.Key, lVal, nil, nil)
				if jp.count >= int(jp.limit) {
					break
				}
			}
		}

		if jp.isDesc {
			lOk, _ = jp.leftStore.Previous(jp.ctx)
		} else {
			lOk, _ = jp.leftStore.Next(jp.ctx)
		}
	}

	return jp.emitter.Finalize(), nil
}

// executeAdaptiveHashJoin implements "Swap Sides" optimization.
func (jp *JoinProcessor) executeAdaptiveHashJoin() (string, error) {
	log.Info("Join: Executing Adaptive Hash Join (Swapped). Buffering Left, Scanning Right.")

	// 1. Buffer Left Store
	leftCache := make(map[string][]cachedItem)
	lOk, _ := jp.leftStore.First(jp.ctx)
	for lOk {
		k := jp.leftStore.GetCurrentKey()
		v, _ := jp.leftStore.GetCurrentValue(jp.ctx)
		joinKey := generateJoinKey(k.Key, v, jp.leftFields)
		leftCache[joinKey] = append(leftCache[joinKey], cachedItem{Key: k.Key, Value: v})
		lOk, _ = jp.leftStore.Next(jp.ctx)
	}

	// 2. Scan Right Store (Probe)
	var rOk bool
	// We must respect the requested sort direction (isDesc).
	// Since we are driving with the Right Store in this strategy, the output order depends on this scan.
	if jp.isDesc {
		rOk, _ = jp.rightStore.Last(jp.ctx)
	} else {
		rOk, _ = jp.rightStore.First(jp.ctx)
	}

	for rOk {
		if jp.count >= int(jp.limit) {
			break
		}

		rKey := jp.rightStore.GetCurrentKey()
		allKeyFields := true
		for _, f := range jp.rightFields {
			isK, _ := isKeyField(rKey.Key, f)
			if !isK {
				allKeyFields = false
				break
			}
		}

		var rVal any
		var err error

		if allKeyFields {
			probeKey := generateJoinKey(rKey.Key, nil, jp.rightFields)
			if _, exists := leftCache[probeKey]; !exists {
				if jp.isDesc {
					rOk, _ = jp.rightStore.Previous(jp.ctx)
				} else {
					rOk, _ = jp.rightStore.Next(jp.ctx)
				}
				continue
			}
			rVal, err = jp.rightStore.GetCurrentValue(jp.ctx)
			if err != nil {
				return "", err
			}
		} else {
			rVal, err = jp.rightStore.GetCurrentValue(jp.ctx)
			if err != nil {
				return "", err
			}
		}

		probeKey := generateJoinKey(rKey.Key, rVal, jp.rightFields)
		if matches, ok := leftCache[probeKey]; ok {
			for _, match := range matches {
				stop, err := jp.emitMatch(match.Key, match.Value, rKey.Key, rVal)
				if err != nil {
					return "", err
				}
				if stop {
					break
				}
			}
		}
		if jp.isDesc {
			rOk, _ = jp.rightStore.Previous(jp.ctx)
		} else {
			rOk, _ = jp.rightStore.Next(jp.ctx)
		}
	}

	return jp.emitter.Finalize(), nil
}
