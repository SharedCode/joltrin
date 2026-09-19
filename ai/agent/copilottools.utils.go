package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/jsondb"
)

const (
	CtxKeyAtomicScriptContext = "_atomic_script_context"
	CtxKeyAtomicLastResult    = "_atomic_last_result"
)

func getOrInitScriptContext(ctx context.Context) *ScriptContext {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		// Fallback for context-less usage (should verify if this introduces shared state risk if the agent caches it)
		// For now, return a new transient context if no payload.
		// NOTE: In production, payload MUST exist for state persistence within a session.
		return NewScriptContext()
	}
	if p.Variables == nil {
		p.Variables = make(map[string]any)
	}
	if val, ok := p.Variables[CtxKeyAtomicScriptContext]; ok {
		if sc, ok := val.(*ScriptContext); ok {
			return sc
		}
	}
	sc := NewScriptContext()
	p.Variables[CtxKeyAtomicScriptContext] = sc
	return sc
}

func getLastAtomicResult(ctx context.Context) any {
	p := ai.GetSessionPayload(ctx)
	if p == nil || p.Variables == nil {
		return nil
	}
	return p.Variables[CtxKeyAtomicLastResult]
}

func setLastAtomicResult(ctx context.Context, res any) {
	p := ai.GetSessionPayload(ctx)
	if p != nil {
		if p.Variables == nil {
			p.Variables = make(map[string]any)
		}
		p.Variables[CtxKeyAtomicLastResult] = res
	}
}

// mapToScriptSteps converts a generic slice of maps into []ai.ScriptStep.
func mapToScriptSteps(list []any) ([]ai.ScriptStep, error) {
	var steps []ai.ScriptStep
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			// Round trip via JSON to robustly handle type conversion
			b, err := json.Marshal(m)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal step: %v", err)
			}
			var step ai.ScriptStep
			if err := json.Unmarshal(b, &step); err != nil {
				return nil, fmt.Errorf("failed to unmarshal step: %v", err)
			}

			// Validation: Ensure valid type and command
			if step.Type == "" {
				step.Type = "command" // Default to command if unspecified
			}

			// Compatibility: map 'op' to 'Command' if 'command' is missing
			// This handles cases where the LLM or UI sends 'op' instead of 'command'.
			if step.Command == "" {
				if op, ok := m["op"].(string); ok && op != "" {
					step.Command = op
				}
			}

			if step.Type == "command" && step.Command == "" {
				return nil, fmt.Errorf("invalid step: 'command' is required for steps of type 'command'")
			}

			steps = append(steps, step)
		} else {
			return nil, fmt.Errorf("invalid step format (expected object)")
		}
	}
	return steps, nil
}

func matchesKey(itemKey, filterKey any) (bool, string) {
	if filterKey == nil {
		return true, ""
	}

	// If filter is a map, it might be an operator map OR a composite key match
	if mFilter, ok := filterKey.(map[string]any); ok {
		// Check if it is an operator map (keys start with $)
		isOp := false
		for k := range mFilter {
			if strings.HasPrefix(k, "$") {
				isOp = true
				break
			}
		}
		if isOp {
			return matchOperator(itemKey, mFilter), ""
		}

		// If itemKey is also a map, check fields
		if mItem, ok := itemKey.(map[string]any); ok {
			for k, v := range mFilter {
				itemVal, exists := mItem[k]

				// Fallback: Case-insensitive key lookup (AI UX improvement)
				// If the exact key isn't found, try finding a key differing only in case.
				if !exists {
					for mk, mv := range mItem {
						if strings.EqualFold(mk, k) {
							itemVal = mv
							exists = true
							break
						}
					}
				}

				// Check for operator map
				if opMap, ok := v.(map[string]any); ok {
					// Check if it is an operator map (keys start with $)
					isOp := false
					for opK := range opMap {
						if strings.HasPrefix(opK, "$") {
							isOp = true
							break
						}
					}

					if isOp {
						if !exists {
							// If field missing, fail unless checking for $ne: null?
							// For simplicity, fail if missing.
							return false, ""
						}
						if !matchOperator(itemVal, opMap) {
							return false, ""
						}
						continue
					}
				}

				// Simple equality check. For nested objects, this might need recursion.
				// But for now, we assume flat keys or strict equality on values.
				alignedV := alignType(v, itemVal)
				if !exists || btree.Compare(itemVal, alignedV) != 0 {
					return false, ""
				}
			}
			return true, ""
		}

		// Handle JSON string keys (e.g. from jsondb stores with Map keys)
		if sKey, ok := itemKey.(string); ok && strings.HasPrefix(strings.TrimSpace(sKey), "{") {
			var mItem map[string]any
			if err := json.Unmarshal([]byte(sKey), &mItem); err == nil {
				// Recurse with the map
				return matchesKey(mItem, mFilter)
			}
		}

		// NEW: Handle Primitive Key vs Map Filter mismatch
		// If itemKey is NOT a map, but filterKey IS a map (and not Op map).
		// And filterKey has exactly 1 entry.
		if len(mFilter) == 1 {
			for k, v := range mFilter {
				// Align types if possible
				alignedV := alignType(v, itemKey)
				matched, _ := matchesKey(itemKey, alignedV)
				if matched {
					return true, k
				}
				return false, ""
			}
		}
	}
	// If primitives, strict equality
	return btree.Compare(itemKey, filterKey) == 0, ""
}

