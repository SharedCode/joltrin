package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestInferSchemaOfFirst_UsesFirstRowShape(t *testing.T) {
	bt := &Btree[any, any]{
		StoreInfo: &sop.StoreInfo{
			Count:     0,
			Schema:    map[string]string{"order_date": "datetime"},
			KeyFields: []string{"order_date"},
		},
	}

	key := any(map[string]any{"order_date": "2026-06-14T00:00:00Z"})
	value := any(map[string]any{"amount": 42})
	item := &Item[any, any]{
		Key:   key,
		Value: &value,
	}

	bt.inferSchemaOfFirst(item)

	if got := bt.StoreInfo.Schema["order_date"]; got != "string" {
		t.Fatalf("expected first-row inference to capture the runtime shape, got %q", got)
	}
}

func TestInferSchemaFromTypes_UsesStructKeyFields(t *testing.T) {
	type keyStruct struct {
		C1 string `json:"c1"`
		C2 int64  `json:"c2"`
	}

	type valueStruct struct {
		Name string `json:"name"`
	}

	result := sop.InferSchemaFromTypes(keyStruct{}, valueStruct{})

	if got := result.Schema["c1"]; got != "string" {
		t.Fatalf("expected schema field c1 to infer as string, got %q", got)
	}
	if got := result.Schema["c2"]; got != "number" {
		t.Fatalf("expected schema field c2 to infer as number, got %q", got)
	}

	if len(result.KeyFields) != 2 || result.KeyFields[0] != "c1" || result.KeyFields[1] != "c2" {
		t.Fatalf("expected key fields [c1 c2], got %+v", result.KeyFields)
	}
}
