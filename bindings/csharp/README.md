# Joltrin for C# (Sop4CS)

**Joltrin** (distributed on NuGet as `Sop4CS`, formerly SOP / Scalable Objects Persistence) is a high-performance, durable memory and verification infrastructure engine for C#, powered by a Go backend. It combines the raw speed of direct disk I/O with the reliability of ACID transactions and the flexibility of modern AI data management.

## Documentation

*   **[API Cookbook](../../docs/COOKBOOK.md)**: Common recipes and patterns (Key-Value, Transactions, AI).
*   **[Examples](Sop.CLI)**: Complete runnable examples.

## Installation

Install the library via NuGet:

```bash
dotnet add package Sop4CS
```

To run the examples and launch the Data Management Console, install the CLI tool:

```bash
dotnet tool install -g Sop4CS.CLI
```

## Key Features

*   **Unified Database**: Single entry point for managing Vector, Model, and Key-Value stores.
*   **Transactional B-Tree Store**: Unlimited, persistent B-Tree storage for key-value data.
*   **Complex Keys**: Support for composite keys (structs/classes) with custom index specifications.
*   **Metadata "Ride-on" Keys**: Store metadata directly in the B-Tree key (e.g., timestamps, status flags) to enable high-speed scanning and filtering of millions of records without fetching the heavy value payload. Ideal for "Big Data" management and analytics.
*   **Vector Database**: Built-in vector search (k-NN) for AI embeddings and similarity search.
*   **Text Search**: Transactional, embedded text search engine (BM25).
*   **AI Model Store**: Versioned storage for machine learning models (B-Tree backed).
*   **ACID Compliance**: Full transaction support (Begin, Commit, Rollback) with isolation.
*   **High Performance**: Written in Go with a lightweight C# wrapper (P/Invoke).
*   **Caching**: Integrated Redis-backed L1/L2 caching for speed.
*   **Replication**: Optional Erasure Coding (EC) for fault-tolerant storage across drives.
*   **Multi-Tenancy**: Native support for Cassandra Keyspaces or Directory-based isolation.
*   **Flexible Deployment**: Supports both **Standalone** (local) and **Clustered** (distributed) modes.

## SOP Data Manager

SOP includes a powerful **SOP Data Manager** that provides **full CRUD** capabilities for your B-Tree stores. It goes beyond simple viewing, offering a complete GUI for inspecting, searching, and managing your data at scale.

*   **Web UI**: A modern, responsive interface for browsing B-Trees, managing stores, and visualizing data.
*   **AI Copilot**: Integrated directly into the UI, the AI Copilot can help you write queries, explain data structures, and even generate code snippets. Configure your LLM provider through the Setup Wizard on first launch.
*   **SystemDB**: View and manage internal system data, including registry information and transaction logs.