func matchOperator(val any, opMap map[string]any) bool {
	for op, target := range opMap {
		// Align target type to val type
		alignedTarget := alignType(target, val)
		cmp := btree.Compare(val, alignedTarget)
		switch op {
		case "$eq":
			if cmp != 0 {
				return false
			}
		case "$ne":
			if cmp == 0 {
				return false
			}
		case "$gt":
			if cmp <= 0 {
				return false
			}
		case "$gte":
			if cmp < 0 {
				return false
			}
		case "$lt":
			if cmp >= 0 {
				return false
			}
		case "$lte":
			if cmp > 0 {
				return false
			}
		}
	}
	return true
}

type OrderedKey struct {
	m    map[string]any
	spec *jsondb.IndexSpecification
}

// MarshalJSON implements json.Marshaler to enforce field order.
func (o OrderedKey) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	// 1. Write indexed fields in order
	written := make(map[string]bool)
	first := true

	for _, field := range o.spec.IndexFields {
		if val, ok := o.m[field.FieldName]; ok {
			if !first {
				buf.WriteByte(',')
			}
			first = false

			kb, _ := json.Marshal(field.FieldName)
			buf.Write(kb)
			buf.WriteByte(':')
			vb, _ := json.Marshal(val)
			buf.Write(vb)

			written[field.FieldName] = true
		}
	}

	// 2. Write remaining fields sorted alphabetically
	var remaining []string
	for k := range o.m {
		if !written[k] {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)

	for _, k := range remaining {
		if !first {
			buf.WriteByte(',')
		}
		first = false

		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, _ := json.Marshal(o.m[k])
		buf.Write(vb)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type OrderedMap struct {
	m          map[string]any
	keys       []string
	isImplicit bool // If true, this order is an artifact of operations (like Join) and can be collapsed/flattened.
}

func (o OrderedMap) MarshalJSON() ([]byte, error) {
	// Debug logging
	// fmt.Printf("DEBUG: OrderedMap.MarshalJSON Keys: %v\n", o.keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(o.m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// reorderItem ensures the output map follows the IndexSpecification order if available,
// or the requested fields order.
func reorderItem(item any, fields []string, indexSpec *jsondb.IndexSpecification) any {
	mItem, ok := item.(map[string]any)
	if !ok {
		return item
	}

	// 1. If explicit fields are requested, filterFields handles ordering.
	if len(fields) > 0 {
		return filterFields(mItem, fields)
	}

	// 2. Use IndexSpecification for default ordering (Key vs Value vs Index Fields)
	if indexSpec != nil {
		// Case A: Item has "key" field which is a Map -> Apply OrderedKey to it
		if kVal, hasKey := mItem["key"]; hasKey {
			if kMap, kOk := kVal.(map[string]any); kOk {
				// Replace "key" with OrderedKey wrapper
				mItem["key"] = OrderedKey{m: kMap, spec: indexSpec}
			}
			// Return OrderedMap ensuring "key" comes before "value"
			// Check if "value" exists
			keys := []string{"key"}
			if _, hasVal := mItem["value"]; hasVal {
				keys = append(keys, "value")
			}
			// Append any other fields (e.g. valid, deleted, etc)
			for k := range mItem {
				if k != "key" && k != "value" {
					keys = append(keys, k)
				}
			}
			return &OrderedMap{m: mItem, keys: keys}
		}

		// Case B: Flat Map - Order by Index Fields
		orderedKeys := make([]string, 0, len(mItem))
		used := make(map[string]bool)

		// Add index fields first
		for _, f := range indexSpec.IndexFields {
			if _, ok := mItem[f.FieldName]; ok {
				orderedKeys = append(orderedKeys, f.FieldName)
				used[f.FieldName] = true
			}
		}

		// Add remaining fields (sorted alphabetically)
		var remaining []string
		for k := range mItem {
			if !used[k] {
				remaining = append(remaining, k)
			}
		}
		sort.Strings(remaining)
		orderedKeys = append(orderedKeys, remaining...)

		return &OrderedMap{
			m:    mItem,
			keys: orderedKeys,
		}
	}

	return item
}

func filterFields(item map[string]any, fields []string) any {
	// filterFields is a legacy/simplified wrapper.
	// We now delegate to the robust renderItem logic which handles:
	// 1. Wildcard expansion (e.g. "a.*")
	// 2. Deep flattening (Key/Value merging)
	// 3. Aliasing ("field AS alias")
	// 4. Scoped filtering ("left.name")
	//
	// We pass 'nil' as the key and 'item' as the value because 'item' is typically
	// already a merged/flattened map from a previous cursor step.
	return renderItem(nil, item, fields)
}

func mergeMap(original, updates map[string]any) map[string]any {
	newMap := make(map[string]any)
	for k, v := range original {
		newMap[k] = v
	}
	for k, v := range updates {
		newMap[k] = v
	}
	return newMap
}

// alignType attempts to convert filterVal to match the type of targetVal.
// This is useful when comparing Int vs String keys.
func alignType(filterVal any, targetVal any) any {
	if _, ok := targetVal.(string); ok {
		return convertToString(filterVal)
	}
	if _, ok := targetVal.(float64); ok {
		return convertToFloat(filterVal)
	}
	if _, ok := targetVal.(int); ok {
		return convertToInt(filterVal)
	}
	return filterVal
}

func convertToFloat(v any) any {
	switch val := v.(type) {
	case int:
		return float64(val)
	case float64:
		return val
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return v
}

func coerceToFloat(v any) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case int32:
		return float64(val)
	case int64:
		return float64(val)
	case float64:
		return val
	case float32:
		return float64(val)
	case string:
		// Clean common LLM artifacts like trailing semicolons
		cleaned := strings.Trim(val, " \t\r\n;,")
		if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return f
		}
	}
	return 0
}

func coerceToStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []string:
		return val
	case []any:
		s := make([]string, len(val))
		for i, x := range val {
			s[i] = fmt.Sprint(x)
		}
		return s
	case string:
		val = strings.TrimSpace(val)
		if strings.HasPrefix(val, "[") {
			var parts []any
			if err := json.Unmarshal([]byte(val), &parts); err == nil {
				s := make([]string, len(parts))
				for i, x := range parts {
					s[i] = fmt.Sprint(x)
				}
				return s
			}
		}
		// Treat comma separated values as list
		if strings.Contains(val, ",") {
			parts := strings.Split(val, ",")
			s := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					s = append(s, p)
				}
			}
			return s
		}
		if val == "" {
			return nil
		}
		return []string{val}
	}
	return nil
}

