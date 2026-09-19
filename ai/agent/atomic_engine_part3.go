package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/jsondb"
)

func normalizeScriptStepForCompatibility(step map[string]any) {
	normalizeScriptStepForCompatibilityWithQueryAndState(step, "", nil)
}

func normalizeScriptStepForCompatibilityWithQuery(step map[string]any, currentQuery string) {
	normalizeScriptStepForCompatibilityWithQueryAndState(step, currentQuery, nil)
}

func normalizeScriptStepForCompatibilityWithQueryAndState(step map[string]any, currentQuery string, state *scriptCompatibilityNormalizerState) {
	op, _ := step["op"].(string)
	if op == "" {
		if cmd, ok := step["command"].(string); ok {
			op = cmd
		}
	}

	argsObj, hasArgs := step["args"].(map[string]any)
	if !hasArgs {
		argsObj = make(map[string]any)
		step["args"] = argsObj
	}

	switch {
	case strings.EqualFold(op, "sort"):
		normalizeCompatibilitySortStep(step, argsObj)
	case strings.EqualFold(op, "filter"), strings.EqualFold(op, "select"):
		if condition, ok := argsObj["condition"].(map[string]any); ok {
			var aliases []string
			var storeFields map[string]map[string]struct{}
			if state != nil {
				aliases = state.aliasesForStep(step)
				storeFields = state.storeFields
			}
			argsObj["condition"] = normalizeCompatibilityConditionMapWithQueryAndAliases(condition, currentQuery, aliases, storeFields)
		}
	case strings.EqualFold(op, "join"), strings.EqualFold(op, "join_right"):
		if onMap, ok := argsObj["on"].(map[string]any); ok {
			argsObj["on"] = normalizeCompatibilityJoinOn(onMap)
		}
	}
}

func currentQueryForScriptGrounding(payload *ai.SessionPayload) string {
	if payload == nil {
		return ""
	}

	candidates := make([]string, 0, 4)
	appendUnique := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	if payload.ClarificationState != nil {
		appendUnique(payload.ClarificationState.TargetQuery)
		appendUnique(payload.ClarificationState.EffectiveResumeAsk)
	}
	if payload.RetryRewriteState != nil {
		appendUnique(payload.RetryRewriteState.ResolvedQuery)
	}
	appendUnique(payload.CurrentUserQuery)

	return strings.Join(candidates, "\n")
}

func normalizeCompatibilitySortStep(step map[string]any, argsObj map[string]any) {
	if step == nil || argsObj == nil {
		return
	}

	if inputVar, _ := step["input_var"].(string); inputVar == "" {
		if pipe, ok := argsObj["pipe"].(string); ok && strings.TrimSpace(pipe) != "" {
			step["input_var"] = strings.TrimSpace(pipe)
			delete(argsObj, "pipe")
		}
	}

	if _, hasFields := argsObj["fields"]; hasFields {
		return
	}

	key, _ := argsObj["key"].(string)
	if strings.TrimSpace(key) == "" {
		key, _ = argsObj["field"].(string)
	}
	if strings.TrimSpace(key) == "" {
		return
	}
	field := strings.TrimSpace(key)
	desc, _ := argsObj["desc"].(bool)
	if !desc {
		desc, _ = argsObj["descending"].(bool)
	}
	if !desc {
		if direction, _ := argsObj["direction"].(string); strings.TrimSpace(direction) != "" {
			switch strings.ToLower(strings.TrimSpace(direction)) {
			case "desc", "descending":
				desc = true
			}
		}
	}
	if desc {
		field += " desc"
	}
	argsObj["fields"] = []any{field}
	delete(argsObj, "key")
	delete(argsObj, "field")
	delete(argsObj, "desc")
	delete(argsObj, "descending")
	delete(argsObj, "direction")
}

