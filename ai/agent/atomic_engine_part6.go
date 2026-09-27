package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	log "log/slog"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

func (e *ScriptEngine) stageJoin(left, right []any, joinType string, on map[string]any, rightAlias string, leftAlias string) ([]any, error) {
	var results []any
	if rightAlias == "" {
		rightAlias = "Right"
	}
	if leftAlias == "" {
		leftAlias = "Left"
	}

	matchedRight := make(map[int]bool)

	for _, lItem := range left {
		matched := false
		for rIdx, rItem := range right {
			match := true
			for lField, rFieldRaw := range on {
				rField := fmt.Sprintf("%v", rFieldRaw)
				lVal := getField(lItem, lField)
				rVal := getField(rItem, rField)
				if fmt.Sprintf("%v", lVal) != fmt.Sprintf("%v", rVal) {
					match = false
					break
				}
			}

			if match {
				matched = true
				if joinType == "full" {
					matchedRight[rIdx] = true
				}
				merged := make(map[string]any)

				if m, ok := lItem.(map[string]any); ok {
					for k, v := range m {
						merged[leftAlias+"."+k] = v
					}
				} else if om, ok := lItem.(*OrderedMap); ok && om != nil {
					for k, v := range om.m {
						merged[leftAlias+"."+k] = v
					}
				} else if om, ok := lItem.(OrderedMap); ok {
					for k, v := range om.m {
						merged[leftAlias+"."+k] = v
					}
				}

				if m, ok := rItem.(map[string]any); ok {
					for k, v := range m {
						merged[rightAlias+"."+k] = v
					}
				} else if om, ok := rItem.(*OrderedMap); ok && om != nil {
					for k, v := range om.m {
						merged[rightAlias+"."+k] = v
					}
				} else if om, ok := rItem.(OrderedMap); ok {
					for k, v := range om.m {
						merged[rightAlias+"."+k] = v
					}
				}

				results = append(results, merged)
			}
		}
		if !matched && (joinType == "left" || joinType == "full") {
			results = append(results, lItem)
		}
	}

	if joinType == "full" {
		for i, rItem := range right {
			if !matchedRight[i] {

				merged := make(map[string]any)
				if m, ok := rItem.(map[string]any); ok {
					for k, v := range m {

						merged[k] = v
					}
				}
				results = append(results, merged)
			}
		}
	}

	return results, nil
}

// JoinRight is a pipeline-friendly alias for Join.
// It expects the input to be the Left stream, and the 'store' argument to be the Right store.
func (e *ScriptEngine) JoinRight(ctx context.Context, input any, args map[string]any) (any, error) {

	if store, ok := args["store"]; ok {
		args["with"] = store

	}

	return e.Join(ctx, input, args)
}

// handleInto handles the 'into' argument for JoinResult.
func (e *ScriptEngine) Update(ctx context.Context, input any, args map[string]any) ([]any, error) {
	var list []any
	if cursor, ok := input.(ScriptCursor); ok {
		for {
			item, ok, err := cursor.Next(ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			list = append(list, item)
		}
	} else if l, ok := input.([]map[string]any); ok {
		for _, x := range l {
			list = append(list, x)
		}
	} else if l, ok := input.([]any); ok {
		list = l
	} else {
		return nil, fmt.Errorf("input must be a list of items or a cursor")
	}

	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}

	return e.stageUpdate(ctx, list, store, args)
}

func (e *ScriptEngine) Delete(ctx context.Context, input any, args map[string]any) ([]any, error) {
	var list []any
	if cursor, ok := input.(ScriptCursor); ok {
		for {
			item, ok, err := cursor.Next(ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			list = append(list, item)
		}
	} else if l, ok := input.([]map[string]any); ok {
		for _, x := range l {
			list = append(list, x)
		}
	} else if l, ok := input.([]any); ok {
		list = l
	} else {
		return nil, fmt.Errorf("input must be a list of items or a cursor")
	}

	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}

	return e.stageDelete(ctx, list, store, args)
}

