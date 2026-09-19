package agent

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
)

// TestJoinPerformance_SmallDataset tests join performance with small dataset
// This reproduces the original LLM-generated query scenario
func TestJoinPerformance_SmallDataset(t *testing.T) {
	ctx := context.Background()

	// Setup session payload
	payload := &ai.SessionPayload{
		CurrentDB: "test_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	agent, cleanup := setupTestAgent(t)
	defer cleanup()

	// Get database options for store creation
	dbOpts := agent.databases["test_db"]

	// Setup: Create stores
	usersStore := "users"
	ordersStore := "orders"
	createTestStore(t, dbOpts, usersStore)
	createTestStore(t, dbOpts, ordersStore)

	// Users store: 100 users
	for i := 1; i <= 100; i++ {
		_, err := agent.Add(ctx, AddArgs{
			Database: "test_db",
			Store:    usersStore,
			Key:      i,
			Value: map[string]any{
				"user_id": i,
				"name":    fmt.Sprintf("User%d", i),
				"email":   fmt.Sprintf("user%d@example.com", i),
			},
		})
		if err != nil {
			t.Fatalf("Failed to add user %d: %v", i, err)
		}
	}

	// Orders store: 500 orders (5 orders per user on average)
	orderID := 1
	for userID := 1; userID <= 100; userID++ {
		for j := 0; j < 5; j++ {
			_, err := agent.Add(ctx, AddArgs{
				Database: "test_db",
				Store:    ordersStore,
				Key:      orderID,
				Value: map[string]any{
					"order_id":     orderID,
					"user_id":      userID,
					"total_amount": float64((orderID * 10) % 1000),
					"status":       "completed",
				},
			})
			if err != nil {
				t.Fatalf("Failed to add order %d: %v", orderID, err)
			}
			orderID++
		}
	}

	t.Logf("Setup complete: %d users, %d orders", 100, 500)

	// Test: Execute join query (reproduces LLM scenario)
	start := time.Now()
	result, err := agent.Join(ctx, JoinArgs{
		Database:        "test_db",
		LeftStore:       usersStore,
		RightStore:      ordersStore,
		LeftJoinFields:  []string{"user_id"},
		RightJoinFields: []string{"user_id"},
		JoinType:        "inner",
		Limit:           10,
	})
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	t.Logf("Join completed in %v", duration)
	t.Logf("Result: %s", result)

	// Performance assertion: Should complete in reasonable time
	if duration > 2*time.Second {
		t.Errorf("Join took too long: %v (expected < 2s for 100 users + 500 orders)", duration)
	}
}

// TestBulkAdd_Performance tests bulk add performance with typed API
func TestBulkAdd_Performance(t *testing.T) {
	ctx := context.Background()

	// Setup session payload
	payload := &ai.SessionPayload{
		CurrentDB: "test_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	agent, cleanup := setupTestAgent(t)
	defer cleanup()

	// Get database options for store creation
	dbOpts := agent.databases["test_db"]

	// Create bulk_test store
	createTestStore(t, dbOpts, "bulk_test")

	// Prepare 1000 items
	items := make([]BulkItem, 1000)
	for i := 0; i < 1000; i++ {
		items[i] = BulkItem{
			Key: i,
			Value: map[string]any{
				"name":  fmt.Sprintf("Item%d", i),
				"value": i * 10,
			},
		}
	}

	// Test auto_batch mode
	start := time.Now()
	result, err := agent.BulkAdd(ctx, BulkAddArgs{
		Database:        "test_db",
		Store:           "bulk_test",
		Items:           items,
		TransactionMode: TransactionModeAutoBatch,
		BatchSize:       100,
	})
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("BulkAdd failed: %v", err)
	}

	t.Logf("BulkAdd (auto_batch, 1000 items): %v", duration)
	t.Logf("Result: Processed=%d, Failed=%d, Success=%v", result.Processed, result.Failed, result.Success)

	if result.Processed != 1000 {
		t.Errorf("Expected 1000 items processed, got %d", result.Processed)
	}

	if !result.Success {
		t.Errorf("Expected success=true, got false with %d failures", result.Failed)
	}
}

// TestBulkAdd_TransactionModes tests all three transaction modes
func TestBulkAdd_TransactionModes(t *testing.T) {
	tests := []struct {
		name  string
		mode  TransactionMode
		items int
	}{
		{"auto_batch_100", TransactionModeAutoBatch, 100},
		{"single_100", TransactionModeSingle, 100},
		{"auto_batch_1000", TransactionModeAutoBatch, 1000},
		{"single_1000", TransactionModeSingle, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Setup session payload
			payload := &ai.SessionPayload{
				CurrentDB: "test_db",
			}
			ctx = context.WithValue(ctx, SessionPayloadKey, payload)

			agent, cleanup := setupTestAgent(t)
			defer cleanup()

			// Get database options and create store
			dbOpts := agent.databases["test_db"]
			storeName := fmt.Sprintf("test_%s", tt.name)
			createTestStore(t, dbOpts, storeName)

			items := make([]BulkItem, tt.items)
			for i := 0; i < tt.items; i++ {
				items[i] = BulkItem{
					Key: i,
					Value: map[string]any{
						"data": fmt.Sprintf("data_%d", i),
					},
				}
			}

			start := time.Now()
			result, err := agent.BulkAdd(ctx, BulkAddArgs{
				Database:        "test_db",
				Store:           storeName,
				Items:           items,
				TransactionMode: tt.mode,
				BatchSize:       100,
			})
			duration := time.Since(start)

			if err != nil {
				t.Errorf("BulkAdd failed: %v", err)
			}

			t.Logf("%s: %v (Processed=%d, Failed=%d)",
				tt.name, duration, result.Processed, result.Failed)

			if result.Processed != tt.items {
				t.Errorf("Expected %d processed, got %d", tt.items, result.Processed)
			}
		})
	}
}