func normalizeCompatibilityConditionMapWithQueryAndAliases(condition map[string]any, currentQuery string, aliases []string, storeFields map[string]map[string]struct{}) map[string]any {
	normalized := make(map[string]any, len(condition))
	for field, raw := range condition {
		field = qualifyCompatibilityConditionField(normalizeCompatibilityFieldPathWithAliases(field, aliases), aliases, storeFields)
		if strings.HasPrefix(strings.TrimSpace(field), "$") {
			addNormalizedCompatibilityConditionEntry(normalized, field, raw, aliases)
			continue
		}

		if nested, ok := raw.(map[string]any); ok {
			if newField, newValue, handled := normalizeMalformedCompatibilityPredicate(field, nested, currentQuery, aliases, storeFields); handled {
				if newField != "" {
					addNormalizedCompatibilityConditionEntry(normalized, newField, newValue, aliases)
				}
				continue
			}
			addNormalizedCompatibilityConditionEntry(normalized, field, normalizeCompatibilityConditionMapWithQueryAndAliases(nested, currentQuery, aliases, storeFields), aliases)
			continue
		}

		if raw == nil && shouldDropCompatibilityPlaceholderField(field, aliases, storeFields) {
			continue
		}

		if placeholder, ok := raw.(bool); ok && placeholder && !isLikelyBooleanFieldName(field) {
			if inferred, changed := inferPredicateFromCurrentQuery(field, currentQuery); changed {
				addNormalizedCompatibilityConditionEntry(normalized, field, inferred, aliases)
				continue
			}
			if inferredField, inferredValue, changed := inferAliasPredicateFromCurrentQuery(field, currentQuery); changed {
				addNormalizedCompatibilityConditionEntry(normalized, inferredField, inferredValue, aliases)
				continue
			}
		}

		if rawStr, ok := raw.(string); ok {
			newField, newValue, changed := normalizeCompatibilityConditionEntry(field, rawStr)
			if changed {
				addNormalizedCompatibilityConditionEntry(normalized, newField, newValue, aliases)
				continue
			}

			if inferredField, inferredValue, changed := normalizeCompatibilityAliasConditionEntry(field, rawStr, currentQuery); changed {
				addNormalizedCompatibilityConditionEntry(normalized, inferredField, inferredValue, aliases)
				continue
			}

			if !isAliasPlaceholderField(field) {
				addNormalizedCompatibilityConditionEntry(normalized, field, map[string]any{"$eq": parseCompatibilityLiteral(rawStr)}, aliases)
				continue
			}
		}

		addNormalizedCompatibilityConditionEntry(normalized, field, raw, aliases)
	}
	return normalized
}

func addNormalizedCompatibilityConditionEntry(normalized map[string]any, field string, value any, aliases []string) {
	if normalized == nil || strings.TrimSpace(field) == "" {
		return
	}
	if len(aliases) == 1 {
		alias := strings.TrimSpace(aliases[0])
		if strings.Contains(field, ".") {
			parts := strings.SplitN(field, ".", 2)
			if strings.EqualFold(strings.TrimSpace(parts[0]), alias) {
				leaf := strings.TrimSpace(parts[1])
				if _, exists := normalized[leaf]; exists {
					return
				}
			}
		} else {
			delete(normalized, alias+"."+field)
		}
	}
	normalized[field] = value
}

func normalizeMalformedCompatibilityPredicate(field string, raw map[string]any, currentQuery string, aliases []string, storeFields map[string]map[string]struct{}) (string, any, bool) {
	if len(raw) == 0 {
		if inferred, ok := inferPredicateFromCurrentQuery(field, currentQuery); ok {
			return field, inferred, true
		}
		return "", nil, true
	}
	if containsPredicateOperator(raw) {
		return field, nil, false
	}
	if inferred, ok := inferPredicateFromCurrentQuery(field, currentQuery); ok {
		return field, inferred, true
	}
	if value, ok := extractCompatibilityPredicateValue(raw); ok {
		return field, map[string]any{"$eq": value}, true
	}
	if shouldDropCompatibilityPlaceholderField(field, aliases, storeFields) {
		return "", nil, true
	}
	return field, nil, false
}

func containsPredicateOperator(raw map[string]any) bool {
	for key := range raw {
		if strings.HasPrefix(strings.TrimSpace(key), "$") {
			return true
		}
	}
	return false
}

func extractCompatibilityPredicateValue(raw map[string]any) (any, bool) {
	if value, ok := raw["value"]; ok && value != nil {
		return value, true
	}
	for _, value := range raw {
		if value == nil {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			if nestedValue, ok := extractCompatibilityPredicateValue(nested); ok {
				return nestedValue, true
			}
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed, true
			}
		default:
			return typed, true
		}
	}
	return nil, false
}