func (e *ScriptEngine) Inspect(ctx context.Context, args map[string]any) (any, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}

	si := store.GetStoreInfo()

	// Auto-Infer Schema from first record
	var schema map[string]string
	if ok, _ := store.First(ctx); ok {
		k := store.GetCurrentKey()
		v, _ := store.GetCurrentValue(ctx)
		flat := flattenItem(k, v)
		schema = inferSchema(flat)
	}

	return map[string]any{
		"name":                          si.Name,
		"count":                         si.Count,
		"slot_length":                   si.SlotLength,
		"is_unique":                     si.IsUnique,
		"is_value_data_in_node_segment": si.IsValueDataInNodeSegment,
		"leaf_load_balancing":           si.LeafLoadBalancing,
		"description":                   si.Description,
		"schema":                        schema,
	}, nil
}

func (e *ScriptEngine) stageFilter(input []any, args map[string]any) ([]any, error) {
	conditionRaw := args["condition"]
	if conditionRaw == nil {
		return input, nil
	}

	output := make([]any, 0)

	if _, ok := conditionRaw.(string); ok {
		for _, item := range input {
			match, err := e.evaluateCondition(item, conditionRaw)
			if err == nil && match {
				output = append(output, item)
			}
		}
		return output, nil
	}

	if matchMap, ok := conditionRaw.(map[string]any); ok {
		for _, item := range input {
			match, err := e.evaluateCondition(item, matchMap)
			if err != nil {
				return nil, err
			}
			if match {
				output = append(output, item)
			}
		}
		return output, nil
	}

	return input, fmt.Errorf("unsupported filter condition type")
}