// BenchmarkJoin_InnerJoin benchmarks inner join performance
func BenchmarkJoin_InnerJoin(b *testing.B) {
	ctx := context.Background()

	// Setup session payload
	payload := &ai.SessionPayload{
		CurrentDB: "test_db",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	agent, cleanup := setupTestAgent(b)
	defer cleanup()

	// Get database options and create stores
	dbOpts := agent.databases["test_db"]
	usersStore := "bench_users"
	ordersStore := "bench_orders"
	createTestStore(b, dbOpts, usersStore)
	createTestStore(b, dbOpts, ordersStore)

	// Setup test data once
	// 100 users
	for i := 1; i <= 100; i++ {
		agent.Add(ctx, AddArgs{
			Database: "test_db",
			Store:    usersStore,
			Key:      i,
			Value: map[string]any{
				"user_id": i,
				"name":    fmt.Sprintf("User%d", i),
			},
		})
	}

	// 500 orders
	orderID := 1
	for userID := 1; userID <= 100; userID++ {
		for j := 0; j < 5; j++ {
			agent.Add(ctx, AddArgs{
				Database: "test_db",
				Store:    ordersStore,
				Key:      orderID,
				Value: map[string]any{
					"order_id":     orderID,
					"user_id":      userID,
					"total_amount": float64(orderID * 10),
				},
			})
			orderID++
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := agent.Join(ctx, JoinArgs{
			Database:        "test_db",
			LeftStore:       usersStore,
			RightStore:      ordersStore,
			LeftJoinFields:  []string{"user_id"},
			RightJoinFields: []string{"user_id"},
			JoinType:        "inner",
			Limit:           10,
		})
		if err != nil {
			b.Fatalf("Join failed: %v", err)
		}
	}
}

// Helper function to setup test agent with in-memory database
func setupTestAgent(t testing.TB) (*CopilotAgent, func()) {
	t.Helper()

	// Create temporary test directory
	dbPath := fmt.Sprintf("test_perf_%d", time.Now().UnixNano())
	os.RemoveAll(dbPath)

	// Create database options
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create the database
	db := database.NewDatabase(dbOpts)

	// Create agent with test database and registry
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"test_db": dbOpts,
		},
		systemDB: db,
	}

	// Initialize registry
	agent.registry = NewRegistry()

	cleanup := func() {
		// Cleanup test directory
		os.RemoveAll(dbPath)
	}

	return agent, cleanup
}

// Helper to create a store in test database
func createTestStore(t testing.TB, dbOpts sop.DatabaseOptions, storeName string) {
	t.Helper()
	ctx := context.Background()

	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create B-tree store with primitive key
	if _, err := core_database.NewBtree[int, any](ctx, dbOpts, storeName, tx, nil, sop.StoreOptions{
		Name:           storeName,
		SlotLength:     10,
		IsPrimitiveKey: true,
	}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("NewBtree %s failed: %v", storeName, err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation of %s failed: %v", storeName, err)
	}
}
