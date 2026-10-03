# API Integration Guide: Database & Space APIs with HTTP Endpoints

## Business Context: Understanding Stores vs Spaces

Before diving into integration, understand SOP's dual-system architecture:

### Stores (Database API)
**Purpose**: Traditional B-Tree storage for structured business data  
**Use For**: Orders, users, transactions, inventory, bulk operations, joins  
**API Files**: `api_types.go`, `api_core.go`, `api_bulk.go`, `api_transaction.go`

### Spaces (Space API)  
**Purpose**: AI-powered knowledge bases with semantic search  
**Use For**: LLM outputs, embeddings, documents, RAG applications, semantic search  
**API Files**: `api_space_types.go`, `api_space.go`, `api_space_bulk.go`

**Revolutionary Architecture**: SOP Spaces use **relativity-based architecture** (not K-means clustering like Pinecone/Weaviate), solving the pigeon hole problem and enabling **full visual management** through KnowledgeBase Studio - something architecturally impossible for traditional vector databases.

**SOP is unique** in providing both Stores and Spaces with unified ACID transactions. See [STORES_VS_SPACES.md](STORES_VS_SPACES.md) for architectural comparison.

---

## Integration Overview

This document explains how the **typed Database API** and **typed Space API** integrate with SOP's existing HTTP REST endpoints and transaction layer.

## Architecture Layers

```
┌─────────────────────────────────────────────────────────────────┐
│                    HTTP REST Endpoints                           │
│              (tools/httpserver/main.go)                          │
│  /api/store/add, /api/store/update, /api/spaces/item/add, etc. │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ├─ Direct Database Operations
                            │  (Current: database.BeginTransaction → Store ops)
                            │
                            ├─ NEW: Typed API Integration
                            │  (agent.Add(), agent.BulkUpsertItems(), etc.)
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              First-Class Typed API Layer                         │
│           (ai/agent/api_*.go, api_space*.go)                    │
│                                                                   │
│  ┌─────────────────────────┐  ┌───────────────────────────────┐│
│  │   Database API          │  │      Space API                ││
│  │   - Add/Update/Delete   │  │   - UpsertCategory/Item       ││
│  │   - BulkAdd/Update/Del  │  │   - BulkUpsertCategories      ││
│  │   - Transaction Mgmt    │  │   - VectorizeSpace            ││
│  └─────────────────────────┘  └───────────────────────────────┘│
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                  SOP Transaction Layer                           │
│           (Shared by both Stores and Spaces)                     │
│                                                                   │
│  • BeginTransaction(ctx, ForReading/ForWriting)                 │
│  • Commit(ctx) / Rollback(ctx)                                  │
│  • TransactionHandle management                                 │
│  • ACID guarantees across B-Trees                               │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Storage Engine (B-Trees)                        │
│                                                                   │
│  ┌──────────────────┐             ┌─────────────────────────┐  │
│  │  Store B-Trees   │             │  Space B-Trees          │  │
│  │  (jsondb stores) │             │  (memory.KnowledgeBase) │  │
│  └──────────────────┘             └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

## Transaction Layer: Unified Control

Both **Database API** (Stores) and **Space API** (KnowledgeBases) share the **same SOP transaction layer**. This provides:

### Transaction Modes

| Mode | Description | Best For | Auto-Commit |
|------|-------------|----------|-------------|
| **single** | ONE transaction for all operations | <10K items, atomicity required | ✅ Yes (at end) |
| **explicit** | Use provided transaction handle | Multi-operation workflows | ❌ No (caller manages) |
| **auto_batch** | Transaction per batch (Database API only) | 10K+ items, scalability | ✅ Yes (per batch) |

### Transaction Lifecycle

```go
// Explicit Transaction (Shared across Database + Space operations)
txHandle, _ := agent.BeginTransaction(ctx, TransactionArgs{
    Database: "dev_db",
    Mode:     "write",
})

// 1. Database Operations
agent.Add(ctx, AddArgs{
    Database:      "dev_db",
    Store:         "users",
    Key:           "user1",
    Value:         map[string]any{"name": "Alice"},
    TransactionID: txHandle.ID,
})

// 2. Space Operations (SAME TRANSACTION)
agent.UpsertCategory(ctx, UpsertCategoryArgs{
    Database:      "dev_db",
    KBName:        "Notes",
    Parameter:     memory.UpsertCategoryParam{...},
    TransactionID: txHandle.ID,
})