func (e *ScriptEngine) stageSort(input []any, args map[string]any) ([]any, error) {
	fieldsRaw, ok := args["fields"].([]any)
	if !ok {
		// Fallback: compact form {field, direction} emitted by some LLM responses.
		if f, _ := args["field"].(string); strings.TrimSpace(f) != "" {
			dir, _ := args["direction"].(string)
			dir = strings.ToLower(strings.TrimSpace(dir))
			if dir == "descending" {
				dir = "desc"
			} else if dir != "desc" {
				dir = "asc"
			}
			fieldsRaw = []any{strings.TrimSpace(f) + " " + dir}
		} else {
			return input, nil
		}
	}
	var fields []string
	for _, f := range fieldsRaw {
		if s, ok := f.(string); ok {
			fields = append(fields, s)
		}
	}

	if len(fields) == 0 {
		return input, nil
	}

	sort.Slice(input, func(i, j int) bool {
		for _, field := range fields {
			parts := strings.Fields(field)
			fieldName := parts[0]
			desc := len(parts) > 1 && strings.ToLower(parts[1]) == "desc"

			valA := getField(input[i], fieldName)
			valB := getField(input[j], fieldName)

			cmp := compare(valA, valB)
			if cmp != 0 {
				if desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})

	return input, nil
}

func (e *ScriptEngine) stageProject(input []any, args map[string]any) ([]any, error) {

	fields := parseProjectionFields(args["fields"])

	log.Debug("stageProject called", "input_len", len(input), "input_fields", fields)

	var output []any
	for i, item := range input {
		newItem := renderItem(nil, item, fields)
		output = append(output, newItem)

		if i == 0 {
			if om, ok := newItem.(*OrderedMap); ok {
				log.Debug("stageProject first item (OrderedMap)", "keys", om.keys)
			} else if om, ok := newItem.(OrderedMap); ok {
				log.Debug("stageProject first item (OrderedMap Value)", "keys", om.keys)
			} else {
				log.Debug("stageProject first item (Map/Other)", "type", fmt.Sprintf("%T", newItem))
			}
		}
	}

	return output, nil
}

func (e *ScriptEngine) stageLimit(input []any, args map[string]any) ([]any, error) {
	var limit int
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	} else if l, ok := args["count"].(float64); ok {
		limit = int(l)
	} else {

		limit = 1000
	}

	log.Debug("stageLimit called", "input_len", len(input), "limit", limit)

	if limit < len(input) {
		return input[:limit], nil
	}
	return input, nil
}

func (e *ScriptEngine) stageUpdate(ctx context.Context, input []any, store jsondb.StoreAccessor, args map[string]any) ([]any, error) {
	if store == nil {
		return nil, fmt.Errorf("update stage requires an active store from a previous scan")
	}
	values, _ := args["values"].(map[string]any)
	if len(values) == 0 {
		return input, nil
	}

	for _, itemObj := range input {
		var item map[string]any
		if m, ok := itemObj.(map[string]any); ok {
			item = m
		} else if om, ok := itemObj.(*OrderedMap); ok && om != nil {
			item = om.m
		} else if om, ok := itemObj.(OrderedMap); ok {
			item = om.m
		} else {
			continue
		}

		key := item["key"]
		if key == nil {

			if info := store.GetStoreInfo(); info.Name != "" {
				key = item[info.Name+".key"]
			}
		}

		currentVal := item["value"]
		var newVal any
		if currMap, ok := currentVal.(map[string]any); ok {

			newMap := make(map[string]any)
			for k, v := range currMap {
				newMap[k] = v
			}
			for k, v := range values {
				newMap[k] = v
			}
			newVal = newMap
		} else {
			newVal = values
		}

		if _, err := store.Update(ctx, key, newVal); err != nil {
			return nil, err
		}

		item["value"] = newVal
	}
	return input, nil
}

func (e *ScriptEngine) stageDelete(ctx context.Context, input []any, store jsondb.StoreAccessor, args map[string]any) ([]any, error) {
	if store == nil {
		return nil, fmt.Errorf("delete stage requires an active store from a previous scan")
	}

	for _, itemObj := range input {
		key := getField(itemObj, "key")
		if key == nil {

			if info := store.GetStoreInfo(); info.Name != "" {
				key = getField(itemObj, info.Name+".key")
			}
		}

		if _, err := store.Remove(ctx, key); err != nil {
			return nil, err
		}
	}
	return []any{}, nil
}

func (e *ScriptEngine) If(ctx context.Context, args map[string]any) error {
	conditionRaw, ok := args["condition"]
	if !ok {
		return fmt.Errorf("if operation requires 'condition'")
	}

	// Evaluate Condition
	var result bool
	if _, ok := conditionRaw.(string); ok {
		return fmt.Errorf("CEL if conditions not supported yet")
	} else if condBool, ok := conditionRaw.(bool); ok {
		result = condBool
	}

	if result {
		if thenBlock, ok := args["then"].([]any); ok {
			// Convert []any to []ScriptInstruction
			var thenScript []ScriptInstruction
			bytes, _ := json.Marshal(thenBlock)
			if err := json.Unmarshal(bytes, &thenScript); err != nil {
				return fmt.Errorf("failed to parse 'then' block: %v", err)
			}
			return e.Execute(ctx, thenScript)
		}
	} else {
		if elseBlock, ok := args["else"].([]any); ok {
			var elseScript []ScriptInstruction
			bytes, _ := json.Marshal(elseBlock)
			if err := json.Unmarshal(bytes, &elseScript); err != nil {
				return fmt.Errorf("failed to parse 'else' block: %v", err)
			}
			return e.Execute(ctx, elseScript)
		}
	}

	return nil
}

func (e *ScriptEngine) Loop(ctx context.Context, args map[string]any) error {

	collectionVar, _ := args["collection"].(string)
	itemVar, _ := args["item_var"].(string)
	if itemVar == "" {
		itemVar = "item"
	}

	bodyBlock, ok := args["body"].([]any)
	if !ok {
		return fmt.Errorf("loop operation requires 'body' block")
	}

	var bodyScript []ScriptInstruction
	bytes, _ := json.Marshal(bodyBlock)
	if err := json.Unmarshal(bytes, &bodyScript); err != nil {
		return fmt.Errorf("failed to parse loop 'body': %v", err)
	}

	collection, ok := e.Context.Variables[collectionVar]
	if !ok {
		return fmt.Errorf("collection variable '%s' not found", collectionVar)
	}

	if list, ok := collection.([]map[string]any); ok {
		for _, item := range list {
			e.Context.Variables[itemVar] = item
			if err := e.Execute(ctx, bodyScript); err != nil {
				return err
			}
		}
	} else if list, ok := collection.([]any); ok {
		for _, item := range list {
			e.Context.Variables[itemVar] = item
			if err := e.Execute(ctx, bodyScript); err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("variable '%s' is not a list", collectionVar)
	}

	return nil
}

func normalizeCallFunctionName(args map[string]any) string {
	for _, key := range []string{"name", "function", "function_name", "functionName", "tool", "tool_name", "toolName", "command"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeCallFunctionParams(args map[string]any) map[string]any {
	for _, key := range []string{"params", "arguments", "args", "input"} {
		if value, ok := args[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func (e *ScriptEngine) CallFunction(ctx context.Context, args map[string]any) (any, error) {
	functionName := normalizeCallFunctionName(args)
	if functionName == "" {
		return nil, fmt.Errorf("function name required")
	}

	if e.FunctionHandler == nil {
		return nil, fmt.Errorf("function handler not configured")
	}

	savedVars := make(map[string]any)
	params := normalizeCallFunctionParams(args)
	for k, v := range params {
		if oldVal, exists := e.Context.Variables[k]; exists {
			savedVars[k] = oldVal
		}
		e.Context.Variables[k] = v
	}

	// Call Handler
	res, err := e.FunctionHandler(ctx, functionName, params)

	for k, v := range savedVars {
		e.Context.Variables[k] = v
	}

	return res, err
}

func (e *ScriptEngine) First(ctx context.Context, args map[string]any) (bool, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return false, fmt.Errorf("store variable '%s' not found", storeName)
	}
	return store.First(ctx)
}

func (e *ScriptEngine) Last(ctx context.Context, args map[string]any) (bool, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return false, fmt.Errorf("store variable '%s' not found", storeName)
	}
	return store.Last(ctx)
}

func (e *ScriptEngine) Next(ctx context.Context, args map[string]any) (bool, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return false, fmt.Errorf("store variable '%s' not found", storeName)
	}
	return store.Next(ctx)
}

func (e *ScriptEngine) Previous(ctx context.Context, args map[string]any) (bool, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return false, fmt.Errorf("store variable '%s' not found", storeName)
	}
	return store.Previous(ctx)
}

func (e *ScriptEngine) Find(ctx context.Context, args map[string]any) (bool, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return false, fmt.Errorf("store variable '%s' not found", storeName)
	}
	key := args["key"]
	desc, _ := args["desc"].(bool)

	if desc {
		return store.FindInDescendingOrder(ctx, key)
	}
	return store.FindOne(ctx, key, true)
}

func (e *ScriptEngine) Add(ctx context.Context, args map[string]any) (any, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}
	key := args["key"]
	value := args["value"]
	return store.Add(ctx, key, value)
}

func (e *ScriptEngine) GetCurrentKey(ctx context.Context, args map[string]any) (any, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}
	k := store.GetCurrentKey()
	return k, nil
}

