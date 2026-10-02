# Joltrin for Python (sop4py)

**Joltrin** (distributed on PyPI as `sop4py`, formerly SOP / Scalable Objects Persistence) is a high-performance, durable memory and verification infrastructure engine for Python, powered by a Go backend. It combines the raw speed of direct disk I/O with the reliability of ACID transactions and the flexibility of modern AI data management.

## Key Features

*   **Unified Database**: Single entry point for managing Vector, Model, and Key-Value stores.
*   **Transactional B-Tree Store**: Unlimited, persistent B-Tree storage for key-value data.
*   **Complex Keys**: Support for composite keys (structs/dataclasses) with custom index specifications (e.g., Region -> Dept -> ID).
*   **Metadata "Ride-on" Keys**: Store metadata directly in the B-Tree key (e.g., timestamps, status flags) to enable high-speed scanning and filtering of millions of records without fetching the heavy value payload. Ideal for "Big Data" management and analytics.
*   **Vector Database**: Built-in vector search (k-NN) for AI embeddings and similarity search.
*   **Text Search**: Transactional, embedded text search engine (BM25).
*   **AI Model Store**: Versioned storage for machine learning models (B-Tree backed).
*   **ACID Compliance**: Full transaction support (Begin, Commit, Rollback) with isolation.
*   **High Performance**: Written in Go with a lightweight Python wrapper (ctypes).
*   **Caching**: Integrated Redis-backed L1/L2 caching for speed.
*   **Replication & Fault Tolerance**: Supports **Erasure Coding** for Blob Store (managing B-Tree nodes & large data files) to distribute data across drives with configurable parity. Also features **Active/Passive Replication** for the Registry to ensure high availability.
*   **Multi-Tenancy**: Native support for Cassandra Keyspaces or Directory-based isolation.
*   **Flexible Deployment**: Supports both **Standalone** (local) and **Clustered** (distributed) modes.

## Performance & Big Data Management

SOP is designed for high-throughput, low-latency scenarios, making it suitable for "Big Data" management on commodity hardware.

*   **"Ride-on" Metadata**: By embedding metadata (like `IsDeleted`, `LastUpdated`, `Category`) directly into the Key struct but *excluding* it from the index (using `IndexSpecification`), you can scan millions of keys per second to filter data. This avoids the I/O penalty of fetching the full Value (which might be a large JSON blob or binary file) just to check a status flag.
*   **Direct I/O**: SOP bypasses OS page caches where appropriate to offer consistent, raw disk performance.
*   **Parallelism**: The underlying Go engine utilizes highly concurrent goroutines for managing B-Tree nodes and vector indexes.

## SOP AI Kit

The **SOP AI Kit** transforms SOP from a storage engine into a complete AI data platform.

*   **Vector Store**: Native support for storing and searching high-dimensional vectors.
*   **RAG Agents**: Build Retrieval-Augmented Generation applications with ease.
*   **Scripts**: A functional AI runtime for drafting, refining, and executing complex workflows (Hybrid Execution Model).

> **Important**: To use the AI Copilot features (e.g., in the Data Manager), configure your LLM provider through the Setup Wizard or add generator configuration to your `config.json`. See the [Main README](../../docs/AI_COPILOT.md) for details.

For comprehensive details on the **SOP Platform Tools** (Scripting, Explain Plans, Self-Correcting Agents), please see the [Platform Tools Documentation](../../docs/SOP_PLATFORM_TOOLS.md).

See [ai/README.md](../../ai/README.md) for a deep dive into the AI capabilities.

## Documentation

*   **[API Cookbook](COOKBOOK.md)**: Common recipes and patterns (Key-Value, Transactions, AI).
*   **[Examples](sop/examples/)**: Complete runnable scripts.

## Installation

Install directly from PyPI:

```bash
pip install sop4py
```

## SOP Data Management Suite

SOP includes a powerful **Data Management Suite** that provides **full CRUD** capabilities for your B-Tree stores. It goes beyond simple viewing, offering a complete GUI for inspecting, searching, and managing your data at scale.

*   **Web UI**: A modern, responsive interface for browsing B-Trees, managing stores, and visualizing data.
*   **AI Copilot**: Integrated directly into the UI, the AI Copilot can help you write queries, explain data structures, and even generate code snippets.
*   **SystemDB**: View and manage internal system data, including registry information and transaction logs.

To launch the Data Manager simply run:

```bash
sop-httpserver
```