func convertToInt(v any) any {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return v
}

func convertToString(v any) any {
	return fmt.Sprintf("%v", v)
}

// GetField extracts a field from a given source object (Map, OrderedMap, or JSON String).
// It supports case-insensitive lookups, dot-notation stripping, Key/Value recursion, and suffix matching.
func GetField(source any, field string) any {
	if source == nil {
		return nil
	}

	// Unwrap OrderedMap
	switch s := source.(type) {
	case *OrderedMap:
		if s != nil {
			source = s.m
		}
	case OrderedMap:
		source = s.m
	}

	switch m := source.(type) {
	case map[string]any:
		if field == "*" {
			return m
		}
		// 1. Exact Match
		if v, ok := m[field]; ok {
			return v
		}

		// Prepare for fuzzy search
		var stripped string
		var dotSuffix string

		// Dot-Notation setup
		if idx := strings.Index(field, "."); idx > 0 {
			stripped = field[idx+1:]
			// 2. Stripped exact match
			if v, ok := m[stripped]; ok {
				return v
			}
		}
		dotSuffix = "." + field

		// Single-pass iteration for all fuzzy searches to avoid multiple map traversals
		var matchStripped any
		var matchSuffix any

		for k, v := range m {
			// 3. Case-insensitive fallback (Highest Priority fuzzy)
			if strings.EqualFold(k, field) {
				return v
			}

			// 4. Case-insensitive stripped fallback (High Priority fuzzy)
			if stripped != "" && matchStripped == nil {
				if strings.EqualFold(k, stripped) {
					matchStripped = v
					// Do not return yet; exact fold match might exist later in the map
				}
			}

			// 7. Suffix Match (Lowest Priority fuzzy - captured here but used last)
			if matchSuffix == nil && strings.HasSuffix(k, dotSuffix) {
				matchSuffix = v
			}
		}

		if matchStripped != nil {
			return matchStripped
		}

		// 5. Key Wrapper Check (recurse into "key" if present)
		if kWrapper, ok := m["key"]; ok {
			if kVal := GetField(kWrapper, field); kVal != nil {
				return kVal
			}
		}

		// 6. Value Wrapper Check (recurse into "value" if present)
		if vWrapper, ok := m["value"]; ok {
			if vVal := GetField(vWrapper, field); vVal != nil {
				return vVal
			}
		}

		// Return suffix match if nothing else found
		if matchSuffix != nil {
			return matchSuffix
		}

		// 8. New Logic: Unique Suffix Match (Reverse Alias)
		// Request "field", Match "table.field"
		if !strings.Contains(field, ".") {
			var candidate any
			foundCount := 0
			suffix := "." + strings.ToLower(field)

			for k, v := range m {
				if strings.HasSuffix(strings.ToLower(k), suffix) {
					candidate = v
					foundCount++
				}
			}

			if foundCount == 1 {
				return candidate
			}
		}

	case string:
		// 2. JSON String
		// Quick check for JSON-like start without allocation
		isJSON := false
		for i := 0; i < len(m); i++ {
			c := m[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				continue
			}
			if c == '{' {
				isJSON = true
			}
			break
		}

		if isJSON {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(m), &decoded); err == nil {
				return GetField(decoded, field)
			}
		}
	}
	return nil
}

