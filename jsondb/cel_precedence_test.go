package jsondb

import (
	"testing"

	"github.com/sharedcode/joltrin/v5/cel"
)

func TestCELPrecedenceOverIndexSpec(t *testing.T) {
	// 1. Define IndexSpec: Sort by "val" Ascending
	idxSpec := NewIndexSpecification([]IndexFieldSpecification{
		{FieldName: "val", AscendingSortOrder: true},
	})

	// 2. Define CEL Evaluator: Sort by "val" Descending
	// Logic: if a.val < b.val return 1 (Desc)
	//        if a.val > b.val return -1 (Desc)
	//        else return 0
	celExpr := "mapX['val'] < mapY['val'] ? 1 : mapX['val'] > mapY['val'] ? -1 : 0"
	eval, err := cel.NewEvaluator("test", celExpr)
	if err != nil {
		t.Fatalf("Failed to create CEL evaluator: %v", err)
	}

	// 3. Construct JsonDBMapKey with both
	j := JsonDBMapKey{
		indexSpecification: idxSpec,
		celEvaluator:       eval,
	}

	// 4. Test Data
	// A = 10, B = 20
	// IndexSpec (Asc): 10 < 20 -> -1
	// CEL (Desc): 10 < 20 -> 1
	a := map[string]any{"val": 10}
	b := map[string]any{"val": 20}

	// 5. Execute
	res := j.proxyComparer(a, b)

	// 6. Verify
	if res != 1 {
		t.Errorf("Expected CEL precedence (result 1), but got %d. (IndexSpec would return -1)", res)
	}
}
