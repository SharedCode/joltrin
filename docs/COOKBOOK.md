# API Cookbook

Simple, copy-pasteable examples for common SOP scenarios.

> **Note**: This cookbook focuses on the Go API. For Python examples, see the [Python Cookbook](../bindings/python/COOKBOOK.md). For Java examples, see the [Java Examples](../bindings/java/README.md#examples). For C# examples, see the [C# Examples](../bindings/csharp/Sop.CLI/). For Rust examples, see the [Rust Examples](../bindings/rust/examples/).

## Interoperability Note: Go vs. Other Languages

When using SOP in Go, you have two primary ways to interact with the database. **Both are first-class citizens with similar performance characteristics.** SOP leverages Go's unique ability to marshal JSON into either strongly-typed structs or dynamic maps, enabling high interoperability.

1.  **Direct Go Generics (Native)**:
    *   **Best for**: Pure Go applications where you want to use specific Go types directly.
    *   **Pros**: Strongly typed (e.g., `NewBtree[string, UserProfile]`), idiomatic Go code.
    *   **Interoperability**: **High**. Because SOP stores data as JSON, a Go struct can be read by Python as a dictionary (and vice versa).
    *   **Note**: If you need complex custom sorting across languages (e.g., "Sort by Age DESC, then Name ASC"), you should use `IndexSpecification`. While Direct Go can support this, `jsondb` makes it easier to configure.
    *   **Data Manager Compliance**: To make your Go-based stores fully compatible with the Data Manager (UI) for CRUD operations, providing a `MapKeyIndexSpecification` in `StoreOptions` is the only requirement. This tells the UI which fields exist and how to sort them, allowing you to keep using idiomatic Go structs & Generics while getting full UI support.

2.  **`jsondb` Package (Interop-Friendly)**:
    *   **Best for**: Scenarios requiring dynamic keys or complex `IndexSpecification` sorting rules shared across languages.
    *   **Pros**: Designed to slice keys into maps, enabling the fancy `IndexSpecification` custom sort ordering/indexing that other languages rely on.
    *   **Use Case**: A system where you need to enforce specific multi-field sorting rules that must be respected by Go, Python, C#, and Java clients alike.

## 1. Storing 100k User Profiles (`database`)

This example demonstrates the **Direct Go Generics** approach.

```go
package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

type UserProfile struct {
	ID    string
	Name  string
	Email string
}

func main() {
	// 1. Initialize Database Options
	// Standalone uses in-memory caching; Clustered uses Redis.
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"/tmp/sop_data"},
	}

	// 2. Start Transaction
	ctx := context.Background()
	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	// 3. Open/Create Store
	// Configure: Medium data size
	opts := sop.ConfigureStore("users", true, 1000, "User Profiles", sop.MediumData, "")
	store, err := database.NewBtree[string, UserProfile](ctx, dbOpts, "users", trans, nil, opts)
	if err != nil {
		panic(err)
	}

	// 4. Add Data
	for i := 0; i < 100000; i++ {
		id := fmt.Sprintf("user_%d", i)
		profile := UserProfile{ID: id, Name: "John Doe", Email: "john@example.com"}
		store.Add(ctx, id, profile)
		
		// Commit every 1000 items to keep memory usage low
		if i%1000 == 0 {
			trans.Commit(ctx)
			// Start new transaction
			trans, _ = db.BeginTransaction(ctx, sop.ForWriting)
			// Re-open store in new transaction
			store, _ = database.OpenBtree[string, UserProfile](ctx, db, "users", trans, nil)
		}
	}
	
	trans.Commit(ctx)
	fmt.Println("Done!")
}
```

> **Pro Tip**: Want to verify your data? Run the **SOP Data Manager** to inspect the `users` store visually:
> ```bash
> go run ./tools/httpserver -registry /tmp/sop_data
> ```

## 2. Multi-Store Transaction

Atomically update a "Bank Account" and a "Transaction Log" in the same transaction.

```go
func TransferFunds(ctx context.Context, db *database.Database, fromID, toID string, amount float64) error {
	// 1. Start Transaction
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 2. Open Stores
	accounts, _ := database.OpenBtree[string, Account](ctx, db, "accounts", trans, nil)
	logs, _ := database.OpenBtree[string, LogEntry](ctx, db, "logs", trans, nil)

	// 3. Deduct
	if found, _ := accounts.FindOne(ctx, fromID, true); found {
		fromAccount, _ := accounts.GetCurrentValue(ctx)
		fromAccount.Balance -= amount
		accounts.UpdateCurrentValue(ctx, fromAccount)
	}

	// 4. Add
	if found, _ := accounts.FindOne(ctx, toID, true); found {
		toAccount, _ := accounts.GetCurrentValue(ctx)
		toAccount.Balance += amount
		accounts.UpdateCurrentValue(ctx, toAccount)
	}

	// 5. Log
	logs.Add(ctx, uuid.NewString(), LogEntry{Action: "Transfer", Amount: amount})

	// 6. Commit (All or Nothing)
	return trans.Commit(ctx)
}
```

## 3. Using Streaming Data (Large Files)

For objects larger than 1MB (e.g., videos, high-res images), use the `streamingdata` package.

```go
import "github.com/sharedcode/joltrin/v5/streamingdata"

func UploadVideo(ctx context.Context, videoID string, data []byte) error {
	// Create a streaming store
	store := streamingdata.NewStore(ctx, "videos")
	
	// Create a writer (Encoder)
	writer, err := store.NewWriter(ctx, videoID)
	if err != nil {
		return err
	}
	
	// Write data (chunks are handled automatically)
	if _, err := writer.Write(data); err != nil {
		return err
	}
	
	// Close to flush and finalize
	return writer.Close()
}
```

## 4. Advanced: Building a Vector Database
SOP's B-Trees are perfect for storing vector embeddings and their metadata.

```go
// 1. Define your Vector Item
type VectorItem struct {
    ID        uuid.UUID
    Embedding []float32
    Content   string
}

// 2. Initialize Database
db := database.NewDatabase(sop.DatabaseOptions{
    Type:          sop.Standalone,
    StoresFolders: []string{"/tmp/sop_vectors"},
})

// 3. Start Transaction
trans, _ := db.BeginTransaction(ctx, sop.ForWriting)

// 4. Open Vector Store
// Note: VectorStore expects map[string]any payload by default in Database wrapper
store, _ := db.OpenVectorStore(ctx, "vector_store", trans, vector.Config{
    UsageMode: ai.Dynamic,
})

// 5. Insert Vectors
item := ai.Item[map[string]any]{
    ID:     uuid.NewString(),
    Vector: []float32{0.1, 0.2, 0.3},
    Payload: map[string]any{"content": "hello world"},
}
store.Upsert(ctx, item)

// 6. Commit
trans.Commit(ctx)
```

## 4. Text Search

Index and search text documents transactionally.

```go
package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/ai/database"
)

func main() {
	ctx := context.Background()
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"/tmp/sop_data"},
	})

	// 1. Index Documents
	t1, _ := db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ := db.OpenSearch(ctx, "articles", t1)
	
	idx.Add(ctx, "doc1", "The quick brown fox")
	idx.Add(ctx, "doc2", "jumps over the lazy dog")
	
	t1.Commit(ctx)

	// 2. Search
	t2, _ := db.BeginTransaction(ctx, sop.ForReading)
	idx, _ = db.OpenSearch(ctx, "articles", t2)
	
	results, _ := idx.Search(ctx, "fox")
	for _, r := range results {
		fmt.Printf("Doc: %s, Score: %f\n", r.DocID, r.Score)
	}
	t2.Commit(ctx)
}
```

## 5. Database & Store Creation: Best Practices

While SOP automatically creates database files on first use, explicitly managing your schema setup is recommended for production apps.

### Understanding the Hierarchy
1.  **Database**: A logical container (Folder on disk or Redis Namespace).
2.  **Store (B-Tree)**: A collection of Key-Value pairs (Table).
3.  **Item**: A single record.

### Creating a Database
SOP follows a "lazy creation" pattern. 
*   **Configuration**: You define `sop.DatabaseOptions` to point to a path (e.g., `/var/lib/sop/data`).
*   **Instantiation**: `database.NewDatabase(opts)` prepares the Go object.
*   **Physical Creation**: The data folders are only created when you open a Transaction and create your first Store.

### Designing Stores for Clarity
When creating stores, especially for AI usage, use descriptive names.
*   **Good**: `users`, `products`, `active_orders`
*   **Bad**: `store1`, `u_table`, `data`

```go
// Example: Explicit Store Creation with Options
func SetupSchema(ctx context.Context, db *database.Database) error {
    t, _ := db.BeginTransaction(ctx, sop.ForWriting)

    // 1. Users Store: Simple String Key
    database.NewBtree[string, User](ctx, db, "users", t, nil)

    // 2. Orders Store: UUID Key + Relation Metadata
    // We explicitly tell SOP that 'user_id' in this store points to 'id' in 'users'.
    orderOpts := sop.StoreOptions{
        Relations: []sop.Relation{
            {SourceFields: []string{"user_id"}, TargetStore: "users", TargetFields: []string{"id"}},
        },
    }
    database.NewBtree[uuid.UUID, Order](ctx, db, "orders", t, nil, orderOpts)
    
    // 3. Products Store: Custom Configuration 
    // (e.g., Small slot length for very large items)
    opts := sop.StoreOptions{
        SlotLength: 500, 
    }
    database.NewBtree[string, Product](ctx, db, "products", t, nil, opts)

    return t.Commit(ctx)
}
```

## 6. Modeling Relations

SOP uses **Relations Metadata** to define connections between stores. This metadata is sufficient for the Join Tool and AI Agents to navigate most relationships (One-to-One, One-to-Many, Many-to-One).

### 1. Relations (Metadata)
Use `StoreOptions.Relations` to register Foreign Keys. This allows the system to resolve lookups automatically using the indexed fields.

**Use Case:**
*   **1** (User) <-> **Many** (Orders): `Order` has `UserID`. The system uses this field to link the records.
*   **Many** (Orders) <-> **1** (User): `Order` has `UserID`. Lookup is direct.

*See Section 5 for the code example.*

### 2. Link Stores (Many-to-Many)
For **Many-to-Many** relationships (e.g., Students <-> Classes, where one Student has many Classes and one Class has many Students), you need a dedicated **Link Store**.

Since neither entity holds the foreign key of the other exclusively, a separate store (e.g., `student_classes`) is required to bridge them.

**How it works:**
1.  **Main Stores**: `students`, `classes`
2.  **Link Store**: `student_classes` (Key: `StudentID:ClassID` or `ClassID:StudentID`)

### Recommendations
1.  **Always define Relations Metadata**: It costs nothing and makes your app "AI-Ready".
2.  **Use Link Stores for M:N**: Only create explicit link stores when modeling complex Many-to-Many graphs.

### Code Examples (Metadata)
*   **Go (Native)**: [examples/relations_demo/main.go](../examples/relations_demo/main.go)
*   **Python**: [bindings/python/sop/examples/relations_demo.py](../bindings/python/sop/examples/relations_demo.py)
*   **Java**: [bindings/java/src/main/java/com/sharedcode/sop/examples/BTreeRelations.java](../bindings/java/src/main/java/com/sharedcode/sop/examples/BTreeRelations.java)
*   **C#**: `Sop.CLI` > Relations Demo

## 7. Managing Stores (Create, Open, Delete)

SOP provides standalone functions for managing the lifecycle of B-Tree stores.

```go
func ManageStores(ctx context.Context, db *database.Database) error {
	t, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 1. Create a new store
	// database.NewBtree[Key, Value](...)
	store, _ := database.NewBtree[string, string](ctx, db, "my_store", t, nil)
	store.Add(ctx, "foo", "bar")
	
	t.Commit(ctx)

	// 2. Open an existing store
	t2, _ := db.BeginTransaction(ctx, sop.ForReading)
	// database.OpenBtree[Key, Value](...)
	store2, _ := database.OpenBtree[string, string](ctx, db, "my_store", t2, nil)
	val, _ := store2.FindOne(ctx, "foo", false)
	fmt.Println(val)
	t2.Commit(ctx)

	// 3. Remove a store
	// database.RemoveBtree(...)
	// Note: This is a destructive operation and does not require a transaction.
	if err := database.RemoveBtree(ctx, db, "my_store"); err != nil {
		return err
	}
	
	return nil
}
```

## 8. Connecting to Multiple Redis Clusters

SOP supports connecting to different Redis clusters (or databases) within the same application by configuring `RedisConfig` in `DatabaseOptions`.

```go
package main

import (
	"context"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func main() {
	ctx := context.Background()

	// Database 1: Connects to Redis instance A
	db1 := sop.DatabaseOptions{
		CacheType: sop.Redis,
		RedisConfig: &sop.RedisCacheConfig{
			URL: "redis://redis-cluster-a:6379/0",
		},
	}

	// Database 2: Connects to Redis instance B
	db2 := sop.DatabaseOptions{
		CacheType: sop.Redis,
		RedisConfig: &sop.RedisCacheConfig{
			URL: "redis://redis-cluster-b:6379/0",
		},
	}

	// Use db1
	t1, _ := database.BeginTransaction(ctx, db1, sop.ForWriting)
	// ... perform operations on db1 ...
	t1.Commit(ctx)

	// Use db2
	t2, _ := database.BeginTransaction(ctx, db2, sop.ForWriting)
	// ... perform operations on db2 ...
	t2.Commit(ctx)
}
```

## 9. Advanced Pattern: Video Streaming Library

For storing large objects (BLOBs) like videos, images, or large documents, SOP recommends a **Split-Store Pattern**. This keeps your metadata store fast and lightweight while efficiently managing large binary data in a dedicated store.

### Architecture
1.  **Metadata Store (`video_catalog`)**: Stores lightweight attributes (Title, Rating, Description) and references to the blob.
2.  **Blob Store (`video_chunks`)**: Stores the actual binary data, split into chunks. The key is a composite of `{VideoID, ChunkIndex}`.

### Data Structure

**Metadata Store (`video_catalog`)**
Key: `UUID` (Video ID)
Value: `VideoMetadata`
```json
{
    "Title": "The Matrix",
    "Rating": "R",
    "Duration": 136,
    "ChunkCount": 500
}
```

**Blob Store (`video_chunks`)**
Key: `StreamingKey` (Composite)
```json
{
    "Key": "550e8400-e29b-41d4-a716-446655440000",  // Video ID
    "ChunkIndex": 0                                // 0, 1, 2, ...
}
```
Value: `[]byte` (Raw binary data, e.g., 1MB chunk)

### Implementation Example

```go
package main

import (
	"context"
	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// 1. Define Types
type VideoMetadata struct {
	Title      string
	Rating     string
	ChunkCount int
}

// StreamingKey is the required key structure for the Blob Store
type StreamingKey struct {
	Key        uuid.UUID `json:"Key"`
	ChunkIndex int       `json:"ChunkIndex"`
}

func UploadVideo(ctx context.Context, dbOpts sop.DatabaseOptions, title string, data []byte) error {
	trans, _ := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)

	// 2. Open Stores
	// Metadata: Standard store
	catalog, _ := database.NewBtree[uuid.UUID, VideoMetadata](ctx, dbOpts, "video_catalog", trans, nil, sop.StoreOptions{
		SlotLength: 2000,
	})
	
	// Blobs: Streaming store (Value is []byte)
	// Note: We disable "ValueInNode" for large blobs to keep the B-Tree structure small and fast.
	chunks, _ := database.NewBtree[StreamingKey, []byte](ctx, dbOpts, "video_chunks", trans, nil, sop.StoreOptions{
		SlotLength: 2000,
		IsValueDataInNodeSegment: false, // CRITICAL for performance with large blobs
	})

	// 3. Process Data
	videoID := uuid.New()
	chunkSize := 1024 * 1024 // 1MB chunks
	totalChunks := 0

	// Upload Chunks
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		
		// Add to Blob Store
		chunks.Add(ctx, StreamingKey{Key: videoID, ChunkIndex: totalChunks}, chunk)
		totalChunks++
	}

	// 4. Save Metadata
	meta := VideoMetadata{
		Title:      title,
		Rating:     "PG-13",
		ChunkCount: totalChunks,
	}
	catalog.Add(ctx, videoID, meta)

	return trans.Commit(ctx)
}
```

### Granular Management (The "Hollywood" Use Case)

Because SOP treats every chunk as a standard record, you have granular control over your data. This is ideal for media production pipelines where specific scenes or chapters need to be updated without re-uploading the entire petabyte-scale file.

**Scenario**: A producer wants to update "Chapter 3" (Chunks 100-150) of a movie.

```go
func UpdateChapter(ctx context.Context, dbOpts sop.DatabaseOptions, videoID uuid.UUID, startChunk int, newChapterData []byte) error {
	trans, _ := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	chunks, _ := database.OpenBtree[StreamingKey, []byte](ctx, dbOpts, "video_chunks", trans, nil)

	chunkSize := 1024 * 1024
	currentChunk := startChunk

	for i := 0; i < len(newChapterData); i += chunkSize {
		end := i + chunkSize
		if end > len(newChapterData) {
			end = len(newChapterData)
		}
		chunk := newChapterData[i:end]

		// Update specific chunk
		// SOP handles this efficiently as a single record update
		chunks.Update(ctx, StreamingKey{Key: videoID, ChunkIndex: currentChunk}, chunk)
		currentChunk++
	}

	return trans.Commit(ctx)
}
```

### Why this pattern?
*   **Petabyte Scale**: SOP breaks down gigantic blobs into manageable pieces. To the database, a 100GB video is just 100,000 small, manageable records.
*   **Network/Hardware Efficiency**: By dealing with standard-sized records, you avoid the memory pressure and network timeouts associated with moving massive monolithic files.
*   **Granular Concurrency**: Multiple editors can work on different chunks of the same asset simultaneously without locking the entire file.
*   **Streaming Optimization**: Developers can implement custom buffering or parallel retrieval logic on top of the chunk access, as SOP provides fast, random access to any chunk.

### Built-in Storage Optimization & Erasure Coding

It is important to note that **SOP natively manages the physical storage of these chunks**. You do not need to devise a special file management system.

*   **Erasure Coding & Parallel I/O**: At the core of SOP's architecture is the ability to use **Erasure Coding** to split chunks across multiple physical disk drives. This mechanism provides High Availability and redundancy natively while giving high throughput for scaling.
*   **Native Architecture**: SOP automatically stores data using an optimal strategy (e.g., hierarchical folders in a file system) to prevent directory bloating and ensure fast I/O.
*   **Universal Pattern**: This isn't just for blobs. SOP uses this "breaking down monoliths into decent-sized files" pattern for *all* data. Whether it's a user profile, a transaction log, or a video chunk, SOP handles it with the same robust, scalable architecture.
*   **Zero Overhead**: Developers get this petabyte-scale file management for free. You focus on the logical `Add` and `Get` operations, and SOP handles the physical distribution, replication, and parallel access on disk.