// extractVal extracts a value from a key or value map/json.
func extractVal(key any, val any, field string) any {
	if field == "key" {
		return key
	}

	if v := GetField(key, field); v != nil {
		return v
	}
	if v := GetField(val, field); v != nil {
		return v
	}

	// Fallback: Smart Suffix Match (Handling Join Aliases)
	// If the user requests "users.name" but the data has "a.name" (system alias), we match it.
	// We check if 'field' has a dot suffix that matches any key's dot suffix.
	// We also support "naked" field requests (e.g. "name") matching prefixed keys (e.g. "users.name").
	{
		var suffix string
		if dotIdx := strings.LastIndex(field, "."); dotIdx != -1 {
			suffix = field[dotIdx:] // ".name"
		} else {
			suffix = "." + field // ".name"
		}

		// Helper to check map for suffix
		checkMap := func(source any) any {
			// Handle OrderedMap unwrapping
			var m map[string]any
			if om, ok := source.(*OrderedMap); ok {
				m = om.m
			} else if om, ok := source.(OrderedMap); ok {
				m = om.m
			} else if mp, ok := source.(map[string]any); ok {
				m = mp
			}

			if m != nil {
				// Priority 1: Exact Base Name Match (e.g. "name" matches "users.name")
				// This handles cases where data is flat (un-prefixed) but query uses alias.
				if len(suffix) > 1 {
					if v, ok := m[suffix[1:]]; ok {
						return v
					}
				}

				// Priority 2: Suffix Match (e.g. "a.name" matches "users.name")
				for k, v := range m {
					if strings.HasSuffix(k, suffix) {
						// Found a candidate
						return v
					}
				}
			}
			return nil
		}

		if v := checkMap(key); v != nil {
			return v
		}
		if v := checkMap(val); v != nil {
			return v
		}
	}

	// Fallback logic for primitive values if "Value" or "value" is requested
	if strings.EqualFold(field, "value") {
		return val
	}
	return nil
}

// isKeyField checks if a field is part of the Key.
func isKeyField(key any, field string) (bool, string) {
	if field == "key" {
		return true, "key"
	}
	if key == nil {
		return false, ""
	}
	// 1. Map
	if km, ok := key.(map[string]any); ok {
		if _, ok := km[field]; ok {
			return true, field
		}
		for k := range km {
			if strings.EqualFold(k, field) {
				return true, k
			}
		}
	}
	// 2. JSON String
	if s, ok := key.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			if _, ok := m[field]; ok {
				return true, field
			}
			for k := range m {
				if strings.EqualFold(k, field) {
					return true, k
				}
			}
		}
	}
	return false, ""
}

// coerce converts val to match the type of target.
func coerce(val any, target any) any {
	if val == nil || target == nil {
		return val
	}

	// If types match, return as is
	if fmt.Sprintf("%T", val) == fmt.Sprintf("%T", target) {
		return val
	}

	switch target.(type) {
	case int:
		switch v := val.(type) {
		case float64:
			return int(v)
		case float32:
			return int(v)
		case int:
			return v
		case int8:
			return int(v)
		case int16:
			return int(v)
		case int32:
			return int(v)
		case int64:
			return int(v)
		case uint:
			return int(v)
		case uint8:
			return int(v)
		case uint16:
			return int(v)
		case uint32:
			return int(v)
		case uint64:
			return int(v)
		case string:
			var i int
			if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
				return i
			}
		}
	case int8:
		switch v := val.(type) {
		case float64:
			return int8(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 8); err == nil {
				return int8(i)
			}
		default:
			// Fallback to int conversion then cast
			if i, ok := val.(int); ok {
				return int8(i)
			}
		}
	case int16:
		switch v := val.(type) {
		case float64:
			return int16(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 16); err == nil {
				return int16(i)
			}
		}
	case int32:
		switch v := val.(type) {
		case float64:
			return int32(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 32); err == nil {
				return int32(i)
			}
		}
	case int64:
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int:
			return int64(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return i
			}
		}
	case uint:
		switch v := val.(type) {
		case float64:
			return uint(v)
		case string:
			if i, err := strconv.ParseUint(v, 10, strconv.IntSize); err == nil {
				return uint(i)
			}
		}
	case uint8:
		switch v := val.(type) {
		case float64:
			return uint8(v)
		case string:
			if i, err := strconv.ParseUint(v, 10, 8); err == nil {
				return uint8(i)
			}
		}
	case uint16:
		switch v := val.(type) {
		case float64:
			return uint16(v)
		case string:
			if i, err := strconv.ParseUint(v, 10, 16); err == nil {
				return uint16(i)
			}
		}
	case uint32:
		switch v := val.(type) {
		case float64:
			return uint32(v)
		case string:
			if i, err := strconv.ParseUint(v, 10, 32); err == nil {
				return uint32(i)
			}
		}
	case uint64:
		switch v := val.(type) {
		case float64:
			return uint64(v)
		case string:
			if i, err := strconv.ParseUint(v, 10, 64); err == nil {
				return i
			}
		}
	case float32:
		switch v := val.(type) {
		case float64:
			return float32(v)
		case string:
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				return float32(f)
			}
		}
	case float64:
		switch v := val.(type) {
		case int:
			return float64(v)
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
		}
	case string:
		return fmt.Sprintf("%v", val)
	case bool:
		switch v := val.(type) {
		case bool:
			return v
		case string:
			return strings.ToLower(v) == "true"
		}
	case sop.UUID:
		if s, ok := val.(string); ok {
			if id, err := sop.ParseUUID(s); err == nil {
				return id
			}
		}
	}
	return val
}

