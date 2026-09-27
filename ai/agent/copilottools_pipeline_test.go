package agent

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

func TestJoinPipeline_ExplicitInto(t *testing.T) {
	// 1. Setup Engine
	ctx := context.Background()
	dbName := "test_pipeline_db"
	dbPath := fmt.Sprintf("/tmp/%s_%d", dbName, time.Now().UnixNano())
	defer os.RemoveAll(dbPath)

	resolver := func(name string) (Database, error) {
		if name == dbName || name == "temp1" || name == "temp2" {
			return database.NewDatabase(sop.DatabaseOptions{
				StoresFolders: []string{dbPath},
			}), nil
		}
		return nil, fmt.Errorf("db not found: %s", name)
	}

	engine := NewScriptEngine(NewScriptContext(), resolver)

	// Create DB and Tx
	db, _ := resolver(dbName)
	engine.Context.Databases[dbName] = db
	tx, err := engine.BeginTx(ctx, map[string]any{"name": "tx1", "database": dbName, "mode": "write"})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	engine.Context.Transactions["tx1"] = tx
	defer engine.RollbackTx(ctx, map[string]any{"name": "tx1"})

	// Setup Data:
	// Store A: 10 items
	// Store B: 10 items (matches A)
	// Store C: 5 items (matches A 0-4)

	createStore := func(name string, count int, prefix string) {
		// Pass database explicitly
		s, err := engine.OpenStore(ctx, map[string]any{
			"name":        name,
			"create":      true,
			"transaction": "tx1",
			"database":    dbName,
		})
		if err != nil {
			t.Fatalf("OpenStore %s failed: %v", name, err)
		}
		for i := 0; i < count; i++ {
			s.Add(ctx, fmt.Sprintf("k%d", i), map[string]any{
				"id":  i,
				"val": fmt.Sprintf("%s%d", prefix, i),
			})
		}
		// Register in context for easier access by variable name if needed
		engine.Context.Stores[name] = s
	}

	createStore("storeA", 10, "A")
	createStore("storeB", 10, "B")
	createStore("storeC", 5, "C")

	// EXECUTE PIPELINE

	// Step 1: Join A and B, dump into 'temp1'
	// input: StoreA (as cursor)
	// args: with=storeB, type=inner, on={id:id}, into=temp1

	// Prepare input cursor for A
	step1Input, err := engine.Scan(ctx, map[string]any{"store": "storeA"}, nil)
	if err != nil {
		t.Fatalf("Scan A failed: %v", err)
	}

	step1Res, err := engine.Join(ctx, step1Input, map[string]any{
		"with":        "storeB",
		"type":        "inner",
		"on":          map[string]any{"id": "id"},
		"into":        "temp1",
		"transaction": "tx1", // Needed for OpenStore("temp1")
	})
	if err != nil {
		t.Fatalf("Step 1 Join failed: %v", err)
	}

	// Verify step1Res is a StoreAccessor
	temp1, ok := step1Res.(jsondb.StoreAccessor)
	if !ok {
		t.Fatalf("Step 1 did not return a StoreAccessor")
	}
	engine.Context.Stores["temp1"] = temp1 // Register for next step

	// Step 2: Join C with temp1 (Right Join), dump into 'temp2'
	// effectively: temp1 Right Join C
	// input: temp1 (as cursor)
	// args: with=storeC, type=right, on={id:id}, into=temp2
	// But note: JoinRight expects Input=Left(Lookup), Store=Right(Driver)
	// Right Join means: Driver=StoreC, Lookup=temp1
	// result = StoreC matches temp1

	// Scan temp1
	step2Input, err := engine.Scan(ctx, map[string]any{"store": "temp1"}, nil)
	if err != nil {
		t.Fatalf("Scan temp1 failed: %v", err)
	}

	// Join
	step2Res, err := engine.Join(ctx, step2Input, map[string]any{
		"with":        "storeC",
		"type":        "right",
		"on":          map[string]any{"id": "id"},
		"into":        "temp2",
		"transaction": "tx1",
	})
	if err != nil {
		t.Fatalf("Step 2 Join failed: %v", err)
	}

	temp2, ok := step2Res.(jsondb.StoreAccessor)
	if !ok {
		t.Fatalf("Step 2 did not return a StoreAccessor")
	}

	// Verify contents of temp2
	// C has 5 items (0-4). temp1 has 10 items (0-9) joined A+B.
	// Matching C(0-4) with temp1(0-9) should yield 5 items.

	count := 0
	ok, err = temp2.First(ctx)
	for ok && err == nil {
		count++
		k := temp2.GetCurrentKey()
		v, _ := temp2.GetCurrentValue(ctx)
		t.Logf("Final Result: %v = %v", k, v)
		ok, err = temp2.Next(ctx)
	}

	if count != 5 {
		t.Errorf("Expected 5 results, got %d", count)
	}
}
