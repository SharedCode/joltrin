package sop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SchemaInferenceResult contains flat schema with field lists for LLM understanding.
type SchemaInferenceResult struct {
	// Flat schema without prefixes for LLM correlation with Relations
	Schema map[string]string
	// Fields that belong to the Key
	KeyFields []string
	// Fields that belong to the Value
	ValueFields []string
}

// InferSchemaFromTypes uses reflection to directly inspect the types of key and value.
// Returns flat schema format without prefixes for better LLM understanding and correlation with Relations.
func InferSchemaFromTypes(key any, value any) SchemaInferenceResult {
	result := SchemaInferenceResult{
		Schema:      make(map[string]string),
		KeyFields:   []string{},
		ValueFields: []string{},
	}

	// Infer key schema.
	// For runtime map-backed keys, inspect the actual map contents instead of
	// collapsing them to the generic "key:object" placeholder.
	if key != nil {
		keyVal := reflect.ValueOf(key)
		if keyVal.Kind() == reflect.Ptr {
			if !keyVal.IsNil() {
				keyVal = keyVal.Elem()
			} else {
				keyType := reflect.TypeOf(key).Elem()
				keySchema := reflectTypeToSchemaFlat(keyType)
				for k, v := range keySchema {
					result.Schema[k] = v
					result.KeyFields = append(result.KeyFields, k)
				}
				return result
			}
		}

		if keyVal.Kind() == reflect.Map {
			keySchema := reflectValueMapToSchemaFlat(keyVal)
			for k, v := range keySchema {
				result.Schema[k] = v
				result.KeyFields = append(result.KeyFields, k)
			}
		} else {
			keyType := reflect.TypeOf(key)
			if keyType.Kind() == reflect.Ptr {
				keyType = keyType.Elem()
			}
			keySchema := reflectTypeToSchemaFlat(keyType)
			for k, v := range keySchema {
				result.Schema[k] = v
				result.KeyFields = append(result.KeyFields, k)
			}
		}
	}

	// Infer value schema
	if value != nil {
		// Handle pointer to value (common in B-tree Item)
		val := reflect.ValueOf(value)
		if val.Kind() == reflect.Ptr {
			if !val.IsNil() {
				value = val.Elem().Interface()
				val = reflect.ValueOf(value)
			} else {
				// Nil pointer - inspect the type it points to
				valueType := reflect.TypeOf(value).Elem()
				valueSchema := reflectTypeToSchemaFlat(valueType)
				for k, v := range valueSchema {
					result.Schema[k] = v
					result.ValueFields = append(result.ValueFields, k)
				}
				return result
			}
		}

		// For map types with runtime values (like map[string]any), inspect the actual contents
		if val.Kind() == reflect.Map {
			valueSchema := reflectValueMapToSchemaFlat(val)
			for k, v := range valueSchema {
				result.Schema[k] = v
				result.ValueFields = append(result.ValueFields, k)
			}
		} else {
			// Use type-based inference for other types
			valueType := reflect.TypeOf(value)
			valueSchema := reflectTypeToSchemaFlat(valueType)
			for k, v := range valueSchema {
				result.Schema[k] = v
				result.ValueFields = append(result.ValueFields, k)
			}
		}
	}

	// Sort field lists for consistency
	sort.Strings(result.KeyFields)
	sort.Strings(result.ValueFields)

	return result
}

// reflectTypeToSchemaFlat converts a reflect.Type to a flat schema map without prefixes.
// This is used for the new FlatSchema format that's easier for LLMs to correlate with Relations.
func reflectTypeToSchemaFlat(t reflect.Type) map[string]string {
	schema := make(map[string]string)

	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		// Special case for UUID
		if t.PkgPath() == "github.com/sharedcode/joltrin/v5" && t.Name() == "UUID" {
			schema["key"] = "uuid"
			return schema
		}
		if t.PkgPath() == "github.com/google/uuid" && t.Name() == "UUID" {
			schema["key"] = "uuid"
			return schema
		}

		// Inspect struct fields without prefix
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)

			// Skip unexported fields
			if !field.IsExported() {
				continue
			}

			// Get field name from json tag if present, otherwise use field name
			fieldName := field.Name
			if jsonTag := field.Tag.Get("json"); jsonTag != "" {
				parts := strings.Split(jsonTag, ",")
				if parts[0] != "" && parts[0] != "-" {
					fieldName = parts[0]
				}
			}

			// Preserve the original field name from the struct tag or type definition.
			schema[fieldName] = reflectTypeToString(field.Type)
		}

	case reflect.Map:
		// For maps without runtime data, just mark as object
		schema["key"] = "object"

	case reflect.Slice, reflect.Array:
		schema["key"] = "list"

	default:
		// Primitive types - use "key" as the field name
		schema["key"] = reflectTypeToString(t)
	}

	return schema
}