// valuesMatch checks if two values match (strict string comparison).
func valuesMatch(v1, v2 any) bool {
	return fmt.Sprintf("%v", v1) == fmt.Sprintf("%v", v2)
}

// generateJoinKey generates a cache key for Hash Join.
func generateJoinKey(key any, val any, fields []string) string {
	var parts []string
	for _, f := range fields {
		v := extractVal(key, val, f)
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, "|")
}

// flattenItem merges key and value into a single map.
// If key/value are maps, their fields are merged at the top level.
// If they are primitives, they are stored as "key" and "value" (or specific field names if needed).
func flattenItem(key any, value any) map[string]any {
	// fmt.Printf("flattenItem Input: keyType=%T valType=%T val=%+v\n", key, value, value)
	result := make(map[string]any)

	// Helper to unwrap pointers
	var unwrap func(v any) any
	unwrap = func(v any) any {
		if v == nil {
			return nil
		}
		// 1. Check for *any (pointer to interface)
		if ptr, ok := v.(*any); ok {
			if ptr == nil {
				return nil
			}
			return unwrap(*ptr) // Recursively unwrap
		}
		return v
	}

	key = unwrap(key)
	value = unwrap(value)

	// 1. Flatten Key
	if kMap, ok := key.(map[string]any); ok {
		for k, v := range kMap {
			result[k] = v
		}
	} else if kOm, ok := key.(OrderedMap); ok {
		for k, v := range kOm.m {
			result[k] = v
		}
	} else if kOm, ok := key.(*OrderedMap); ok && kOm != nil {
		for k, v := range kOm.m {
			result[k] = v
		}
	} else {
		if key != nil {
			result["key"] = key
		}
	}

	// 2. Flatten Value
	if vMap, ok := value.(map[string]any); ok {
		for k, v := range vMap {
			result[k] = v
		}
	} else if vOm, ok := value.(OrderedMap); ok {
		for k, v := range vOm.m {
			result[k] = v
		}
	} else if vOm, ok := value.(*OrderedMap); ok && vOm != nil {
		for k, v := range vOm.m {
			result[k] = v
		}
	} else {
		// Attempt to flatten structs via JSON to respect struct tags
		if value != nil {
			isStruct := false
			// Simple heuristics (or just try json)
			// We only want to do this for things that look like structs, not primitives
			switch value.(type) {
			case int, int64, float64, string, bool:
				// Primitives: Just set as "value"
			default:
				isStruct = true
			}

			if isStruct {
				b, err := json.Marshal(value)
				if err == nil {
					var m map[string]any
					if err := json.Unmarshal(b, &m); err == nil {
						for k, v := range m {
							result[k] = v
						}
						// If successfully flattened, we don't necessarily need "value" key,
						// but 'renderItem' logic might rely on 'value' field availability.
						// However, getField looks at root too.
						// Let's store strict value as well for reference?
						// "value" key collision risk if struct has "value" field.
						// The original code did result["value"] = value.
						// If we flattened, we did valid expansion.
						// We should NOT set result["value"] if we flattened, to avoid shadowing if possible,
						// OR we set it so tools inspecting the object as a whole can see strict types?
						// Let's err on side of just flattening.
						return result
					}
				}
			}
			result["value"] = value
		}
	}

	return result
}

type ProjectionField struct {
	Src string
	Dst string
}