func shouldDropCompatibilityPlaceholderField(field string, aliases []string, storeFields map[string]map[string]struct{}) bool {
	field = normalizeCompatibilityFieldPath(field)
	if field == "" || strings.HasPrefix(field, "$") {
		return true
	}
	if isRecognizedCompatibilityField(field, aliases, storeFields) {
		return false
	}
	lower := strings.ToLower(field)
	if strings.Contains(lower, "_match") || strings.Contains(lower, "_value") || strings.HasSuffix(lower, "_store") {
		return true
	}
	return len(aliases) > 0 || len(storeFields) > 0
}

func isRecognizedCompatibilityField(field string, aliases []string, storeFields map[string]map[string]struct{}) bool {
	if field == "" {
		return false
	}
	if strings.Contains(field, ".") {
		parts := strings.SplitN(field, ".", 2)
		fields := storeFields[strings.ToLower(strings.TrimSpace(parts[0]))]
		if len(fields) == 0 {
			return false
		}
		_, ok := fields[strings.ToLower(strings.TrimSpace(parts[1]))]
		return ok
	}
	for _, alias := range aliases {
		fields := storeFields[strings.ToLower(strings.TrimSpace(alias))]
		if len(fields) == 0 {
			continue
		}
		if _, ok := fields[strings.ToLower(field)]; ok {
			return true
		}
	}
	return false
}

func normalizeCompatibilityFieldPathWithAliases(field string, aliases []string) string {
	field = normalizeCompatibilityFieldPath(field)
	if field == "" || strings.Contains(field, ".") {
		return field
	}
	lower := strings.ToLower(field)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		prefix := strings.ToLower(alias) + "_"
		if strings.HasPrefix(lower, prefix) && len(field) > len(prefix) {
			return alias + "." + field[len(prefix):]
		}
	}
	return field
}

func qualifyCompatibilityConditionField(field string, aliases []string, storeFields map[string]map[string]struct{}) string {
	field = normalizeCompatibilityFieldPath(field)
	if field == "" || strings.HasPrefix(field, "$") || strings.Contains(field, ".") {
		return field
	}
	if len(aliases) <= 1 || len(storeFields) == 0 {
		return field
	}
	for _, alias := range aliases {
		fields := storeFields[strings.ToLower(strings.TrimSpace(alias))]
		if len(fields) == 0 {
			continue
		}
		if _, ok := fields[strings.ToLower(field)]; ok {
			return alias + "." + field
		}
	}
	return field
}

type scriptCompatibilityNormalizerState struct {
	storeVars     map[string]string
	resultAliases map[string][]string
	storeFields   map[string]map[string]struct{}
}

func (a *CopilotAgent) newScriptCompatibilityNormalizerState(ctx context.Context, rawSteps []map[string]any) *scriptCompatibilityNormalizerState {
	return &scriptCompatibilityNormalizerState{
		storeVars:     make(map[string]string),
		resultAliases: make(map[string][]string),
		storeFields:   a.inferScriptStoreFieldSets(ctx, rawSteps),
	}
}

func (s *scriptCompatibilityNormalizerState) aliasesForStep(step map[string]any) []string {
	if s == nil || step == nil {
		return nil
	}
	inputVar, _ := step["input_var"].(string)
	if aliases := s.resultAliases[strings.TrimSpace(inputVar)]; len(aliases) > 0 {
		return append([]string(nil), aliases...)
	}
	argsObj, _ := step["args"].(map[string]any)
	if argsObj == nil {
		return nil
	}
	if storeRef, _ := argsObj["store"].(string); strings.TrimSpace(storeRef) != "" {
		return s.resolveAliasesForStoreRef(storeRef)
	}
	return nil
}