// reflectValueMapToSchemaFlat inspects a map value at runtime without prefixes.
// This is used for the new FlatSchema format that's easier for LLMs to correlate with Relations.
func reflectValueMapToSchemaFlat(mapVal reflect.Value) map[string]string {
	schema := make(map[string]string)

	if !mapVal.IsValid() || mapVal.IsNil() {
		return schema
	}

	// Iterate over map keys
	iter := mapVal.MapRange()
	for iter.Next() {
		key := iter.Key()
		val := iter.Value()

		// Only handle string keys
		if key.Kind() == reflect.String {
			// Preserve the runtime map key exactly as provided by the caller.
			fieldName := key.String()
			schema[fieldName] = inferSchemaTypeFromValue(val)
		}
	}

	return schema
}

// inferSchemaTypeFromValue inspects a reflect.Value to determine its schema type.
func inferSchemaTypeFromValue(v reflect.Value) string {
	if !v.IsValid() {
		return "object"
	}

	// Unwrap interface
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return "object"
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		// Check if it's a UUID string
		if v.String() != "" {
			if _, err := uuid.Parse(v.String()); err == nil {
				return "uuid"
			}
		}
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "number"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Map:
		return "object"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Struct:
		// Check for UUID types
		if v.Type().PkgPath() == "github.com/sharedcode/joltrin/v5" && v.Type().Name() == "UUID" {
			return "uuid"
		}
		if v.Type().PkgPath() == "github.com/google/uuid" && v.Type().Name() == "UUID" {
			return "uuid"
		}
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return "datetime"
		}
		return "object"
	default:
		return "object"
	}
}

// reflectTypeToString converts a reflect.Type to a schema type string.
func reflectTypeToString(t reflect.Type) string {
	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "number"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Struct:
		// Check for UUID types
		if t.PkgPath() == "github.com/sharedcode/joltrin/v5" && t.Name() == "UUID" {
			return "uuid"
		}
		if t.PkgPath() == "github.com/google/uuid" && t.Name() == "UUID" {
			return "uuid"
		}
		if t == reflect.TypeOf(time.Time{}) {
			return "datetime"
		}
		return "object"
	case reflect.Map:
		return "object"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Interface:
		return "object"
	default:
		return "object"
	}
}

// FlattenForSchema converts key and value of any type into a flat map[string]any
// suitable for schema inference. Uses JSON marshaling to handle structs.
// NOTE: InferSchemaFromTypes is preferred when type information is available.
func FlattenForSchema(key any, value any) map[string]any {
	result := make(map[string]any)

	// Helper to flatten a single value into result map
	flatten := func(v any, isKey bool) {
		if v == nil {
			return
		}

		// Try to convert to map via JSON for structs
		vType := reflect.TypeOf(v)
		vKind := vType.Kind()

		// Handle primitives directly
		switch vKind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64,
			reflect.Bool, reflect.String:
			if isKey {
				result["key"] = v
			} else {
				result["value"] = v
			}
			return
		}

		// Handle UUID type
		if _, ok := v.(UUID); ok {
			if isKey {
				result["key"] = v
			} else {
				result["value"] = v
			}
			return
		}

		// Try JSON marshaling for structs and maps
		b, err := json.Marshal(v)
		if err == nil {
			var m map[string]any
			if err := json.Unmarshal(b, &m); err == nil {
				for k, val := range m {
					result[k] = val
				}
				return
			}
		}

		// Fallback: store as-is
		if isKey {
			result["key"] = v
		} else {
			result["value"] = v
		}
	}

	flatten(key, true)
	flatten(value, false)

	return result
}

// InferSchema inspects a map and returns a simplified type definition (e.g. {"id": "uuid", "age": "number"}).
func InferSchema(item map[string]any) map[string]string {
	schema := make(map[string]string)
	for k, v := range item {
		schema[k] = InferSchemaType(v)
	}
	return schema
}

// InferSchemaType returns a string representation of the type of a value for schema inference.
func InferSchemaType(v any) string {
	if v == nil {
		return "object"
	}

	switch val := v.(type) {
	case string:
		if _, err := uuid.Parse(val); err == nil {
			return "uuid"
		}
		return "string"
	case UUID:
		return "uuid"
	case uuid.UUID:
		return "uuid"
	case int, int64, int32, int8, int16, uint, uint64, uint32, uint8, uint16, float64, float32:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// FormatSchema formats a schema map as a sorted string like "{field: type, ...}".
func FormatSchema(schema map[string]string) string {
	var keys []string
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s: %s", k, schema[k]))
	}
	return fmt.Sprintf("{%s}", joinStrings(pairs, ", "))
}

// Helper to join strings
func joinStrings(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	res := items[0]
	for i := 1; i < len(items); i++ {
		res += sep + items[i]
	}
	return res
}