func parseProjectionFields(input any) []ProjectionField {
	var result []ProjectionField
	aliasRe := regexp.MustCompile(`(?i)\s+as\s+`)

	cleanName := func(f string) string {
		prefixes := []string{"left.", "right.", "a.", "b.", "left_", "right_"}
		lowerF := strings.ToLower(f)
		for _, p := range prefixes {
			if strings.HasPrefix(lowerF, p) {
				// If it's a wildcard projection like "a.*" or "left.*", preserve the prefix!
				// We need the prefix in renderItem to know WHICH side to broaden.
				if len(f) > len(p) && f[len(p):] == "*" {
					return f
				}
				// Handle case where LLM sends "a." or "left." meaning "a.*"
				if len(f) == len(p) {
					return f + "*"
				}
				// FIX: Do NOT strip the prefix for simple fields yet.
				// We need the full source path (e.g. "users.name") to extract the value correctly during projection.
				// The cleanName function is used for Dst (Alias) calculation when no explicit alias is given.
				// If we strip "users." from "users.name", the Alias becomes "name", which is correct.
				// BUT if we use cleanName on Src below, it breaks data extraction.
				// Wait, this cleanName is ONLY called for the ELSE block of alias checking (implicit alias).
				// So dst = cleanName(f) is correct: we want "name" from "users.name".
				return f[len(p):]
			}
		}

		// Also stripping "Department." if table alias expansion happened?
		// We should probably strip ANY prefix if it looks like Table.Col
		// (Jan 2026): Generic stripping disabled to allow "config.version" and "version" to coexist without collision.
		// If user wants short names, they should use "AS" alias.
		return f
	}

	processString := func(f string) {
		src := f
		dst := f
		if loc := aliasRe.FindStringIndex(f); loc != nil {
			src = strings.TrimSpace(f[:loc[0]])
			dst = strings.TrimSpace(f[loc[1]:])
		} else {
			dst = cleanName(f)
		}

		// Handle "a." -> "a.*" expansion manually, but DO NOT strip prefixes for fields
		// We want to pass "b.name" as "b.name" so extractVal can find the aliased key.
		prefixes := []string{"left.", "right.", "a.", "b.", "left_", "right_"}
		lowerSrc := strings.ToLower(src)
		for _, p := range prefixes {
			if strings.HasPrefix(lowerSrc, p) {
				if len(src) == len(p) {
					src = src + "*"
				}
				break
			}
		}

		result = append(result, ProjectionField{Src: src, Dst: dst})
	}

	// Definition of a Map Parsing Rule:
	// Tries to interpret a map object as a projection definition.
	// Returns true if handled, and appends fields to the result.
	type MapParsingRule func(map[string]any) ([]ProjectionField, bool)

	rules := []MapParsingRule{
		// Rule 1: Specific {"field": "src", "alias": "dst"} format
		// Used by some SQL Agents for explicit aliasing
		func(m map[string]any) ([]ProjectionField, bool) {
			// Check for "alias" or "as"
			var alias string
			if a, ok := m["alias"].(string); ok {
				alias = a
			} else if a, ok := m["as"].(string); ok {
				alias = a
			} else if a, ok := m["Dst"].(string); ok {
				// Support Dst/Src format explicitly
				alias = a
			}

			if alias != "" {
				if field, okF := m["field"].(string); okF {
					return []ProjectionField{{Src: field, Dst: alias}}, true
				} else if field, okS := m["Src"].(string); okS {
					// Support Dst/Src format explicitly
					return []ProjectionField{{Src: field, Dst: alias}}, true
				}
			}
			return nil, false
		},
		// Rule 2: Generic Mapping {"alias": "src"} (Target : Source)
		// Used by Agent for JSON construction: {"employee": "right.name"} -> employee = right.name
		func(m map[string]any) ([]ProjectionField, bool) {
			var res []ProjectionField
			// Sort keys to ensure deterministic output order (map iteration is random)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				v := m[k]
				src := fmt.Sprintf("%v", v)

				// Apply cleanName logic to source to handle wildcards properly
				// e.g. "a." -> "a.*"
				// cleanSrc := cleanName(src) // Unused now, we do manual logic below

				prefixes := []string{"left.", "right.", "a.", "b.", "left_", "right_"}
				// isSpecial := false // Unused
				lowerSrc := strings.ToLower(src)

				// If source is just "left" or "right" (common mistake), treat as wildcard
				if lowerSrc == "left" || lowerSrc == "right" {
					src = src + ".*"
					// isSpecial = true
				} else {
					for _, p := range prefixes {
						if strings.HasPrefix(lowerSrc, p) {
							if len(src) == len(p) {
								src = src + "*" // "left." -> "left.*"
							}
							// isSpecial = true
							break
						}
					}
				}

				res = append(res, ProjectionField{Src: src, Dst: k})
			}
			// This rule is greedy and always matches if the map is not empty
			return res, len(res) > 0
		},
	}

	if list, ok := input.([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				processString(s)
			} else if m, ok := item.(map[string]any); ok {
				// Try apply rules in order
				for _, rule := range rules {
					if res, handled := rule(m); handled {
						result = append(result, res...)
						break
					}
				}
			}
		}
	} else if list, ok := input.([]string); ok {
		for _, s := range list {
			processString(s)
		}
	} else if list, ok := input.([]ProjectionField); ok {
		return list
	} else if m, ok := input.(map[string]any); ok {
		// Handle single map as projection definition (Target: Source)
		for _, rule := range rules {
			if res, handled := rule(m); handled {
				return res
			}
		}
	}

	return result
}