func (s *scriptCompatibilityNormalizerState) observeStep(step map[string]any) {
	if s == nil || step == nil {
		return
	}
	op, _ := step["op"].(string)
	resultVar, _ := step["result_var"].(string)
	resultVar = strings.TrimSpace(resultVar)
	argsObj, _ := step["args"].(map[string]any)
	if argsObj == nil {
		argsObj = map[string]any{}
	}
	inputVar, _ := step["input_var"].(string)
	inputVar = strings.TrimSpace(inputVar)

	switch strings.ToLower(strings.TrimSpace(op)) {
	case "open_store":
		storeName, _ := argsObj["name"].(string)
		storeName = strings.TrimSpace(storeName)
		if resultVar != "" && storeName != "" {
			s.storeVars[resultVar] = storeName
			s.resultAliases[resultVar] = []string{storeName}
		}
	case "scan", "select":
		if resultVar != "" {
			if storeRef, _ := argsObj["store"].(string); strings.TrimSpace(storeRef) != "" {
				s.resultAliases[resultVar] = s.resolveAliasesForStoreRef(storeRef)
			}
		}
	case "join", "join_right":
		if resultVar != "" {
			aliases := s.resultAliases[inputVar]
			aliases = append(append([]string(nil), aliases...), s.targetAliases(argsObj)...)
			s.resultAliases[resultVar] = dedupeStringSlice(aliases)
		}
	case "filter", "sort", "project", "limit", "first", "last", "next", "previous", "find":
		if resultVar != "" && inputVar != "" {
			s.resultAliases[resultVar] = append([]string(nil), s.resultAliases[inputVar]...)
		}
	}
}

func (s *scriptCompatibilityNormalizerState) targetAliases(argsObj map[string]any) []string {
	for _, key := range []string{"target", "store", "with", "relation"} {
		if ref, _ := argsObj[key].(string); strings.TrimSpace(ref) != "" {
			if aliases := s.resolveAliasesForStoreRef(ref); len(aliases) > 0 {
				return aliases
			}
		}
	}
	return nil
}

func (s *scriptCompatibilityNormalizerState) resolveAliasesForStoreRef(ref string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if storeName := strings.TrimSpace(s.storeVars[ref]); storeName != "" {
		return []string{storeName}
	}
	if aliases := s.resultAliases[ref]; len(aliases) > 0 {
		return append([]string(nil), aliases...)
	}
	return []string{ref}
}

func (a *CopilotAgent) inferScriptStoreFieldSets(ctx context.Context, rawSteps []map[string]any) map[string]map[string]struct{} {
	storeNames := make(map[string]struct{})
	for _, step := range rawSteps {
		op, _ := step["op"].(string)
		if !strings.EqualFold(strings.TrimSpace(op), "open_store") {
			continue
		}
		argsObj, _ := step["args"].(map[string]any)
		storeName, _ := argsObj["name"].(string)
		storeName = strings.TrimSpace(storeName)
		if storeName != "" {
			storeNames[storeName] = struct{}{}
		}
	}
	if len(storeNames) == 0 {
		return nil
	}

	dbName := ""
	if payload := ai.GetSessionPayload(ctx); payload != nil {
		dbName = strings.TrimSpace(payload.CurrentDB)
	}
	for _, step := range rawSteps {
		op, _ := step["op"].(string)
		argsObj, _ := step["args"].(map[string]any)
		switch {
		case strings.EqualFold(strings.TrimSpace(op), "open_db"):
			if name, _ := argsObj["name"].(string); strings.TrimSpace(name) != "" {
				dbName = strings.TrimSpace(name)
			}
		case strings.EqualFold(strings.TrimSpace(op), "begin_tx"):
			if name, _ := argsObj["database"].(string); strings.TrimSpace(name) != "" {
				dbName = strings.TrimSpace(name)
			}
		}
	}
	if dbName == "" {
		return nil
	}

	db, err := a.resolveDatabase(dbName)
	if err != nil || db == nil {
		return nil
	}
	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil
	}
	defer tx.Rollback(ctx)

	storeFields := make(map[string]map[string]struct{}, len(storeNames))
	for storeName := range storeNames {
		store, err := jsondb.OpenStore(ctx, db.Config(), storeName, tx)
		if err != nil || store == nil {
			continue
		}
		if ok, _ := store.First(ctx); !ok {
			continue
		}
		flat := flattenItem(store.GetCurrentKey(), mustCurrentValue(ctx, store))
		schema := inferSchema(flat)
		if len(schema) == 0 {
			continue
		}
		fieldSet := make(map[string]struct{}, len(schema))
		for field := range schema {
			fieldSet[strings.ToLower(strings.TrimSpace(field))] = struct{}{}
		}
		storeFields[strings.ToLower(storeName)] = fieldSet
	}
	if len(storeFields) == 0 {
		return nil
	}
	return storeFields
}

