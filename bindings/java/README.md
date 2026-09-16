# SOP for Java (sop4j)

SOP bindings for Java allow you to use the high-performance SOP storage engine directly from your Java applications.

## Features

*   **Full SOP Capability**: Access all SOP features including B-Tree management, ACID transactions, and Clustered mode (Redis).
*   **Native Performance**: Uses JNA (Java Native Access) to call the optimized Go shared library directly.
*   **Type Safety**: Generic `BTree<K, V>` implementation for type-safe key-value storage.
*   **Zero Dependencies**: The core binding only requires JNA and Jackson (for JSON serialization).

## SOP Data Management Suite

SOP includes a powerful **Data Management Suite** that provides **full CRUD** capabilities for your B-Tree stores. It goes beyond simple viewing, offering a complete GUI for inspecting, searching, and managing your data at scale.

*   **Web UI**: A modern, responsive interface for browsing B-Trees, managing stores, and visualizing data.
*   **AI Copilot**: Integrated directly into the UI, the AI Copilot can help you write queries, explain data structures, and even generate code snippets. Configure your LLM provider through the Setup Wizard on first launch.
*   **SystemDB**: View and manage internal system data, including registry information and transaction logs.

To launch the Data Manager, download the all-in-one single-file installer from [SOP Releases](https://github.com/SharedCode/joltrin/releases). Alternatively, you can use the Go toolchain:

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

Example:

```java
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class CallScript {
    public static void main(String[] args) throws Exception {
        String jsonBody = "{\"name\":\"user_audit\", \"category\":\"general\", \"args\":{\"user_id\":999}}";
        
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create("http://localhost:8080/api/scripts/execute"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
        System.out.println(response.body());
    }
}
```

## Prerequisites

*   **Java**: JDK 11 or higher.
*   **Maven**: For building the project.
*   **Go**: (Optional) Only if you need to rebuild the shared library (`libjsondb.dylib`/`.so`/`.dll`).

## Installation

1.  Clone the repository:
    ```bash
    git clone https://github.com/SharedCode/joltrin.git
    cd joltrin/bindings/java
    ```

2.  Build the project:
    ```bash
    mvn clean install
    ```

## Quick Start

### 1. Basic Usage (Standalone - Local disk or shared Network drive)

```java
import com.sharedcode.sop.*;
import java.util.Collections;

public class QuickStart {
    public static void main(String[] args) {
        try (Context ctx = new Context()) {
            // Configure Database
            DatabaseOptions dbOpts = new DatabaseOptions();
            dbOpts.stores_folders = Collections.singletonList("sop_data");
            dbOpts.type = DatabaseType.Standalone;
            Database db = new Database(dbOpts);

            // Start Transaction
            try (Transaction trans = db.beginTransaction(ctx)) {
                // Create B-Tree
                BTree<String, String> btree = db.newBtree(ctx, "users", trans, null, String.class, String.class);

                // Add Item
                btree.add("user1", "Alice");

                // Commit
                trans.commit();
            }
        } catch (SopException e) {
            e.printStackTrace();
        }
    }
}
```

### 2. Clustered Mode (Redis)

```java
import com.sharedcode.sop.*;

// Initialize Redis (Global)
Redis.initialize("redis://localhost:6379");

// Use DatabaseType.Clustered (1)
dbOpts.type = DatabaseType.Clustered; 

// ... operations ...

// Cleanup
Redis.close();
```

## SOP Data Management Suite

SOP includes a powerful **Data Management Suite** that provides **full CRUD** capabilities for your B-Tree stores. It goes beyond simple viewing, offering a complete GUI for inspecting, searching, and managing your data at scale.

To launch it, simply run:

```bash
sop-httpserver
```

### Key Capabilities

*   **Universal Database Server**: Acts as a standalone server for local development or a stateless management node in a clustered enterprise swarm (Kubernetes/EC2).
*   **Full Data Management**: Perform comprehensive CRUD (Create, Read, Update, Delete) operations on any record directly from the UI.
*   **High-Performance Search**: Utilizes B-Tree positioning for instant lookups, even in datasets with millions of records. Supports both simple keys and complex composite keys (e.g., searching by `Country` + `City`).
*   **Efficient Navigation**: Smart pagination and traversal controls (First, Previous, Next, Last) allow you to browse massive datasets without performance penalties.
*   **Bulk Operations**: Designed for rapid-fire management of records with a clean, non-distracting interface.
*   **Responsive & Cross-Platform**: Works across diverse monitor sizes and devices without layout breakage.
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

### 5. Remote Execution
You can trigger these scripts from your Java code via the REST API:

```java
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class RemoteScript {
    public static void main(String[] args) throws Exception {
        String json = "{\"message\": \"/play user_audit user_id=999\", \"agent\": \"sql_admin\"}";
        
        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create("http://localhost:8080/api/ai/chat"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(json))
                .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
        System.out.println(response.body());
    }
}
```

### 3. Cassandra Backend

SOP integrates with Apache Cassandra to empower it with features it natively lacks, such as full ACID transactions, efficient Blob storage, and Swarm Computing capabilities.

This mode allows organizations to leverage their existing Cassandra infrastructure and Ops teams for managing the **Registry** and its replication. Meanwhile, SOP handles the heavy lifting of data management, providing Erasure Coding-based replication for data blobs and B-Tree nodes.

```java
import com.sharedcode.sop.*;
import java.util.Collections;

// 1. Configure Cassandra
CassandraConfig cassConfig = new CassandraConfig();
cassConfig.clusterHosts = Collections.singletonList("localhost");
cassConfig.replicationClause = "{'class':'SimpleStrategy', 'replication_factor':1}";
Cassandra.initialize(cassConfig);

// 2. Configure Redis (Required for Clustered mode)
Redis.initialize("redis://localhost:6379");

// 3. Configure Database
DatabaseOptions dbOpts = new DatabaseOptions();
dbOpts.type = DatabaseType.Clustered;
dbOpts.keyspace = "sop_test"; // Ensure this keyspace exists in Cassandra
Database db = new Database(dbOpts);

// ... operations ...

// Cleanup
Redis.close();
Cassandra.close();
```

## Examples

The `src/main/java/com/sharedcode/sop/examples` directory contains comprehensive examples:

| Example | Description |
| :--- | :--- |
| `BTreeBasic` | Basic CRUD operations (Add, Update, Remove, Find). |
| `BTreeBatched` | High-performance batch insertion and iteration. |
| `BTreePaging` | Forward and backward paging through large datasets. |
| `BTreeComplexKey` | Using composite keys with custom sort orders. |
| `BTreeMetadata` | Using "ride-on" metadata in keys. |
| `ConcurrentTransactionsDemoStandalone` | Multi-threaded ACID transactions (Standalone). |
| `ConcurrentTransactionsDemoClustered` | Multi-threaded ACID transactions (Clustered/Redis). |
| `CassandraDemo` | Using Cassandra as the storage backend. |
| `LoggingDemo` | Configuring the SOP logger. |

`examples/spring-boot-checkout-store` is a separate Maven module, a real
Spring Boot REST service (`CheckoutOrderController` -> `CheckoutOrderStore`
-> `BTree<String, CheckoutOrder>`) using sop4j as its persistence layer
instead of JPA. Verified end to end: `mvn compile` against the real
sop4j jar, then run against the native `libjsondb` library (built via
`go build -buildmode=c-shared` from `bindings/main`) and exercised with
curl through create/read/update/list/delete, all real B-Tree operations,
not stubs. See its own README for the exact commands.

To run an example:

```bash
mvn compile exec:java -Dexec.mainClass="com.sharedcode.sop.examples.BTreeBasic" -Djna.library.path=../../
```

## Architecture

The Java binding is a thin wrapper around the SOP Go library (`libjsondb`).
1.  **Java Layer**: Provides a clean, idiomatic Java API (`BTree`, `Transaction`, `Database`).
2.  **JNA Layer**: Marshals calls to the C-shared library.
3.  **Go Layer**: The core SOP engine handles storage, indexing, and transactions.

## Troubleshooting

*   **`UnsatisfiedLinkError`**: Ensure `-Djna.library.path` points to the directory containing `libjsondb.dylib` (macOS), `libjsondb.so` (Linux), or `libjsondb.dll` (Windows).
*   **`SopException`**: Wraps errors returned by the underlying Go engine. Check the message for details.
