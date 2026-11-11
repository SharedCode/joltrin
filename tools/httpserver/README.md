# SOP Data Manager (HTTP Server & UI)

A powerful, web-based management suite for SOP B-Tree repositories. This tool transforms SOP from a library into a full-fledged SOP HTTP Server with a rich User Interface.

## Installation

### Download Binaries
You can download the latest pre-compiled binaries for macOS, Linux, and Windows from the [Releases Page](https://github.com/sharedcode/joltrin/releases).

### Build from Source
```bash
go build -o sop-manager ./tools/httpserver
```

## Features
- **Phase 1 Authentication Facade**: A minimal bearer-token auth flow backed by `Config.Users` and the setup-root user, with OAuth2-style form login support and configurable token expiry.
- **Multi-Database Support**: Seamlessly switch between different databases (e.g., Local Dev, Production Cluster) from a single UI.
- **Full CRUD Management**: Create, Read, Update, and Delete records directly from the UI.
- **RDBMS-Grade Indexing**: Leverages SOP's `IndexSpecification` to support rich, compound indexes with multiple fields and custom sort orders.
- **Universal Database Management**: Manage *any* SOP database and B-Tree, regardless of the data type or language used to create it.
- **Store Listing**: View and manage all B-Trees in a database.
- **Bulk Management Friendly**: Efficiently handle large datasets and batch operations.
- **Data Grid**: Browse key/value pairs with pagination, resizable columns, and row highlighting.
- **Navigation**: Seamlessly navigate between data pages (Next/Previous) to explore large datasets.
- **Advanced Search**: Perform complex queries on multi-field keys to jump directly to specific records.
- **JSON Inspection**: View and edit complex value structures as formatted JSON.
- **Database Configuration View**: Inspect critical database settings directly from the UI, including Erasure Coding parameters, Redis configuration, and Cache types.
- **CEL Expression Editor**: Integrated editor for defining and testing Common Expression Language (CEL) scripts for custom sorting logic.
- **Bulk Operations**: Select multiple items in the grid for batch deletion.
- **Natural Language SQL**: Execute complex Selects, Joins, and CRUD operations using plain English via the AI Copilot.
- **Mobile Optimized**: Fully responsive design with touch-friendly controls and a fullscreen AI assistant for managing data on the go.

## The Power of the SOP Data Manager: From Embedded to Enterprise

The SOP Data Manager is not just a simple admin tool; it is a demonstration of the SOP architecture's flexibility. Because SOP is a library that turns your application into the database engine, this SOP HTTP Server is effectively a **universal database server** that can adapt to any scale.

### 1. Embedded & Single-Node
For local development, IoT devices, or single-user desktop applications, the SOP Data Manager acts as a standalone SOP HTTP Server.
- **Zero Setup**: Just point it to a folder.
- **Instant Access**: Serve one or many databases from a single lightweight process.
- **Use Case**: A developer inspecting their local data, or an embedded device exposing a management interface.

### 2. Enterprise Swarm (Clustered Peer Nodes)
In a large-scale enterprise environment, the SOP Data Manager shines as a stateless, high-performance peer node within your storage swarm. 
- **True Scalability**: Deploy the SOP Data Manager behind a **Load Balancer** on **Kubernetes**, **AWS EC2 Auto Scaling Groups**, or **Linux Bare Metal** farms. Because the frontend UI uses relative paths, users can connect their browser to any `httpserver` instance in the network, and the traffic will natively route and distribute perfectly.
- **Bare Metal I/O**: Each `httpserver` acts as a peer server capable of doing heavy bare-metal I/O on attached network volumes (EBS/EFS) or local SSDs, distributing the computational load of serving massive client requests across many machines.
- **Cluster Coordination**: When configured with `"mode": "clustered"` and connected to your infrastructure's **Redis cluster**, the HTTP servers seamlessly negotiate distributed locks, caching, and state coordination across the entire swarm.
- **Safe Production Access**: You can view, edit, and manage live production data safely. Because every HTTP server peer respects the Redis distributed locks, manual admin actions or AI Copilot queries never corrupt data or violate ACID properties, even while your high-throughput microservices are hammering the same data files.
- **Distributed Script Execution**: With built-in scripting support and API endpoints, users can cleanly offload heavy computing tasks directly to the cluster. Whether operating in standalone, peer-to-peer, or fully clustered mode, these intensive script executions run correctly and safely across nodes, orchestrated flawlessly by the Redis Cluster backend.
- **AI Agent Swarm**: Because the autonomous AI RunLoop and Copilot engine are core components baked directly into the HTTP Server, scaling the server instantly creates an AI swarm. Heavy reasoning tasks, autonomous RAG pipelines, and concurrent chat sessions are naturally load-balanced across your peer nodes, yielding a massive, horizontally scalable AI workforce analyzing your enterprise data in parallel.

## Usage

### Prerequisites
- Go 1.26.11+ installed.
- Run from the root of the `sop` repository so `go run ./tools/httpserver` resolves the module correctly.
- Either:
  - an existing SOP database folder for single-database mode, or
  - a `config.json` file for multi-database mode.
- Building code paths that include the AI package requires a working `clang`/C toolchain:
  - macOS: install Xcode Command Line Tools or Xcode.
  - Linux: install `clang` from your distro package manager.
  - Windows: install LLVM/Clang, or use an environment that provides it such as MSYS2/MinGW or Visual Studio Build Tools with LLVM support.
- Redis is required only when running with `"mode": "clustered"`.
- An LLM API key is required only if you plan to use AI Copilot features.

No separate frontend build step is required; the UI assets are embedded into the Go binary.

### Environment Variables

- `SOP_ALLOW_INVALID_MAP_KEY`: Set to `true` to bypass the validation that requires Map Key types to have an Index Specification or CEL Expression. This is primarily for testing purposes.
- `SOP_ROOT_PASSWORD`: Optional legacy compatibility value for admin override flows; the Phase 1 login/auth facade now prefers the configured root user from `Config.Users`.

### Phase 1 Auth Facade (V1)

The current V1 auth path is intentionally small:

- The setup wizard creates the initial root user in `Config.Users`.
- The browser/login flow calls `POST /api/auth/login` with JSON or `application/x-www-form-urlencoded` credentials.
- The login endpoint returns a bearer token (`access_token`) together with the authenticated user role.
- Protected calls should send `Authorization: Bearer <token>`.
- Session tokens expire after `session_token_ttl_minutes` (default: 30 minutes). This gives the system a simple renewal window for resumed work.
- The token facade is intentionally abstract so the signed bearer-token flow can evolve further while preserving the existing refresh-session path for renewal and revocation.

This keeps the first release easy to reason about while still allowing Copilot sessions to resume with the same authenticated context.

### Recent Progress (2026-06-15)

- The bearer-token flow now supports signed access tokens for both browser sessions and remote clients (CLI, scripts, microservices) via `Authorization: Bearer <token>`.
- Access-token expiry defaults to 30 minutes, with refresh-token renewal preserved for the DB-backed session lifecycle.
- The auth facade now supports a dedicated `SOP_SESSION_SECRET` / `session_secret` signing secret for the hot path, instead of depending on password-derived defaults.
- Store-management flow improvements now preserve runtime schema metadata, reduce false structural-change validation, and improve add-item / wrong-type handling in the UI and backend path.

## User Guide

### 1. Managing Stores

The Data Manager provides a comprehensive interface for managing your B-Tree stores.

*   **Add Store**: Click the **+** button in the sidebar next to "Stores". This opens a modal where you can define the store name, key/value types, and advanced settings like Index Specifications or CEL Expressions.
*   **Edit Store**: Select a store, then click the **Pencil** icon in the main header.
    *   *Note*: For populated stores, structural fields (Key/Value types) are locked to prevent data corruption. You can still edit the Description and Cache settings.
*   **Delete Store**: Select a store, then click the **Trash** icon in the main header. You will be asked to type the store name to confirm deletion.

### 2. AI Copilot & Scripts

The built-in AI Copilot allows you to interact with your data using natural language and automate tasks using Scripts.

*   **Open Copilot**: Click the floating chat button in the bottom-left corner.
*   **Natural Language SQL**: Perform complex queries without writing code.
    *   *Select*: "Find all users where Age > 25 and City is 'Seattle'."
    *   *Join*: "Join 'Users' and 'Orders' on 'UserID' and show me the top 5 spenders."
    *   *CRUD*: "Add a new user named 'John Doe' with age 30." or "Delete the record with key 'user_123'."
*   **Record Script**: Tell the assistant to record your actions.
    *   *Command*: "/script new MyScript" (then perform actions).
    *   The assistant will generate the steps and save them to the `SystemDB`.
    *   *Stop*: "/script stop"
*   **Play Script**: Execute a saved script.
    *   *Command*: "/script run MyScript"
    *   The assistant will execute the recorded steps and display the results.
*   **Parameterized Scripts (Beta)**: You can now record scripts with placeholders and pass values at runtime.
    *   *Record*: "/script new FindUser" (then find user by 'UserID').
    *   *Play*: "/script run FindUser UserID='123'"
*   **View Script Steps**:
    *   If the assistant returns a script trace, it will be displayed as an interactive tree view in the chat.
    *   You can also view raw script data in the **SystemDB** (see below).

### 3. Advanced Queries (SQL Joins & Scripts as Views)

The AI Copilot supports complex queries that mimic SQL operations, even though SOP is a NoSQL Key-Value store. It achieves high performance by leveraging the underlying B-Tree structure.

*   **Inner Join & Prefix Queries**: Ask the assistant to join two stores.
    *   *Command*: "Join 'Users' and 'Orders' on 'UserID'."
    *   **Performance**: The engine utilizes **Prefix Queries** in Hash Joins, fully exploiting the B-Tree's `Find` and navigation APIs. This allows for efficient range queries and lookups without full table scans.
*   **Scripts as Views**: You can use a Script as a data source in your queries, effectively treating it as a SQL View.
    *   *Command*: "Select * from 'MyScript' where Age > 20."
    *   *Join*: "Join 'Users' and 'MyScript' on 'RegionID'."
    *   **Efficiency**: Because Scripts stream their results, using them as Views is extremely lightweight. The system pipelines the data, allowing for complex transformations with minimal memory footprint.
*   **Agent Streaming**: Results are streamed in real-time from the Agent to the Data Manager.
    *   **Benefit**: You see results immediately as they are found.
    *   **Resource Utilization**: This streaming architecture ensures **lightweight resource utilization**, as the server doesn't need to buffer the entire result set. It combines the raw speed of B-Tree navigation with the flexibility of an AI agent to provide a complete, high-performance solution.

### 4. SystemDB

The `SystemDB` is a special database that holds internal SOP metadata, including Registry information and Scripts.

*   **View SystemDB**: In the "Databases" dropdown in the sidebar, select **SystemDB** (if available/configured).
*   **View Scripts**:
    1.  Select the **Script** store within SystemDB.
    2.  Browse the list of scripts.
    3.  Click on a script to view its details in the "Item Details" panel. The `Value` field contains the JSON definition of the script steps.

### Running

You can run the SOP HTTP Server in two ways: using command-line flags for a single database, or using a configuration file for multiple databases.

#### Option 1: Single Database (Quick Start)

From the root of the `sop` repository:

```bash
go run ./tools/httpserver -database /path/to/your/sop/data
```

#### Developer trial with the built-in SOP KB

If you want to exercise the SOP Knowledge Base preload flow from source:

```bash
cd ai/cmd/knowledge_compiler
./run.sh
cd ../..
go run ./tools/httpserver
```

The generated file lives at `ai/sop_base_knowledge.json`. When the SOP KB preload path is used in the UI, the server automatically detects and loads the first matching file from `sop_base_knowledge.json`, `ai/sop_base_knowledge.json`, or `../ai/sop_base_knowledge.json`.

#### Option 2: Multiple Databases (Recommended)

Create a JSON configuration file (e.g., `config.json`) to define your environments:

```json
{
  "port": 8080,
  "pageSize": 40,
  "root_password": "optional_admin_password",
  "enable_rest_auth": false,
  "production_mode": false,
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

> **Note**: This example shows the structure of `system_db`, but for a fresh setup it is usually better to let the Data Manager Setup Wizard create and initialize it.

**Security Note:** For production environments, it is recommended to set the root password using the `SOP_ROOT_PASSWORD` environment variable instead of storing it in the config file. The environment variable takes precedence over the config file setting.

```bash
export SOP_ROOT_PASSWORD="my_secure_password"
go run ./tools/httpserver -config config.json
```

Run the SOP HTTP Server with the config file:

```bash
go run ./tools/httpserver -config config.json
```

### Importing a custom Knowledge Base JSON

You can use the same compile-and-import workflow to bring a custom KB into a local or deployed SOP Data Manager installation:

1. Author or adapt the source Markdown and the KB curation manifest for your domain.
2. Generate the importable JSON with the KB compiler:
   ```bash
   cd ai/cmd/knowledge_compiler
   ./run.sh
   ```
   If you are using a different curated input file, update the compiler invocation accordingly.
3. Start the Data Manager:
   ```bash
   go run ./tools/httpserver
   ```
4. In the UI, open the Space/KB view and click the Import JSON button to load the generated file.

This lets end users package their own curated KB as a JSON file and load it into SOP Data Manager without changing the server code.

### Administrative Features

#### Store Metadata Override
The Web UI allows administrators to perform advanced maintenance tasks, such as updating the **Index Specification** and/or **CEL Expression** of an existing store. These are structural changes that can affect data retrieval and sorting.

*   **Protection**: These operations are protected and require the `RootPassword` (configured via JSON or `SOP_ROOT_PASSWORD` env var).
*   **Workflow**: Structural fields are disabled by default for non-empty stores. Click the **Unlock** button (lock icon), enter the Admin Token, and upon validation, the fields become editable.
*   **Warning**: A warning is displayed immediately after validation to remind you that modifying the schema of a populated store is an advanced operation that may impact existing data ordering.

### Important Note on Concurrency

If database(s) are configured in **standalone mode**, ensure that the http server is the only process/app running to manage the database(s). Alternatively, you can add its HTTP REST endpoint to your embedded/standalone app so it can continue its function and serve HTTP pages at the same time.

If **clustered**, no worries, as SOP takes care of Redis-based coordination with other apps and/or SOP HTTP Servers managing databases using SOP in clustered mode.

### Accessing
Open your browser and navigate to:
http://localhost:8080

## Topology and Architecture

SOP is designed to break the traditional database monolith. Unlike conventional RDBMS where a central server process controls all data access, SOP is a **library** that turns your application cluster into the database engine itself.

### Deployment Models

Understanding how to deploy SOP in the enterprise requires distinguishing between **Standalone** and **Clustered** modes.

#### 1. Standalone Mode
In **Standalone** mode, the database is managed with the expectation that the SOP HTTP Server instance (or your application) is the sole manager of the data.

- **Topology**: Single Instance.
- **Use Case**: Local development, single-user desktop apps, or embedded scenarios.
- **Concurrency**: You can open multiple browser tabs or windows pointing to the same SOP HTTP Server. The server handles the coordination internally.
- **Guarantee**: Changes are persisted with full ACID guarantees.
- **Constraint**: Only *one* application process (the HTTP server) should be accessing the database files at a time.

#### 2. Clustered Mode
In **Clustered** mode, the database is designed to be managed by many SOP HTTP Server instances and application nodes simultaneously.

- **Topology**: Distributed / Swarm.
- **Use Case**: Kubernetes clusters, EC2 auto-scaling groups, or bare-metal server farms.
- **Coordination**: All instances must be configured with the same **Redis** cluster address. Redis is used for distributed locking and transaction coordination.
- **Scalability**: You can spin up as many instances of the SOP Data Manager as needed.
- **Guarantee**: Management actions from any node are orchestrated properly with ACID guarantees. If User A updates a record via Server 1, and User B tries to update the same record via Server 2, SOP uses Redis to detect the conflict and ensure data integrity.

### The "Masterless" Philosophy

The **SOP Data Management Suite** follows this philosophy:
- **No Central Server**: This tool is not a "master" node. It is simply another client application (a "viewer") that connects to the same distributed storage backend as your production apps.
- **Flat Architecture**: You can spin up as many instances of this management tool as needed, anywhere in your cluster or on your local machine.
- **Masterless**: Since SOP uses decentralized coordination (via Redis or File Locking), this management tool can perform full CRUD operations safely alongside your high-throughput production workloads without becoming a bottleneck or a single point of failure.
- **"All Masters"**: In the SOP ecosystem, every running process (including this UI) is a "master" capable of reading and writing data directly to the storage layer, coordinated only by the lightweight locking mechanism.
- **Swarm Participation**: Each user managing data via this app participates in **"swarm" computing**. Their changes to records are efficiently merged (or rejected if conflicting) by SOP, all under strict **ACID transaction guarantees**.
- **Network & Cloud Ready**: While it works great on a local laptop disk, SOP is built to manage data on **Network Attached Storage (NAS)**, **S3-mounted drives**, or **Cloud Volumes**. This allows you to manage massive datasets that exceed local disk capacity, shared across the entire cluster.

## Performance

SOP is designed for high performance. You can measure the performance on your own hardware using the included benchmark tool.

```bash
go run tools/benchmark/main.go --count 10000 --path /tmp/sop_bench
```

Typical results on a modern laptop (M1/M2/M3 Mac):
- **Insert**: ~20,000+ ops/sec
- **Read**: ~50,000+ ops/sec
- **Delete**: ~30,000+ ops/sec

## Demo & Tutorial

The SOP Data Manager includes a built-in **Setup Wizard** that can populate your database with a sample E-commerce dataset (Users, Products, Orders).

This dataset is perfect for exploring the **AI Copilot** capabilities:
1.  **Natural Language Queries**: "Show me all users from France"
2.  **Joins**: "Find orders for user 'James Smith'"
3.  **Scripts**: Create reusable workflows for complex reporting.

## Language Bindings & Downloads

> **New to SOP?** Check out the **[Getting Started Guide](../../docs/GETTING_STARTED.md)** for a step-by-step tutorial.

When you download a release from GitHub, you can choose between:

1.  **Platform Bundles (Recommended)**: A single `.zip` file containing the `sop-manager` server AND all language bindings for your specific OS/Architecture.
    *   `sop-bundle-macos-arm64.zip` (Apple Silicon)
    *   `sop-bundle-macos-amd64.zip` (Intel Mac)
    *   `sop-bundle-linux-amd64.zip` (Linux x64)
    *   `sop-bundle-linux-arm64.zip` (Linux ARM64)
    *   `sop-bundle-windows-amd64.zip` (Windows)

    **Inside the Bundle:**
    *   `sop-manager`: The server executable.
    *   `libs/`: The shared library (`.dylib`, `.so`, `.dll`) for your OS.
    *   `python/`: Python `.whl` package.
    *   `java/`: Java `.jar` library.
    *   `dotnet/`: C# `.nupkg` package.

2.  **Individual Artifacts**: If you only need a specific language package, you can download them separately:
    *   **Python**: `sop-x.y.z-py3-none-any.whl`
    *   **C#**: `Sop.x.y.z.nupkg`
    *   **Java**: `sop-java-x.y.z.jar`

### Installation

#### 1. Start the Server
Unzip your bundle and run the manager:
```bash
./sop-manager

- **Backend**: Go HTTP server using `sop/infs` to open B-Trees as `[any, any]`.
- **Frontend**: Single HTML file with vanilla JS for API interaction.
- **Performance**: The browser is highly responsive because SOP includes a built-in caching layer, even in Standalone mode, ensuring fast access to frequently viewed nodes.

## Security & Deployment Modes

For details on how to configure the server for **Internal (Open)** vs **Public (Secure)** modes, including how to secure the REST API with Bearer tokens, please see [SECURITY.md](SECURITY.md).


## API Contracts & Models

The `httpserver` operates as a strictly typed headless REST platform. To guarantee a stable schema that can support third-party integrations, SDK generation, and multiple UI paradigms (e.g., React, Vue, Native Mobile), we have formally decoupled our network Data Transfer Objects (DTOs) from our core Database types. 

All public API models are centralized in `api_models.go`.

### Space Management
- `CreateSpaceRequest`: The JSON payload required to provision a new standard or AI-enhanced Knowledge Base Space.
  - Required fields: `database_name`, `space_name`
  - Optional fields: `attributes` (for Knowledge Base configurations).
- `PreloadSpaceRequest`: Payload designed to bootstrap a complete Space out of an overarching template.
- `TemplateMetadata`: A schema describing bundled templates.

### Data Ingest & Streaming
- `IngestSpaceRequest`: Initiates background chunk/document ingestion into a structured dataset.
- `SpaceIngestChunk`: Represents a single unified chunk of parsed document data or generalized knowledge, unifying embeddings (`vectors`), textual abstractions (`text`, `description`), and ML derivatives (`summaries`).

### Item CRUD Operations 
Instead of passing inline schemas, Item Management natively requires explicit struct definitions:
- `AddSpaceCategoryRequest`: JSON schema for provisioning a new hierarchical B-Tree namespace. 
- `AddSpaceItemRequest`: Adds a single Item metadata slice inside a Category Namespace.
  - Critical Fields: `category_id`, `data` (an unconstrained `map[string]any` allowing scalable entity storage decoupled from strictly routed keys).
- `AddSpaceItemsBatchRequest`: Provides batching mechanisms scaling massive Item loading via an array of the above.
- `UpdateSpaceItemRequest`: Edits targeted metadata of an existing dataset.

### ViewModels
- `SpaceItemView`: Decouples our generic, extremely fast internal database types (`Item[T]`) from UI-friendly rendering maps. This restricts massive over-network payloads by curating exact display requirements (`id`, `category`, `text`, `description`, `summaries`).

### Export, Import, and Knowledge Representation Models
While the `httpserver` package limits bandwidth via `SpaceItemView` for UI interaction, importing and exporting entire Knowledge Bases involves full structural fidelity. To facilitate 3rd-party integrations (like transferring spaces between databases, or cross-platform portability without leaking internal DB UUIDs), we rely on generic engine shapes:

- `ExportItem[T]`: Acts as the canonical network schema for full Item dumps (`/api/v1/spaces/{database_name}/{space_name}/export` and `import`). 
  - Emits string representations of `Category` paths directly instead of UUIDs, explicitly erasing physical B-Tree indices to allow portability across entirely different Data-Stores or installations.
  - Retains all structural memory assets (`summaries`, `positions`, `vector_hash` and the deeply typed generic `Data [T]`).
  - *Note: Our `Ingest` tools and `SpaceIngestChunk` directly mimic this conceptual shape, acting as its programmatic precursor.*

- `Category`: Represents the pure layout of hierarchical nodes (formerly Centroids). Defines a Directed Acyclic Graph (DAG) for categories, ensuring multi-parent (`parents`) linkage, radius, mathematical centroids (`center_vector`), and item tracking (`item_count`).