func mustCurrentValue(ctx context.Context, store jsondb.StoreAccessor) any {
	value, _ := store.GetCurrentValue(ctx)
	return value
}

func dedupeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeCompatibilityAliasConditionEntry(fieldHint string, raw string, currentQuery string) (string, any, bool) {
	raw = strings.TrimSpace(strings.Trim(raw, "\"'"))
	fieldHint = strings.TrimSpace(strings.Trim(fieldHint, "\"'"))
	if raw == "" || fieldHint == "" || !isAliasPlaceholderField(fieldHint) {
		return fieldHint, raw, false
	}

	combinedField := normalizeCompatibilityFieldPath(fieldHint + "." + raw)
	if predicate, ok := inferPredicateFromCurrentQuery(combinedField, currentQuery); ok {
		return combinedField, predicate, true
	}

	return combinedField, map[string]any{"$eq": parseCompatibilityLiteral(raw)}, true
}

func inferAliasPredicateFromCurrentQuery(fieldHint string, currentQuery string) (string, map[string]any, bool) {
	fieldHint = strings.TrimSpace(strings.Trim(fieldHint, "\"'"))
	if fieldHint == "" || !isAliasPlaceholderField(fieldHint) {
		return fieldHint, nil, false
	}

	if inferredField, predicate, ok := inferAliasPredicateFromQueryPattern(fieldHint, currentQuery); ok {
		return inferredField, predicate, true
	}

	return fieldHint, nil, false
}

func inferAliasPredicateFromQueryPattern(alias string, currentQuery string) (string, map[string]any, bool) {
	query := strings.TrimSpace(currentQuery)
	if query == "" {
		return alias, nil, false
	}

	patterns := []struct {
		re *regexp.Regexp
		op string
	}{
		{re: regexp.MustCompile(`(?i)([a-zA-Z][a-zA-Z0-9_]*(?:[\s_]+[a-zA-Z0-9_]+){0,2})\s*(>=|<=|>|<|=|==)\s*(-?\d+(?:\.\d+)?)`), op: ""},
		{re: regexp.MustCompile(`(?i)([a-zA-Z][a-zA-Z0-9_]*(?:[\s_]+[a-zA-Z0-9_]+){0,2})\s*(?:is\s+)?greater\s+than\s+(-?\d+(?:\.\d+)?)`), op: "$gt"},
		{re: regexp.MustCompile(`(?i)([a-zA-Z][a-zA-Z0-9_]*(?:[\s_]+[a-zA-Z0-9_]+){0,2})\s*(?:is\s+)?less\s+than\s+(-?\d+(?:\.\d+)?)`), op: "$lt"},
		{re: regexp.MustCompile(`(?i)([a-zA-Z][a-zA-Z0-9_]*(?:[\s_]+[a-zA-Z0-9_]+){0,2})\s*(?:is|=|==|equals?)?\s*['"]([^'"]+)['"]`), op: "$eq"},
	}

	for _, pattern := range patterns {
		matches := pattern.re.FindAllStringSubmatch(query, -1)
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			leaf := normalizeAliasLeafCandidate(match[1])
			if leaf == "" {
				continue
			}
			field := alias + "." + leaf
			op := pattern.op
			valueIndex := 2
			if op == "" {
				op = comparisonOperatorToAST(match[2])
				valueIndex = 3
			}
			value := parseCompatibilityLiteral(match[valueIndex])
			return field, map[string]any{op: value}, true
		}
	}

	return alias, nil, false
}

func normalizeAliasLeafCandidate(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.Trim(raw, "\"'")))
	if raw == "" {
		return ""
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ' ', '_', '.':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}
	stopWords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "by": {}, "find": {}, "for": {}, "from": {}, "in": {},
		"is": {}, "of": {}, "on": {}, "or": {}, "the": {}, "to": {}, "where": {}, "with": {},
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if _, isStopWord := stopWords[part]; isStopWord {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) > 0 {
		parts = filtered
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return strings.Join(parts, "_")
}

