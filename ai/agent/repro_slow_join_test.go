package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	aidatabase "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/database"
)

func TestReproSlowJoin(t *testing.T) {
	// 0. Cleanup from previous runs
	os.RemoveAll("./test_data/repro_slow_3")
	defer os.RemoveAll("./test_data/repro_slow_3")

	// 1. Setup Database
	dbName := "TestReproSlowJoinDB"
	t.Log("Initializing database...")

	// Create DB Options
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./test_data/repro_slow_3"},
	}

	cfg := Config{}
	dbs := map[string]sop.DatabaseOptions{
		dbName: dbOpts,
	}
	agent := NewCopilotAgent(cfg, dbs, nil)

	ctx := context.Background()
	payload := &ai.SessionPayload{
		CurrentDB: dbName,
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// Direct DB access for setup
	db := aidatabase.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Initialize Agent with Transaction and Tools
	payload.Transaction = tx
	if err := agent.Open(ctx); err != nil {
		t.Fatalf("Failed to open agent: %v", err)
	}

	// 2. Create Stores & Populate
	t.Log("Creating and populating stores...")

	soDept := sop.StoreOptions{
		Name:           "department",
		SlotLength:     10,
		IsPrimitiveKey: true,
		// Simulate Composite Index: region, department
		MapKeyIndexSpecification: `{"IndexFields": [{"FieldName": "region"}, {"FieldName": "department"}]}`,
	}
	// Note: We use string as Key, any as Value (since value is a map)
	dept, err := database.NewBtree[string, any](ctx, dbOpts, "department", tx, nil, soDept)
	if err != nil {
		t.Fatalf("Failed to create department store: %v", err)
	}

	soEmp := sop.StoreOptions{
		Name:           "employees",
		SlotLength:     10,
		IsPrimitiveKey: true,
	}
	emp, err := database.NewBtree[string, any](ctx, dbOpts, "employees", tx, nil, soEmp)
	if err != nil {
		t.Fatalf("Failed to create employees store: %v", err)
	}

	// Populating department
	for i := 0; i < 100; i++ {
		region := "US"
		if i%2 == 0 {
			region = "EU"
		}
		deptName := fmt.Sprintf("Dept%d", i)
		val := map[string]any{
			"region":     region,
			"department": deptName,
			"name":       fmt.Sprintf("Name%d", i),
		}
		if ok, err := dept.Add(ctx, fmt.Sprintf("%d", i), val); err != nil || !ok {
			t.Fatalf("Failed to add department: %v", err)
		}
	}

	// Populating employees
	for i := 0; i < 1000; i++ {
		region := "US"
		if i%2 == 0 {
			region = "EU"
		}
		deptName := fmt.Sprintf("Dept%d", i%100)
		val := map[string]any{
			"region":     region,
			"department": deptName,
			"name":       fmt.Sprintf("EmpName%d", i),
			"salary":     50000,
			"secrets":    "hidden_data", // Field that should NOT appear
		}
		if ok, err := emp.Add(ctx, fmt.Sprintf("%d", i), val); err != nil || !ok {
			t.Fatalf("Failed to add employee: %v", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit setup transaction: %v", err)
	}

	// 4. Run the problematic query via toolJoin
	// SQL: select a.*, b.name as employee from department a inner join employees b on a.region=b.region,a.department=b.department limit 4

	args := map[string]any{
		"left_store":        "department",
		"right_store":       "employees",
		"join_type":         "inner",
		"left_join_fields":  []string{"region", "department"},
		"right_join_fields": []string{"region", "department"},
		// Fields handling is tricky in toolJoin, it expects list of strings.
		// "a.*" implies all left fields. "b.name as employee" aliases.
		"fields": []string{"a.*", "b.name as employee"},
		"limit":  4,
	}

	t.Logf("Running join tool...")

	start := time.Now()

	// Set a timeout
	done := make(chan string)
	go func() {
		res, err := agent.Execute(ctx, "join", args)
		if err != nil {
			done <- fmt.Sprintf("Error: %v", err)
		} else {
			done <- res
		}
	}()

	select {
	case res := <-done:
		elapsed := time.Since(start)
		t.Logf("Query completed in %v", elapsed)
		t.Logf("Result: %s", res)

		// Assertions
		if strings.Contains(res, "secrets") {
			t.Errorf("FAIL: Result contains 'secrets' field which should have been filtered out by 'b.name'")
		}
	case <-time.After(10 * time.Second): // 10 seconds timeout
		t.Fatal("Query timed out! It is hanging.")
	}
}