Or download the all-in-one single-file installer (no Python/pip required) from [SOP Releases](https://github.com/SharedCode/joltrin/releases).

### Key Capabilities

*   **Universal Database Server**: Acts as a standalone server for local development or a stateless management node in a clustered enterprise swarm (Kubernetes/EC2).
*   **Full Data Management**: Perform comprehensive CRUD (Create, Read, Update, Delete) operations on any record directly from the UI.
*   **High-Performance Search**: Utilizes B-Tree positioning for instant lookups, even in datasets with millions of records. Supports both simple keys and complex composite keys (e.g., searching by `Country` + `City`).
*   **Efficient Navigation**: Smart pagination and traversal controls (First, Previous, Next, Last) allow you to browse massive datasets without performance penalties.
*   **Bulk Operations**: Designed for rapid-fire management of records with a clean, non-distracting interface.
*   **Responsive & Cross-Platform**: Works seamlessly across diverse monitor sizes and devices.
*   **Automatic Setup**: The tool automatically downloads the correct binary for your OS/Architecture upon first run.

**Usage**: By default, it opens on `http://localhost:8080`.
**Arguments**: You can pass standard flags, e.g., `sop-httpserver -port 9090 -database ./my_data`.

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

> **Note**: This example shows the structure of `system_db`, but it is best to let the **Data Manager Setup Wizard** create and populate it automatically on first launch. The Wizard ensures that essential stores (like `Script` and `llm_knowledge`) are correctly initialized for the AI Copilot.

Run with: `sop-httpserver -config config.json`

### Important Note on Concurrency

If database(s) are configured in **standalone mode**, ensure that the http server is the only process/app running to manage the database(s). Alternatively, you can add its HTTP REST endpoint to your embedded/standalone app so it can continue its function and serve HTTP pages at the same time.

If **clustered**, no worries, as SOP takes care of Redis-based coordination with other apps and/or SOP HTTP Servers managing databases using SOP in clustered mode.

## AI Copilot & Scripts

The SOP Data Manager includes a built-in **AI Copilot** that allows you to interact with your data using natural language and automate workflows using **Scripts**.

### 1. Launch the Assistant
Start the server:
```bash
sop-httpserver
```
Open your browser to `http://localhost:8080` and click the **AI Copilot** floating widget.

### 2. Natural Language Commands
You can ask the assistant to perform tasks or query data:
*   "Show me the schema for the 'users' store."
*   "Find all records where age is greater than 30."
*   "Explain the structure of the 'orders' B-Tree."

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

### 4. Passing Parameters
You can make scripts dynamic by using parameters.
*   **Record**: When recording, use specific values (e.g., "user_123").
*   **Edit**: You can edit the script JSON to use templates like `{{.user_id}}`.
*   **Play**: Pass values at runtime.
    ```
    /script run user_audit user_id=456
    ```

### 5. Remote Execution (Stored Procedure Style)
You can trigger these scripts from your Python code via the REST API. This is similar to calling a Stored Procedure where you pass the procedure name and arguments.

```python
import requests

# Execute the 'user_audit' script with parameters
response = requests.post(
    "http://localhost:8080/api/scripts/execute",
    json={
        "name": "user_audit",
        "category": "general",
        "args": {
            "user_id": 999
        }
    }
)

# The response is a JSON list of execution steps and results
results = response.json()
for step in results:
    if "final_output" in step:
        print("Result:", step["final_output"])
```

## Generating Sample Data

To see the Data Management Suite in action, you can generate a sample database with complex keys using the included example script:

1.  **Run the generator**:
    ```bash
    # If installed via pip
    sop-demo run large_complex_demo
    
    # Or manually if you have the source
    python3 examples/large_complex_demo.py
    ```
    This will create a database in `data/large_complex_db` with two stores: `people` (Complex Key) and `products` (Composite Key).

2.  **Open in Browser**:
    ```bash
    sop-httpserver -database data/large_complex_db
    ```

## Prerequisites

*   **Redis**: Required for caching and transaction coordination (especially in Clustered mode). **Note**: Redis is NOT used for data storage, just for coordination & to offer built-in caching.

## Running the Examples

SOP comes with a bundled CLI tool `sop-demo` to easily list and run examples directly from your installation.

**List available examples:**
```bash
sop-demo list
```

**Run a specific example:**
```bash
sop-demo run vector_demo
```

**Copy examples to your workspace:**
If you want to inspect the code or modify the examples, you can copy them to your local directory:
```bash
sop-demo copy
# Copies to ./sop_examples/
```

**Manual Execution:**
If you have copied the examples locally, you can also run them using python directly:
```bash
python3 sop_examples/concurrent_demo.py
```

**Concurrent Transactions (Standalone):**
This demo shows how to run concurrent transactions without a Redis dependency. It simulates real-world scenarios by introducing a small random sleep interval (jitter) between batch transactions to mimic network latency and reduce contention.
```bash
sop-demo run concurrent_demo_standalone
```

**Concurrent Transactions (Clustered):**
This demo shows how to run concurrent transactions in a distributed environment (requires Redis). Similar to the standalone demo, it uses jitter to simulate realistic commit timing across different machines in a cluster.
```bash
sop-demo run concurrent_demo
```

**Vector Search:**
```bash
sop-demo run vector_demo
```

See the `examples/` directory for more scripts.
    ```

3.  **Set PYTHONPATH**:
    ```bash
    export PYTHONPATH=$PYTHONPATH:$(pwd)/jsondb/python
    ```

## Quick Start Guide

SOP uses a unified `Database` object to manage all types of stores (Vector, Model, and B-Tree). All operations are performed within a **Transaction**.

### 1. Initialize Database & Context

First, create a Context and open a Database connection.

```python
from sop import Context, TransactionMode, TransactionOptions, Btree, BtreeOptions, Item
from sop.ai import Database, DatabaseType, Item as VectorItem
from sop.database import DatabaseOptions

# Initialize Context
ctx = Context()

# Open Database (Standalone Mode)
# This creates/opens a database at the specified path.
db = Database(DatabaseOptions(stores_folders=["data/my_db"], type=DatabaseType.Standalone))

# Open Database (Clustered Mode with Multi-Tenancy)
# Connects to a specific Cassandra Keyspace ("tenant_1").
# Requires Cassandra and Redis.
# db_clustered = Database(DatabaseOptions(stores_folders=["data/blobs"], keyspace="tenant_1", type=DatabaseType.Clustered))
```

### 2. Start a Transaction

All data operations (Create, Read, Update, Delete) must happen within a transaction.

```python
# Begin a transaction (Read-Write)
# You can use 'with' block for auto-commit/rollback, or manage manually.
with db.begin_transaction(ctx) as tx:
    
    # --- 3. Vector Store (AI) ---
    # Open a Vector Store named "products"
    vector_store = db.open_vector_store(ctx, tx, "products")
    
    # Upsert a Vector Item
    vector_store.upsert(ctx, VectorItem(
        id="prod_101",
        vector=[0.1, 0.5, 0.9],
        payload={"name": "Laptop", "price": 999}
    ))

    # --- 4. Model Store (AI) ---
    # Open a Model Store named "classifiers"
    model_store = db.open_model_store(ctx, tx, "classifiers")
    
    # Save a Model
    model_store.save(ctx, "churn", "v1.0", {
        "algorithm": "random_forest",
        "trees": 100
    })

    # --- 5. B-Tree Store (Key-Value) ---
    # Open a B-Tree named "users"
    # Use new_btree to create a new store, or open_btree for existing ones.
    # BtreeOptions.name is optional if you pass the name directly to new_btree.
    btree = db.new_btree(ctx, "users", tx)
    
    # Add a Key-Value pair
    btree.add(ctx, Item(key="user_123", value="John Doe"))
    
    # Find a value
    if btree.find(ctx, "user_123"):
        # Fetch the value
        items = btree.get_values(ctx, Item(key="user_123"))
        if items and items[0].value:
            print(f"Found User: {items[0].value}")

    # --- 6. Complex Keys (Structs) ---
    # Define a composite key using a dataclass
    from dataclasses import dataclass
    from sop.btree import IndexSpecification, IndexFieldSpecification

    @dataclass
    class EmployeeKey:
        region: str
        department: str
        id: int

    # Create B-Tree with custom index (Region -> Dept -> ID)
    # This enables fast prefix scans (e.g., "Get all employees in US")
    spec = IndexSpecification(index_fields=(
        IndexFieldSpecification("region", ascending_sort_order=True),
        IndexFieldSpecification("department", ascending_sort_order=True),
        IndexFieldSpecification("id", ascending_sort_order=True)
    ))
    
    # Pass spec as index_spec argument
    employees = db.new_btree(ctx, "employees", tx, index_spec=spec)

    # Add item with complex key
    employees.add(ctx, Item(
        key=EmployeeKey("US", "Sales", 101), 
        value={"name": "Alice"}
    ))

    # --- 7. Simplified Lookup (Dictionary Keys) ---
    # You can search for items using a plain dictionary, without needing the original dataclass.
    # This is useful for consumer apps that just need to read data.
    
    # Open existing B-Tree (no IndexSpec needed, it's loaded from disk)
    employees_read = db.open_btree(ctx, "employees", tx)
    
    # Search using a dict matching the key structure
    if employees_read.find(ctx, {"region": "US", "department": "Sales", "id": 101}):
        print("Found Alice!")

    # --- 8. Text Search ---
    # Open a Search Index
    idx = db.open_search(ctx, "articles", tx)
    idx.add("doc1", "The quick brown fox")

# Transaction commits automatically here.
# If an exception occurs, it rolls back.
```

### 6. Querying Data

You can perform queries in a separate transaction (e.g., Read-Only).

```python
# Begin a Read-Only transaction (optional optimization)
with db.begin_transaction(ctx, mode=TransactionMode.ForReading.value) as tx:
    
    # --- Vector Search ---
    vs = db.open_vector_store(ctx, tx, "products")
    hits = vs.query(ctx, vector=[0.1, 0.5, 0.8], k=5)
    for hit in hits:
        print(f"Vector Match: {hit.id}, Score: {hit.score}")

    # --- Model Retrieval ---
    ms = db.open_model_store(ctx, tx, "classifiers")
    model = ms.get(ctx, "churn", "v1.0")
    print(f"Loaded Model: {model['algorithm']}")

    # --- B-Tree Lookup ---
    us = db.open_btree(ctx, "user_store", tx)
    if us.find(ctx, "user1"):
        # Fetch the current item
        item = us.get_current_item(ctx)
        print(f"User Found: {item.value}")
```

**Performance Tip**: For **Vector Search** workloads that are "Build-Once-Query-Many", use `TransactionMode.NoCheck`. This bypasses transaction overhead for maximum query throughput.

```python
# High-performance Vector Search (No ACID checks)
with db.begin_transaction(ctx, mode=TransactionMode.NoCheck.value) as tx:
    vs = db.open_vector_store(ctx, tx, "products")
    hits = vs.query(ctx, vector=[0.1, 0.5, 0.8], k=5)
```

## Advanced Configuration

### Logging

You can configure the internal logging of the SOP engine (Go backend) to output to a file or standard error, and control the verbosity.

```python
from sop import Logger, LogLevel

# Configure logging to a file with Debug level
Logger.configure(LogLevel.Debug, "sop_engine.log")

# Or configure logging to stderr (default) with Info level
Logger.configure(LogLevel.Info)
```

### Transaction Options

You can configure timeouts, isolation levels, and more.

```python
from sop import TransactionOptions

opts = TransactionOptions(
    max_time=15,  # 15 minutes timeout
)

tx = db.begin_transaction(ctx, options=opts)
```

### Clustered Mode

For distributed deployments, switch to `DatabaseType.Clustered`. This requires Redis for coordination.

```python
from sop.ai import DatabaseType

db = Database(
    ctx, 
    stores_folders=["/mnt/shared_data"], 
    type=DatabaseType.Clustered
)
```

### SOP Data Manager Visibility

To ensure your Python-created databases are visible and fully manageable in the **SOP Data Manager** (GUI), you should use the `setup` method during initialization. This persists your configuration options (like store paths, erasure coding settings, etc.) so the UI can discover them.

```python
# 1. Define Options
options = DatabaseOptions(
    stores_folders=["./data/my_db"], 
    type=DatabaseType.Standalone
)

# 2. Persist Options (One-time setup or on startup)
# This saves 'dboptions.json' in the database folder
Database.setup(ctx, options)

# 3. Initialize
db = Database(options)
```

Later, you (or the Data Manager) can inspect these options using `get_options`:

```python
# Retrieve config from a path
opts = Database.get_options(ctx, "./data/my_db")
print(f"Database Type: {opts.type}")
```

### Clustered Backend Setup (Cassandra + Redis)

For production environments using `Clustered` mode, you should initialize both Cassandra (for storage) and Redis (for distributed locking and caching) at application startup.

```python
from sop import Redis
from sop.cassandra import Cassandra
from sop.database import Database, DatabaseOptions, DatabaseType

# 1. Initialize Redis (Required for Locking/Caching in Clustered mode)
# Format: redis://<user>:<password>@<host>:<port>/<db_number>
Redis.initialize("redis://:password@localhost:6379/0")

# 2. Initialize Cassandra (Global Connection)
Cassandra.initialize({
    "cluster_hosts": ["127.0.0.1"],
    "consistency": 1,          # 1 = LocalQuorum
    "authenticator": {
        "username": "cassandra",
        "password": "password"
    }
})

# ... Application Logic ...

# Connect to a specific tenant's keyspace
db = Database(DatabaseOptions(
    keyspace="tenant_1",
    type=DatabaseType.Clustered
))

# ...

# Cleanup on shutdown
Redis.close()
Cassandra.close()
```

## Architecture

SOP uses a split architecture:
1.  **Core Engine (Go)**: Handles disk I/O, B-Tree algorithms, caching, and transactions. Compiled as a shared library (`.dylib`, `.so`, `.dll`).
2.  **Python Wrapper**: Uses `ctypes` to interface with the Go engine, providing a Pythonic API (`sop` package).

## Project Links

*   **Source Code**: [GitHub - sharedcode/joltrin](https://github.com/sharedcode/joltrin)
*   **PyPI**: [sop4py](https://pypi.org/project/sop4py)

## Contributing

Contributions are welcome! Please check the `CONTRIBUTING.md` file in the repository for guidelines.