func normalizeCompatibilityConditionEntry(fieldHint string, raw string) (string, any, bool) {
	raw = strings.TrimSpace(strings.Trim(raw, "\"'"))
	fieldHint = strings.TrimSpace(strings.Trim(fieldHint, "\"'"))
	if raw == "" || fieldHint == "" {
		return fieldHint, raw, false
	}

	if strings.HasPrefix(raw, "$") {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) == 2 {
			op := strings.TrimSpace(parts[0])
			value := parseCompatibilityLiteral(parts[1])
			if op != "" {
				return fieldHint, map[string]any{op: value}, true
			}
		}
	}

	re := regexp.MustCompile(`^([a-zA-Z0-9_.]+)\s*(==|=|!=|>=|<=|>|<)\s*(.+)$`)
	if parts := re.FindStringSubmatch(raw); len(parts) == 4 {
		field := strings.TrimSpace(parts[1])
		op := strings.TrimSpace(parts[2])
		value := parseCompatibilityLiteral(parts[3])
		if field != "" {
			if !strings.Contains(field, ".") && isAliasPlaceholderField(fieldHint) {
				field = fieldHint + "." + field
			}
			return field, map[string]any{comparisonOperatorToAST(op): value}, true
		}
	}

	return fieldHint, raw, false
}

func normalizeCompatibilityJoinOn(onMap map[string]any) map[string]any {
	normalized := make(map[string]any, len(onMap))
	for left, raw := range onMap {
		left = normalizeCompatibilityFieldPath(left)
		if rawStr, ok := raw.(string); ok {
			rawStr = strings.TrimSpace(strings.Trim(rawStr, "\"'"))
			if strings.Contains(rawStr, "=") {
				parts := strings.SplitN(rawStr, "=", 2)
				lhs := strings.TrimSpace(parts[0])
				rhs := strings.TrimSpace(parts[1])
				if lhs != "" && rhs != "" {
					if !strings.Contains(lhs, ".") && isAliasPlaceholderField(left) {
						lhs = left + "." + lhs
					}
					normalized[lhs] = rhs
					continue
				}
			}
			if isAliasPlaceholderField(left) && rawStr != "" && !strings.Contains(rawStr, ".") {
				normalized[left+"."+rawStr] = "key"
				continue
			}
		}
		normalized[left] = raw
	}
	return normalized
}

func normalizeCompatibilityFieldPath(field string) string {
	field = strings.TrimSpace(strings.Trim(field, "\"'"))
	return field
}

func comparisonOperatorToAST(op string) string {
	switch strings.TrimSpace(op) {
	case "=", "==":
		return "$eq"
	case "!=":
		return "$ne"
	case ">":
		return "$gt"
	case ">=":
		return "$gte"
	case "<":
		return "$lt"
	case "<=":
		return "$lte"
	default:
		return "$eq"
	}
}

func parseCompatibilityLiteral(raw string) any {
	raw = strings.TrimSpace(strings.Trim(raw, "\"'"))
	if raw == "" {
		return ""
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func inferPredicateFromCurrentQuery(field string, currentQuery string) (map[string]any, bool) {
	query := strings.TrimSpace(currentQuery)
	if query == "" {
		return nil, false
	}

	field = normalizeCompatibilityFieldPath(field)
	leaf := field
	if idx := strings.LastIndex(leaf, "."); idx >= 0 {
		leaf = leaf[idx+1:]
	}
	leaf = strings.TrimSpace(leaf)
	if leaf == "" {
		return nil, false
	}

	fieldPattern := queryFieldPattern(field)
	leafPattern := queryFieldPattern(leaf)
	patterns := []string{fieldPattern}
	if leafPattern != fieldPattern {
		patterns = append(patterns, leafPattern)
	}

	for _, pattern := range patterns {
		if predicate, ok := inferNumericPredicateFromQueryPattern(query, pattern); ok {
			return predicate, true
		}
		if predicate, ok := inferQuotedStringPredicateFromQueryPattern(query, pattern); ok {
			return predicate, true
		}
	}

	return nil, false
}