func (e *ScriptEngine) GetCurrentValue(ctx context.Context, args map[string]any) (any, error) {
	storeName, _ := args["store"].(string)
	store, ok := e.getStore(storeName)
	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeName)
	}
	v, err := store.GetCurrentValue(ctx)
	return v, err
}

func (e *ScriptEngine) ListAppend(ctx context.Context, input any, args map[string]any) (any, error) {
	listVar, _ := args["list"].(string)
	item := args["item"]
	if item == nil {
		item = args["value"]
	}

	var list any

	if listVar != "" {
		var ok bool
		list, ok = e.Context.Variables[listVar]
		if !ok {
			return nil, fmt.Errorf("list variable '%s' not found", listVar)
		}
	} else {

		if input == nil {
			return nil, fmt.Errorf("list variable not specified and no input provided")
		}
		list = input
	}

	var resultList []any

	if l, ok := list.([]any); ok {

		resultList = append(l, item)
	} else if l, ok := list.([]map[string]any); ok {

		resultList = make([]any, len(l)+1)
		for i, v := range l {
			resultList[i] = v
		}
		resultList[len(l)] = item
	} else {
		return nil, fmt.Errorf("variable (or input) is not a list")
	}

	if listVar != "" {
		e.Context.Variables[listVar] = resultList
	}

	return resultList, nil
}

func (e *ScriptEngine) MapMerge(ctx context.Context, args map[string]any) (map[string]any, error) {
	map1Raw := args["map1"]
	map2Raw := args["map2"]

	result := make(map[string]any)

	if m1, ok := map1Raw.(map[string]any); ok {
		for k, v := range m1 {
			result[k] = v
		}
	}
	if m2, ok := map2Raw.(map[string]any); ok {
		for k, v := range m2 {
			result[k] = v
		}
	}
	return result, nil
}