// 3. Commit (applies both Database and Space changes atomically)
agent.CommitTransaction(ctx, TransactionCommitArgs{
    TransactionID: txHandle.ID,
})
```

## Integration Patterns

### Pattern 1: Direct HTTP Endpoint Usage

**Current Implementation** (handleAddItem):
```go
func handleAddItem(w http.ResponseWriter, r *http.Request) {
    // Parse request
    var req struct {
        Database  string `json:"database"`
        StoreName string `json:"store"`
        Key       any    `json:"key"`
        Value     any    `json:"value"`
    }
    json.NewDecoder(r.Body).Decode(&req)
    
    // Direct database operations
    trans, _ := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
    defer trans.Rollback(ctx)
    
    store, _ := jsondb.OpenStore(ctx, dbOpts, req.StoreName, trans)
    store.Add(ctx, req.Key, req.Value)
    trans.Commit(ctx)
}
```

**NEW: Typed API Integration**:
```go
func handleAddItem(w http.ResponseWriter, r *http.Request) {
    // Parse request
    var req AddArgs
    json.NewDecoder(r.Body).Decode(&req)
    
    // Use typed API (benefits: validation, type safety, transaction modes)
    agentSvc := getAgentForDatabase(req.Database)
    result, err := agentSvc.Add(ctx, req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": result})
}
```

**Benefits**:
- ✅ Type validation at API boundary
- ✅ Consistent error handling
- ✅ Transaction mode support
- ✅ OpenAPI schema generation
- ✅ Metrics collection (BulkOperationResult)

### Pattern 2: Bulk Operation Endpoints

**NEW: Bulk Endpoint for Categories**:
```go
func handleBulkUpsertCategories(w http.ResponseWriter, r *http.Request) {
    var req BulkUpsertCategoriesArgs
    json.NewDecoder(r.Body).Decode(&req)
    
    agentSvc := getAgentForDatabase(req.Database)
    result, err := agentSvc.BulkUpsertCategories(ctx, req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Result includes: Success, Processed, Failed, Duration, ItemsPerSecond
    json.NewEncoder(w).Encode(result)
}
```

**Register in main.go**:
```go
// Add to main.go route registration
http.HandleFunc("/api/spaces/categories/bulk", handleBulkUpsertCategories)
http.HandleFunc("/api/spaces/categories/bulk/delete", handleBulkDeleteCategories)
http.HandleFunc("/api/spaces/items/bulk", handleBulkUpsertItems)
http.HandleFunc("/api/spaces/items/bulk/delete", handleBulkDeleteItems)
http.HandleFunc("/api/store/items/bulk", handleBulkAddItems)
http.HandleFunc("/api/store/items/bulk/update", handleBulkUpdateItems)
http.HandleFunc("/api/store/items/bulk/delete", handleBulkDeleteItems)
```

### Pattern 3: AI Agent Tool Integration

The typed APIs are the **primary implementation layer** for LLM tools. The existing tool layer becomes a thin adapter:

**Before (copilottools.go)**:
```go
func (a *CopilotAgent) toolAdd(ctx context.Context, args map[string]any) (string, error) {
    // Complex validation, type conversion, error handling...
    db, _ := getDB(args["database"])
    tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
    defer tx.Rollback(ctx)
    
    store, _ := jsondb.OpenStore(ctx, db.Options(), args["store"], tx)
    _, err := store.Add(ctx, args["key"], args["value"])
    tx.Commit(ctx)
    return "Added.", err
}
```

**After (using typed API)**:
```go
func (a *CopilotAgent) toolAdd(ctx context.Context, args map[string]any) (string, error) {
    var typed AddArgs
    mapToStruct(args, &typed)
    
    result, err := a.Add(ctx, typed)  // Delegate to typed API
    return result, err
}
```

**Benefits**:
- ✅ Tools become thin adapters (5-10 lines vs 50+ lines)
- ✅ Business logic lives in typed API (single source of truth)
- ✅ Easier to test, maintain, and evolve
- ✅ HTTP endpoints and AI tools share same implementation

### Pattern 4: Cross-Domain Transactions

**Atomic Multi-Operation Workflow**:
```go
// BEGIN: Start transaction
txHandle, _ := agent.BeginTransaction(ctx, TransactionArgs{
    Database: "dev_db",
    Mode:     "write",
})

defer func() {
    if err != nil {
        agent.RollbackTransaction(ctx, TransactionRollbackArgs{
            TransactionID: txHandle.ID,
        })
    }
}()

// 1. Add data to Store
_, err = agent.Add(ctx, AddArgs{
    Database:      "dev_db",
    Store:         "products",
    Key:           "prod123",
    Value:         productData,
    TransactionID: txHandle.ID,
})

// 2. Index in Space
_, err = agent.UpsertItem(ctx, UpsertItemArgs{
    Database:      "dev_db",
    KBName:        "ProductCatalog",
    Parameter:     itemParam,
    TransactionID: txHandle.ID,
})

// 3. Update metadata in another Store
_, err = agent.Update(ctx, UpdateArgs{
    Database:      "dev_db",
    Store:         "metadata",
    Key:           "last_sync",
    Value:         map[string]any{"timestamp": time.Now()},
    TransactionID: txHandle.ID,
})

// COMMIT: All or nothing
agent.CommitTransaction(ctx, TransactionCommitArgs{
    TransactionID: txHandle.ID,
})
```

## Implementation Checklist

### Phase 1: Database API Endpoints (Stores)
- [ ] `POST /api/store/add` → Use `agent.Add()`
- [ ] `POST /api/store/update` → Use `agent.Update()`
- [ ] `POST /api/store/delete` → Use `agent.Delete()`
- [ ] `POST /api/store/items/bulk` → Use `agent.BulkAdd()`
- [ ] `POST /api/store/items/bulk/update` → Use `agent.BulkUpdate()`
- [ ] `POST /api/store/items/bulk/delete` → Use `agent.BulkDelete()`
- [ ] `POST /api/transaction/begin` → Use `agent.BeginTransaction()`
- [ ] `POST /api/transaction/commit` → Use `agent.CommitTransaction()`
- [ ] `POST /api/transaction/rollback` → Use `agent.RollbackTransaction()`

### Phase 2: Space API Endpoints
- [ ] `POST /api/spaces/category/add` → Use `agent.UpsertCategory()`
- [ ] `POST /api/spaces/category/delete` → Use `agent.DeleteCategory()`
- [ ] `POST /api/spaces/categories/bulk` → Use `agent.BulkUpsertCategories()`
- [ ] `POST /api/spaces/categories/bulk/delete` → Use `agent.BulkDeleteCategories()`
- [ ] `POST /api/spaces/item/add` → Use `agent.UpsertItem()`
- [ ] `POST /api/spaces/item/delete` → Use `agent.DeleteItem()`
- [ ] `POST /api/spaces/items/bulk` → Use `agent.BulkUpsertItems()`
- [ ] `POST /api/spaces/items/bulk/delete` → Use `agent.BulkDeleteItems()`
- [ ] `POST /api/spaces/vectorize` → Use `agent.VectorizeSpace()`
- [ ] `POST /api/spaces/categories/vectorize` → Use `agent.BulkVectorizeCategories()`
- [ ] `POST /api/spaces/items/vectorize` → Use `agent.BulkVectorizeItems()`

### Phase 3: Agent Tool Adapters
- [ ] Refactor `toolAdd` → delegate to `agent.Add()`
- [ ] Refactor `toolUpdate` → delegate to `agent.Update()`
- [ ] Refactor `toolDelete` → delegate to `agent.Delete()`
- [ ] Refactor `toolUpsertCategories` → delegate to `agent.BulkUpsertCategories()`
- [ ] Refactor `toolDeleteCategories` → delegate to `agent.BulkDeleteCategories()`
- [ ] Refactor `toolUpsertItems` → delegate to `agent.BulkUpsertItems()`
- [ ] Refactor `toolDeleteItems` → delegate to `agent.BulkDeleteItems()`

### Phase 4: Documentation & Testing
- [ ] Update OpenAPI spec with all new endpoints
- [ ] Create integration tests for cross-domain transactions
- [ ] Update AI_COPILOT_USAGE.md with transaction examples
- [ ] Add performance benchmarks for bulk operations

## Benefits Summary

### For HTTP Endpoints
- **Type Safety**: JSON schemas validated at API boundary
- **Consistency**: Same validation/error handling across all endpoints
- **Performance Metrics**: Built-in timing and throughput tracking
- **Transaction Modes**: Single, explicit, auto_batch support
- **Observability**: Structured results with error details

### For AI Agents
- **Simplified Tools**: Tools become thin adapters (90% code reduction)
- **Maintainability**: Business logic in one place
- **Consistency**: Same behavior via HTTP or AI tools
- **Schema Reuse**: OpenAPI schemas → LLM guidance (93% reduction)

### For Developers
- **Single Source of Truth**: Typed API is canonical implementation
- **Testability**: Test typed API, both HTTP and AI benefit
- **Evolution**: Add features once, available everywhere
- **Documentation**: OpenAPI + godoc auto-generate
- **Cross-Domain**: Unified transaction model for Stores + Spaces

## Example: End-to-End Integration

### HTTP Client Request
```bash
curl -X POST http://localhost:3030/api/store/items/bulk \
  -H "Content-Type: application/json" \
  -d '{
    "database": "dev_db",
    "store": "users",
    "items": [
      {"key": "user1", "value": {"name": "Alice", "role": "admin"}},
      {"key": "user2", "value": {"name": "Bob", "role": "user"}}
    ],
    "transaction_mode": "single"
  }'

# Response
{
  "success": true,
  "processed": 2,
  "failed": 0,
  "duration": "15ms",
  "metrics": {
    "total_items": 2,
    "items_per_second": 133.33,
    "avg_item_time": "7.5ms"
  }
}
```

### AI Agent Interaction
```
User: Add two users to the dev_db database: Alice (admin) and Bob (user)

Agent: [Calls bulk_add tool with transaction_mode=single]

Result: ✅ Added 2 users successfully in 15ms (133 items/sec)
```

**Both paths use the same typed API implementation!**

## Migration Strategy

1. **Phase 1**: Implement typed APIs (✅ Complete for Database, ✅ Complete for Space)
2. **Phase 2**: Create new HTTP endpoints using typed APIs
3. **Phase 3**: Refactor existing endpoints to use typed APIs
4. **Phase 4**: Convert LLM tools to thin adapters
5. **Phase 5**: Deprecate old direct database operations in endpoints

## Next Steps

See [API_ARCHITECTURE.md](API_ARCHITECTURE.md) for complete API reference.
See [README_API.md](README_API.md) for quick start guide.