// renderItem creates a result map from the key and value, applying standard flattening or field selection.
// It is used by Scan, Select, and Join cursors to ensure consistent output format.
func renderItem(key any, val any, fields any) any {
	// 1. Wildcard / Flatten Mode
	shouldFlatten := false
	if fields == nil {
		shouldFlatten = true
	} else if l, ok := fields.([]string); ok {
		if len(l) == 0 || (len(l) == 1 && l[0] == "") {
			shouldFlatten = true
		}
	} else if l, ok := fields.([]any); ok {
		if len(l) == 0 {
			shouldFlatten = true
		} else if len(l) == 1 {
			if s, ok := l[0].(string); ok && s == "" {
				shouldFlatten = true
			}
		}
	}

	if shouldFlatten {
		// New Strategy: Collapse Unique Prefixes
		// 1. Flatten into "Strict Map" (Aliased)
		flat := flattenItem(key, val)
		return collapseUniqueKeys(flat)
	}

	// Parse fields using the common helper if not already parsed
	var pFields []ProjectionField
	if pf, ok := fields.([]ProjectionField); ok {
		pFields = pf
	} else {
		pFields = parseProjectionFields(fields)
	}

	// 2. Projection Mode
	resultMap := OrderedMap{m: make(map[string]any), keys: make([]string, 0)}

	for _, f := range pFields {
		// Handle Wildcard Projection (e.g. "a.*" or "*")
		if strings.HasSuffix(f.Src, "*") {
			// Determine filter scope
			isAny := f.Src == "*"
			isLeft := strings.HasPrefix(strings.ToLower(f.Src), "a.") || strings.HasPrefix(strings.ToLower(f.Src), "left.")
			isRight := strings.HasPrefix(strings.ToLower(f.Src), "b.") || strings.HasPrefix(strings.ToLower(f.Src), "right.")
			// Also support custom aliases ending in ".*"
			// If not matching specific keywords but has prefix
			customPrefix := ""
			if !isLeft && !isRight && !isAny {
				if idx := strings.Index(f.Src, ".*"); idx > 0 {
					customPrefix = f.Src[:idx+1] // e.g. "my."
				}
			}

			// isAny declaration moved up

			// Flatten the source object and merge all keys
			flat := flattenItem(key, val)

			// Collect keys in deterministic order
			var flatKeys []string
			for k := range flat {
				flatKeys = append(flatKeys, k)
			}
			sort.Strings(flatKeys)

			for _, k := range flatKeys {
				v := flat[k]

				// Apply Scope Filtering
				include := false
				if isAny {
					include = true
				} else if isLeft {
					// Left side "a.*" or "left.*"
					// Include keys relative to Left.
					// Strictly, this means keys starting with "a." or keys WITHOUT any dot (standard SQL legacy where Left is naked).
					// But we must EXCLUDE keys that definitely belong to another alias (e.g. "b.something", "right.something").

					hasDot := strings.Contains(k, ".")
					if !hasDot {
						include = true
					} else {
						// It has a dot. Only include if it matches "left." or "a." (our prefixes)
						// OR if it doesn't match known "alien" prefixes.
						// Since we don't know all aliases, we assume if it starts with "Right." => Alien.
						// If it starts with "b." => Alien (heuristic common pattern).
						// Ideally we should prefer including ONLY if it starts with "a." or "left."?
						// But what if Left data has "user.name"?
						// SQL behavior: a.* returns columns of table A.

						if strings.HasPrefix(strings.ToLower(k), "a.") || strings.HasPrefix(strings.ToLower(k), "left.") {
							include = true
						} else if !strings.HasPrefix(k, "Right.") && !strings.HasPrefix(strings.ToLower(k), "b.") {
							// HEURISTIC: Exclude "b." and "Right." to fix duplication issue in "select a.*, b.name".
							// This is valid because we now enforce prefixing for "b" in JoinRight.
							include = true
						}
					}
				} else if isRight {
					// Right side = include keys starting with "Right."
					if strings.HasPrefix(k, "Right.") {
						include = true
					}
					// Also include keys matching the specific requested prefix (e.g. "b." from "b.*")
					prefix := f.Src[:len(f.Src)-1]
					if len(prefix) > 0 && strings.HasPrefix(k, prefix) {
						include = true
					}
				} else if customPrefix != "" {
					// Smart Heuristic:
					// If we find ANY key that matches the prefix (e.g. "orders.id"), then we know "orders" is present as a namespaced entity.
					// In that case, we should NOT grab un-dotted keys, because they likely belong to a different (Left) store.
					// We only fall back to grabbing un-dotted keys if there are NO prefixed matches (meaning the requested alias is likely the Left/Flat store).
					hasPrefixMatch := false
					for _, checkK := range flatKeys {
						if strings.HasPrefix(checkK, customPrefix) {
							hasPrefixMatch = true
							break
						}
					}

					if strings.HasPrefix(k, customPrefix) {
						include = true
						// UX Improvement: Strip the prefix from the output key
						// If we are projecting "users.*" (customPrefix="users."), and we find "users.age",
						// we want the output key to be "age".
						k = k[len(customPrefix):]
						// Ensure we don't accidentally match empty string if prefix matches exact key?
						if k == "" {
							include = false
						}
					}
					// Restore heuristic that matches un-dotted keys for custom aliases.
					// This is necessary because in many pipelines (like scan -> join), the left-most store's fields are NOT prefixed.
					// So if the user does scan("users") -> project("users.*"), we must assume un-dotted fields belong to "users".
					// EDIT: Only apply this if we didn't find a strong signal (prefix match) elsewhere.
					if !hasPrefixMatch && !strings.Contains(k, ".") {
						include = true
					}
				}

				if include {
					// EXCLUDE "value" system field from wildcard expansion.
					// It is often an artifact of flattening and redundant (e.g. duplicate of ID).
					// User can still explicitly request it.
					// Exception: If "value" is the ONLY field, keep it (e.g. primitive int/string).
					if k == "value" && len(flatKeys) > 1 {
						continue
					}

					// Don't overwrite existing explicit explicit aliased fields (precedence)
					// Actually, in projection lists, order matters.
					resultMap.m[k] = v

					// Add to key list if not present
					found := false
					for _, existingKey := range resultMap.keys {
						if existingKey == k {
							found = true
							break
						}
					}
					if !found {
						resultMap.keys = append(resultMap.keys, k)
					}
				}
			}
			continue
		}

		// Extract Value
		v := extractVal(key, val, f.Src)

		// Include even if nil?
		if v != nil {
			resultMap.m[f.Dst] = v
			resultMap.keys = append(resultMap.keys, f.Dst)
		}
	}
	return &resultMap
}