func (a *CopilotAgent) opCallScript(ctx context.Context, scriptCtx *ScriptContext, args map[string]any) (any, error) {
	scriptName, _ := args["name"].(string)
	if scriptName == "" {
		return nil, fmt.Errorf("script name required")
	}

	if a.systemDB == nil {
		return nil, fmt.Errorf("system database not available")
	}

	sysTx, err := a.systemDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, fmt.Errorf("failed to begin system tx: %v", err)
	}
	defer sysTx.Rollback(ctx)

	funcStore, err := a.systemDB.OpenModelStore(ctx, "scripts", sysTx)
	if err != nil {
		return nil, fmt.Errorf("failed to open scripts store: %v", err)
	}

	category, _ := args["category"].(string)
	if category == "" {
		category = ai.DefaultScriptCategory
	}

	var funcMap map[string]any
	if err := funcStore.Load(ctx, category, scriptName, &funcMap); err != nil {
		return nil, fmt.Errorf("script '%s/%s' not found: %v", category, scriptName, err)
	}

	stepsRaw, ok := funcMap["steps"].([]any)
	if !ok {
		return nil, nil
	}

	isAtomic := false
	if len(stepsRaw) > 0 {
		if firstStep, ok := stepsRaw[0].(map[string]any); ok {
			_, hasOp := firstStep["op"]
			_, hasType := firstStep["type"]

			if hasOp && !hasType {
				isAtomic = true
			}
		}
	}

	if isAtomic {

		bytes, err := json.Marshal(stepsRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal script steps: %v", err)
		}

		sum := sha256.Sum256(bytes)
		currentHash := hex.EncodeToString(sum[:])

		cachedEntry, cached := func() (CachedScript, bool) {
			a.compiledScriptsMu.RLock()
			defer a.compiledScriptsMu.RUnlock()
			entry, ok := a.compiledScripts[scriptName]
			return entry, ok
		}()

		var compiled CompiledScript

		if !cached || cachedEntry.Hash != currentHash {
			var script []ScriptInstruction
			if err := json.Unmarshal(bytes, &script); err != nil {
				return nil, fmt.Errorf("failed to parse script: %v", err)
			}

			compiled, err = CompileScript(script)
			if err != nil {
				return nil, fmt.Errorf("failed to compile script '%s': %v", scriptName, err)
			}

			func() {
				a.compiledScriptsMu.Lock()
				defer a.compiledScriptsMu.Unlock()
				a.compiledScripts[scriptName] = CachedScript{Script: compiled, Hash: currentHash}
			}()
		} else {
			compiled = cachedEntry.Script
		}

		resolver := func(name string) (Database, error) {
			if name == "" {
				if p := ai.GetSessionPayload(ctx); p != nil && p.CurrentDB != "" {
					name = p.CurrentDB
				}
			}
			return a.resolveDatabase(name)
		}
		engine := NewScriptEngine(scriptCtx, resolver)
		engine.FunctionHandler = func(c context.Context, name string, args map[string]any) (any, error) {
			return a.opCallScript(c, engine.Context, map[string]any{"name": name, "params": args})
		}

		if err := compiled(ctx, engine); err != nil {
			return nil, err
		}

		if val, ok := engine.Context.Variables["output"]; ok {
			return val, nil
		}

		if engine.HasReturned {
			return engine.ReturnValue, nil
		}

		return engine.LastResult, nil
	}

	for _, step := range stepsRaw {
		stepMap, _ := step.(map[string]any)
		toolName, _ := stepMap["tool"].(string)
		toolArgs, _ := stepMap["args"].(map[string]any)

		if a.registry == nil {
			return nil, fmt.Errorf("registry not initialized")
		}

		toolDef, ok := a.registry.Get(toolName)
		if !ok {
			return nil, fmt.Errorf("tool '%s' not found", toolName)
		}
		_, err := toolDef.Handler(ctx, toolArgs)
		if err != nil {
			return nil, fmt.Errorf("script step '%s' failed: %v", toolName, err)
		}
	}

	return nil, nil
}