To launch the SOP Data Manager, download the all-in-one single-file installer from [SOP Releases](https://github.com/SharedCode/joltrin/releases). Alternatively, you can use the Go toolchain:

```bash
# From the root of the repository
go run ./tools/httpserver
```

## SOP AI Kit

The **SOP AI Kit** transforms SOP from a storage engine into a complete AI data platform.

*   **Vector Store**: Native support for storing and searching high-dimensional vectors.
*   **RAG Agents**: Build Retrieval-Augmented Generation applications with ease.
*   **Scripts**: A functional AI runtime for drafting, refining, and executing complex workflows (Hybrid Execution Model).

See [ai/README.md](../../ai/README.md) for a deep dive into the AI capabilities.

## Executing SOP Scripts

SOP Scripts allow you to execute complex workflows on the server side, similar to Stored Procedures.
Currently, scripts are executed via the SOP HTTP API Server.

Example using `HttpClient`:

```csharp
using System;
using System.Net.Http;
using System.Text;
using System.Threading.Tasks;

class Program
{
    static async Task Main(string[] args)
    {
        using var client = new HttpClient();
        var json = "{\"name\":\"user_audit\", \"category\":\"general\", \"args\":{\"user_id\":999}}";
        var content = new StringContent(json, Encoding.UTF8, "application/json");

        var response = await client.PostAsync("http://localhost:8080/api/scripts/execute", content);
        var result = await response.Content.ReadAsStringAsync();
        
        Console.WriteLine(result);
    }
}
```

## Performance & Big Data Management

SOP is designed for high-throughput, low-latency scenarios, making it suitable for "Big Data" management on commodity hardware.

*   **"Ride-on" Metadata**: By embedding metadata (like `IsDeleted`, `LastUpdated`, `Category`) directly into the Key struct but *excluding* it from the index (using `IndexSpecification`), you can scan millions of keys per second to filter data. This avoids the I/O penalty of fetching the full Value (which might be a large JSON blob or binary file) just to check a status flag.
*   **Direct I/O**: SOP bypasses OS page caches where appropriate to offer consistent, raw disk performance.
*   **Parallelism**: The underlying Go engine utilizes highly concurrent goroutines for managing B-Tree nodes and vector indexes.

## Running the Examples

The `Sop4CS.CLI` tool provides a comprehensive suite of examples covering B-Trees, Vector Search, Model Store, and more.

Once installed as a global tool:

```bash
# Run interactive menu
sop-cli

# Run a specific example (e.g., Complex Keys)
sop-cli run 2

# Launch the SOP Data Management Console
sop-cli httpserver
```

The suite includes:
1.  **Basic B-Tree**: CRUD operations.
2.  **Complex Keys**: Composite keys and anonymous type lookups.
3.  **Metadata**: "Ride-on" keys for high-performance updates.
4.  **Paging**: Forward/Backward navigation.
5.  **Vector Search**: Simulated AI/RAG embedding search.
6.  **Model Store**: Large binary object storage.
7.  **Logging**: Demonstration of the logging capabilities.
8.  **Batched Operations**: High-performance batched inserts/updates.
9.  **Cassandra Init**: Demo of Cassandra-backed initialization.
10. **Text Search**: Full-text search capabilities.
11. **Clustered Database**: Distributed database operations (requires Redis).
12. **Concurrent Transactions**: Multi-threaded transaction handling (requires Redis).
13. **Concurrent Transactions (Standalone)**: Multi-threaded transaction handling (local only).
14. **Large Complex Data Generation Demo**: Generates large, complex datasets for use with the Data Management Console and stress testing.
15. **Erasure Coding Config Demo**: Demonstrates configuring erasure coding for blob store for fault-tolerant storage across multiple drives.
16. **Full Replication Config Demo**: Demonstrates configuring full data replication, active/passive drives for registry & erasure coding for blob store.

## SOP HTTP Server (Data Management & REST API)

SOP includes a powerful **SOP HTTP Server** that acts as a comprehensive **Data Management Console** and a **RESTful API**. It transforms your embedded SOP database into a fully manageable server instance.

To launch the Management Console / SOP HTTP Server:

```bash
sop-cli httpserver
```

### SOP HTTP Server Capabilities

It is important to distinguish between the **SOP HTTP Server** (this tool) and SOP's internal **Clustered Mode**:

1.  **SOP HTTP Server**: This is a standard web server that serves the SOP Data Manager UI and REST API.
    *   **Multi-Client**: It can serve **many concurrent HTTP clients** (users on web browsers, mobile apps, or other services).
    *   **Collaborative Management**: Multiple team members can access the console simultaneously to view, edit, and query data in real-time.
    *   **REST API**: Exposes your B-Tree stores via standard HTTP endpoints, allowing you to integrate SOP with any language or tool (curl, Postman, Python scripts).

2.  **SOP Clustered Mode (Internal)**: This refers to the low-level coordination between multiple SOP nodes (e.g., multiple microservices using the SOP library).
    *   **Swarm Computing**: Uses Redis to coordinate transactions, merge changes, and handle conflict resolution across distributed nodes.
    *   **High Availability**: Ensures data consistency when multiple machines are writing to the same logical store.

**In short**: You run `sop-cli httpserver` to give your team a GUI and API. You configure "Clustered Mode" in your code when building distributed applications.

### Launching the SOP HTTP Server

To launch it using the global tool:

```bash
sop-cli httpserver
```

### Programmatic Usage

You can also launch the SOP HTTP Server directly from your C# application using the `Sop.Server` namespace:

```csharp
using Sop.Server;

// Launch the SOP HTTP Server (downloads binary if needed)
await SopServer.RunAsync(args);
```

### Key Features

*   **Full Data Management**: Perform comprehensive CRUD (Create, Read, Update, Delete) operations on any record directly from the UI. Edit complex JSON objects or binary data with ease.
*   **High-Performance Search**: Utilizes B-Tree positioning for **instant lookups**, even in datasets with millions of records. Supports both simple keys and complex composite keys (e.g., searching by `Country` + `City` + `Zip`).
*   **Visual Tree Navigation**: Don't just search - explore. Smart pagination and traversal controls (First, Previous, Next, Last) allow you to walk through your B-Tree structure efficiently.
*   **Bulk Operations**: Designed for rapid-fire management. Delete thousands of records or update batch configurations without writing a single line of code.
*   **Responsive & Cross-Platform**: A modern, dark-themed UI that works seamlessly across diverse monitor sizes and devices.
*   **Zero-Config Setup**: The tool automatically downloads the correct optimized binary for your OS/Architecture upon first run. No manual installation required.

**Usage**: By default, it opens on `http://localhost:8080`.
**Arguments**: You can pass standard flags to configure the SOP HTTP Server.
```bash
# Specify a custom database path
sop-cli httpserver -database ./my_data

# Specify a custom port
sop-cli httpserver -port 9090

# Enable clustered mode
# In this mode, the httpserver will participate in clustered data management with other nodes in the cluster.
sop-cli httpserver -clustered
```

## AI Copilot & Scripts

The SOP Data Manager includes a built-in **AI Copilot** that allows you to interact with your data using natural language and automate workflows using **Scripts**.

### 1. Launch the Assistant
Start the SOP HTTP Server:
```bash
sop-cli httpserver
```
Open your browser to `http://localhost:8080` and click the **AI Copilot** floating widget.

### 2. Natural Language Commands
You can ask the assistant to perform tasks or query data:
*   "Show me the schema for the 'users' store."
*   "Find all records where age is greater than 30."
*   "Join 'Users' and 'Orders' on 'UserID'."
*   "Add a new product 'Laptop' with price 999."

### 3. Scripts: Record & Replay
Scripts allow you to record a sequence of actions and replay them later. This is a "Natural Language Programming" system where the LLM compiles your intent into a high-performance script.

**Step 1: Record**
Type `/script new <name>` in the chat.
```
/script new daily_check
```

**Step 2: Perform Actions**
Interact with the AI naturally.
```
Check the 'logs' store for errors.
Count the number of active users.
```

**Step 3: Stop**
Save the script.
```
/script stop
```

**Step 4: Replay**
Execute the script instantly. The system runs the compiled steps without invoking the LLM again.
```
/script run daily_check
```

### 4. Parameterized Scripts (Beta)
You can make scripts dynamic by using parameters.
*   **Record**: When recording, use specific values (e.g., "user_123").
*   **Edit**: You can edit the script JSON to use templates like `{{.user_id}}`.
*   **Play**: Pass values at runtime.
    ```
    /play user_audit user_id=456
    ```

### 5. Scripts as Views & Streaming
The SOP Data Manager supports **Streaming Results**, allowing you to use Scripts as data sources (Views) in your queries.
*   **Efficiency**: Results are streamed in real-time, enabling low-latency processing of large datasets.
*   **Composition**: You can join a Script's output with a B-Tree store: "Join 'Users' and 'MyScript' on 'ID'".

### 6. Remote Execution
You can trigger these scripts from your C# code via the REST API:

```csharp
using System;
using System.Net.Http;
using System.Text;
using System.Threading.Tasks;

public class RemoteScript
{
    public static async Task Main()
    {
        var json = "{\"message\": \"/play user_audit user_id=999\", \"agent\": \"sql_admin\"}";
        var content = new StringContent(json, Encoding.UTF8, "application/json");

        using var client = new HttpClient();
        var response = await client.PostAsync("http://localhost:8080/api/ai/chat", content);
        
        var result = await response.Content.ReadAsStringAsync();
        Console.WriteLine(result);
    }
}
```

### Multiple Databases Configuration (Recommended)

For managing multiple environments (e.g., Dev, Staging, Prod), create a `config.json`:

```json
{
  "port": 8080,
  "databases": [
    {
      "name": "Local Development",
      "path": "./data/dev_db",
      "mode": "standalone"
    },
    {
      "name": "Production Cluster",
      "path": "/mnt/data/prod",
      "mode": "clustered",
      "redis": "redis-prod:6379"
    }
  ],
  "system_db": {
      "name": "system",
      "path": "./data/sop_system",
      "mode": "standalone"
  }
}
```

Run with: `sop-cli httpserver -config config.json`

### Important Note on Concurrency

If database(s) are configured in **standalone mode**, ensure that the http server is the only process/app running to manage the database(s). Alternatively, you can add its HTTP REST endpoint to your embedded/standalone app so it can continue its function and serve HTTP pages at the same time.

If **clustered**, no worries, as SOP takes care of Redis-based coordination with other apps and/or SOP HTTP Servers managing databases using SOP in clustered mode.

### Configuration File

You can also configure the SOP HTTP Server using a JSON configuration file. This is useful for persisting settings across sessions.

**Example `config.json`:**
```json
{
  "Port": 9090,
  "RegistryPath": "./my_data",
  "Theme": "dark"
}
```

Pass the config file using the `-config` flag:
```bash
sop-cli httpserver -config ./config.json
```

## Production Deployment

For production environments (e.g., Kubernetes, Docker, Linux Servers), you should run the standalone SOP HTTP Server binary directly instead of using the `dotnet tool` wrapper.

1.  **Download**: Get the latest binary for your platform (Linux, Windows, macOS) from the [GitHub Releases](https://github.com/sharedcode/joltrin/releases) page.
2.  **Run**: Execute the binary with your configuration.

**Example (Docker/Kubernetes):**
```dockerfile
FROM alpine:latest
COPY sop-httpserver-linux-amd64 /app/sop-httpserver
RUN chmod +x /app/sop-httpserver
CMD ["/app/sop-httpserver", "-database", "/data", "-port", "8080"]
```

This ensures a minimal footprint and removes the dependency on the .NET Runtime for the SOP HTTP Server process.

## Generating Sample Data

To see the Management Console in action, you can generate a sample database with complex keys using the included example:

1.  **Run the generator**:
    ```bash
    sop-cli run 14
    ```
    This will create a database in `sop_data_complex` (or similar path defined in the example) with two stores: `people` (Complex Key) and `products` (Composite Key).

2.  **Open in Console**:
    ```bash
    sop-cli httpserver -database data/large_complex_db
    ```

## Prerequisites

*   **Redis**: Required for caching and transaction coordination (especially in Clustered mode). **Note**: Redis is NOT used for data storage, just for coordination & to offer built-in caching.
*   **Storage**: Local disk or shared network drive space (supports multiple drives/folders).
*   **OS**: macOS, Linux, or Windows.
    *   **Architectures**: x64 (AMD64/Intel64) and ARM64 (Apple Silicon/Linux aarch64).
*   **.NET SDK**: .NET 10.0 or later.

## Installation

1.  **Build the Go Bridge**:
    From the repository root:
    ```bash
    go build -buildmode=c-shared -o bindings/csharp/Sop.CLI/bin/Debug/net10.0/libjsondb.dylib ./bindings/main/...
    # Note: Adjust the output path and extension (.so for Linux, .dll for Windows) as needed.
    ```

2.  **Add Reference**:
    Add the `Sop` project to your solution or reference the compiled assembly.

3.  **Native Library**:
    Ensure the compiled `libjsondb` (dylib/so/dll) is in your application's output directory (e.g., `bin/Debug/net10.0/`).

## Quick Start Guide

SOP uses a unified `Database` object to manage all types of stores. All operations are performed within a **Transaction**.

### 1. Initialize Database & Context

First, create a Context and open a Database connection.

```csharp
using Sop;
using System.Collections.Generic;

// Initialize Context
using var ctx = new Context();

// Open Database (Standalone Mode)
var dbOpts = new DatabaseOptions 
{ 
    StoresFolders = new List<string> { "./sop_data" },
    Type = (int)DatabaseType.Standalone
};

var db = new Database(dbOpts);
```

### SOP Data Manager Visibility

To ensure your C#-created databases are fully discoverable and manageable in the **SOP Data Manager** GUI, you should use the `Database.Setup` method. This persists your configuration options (like schema types, store paths, etc.) to the disk.

```csharp
var dbOpts = new DatabaseOptions 
{ 
    StoresFolders = new List<string> { "./sop_data" },
    Type = (int)DatabaseType.Standalone
};

// Persist options for discoverability
Database.Setup(ctx, dbOpts);

var db = new Database(dbOpts);
```

You can also retrieve these options programmatically:

```csharp
var opts = Database.GetOptions(ctx, "./sop_data");
Console.WriteLine($"DB Type: {opts.Type}");
```

### 2. Start a Transaction

All data operations (Create, Read, Update, Delete) must happen within a transaction.

```csharp
// Begin a transaction
var trans = db.BeginTransaction(ctx);
try
{
    // --- 3. Vector Store (AI) ---
    // Open a Vector Store named "products"
    var vectorStore = db.OpenVectorStore(ctx, "products", trans);
    
    // Upsert a Vector Item
    vectorStore.Upsert(new VectorItem 
    { 
        Id = "prod_101", 
        Vector = new float[] { 0.1f, 0.5f, 0.9f },
        Payload = new Dictionary<string, object> { { "name", "Laptop" }, { "price", 999 } }
    });

    // --- 4. Model Store (AI) ---
    // Open a Model Store named "classifiers"
    var modelStore = db.OpenModelStore(ctx, "classifiers", trans);
    
    // Save a Model (any serializable object)
    modelStore.Save("churn", "v1.0", new { Algorithm = "random_forest", Trees = 100 });

    // --- 5. B-Tree Store (Key-Value) ---
    // Open/Create a B-Tree named "users"
    var btree = db.NewBtree<string, string>(ctx, "users", trans);
    
    // Add a Key-Value pair
    btree.Add(ctx, new Item<string, string>("user_123", "John Doe"));
    
    // Find a value
    if (btree.Find(ctx, "user_123"))
    {
        var items = btree.GetValues(ctx, "user_123");
        Console.WriteLine($"Found User: {items[0].Value}");
    }

    // --- 6. Complex Keys & Index Specification ---
    // Define a composite key structure
    public class EmployeeKey
    {
        public string Region { get; set; }
        public string Department { get; set; }
        public int Id { get; set; }
    }

    // Define Index Specification
    // This enables fast prefix scans (e.g., "Get all employees in US")
    var indexSpec = new IndexSpecification
    {
        IndexFields = new List<IndexFieldSpecification>
        {
            new IndexFieldSpecification { FieldName = "Region", AscendingSortOrder = true },
            new IndexFieldSpecification { FieldName = "Department", AscendingSortOrder = true },
            new IndexFieldSpecification { FieldName = "Id", AscendingSortOrder = true }
        }
    };

    var empOpts = new BtreeOptions("employees") { IndexSpecification = indexSpec };
    var employees = db.NewBtree<EmployeeKey, string>(ctx, "employees", trans, empOpts);

    employees.Add(ctx, new Item<EmployeeKey, string>(
        new EmployeeKey { Region = "US", Department = "Sales", Id = 101 }, 
        "Alice"
    ));

    // --- 7. Metadata "Ride-on" Keys (UpdateCurrentKey) ---
    // Efficiently update metadata embedded in the key without fetching/writing the value.
    if (employees.Find(ctx, new EmployeeKey { Region = "US", Department = "Sales", Id = 101 }))
    {
        var currentItem = employees.GetCurrentKey(ctx);
        // Update metadata (e.g. promote employee, change status)
        // Note: In a real scenario, you'd likely have a mutable field in the key.
        // This operation is very fast as it avoids value I/O.
        employees.UpdateCurrentKey(ctx, currentItem);
    }

    // --- 8. Simplified Lookup (Anonymous Types) ---
    // You can search using an anonymous object that matches the key structure.
    // This is useful if you don't have the original Key class definition.
    
    // Open existing B-Tree using 'object' as the key type
    var employeesSimple = db.OpenBtree<object, string>(ctx, "employees", trans);
    
    // Search using an anonymous type
    var searchKey = new { Region = "US", Department = "Sales", Id = 101 };
    
    if (employeesSimple.Find(ctx, searchKey))
    {
        var values = employeesSimple.GetValues(ctx, searchKey);
        Console.WriteLine($"Found Alice using anonymous object: {values[0].Value}");
    }

    // --- 9. Paging Navigation ---
    // Efficiently page through keys (metadata) without fetching values.
    var pagingInfo = new PagingInfo 
    { 
        PageSize = 50, 
        PageOffset = 0 
    };
    
    // Get first page of keys
    var keys = employees.GetKeys(ctx, pagingInfo);
    foreach (var item in keys)
    {
        Console.WriteLine($"Employee: {item.Key.Region}/{item.Key.Department}/{item.Key.Id}");
    }

    // --- 10. Text Search ---
    var idx = db.OpenSearch(ctx, "articles", trans);
    idx.Add("doc1", "The quick brown fox");

    // --- 11. Batched Operations ---
    // Add multiple items in a single call for better performance
    var batchItems = new List<Item<string, string>>
    {
        new Item<string, string>("k1", "v1"),
        new Item<string, string>("k2", "v2")
    };
    btree.Add(ctx, batchItems);

    // Commit the transaction
    trans.Commit();
}
catch
{
    trans.Rollback();
    throw;
}
```

### 3. Querying Data (Read-Only)

```csharp
using var trans = db.BeginTransaction(ctx, mode: TransactionMode.ForReading);
try
{
    // --- Vector Search ---
    var vs = db.OpenVectorStore(ctx, "products", trans);
    var hits = vs.Query(new float[] { 0.1f, 0.5f, 0.8f }, k: 5);
    foreach (var hit in hits)
    {
        Console.WriteLine($"Match: {hit.Id}, Score: {hit.Score}");
    }

    // --- Text Search ---
    var idx = db.OpenSearch(ctx, "articles", trans);
    var results = idx.SearchQuery("fox");
    foreach (var res in results)
    {
        Console.WriteLine($"Doc: {res.DocID}, Score: {res.Score}");
    }

    // --- Model Retrieval ---
    var ms = db.OpenModelStore(ctx, "classifiers", trans);
    var model = ms.Load<dynamic>("churn", "v1.0");
    
    trans.Commit();
}
catch
{
    trans.Rollback();
}
```

## Advanced Configuration

### Logging

Configure the global logger to output to a file or stderr.

```csharp
// Log to a file
Logger.Configure(LogLevel.Debug, "sop.log");

// Log to stderr (default)
Logger.Configure(LogLevel.Info, "");
```

### Redis Configuration

For Clustered mode or when using Redis caching, you can configure the Redis connection directly in the `DatabaseOptions`. This allows different databases to use different Redis instances.

```csharp
var db = new Database(new DatabaseOptions
{
    StoresFolders = new List<string> { "./data" },
    Type = (int)DatabaseType.Clustered,
    RedisConfig = new RedisConfig 
    { 
        Address = "localhost:6379",
        // Password = "optional_password",
        // DB = 0
    }
});
```

*Note: The legacy `Redis.Initialize()` method is still supported for backward compatibility but is deprecated.*

### Cassandra Connection

Initialize the shared Cassandra connection for multi-tenant storage.

```csharp
var config = new CassandraConfig
{
    ClusterHosts = new List<string> { "localhost" },
    Consistency = 1,
    ReplicationClause = "{'class':'SimpleStrategy', 'replication_factor':1}"
};

Cassandra.Initialize(config);

// ... perform operations ...

Cassandra.Close();
```

### Clustered Database

In **Clustered Mode**, SOP uses Redis to coordinate transactions across multiple nodes. This allows many machines to participate in data management for the same Database/B-Tree files on disk while maintaining ACID guarantees.

**Note**: The database files generated in Standalone and Clustered modes are fully compatible. You can switch between modes as needed but make sure if switching to Standalone mode, that there is only one process that writes to the database files.

```csharp
var dbOpts = new DatabaseOptions 
{ 
    StoresFolders = new List<string> { "/mnt/data1", "/mnt/data2" },
    Type = (int)DatabaseType.Clustered,
    Keyspace = "my_tenant_keyspace",
    // Erasure Config allows you to specify 
    ErasureConfig = new Dictionary<string, ErasureCodingConfig>
    {
        { "default", new ErasureCodingConfig { DataShards = 2, ParityShards = 1 } }
    },
    // Configure Redis for coordination (defaults to localhost:6379 if omitted)
    RedisConfig = new RedisConfig { Address = "localhost:6379" }
};

var db = new Database(dbOpts);
```

## Concurrent Transactions Example

SOP supports concurrent access from multiple threads or processes. The library handles conflict detection and merging automatically.

**Important**: Pre-seed the B-Tree with at least one item in a separate transaction before launching concurrent workers.
> **Note:** This requirement is simply to have at least one item in the tree. It can be a real application item or a dummy seed item.

```csharp
using Sop;
using System.Threading;
using System.Threading.Tasks;

// 1. Setup & Pre-seed
using var ctx = new Context();

// Option A: Standalone (Local disk or shared Network drive, In-Memory Cache)
var db = new Database(new DatabaseOptions { 
    StoresFolders = new List<string> { "./sop_data" },
    Type = (int)DatabaseType.Standalone 
});

// Option B: Clustered (Redis Cache) - Required for distributed swarm
// var db = new Database(new DatabaseOptions { 
//     StoresFolders = new List<string> { "./sop_data" },
//     Type = (int)DatabaseType.Clustered,
//     RedisConfig = new RedisConfig { Address = "localhost:6379" }
// });

using (var trans = db.BeginTransaction(ctx))
{
    var btree = db.NewBtree<int, string>(ctx, "concurrent_tree", trans);
    btree.Add(ctx, new Item<int, string> { Key = -1, Value = "Root Seed" });
    trans.Commit();
}

// 2. Launch Threads
Parallel.For(0, 5, i => 
{
    int threadId = i;
    int retryCount = 0;
    bool committed = false;
    
    while (!committed && retryCount < 10)
    {
        try 
        {
            using var trans = db.BeginTransaction(ctx);
            var btree = db.OpenBtree<int, string>(ctx, "concurrent_tree", trans);

            for (int j = 0; j < 100; j++)
            {
                int key = (threadId * 100) + j;
                btree.Add(ctx, new Item<int, string> { Key = key, Value = $"Thread {threadId} - Item {j}" });
            }
            trans.Commit();
            committed = true;
        }
        catch
        {
            retryCount++;
            Thread.Sleep(100 * retryCount);
        }
    }
});
```