// CleanArgs extracts non-reserved arguments from a map.
// It skips keys starting with "_" and any key in the reserved list.
func CleanArgs(args map[string]any, reserved ...string) map[string]any {
	out := make(map[string]any)
	reservedMap := make(map[string]struct{})
	for _, r := range reserved {
		reservedMap[r] = struct{}{}
	}

	for k, v := range args {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if _, ok := reservedMap[k]; ok {
			continue
		}
		out[k] = v
	}
	return out
}

// collapseUniqueKeys implements the "Smart Collapsing" logic.
// It iterates through the map, identifies unique suffixes (e.g. "id" from "users.id"),
// and creates non-prefixed entry if the suffix is unique across the map.
func collapseUniqueKeys(m map[string]any) map[string]any {
	// 1. Analyze frequency of suffixes
	suffixCount := make(map[string]int)

	for k := range m {
		suffix := k
		if idx := strings.Index(k, "."); idx != -1 {
			suffix = k[idx+1:]
		}
		// Only count valid identifiers
		if suffix != "" {
			suffixCount[suffix]++
		}
	}

	// fmt.Printf("DEBUG: Suffix Counts for keys %v: %v\n", getKeys(m), suffixCount)

	// 2. Add keys
	// We operate in place since flattenItem already gave us a fresh map
	for k, v := range m {
		// Only alias keys (with dot) are candidates for collapsing
		if idx := strings.Index(k, "."); idx != -1 {
			suffix := k[idx+1:]

			// Criteria:
			// 1. Suffix is unique across all keys (count == 1)
			// 2. The key doesn't already exist (though count==1 implies it, unless it's the SAME key which is impossible for dot vs no-dot)
			if suffixCount[suffix] == 1 {
				// Safety check: Don't overwrite existing
				if _, exists := m[suffix]; !exists {
					m[suffix] = v
					// STRIP STRATEGY: Remove the verbose prefixed key to keep output friendly (non-redundant)
					delete(m, k)
				}
			}
		}
	}

	return m
}

// parseSlashCommand parses a command string like "tool_name key1=value1 key2=\"value with spaces\""

// CompareLoose performs a loose comparison of two values, handling mixed numeric types.
// It leverages btree.Compare for strict same-type comparisons but promotes mixed numeric types
// to float64 for correct relational ordering (e.g., 9 < 10.0).
func CompareLoose(a any, b any) int {
	// 1. Try strict comparison first if types match?
	// Actually no, primitive vs float mismatch is mostly what we care about.
	// But let's check for "IsNumber" on both sides.

	isNumA := isNumber(a)
	isNumB := isNumber(b)

	if isNumA && isNumB {
		fA, _ := coerceToFloatFull(a)
		fB, _ := coerceToFloatFull(b)
		if fA < fB {
			return -1
		} else if fA > fB {
			return 1
		}
		return 0
	} else if isNumA && !isNumB {
		// Number vs String/Other -> Fallback to string
	} else if !isNumA && isNumB {
		// String/Other vs Number -> Fallback to string
	}

	// Default to btree.Compare (which does strict type match or string fallback)
	return btree.Compare(a, b)
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}

// Helper for Join Numeric Coercion (Exposed here or duplicate from join_execution if private)
// Since it was private in join_execution, we'll redefine/ensure it is available.
// Actually, let's just use a simple one here.
func coerceToFloatFull(v any) (float64, bool) {
	switch val := v.(type) {
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	case float32:
		return float64(val), true
	case float64:
		return val, true
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
