# SOP AI Kit
The `sop/ai` package is the **SOP AI Kit**. It extends SOP with AI-oriented storage, retrieval, scripting, and model-integration components.

It supports:
*   **Domain-Specific AI Copilots**: Adapt the Scripting engine to any vertical (Finance, Healthcare, Media).
*   **Embedded Applications**: Combine the database + scripting engine for edge and local deployments.
*   **Personal AI Copilots**: Privacy-first, web-based assistants.
*   **Enterprise AI Systems**: ACID-compliant RAG and autonomous agents.

It provides components for building local AI applications on top of SOP's B-Tree storage engine.

## Related Guides

*   [AI Copilot & Agent Architecture](../docs/AI_COPILOT.md)
*   [AI Script Architecture](AI_SCRIPT_ARCHITECTURE.md)
*   [SOP AI Copilot Usage](AI_COPILOT_USAGE.md)
*   [Vector Store Design](vector/VECTOR_STORE_DESIGN.md)
*   [Dynamic Vector Store Design](DYNAMIC_VECTOR_STORE_DESIGN.md)

## Core Components

### 1. New Memory Architecture (`ai/memory`) (Recommended)
The core user-facing and agent-driven storage engine. It is the recommended approach for new applications. It provides human-readable ontologies, structured data for the UI and Copilot, visual analysis tools within the Data Manager, and user-manageable overrides.
*   **Visual & Manageable**: Data Manager provides full UI integration to see how information is categorized. Users can manually inspect, manage, and override how the AI linked concepts, creating user-enrichable AI memories.
*   **Knowledge Bases**: Groups information logically rather than physically. Implements the `OpenKnowledgeBase` pattern.
*   **LLM Categorization**: Unlike legacy systems that use mathematics, this system uses LLMs to read data and determine its **Category** (e.g., "Billing", "HR").
*   **Deterministic Pre-Filtering**: Caching and filtering by human-readable categories allows UI Copilots to skip 99% of the search space instantly before any math is done.
*   **No Optimization Phase**: The memory system shapes items into semantic categories during normal operation, so it does not require a blocking `Optimize()` phase like the legacy K-Means vector path.
*   **Granular Items**: The memory system inherently understands relationship mappings and structured JSON arrays for document retrieval.
*   **Portable Knowledge Bases**: Because this memory structure is user-manageable, Knowledge Bases can be reviewed in the UI, exported, and reused across environments.

### 2. K-Means Vector Database (`ai/vector`) (Supported)
A persistent, ACID-compliant vector store optimized for mathematical throughput. While the new memory architecture is recommended, the K-Means VectorDB remains supported for high-volume pipelines, backward compatibility, and cross-language FFI bindings.
*   **Import Path to Spaces**: Legacy vector content can be imported into the newer Memory Spaces model. This allows older vector datasets to remain usable while the maintained product surface shifts toward Dynamic Vector Store and KnowledgeBase Studio workflows.
*   **Mathematical Centroids**: Zero-LLM ingestion. Vectors are grouped into clusters (Centroids) by cosine distance. This avoids API calls during ingestion.
*   **Blocking Optimizations (Weakness)**: Because centroids are purely mathematical, data drifts as vectors are added. It strictly requires periodic calls to the `Optimize()` function to recalculate the centers and shift data, during which writing may be blocked.
*   **Storage**: Uses SOP B-Trees to store vectors and metadata. Parallel queries point directly at mathematical clusters.
*   **Usage**: Suitable for high-volume ingestion pipelines and language-wrapper scenarios where mathematical clustering is preferred over category-oriented memory.
*   **Modes**: Supports **Standalone** (In-Memory Cache) for local use and **Clustered** (Redis Cache) for distributed deployments.
*   **Search**: Supports cosine similarity search with metadata filtering.
*   **Partitioning**: Designed to scale through natural partitioning.
*   **Optimization**: Built-in `Optimize()` method to rebalance clusters (Centroids) and ensure optimal search performance as data grows.
    *   **Scalability**: The optimization process is batched (commits every 200 items), allowing it to scale to millions of records without hitting transaction timeouts.
    *   **Operational Constraint**: To ensure data consistency and simplicity, the Vector Store enters a **Read-Only** mode during optimization. Any attempts to `Upsert` or `Delete` will return an error until `Optimize` completes.
    *   **Crash Recovery**: If the process crashes during optimization, simply restart it. The next call to `Optimize` will automatically detect and clean up any stale artifacts before starting fresh.
    *   **Rolling Version Safety**: When migrating items to a new optimization version, the system promotes the "Next" state to the "Current" state before overwriting it. This ensures that if the optimization process crashes mid-flight, the data remains accessible via the valid "Current" version, preventing data loss or corruption during the transition.
*   **Deletion & Cleanup**: Implements a **Tombstone** mechanism for efficient deletions.
    *   **Soft Delete**: `Delete()` marks items as deleted in both the Index and Content stores, ensuring they are immediately hidden from search results.
    *   **Garbage Collection**: The `Optimize()` process acts as a Garbage Collector. It detects these tombstones and performs a physical delete on the underlying data, reclaiming storage space during the maintenance cycle.
*   **Deduplication**: Optional deduplication check during ingestion. Can be disabled (`SetDeduplication(false)`) for maximum write performance when data is known to be unique.
*   **Rich Key Structure**: The Vector Store uses a specialized `ContentKey` struct as the B-Tree key.
    *   **Metadata Carrier**: Stores `CentroidID`, `Distance`, `Version`, and `Deleted` status directly in the key.
    *   **Efficiency**: Allows the system to perform structural operations (like filtering deleted items or finding vectors in a specific cluster) by scanning only the keys, without fetching the potentially large vector payload.
*   **Usage Modes**:
    *   **BuildOnceQueryMany**: Optimized for static datasets. Ingest data -> Call `Optimize()` -> Serve queries. Discards temporary build artifacts for efficiency.
    *   **Dynamic**: For systems with continuous updates. Maintains auxiliary structures to handle frequent inserts/deletes.
    *   **DynamicWithVectorCountTracking**: Specialized mode for external centroid management (e.g., Agents). It tracks vector counts per centroid to help you decide when to trigger `Optimize()`.

### 2. Versatile Scripting Engine (`ai/agent`)
The core of the **Computing Platform**. It allows you to define complex, multi-step workflows using **Natural Language Programming** (Scripts/Scripts).
*   **Adaptable**: Can be tailored to any domain (Finance, Media, etc.) by registering custom Tools.
*   **Swarm Computing**: Async execution of steps for high-performance parallel processing.
*   **Agent Framework**: Define agents with Personality, Memory, and Tool access.
*   **Interoperable**: Scripts are stored as JSON and can be managed/visualized via the SOP Data Manager.

See [ai/agent/README.md](agent/README.md) for full documentation on Scripts, Swarm Computing, and the Tool Registry.

### 3. Memory Architecture (SOP Unique Design)
The SOP Agent is equipped with a dual-memory system that leverages the Database Engine itself:
*   **Short-Term Memory (Session Context)**: Uses `RunnerSession` / `ConversationThread` to track **Topics** and **Goals**. Unlike standard chat history (flat list), this structured approach allows the Agent to maintain distinct threads of thought and switch contexts with clearer state boundaries.
*   **Long-Term Memory (System Knowledge)**: Uses a persistent, transactional B-Tree (`llm_knowledge`). The Agent records reusable knowledge through ACID-backed storage rather than relying only on transient prompt context.
*   **Persona Isolation**: Knowledge Bases can carry distinct system prompts, embedders, and tool restrictions. Runtime memory is scoped to the active Knowledge Base so domain-specific personas do not bleed into unrelated asks.
*   **System vs. Domain Roles**: The runtime can keep a system-level supervisor for routing and policy while delegating domain reasoning to the active Knowledge Base persona.

### 4. Generators & Embedders (`ai/generator`, `ai/embed`)
Interfaces for connecting to AI models:
*   **Generators**: Connect to LLMs like OpenAI, Gemini, or local Ollama instances.
*   **Embedders**: Convert text to vectors. Includes a "Simple" keyword-based embedder (for testing) and an "Agent Embedder" (for semantic understanding).

#### Provider-Owned ReAct Loops

The generator layer supports both shared and provider-owned ReAct loops.

- Providers that preserve native tool and conversation state can own the inner loop directly.
- Providers without a native loop continue to use the shared engine path.
- Carryover, repair continuity, and orchestration policy are documented in [AI Copilot & Agent Architecture](../docs/AI_COPILOT.md) and [Store Orchestration Modes](STORE_ORCHESTRATION_MODES.md).

### 4. Model Store (`ai/database/model_store.go`)
A unified interface for persisting AI models, from small "Skills" (Perceptrons) to large "Brains" (Neural Nets).
*   **Backend**: Uses transactional B-Tree storage (`BTreeModelStore`) for reliability and consistency.
*   **Categorization**: Models are stored with a composite key `{Category, Name}`, allowing for organized grouping of model artifacts.
*   **Transactional**: The B-Tree backend allows model updates to be part of the same ACID transaction as vector data changes.

### 5. Text Search (`search`)
A transactional, embedded text search engine.
*   **ACID Compliant**: Index documents within the same transaction as your data.
*   **BM25 Scoring**: Uses industry-standard ranking for relevance.
*   **Architecture**: Stores Inverted Indices in SOP B-Trees.
*   **Usage**: Ideal for "Search this wiki" or "Filter by text" features alongside Vector Search.

### 6. Script System (`ai/SCRIPTS.md`)
A **Hybrid Execution** engine that runs inside the Agent.
*   **Explicit Execution**: No magic, no guessing. The engine executes scripts line-by-line, exactly as defined.
*   **Hybrid Workflows**: Seamlessly mixes **Deterministic Code** (`loop`, `fetch` tables) with **Non-Deterministic AI** (`ask` "Analyze this data").
*   **Optimization**: Deterministic steps run in the local execution engine; AI steps are invoked only when explicit reasoning is required.
*   [Read the full documentation](SCRIPTS.md).

### 7. AI Copilot (Interactive Mode)
A conversational interface for interacting with your data, building scripts, and managing AI Spaces.

#### Space Management and Search in AI Copilot

The AI Copilot is the interactive surface for the full Space lifecycle:

1. **Create / Mint a Space**: Launch a new Knowledge Base, define its purpose, and seed its categories.
2. **Manage Categories and Items**: Edit categories, add content, refine summaries, and organize the KB structure in the UI.
3. **Vectorize When Ready**: Trigger full-space, category, or item vectorization once the curated content is ready.
4. **Search the Space**: Ask the Copilot to search the active Space using semantic retrieval, text search, or combined retrieval paths.

This is the same user flow that developers can later reproduce in code with the `ai` package.
*   **Natural Language Queries**: "Select all users where role is admin".
*   **CRUD Operations**: Add, update, and delete records using plain English.
*   **AI Spaces Management**: Effortlessly generate, import, and manage "Spaces". A Space (or Knowledge Base) is a new AI memory subsystem combining VectorDB, Text Search, and a specialized schema (Thoughts: Category/Items).
*   **Script Drafting**: Teach the assistant workflows by defining them step-by-step.
*   [Read the User Guide](AI_COPILOT_USAGE.md).


### 8. Knowledge Vectorization (Setup Wizard)
The AI Copilot does not hardcode context rules. Instead, it relies on an embedded **Setup Wizard** and an internal **Vectorizer (compiler)**. 
When the system initializes, the Setup Wizard compiles documents like this `README.md` and the user/tool guides (`AI_COPILOT_USAGE.md`), converting guidelines, relational schema strategies, and tool lists directly into **Active Memory** (Vectors). 
This allows the AI to learn how your tables are linked, what tools it is allowed to use (`select`, `join`, `script`), and which CEL expressions map to your domain, directly from your documentation rather than rigid code blocks.

KnowledgeBase Studio and the Space import/export surfaces are also the forward path for moving legacy vector content into the maintained Memory Spaces model.


## AI Tools
*   [AI Script Architecture](AI_SCRIPT_ARCHITECTURE.md)
*   [Knowledge Base Studio](KNOWLEDGE_BASE_STUDIO.md)
*   [Vector Store Design](vector/VECTOR_STORE_DESIGN.md)
*   [Dynamic Vector Store Design](DYNAMIC_VECTOR_STORE_DESIGN.md)
*   [Swarm Design](SWARM_DESIGN.md)
*   [SOP AI Copilot Usage](AI_COPILOT_USAGE.md)

## Standards & Compatibility

The SOP AI Kit is designed to play nicely with the broader AI ecosystem while adhering to strict software engineering standards.

### Supported Interfaces
*   **Generators (LLMs)**:
    *   **Google (Gemini)**: Native support for Gemini Pro. **(Default & Tested)**
    *   **OpenAI (ChatGPT)**: Native support for GPT-3.5/4.
    *   **Ollama**: Native support for local models (Llama 3, Mistral, Gemma).
    *   **Custom**: Implement the `ai.Generator` interface to connect any other provider.
*   **Embedders**:
    *   **Google Gemini embeddings**: Supports `gemini-embedding-2` through the Generative Language `batchEmbedContents` API. The runtime normalizes the model name to the `models/...` form and sends `taskType=RETRIEVAL_DOCUMENT` with `outputDimensionality=768`.
    *   **Ollama**: Use local models for embeddings.
    *   **Local (kelindar / GGUF)**: Supports local GGUF embedders through the Kelindar integration, including `nomic-embed-text-v1.5-q8_0` and `bge-small-en-v1.5-q8_0`.
    *   **Agent-as-Embedder**: Use another SOP Agent to "embed" (translate) text, enabling recursive agent architectures.
*   **Vector Store**:
    *   **LangChain**: The Python wrapper (`sop4py`) includes convenience methods for LangChain integration.
    *   **Generic**: The Go API uses generics (`VectorStore[T]`), allowing you to store strongly-typed structs or dynamic `map[string]any` payloads.

#### Current embedder implementations

The current runtime has three documented embedder paths for the main local and hosted setups:

*   **Gemini `gemini-embedding-2`**: Hosted 768-dimensional embeddings via Google's batch endpoint. SOP sends retrieval-oriented payloads and explicitly requests `outputDimensionality=768`.
*   **Kelindar / Nomic (`nomic-embed-text-v1.5-q8_0`)**: Local GGUF embedder with asymmetric prefixes for storage and query paths plus Matryoshka-style routing slices.
*   **Kelindar / BGE Small (`bge-small-en-v1.5-q8_0`)**: Local GGUF embedder for lighter-weight 384-dimensional embeddings, especially useful for short labels, titles, and compact retrieval passages.

#### Local kelindar / Nomic GGUF notes

The default local embedding engine (`nomic-embed-text`) supports a native context window of **8,192 tokens** (approximately **6,000 English words**).

*   **Automatic Handling:** If a text block inside your batch exceeds this size, the library will automatically truncate the text down to the maximum allowed limit before generating the vector.
*   **Safety:** Your application will not crash, throw out-of-memory (OOM) exceptions, or panic when large documents are submitted.
*   **Best Practice:** For large documents, chunk or split strings before passing them to the embedder to avoid losing context during truncation.

For Kelindar models, SOP uses profile-driven behavior from embedder metadata:

*   **Nomic Matryoshka routing**: Category and routing vectors are sliced to the routing dimension and normalized after slicing when the profile marks the model as Matryoshka-capable.
*   **BGE-small**: The BGE small profile keeps 384-dimensional vectors and does not apply the Matryoshka routing normalization path.
*   **Mode-specific prefixes**: Local routing, document-storage, and query vectors can use different prefixes so storage and retrieval follow the model contract rather than a single generic embedding call.

#### Normalized vector distance path

For normalized vectors, SOP uses a faster distance path in the memory/vector math layer.

*   **One-time normalization**: Routing/category vectors from local Matryoshka models are normalized when they are sliced or indexed.
*   **Fast distance**: `Distance(a, b, true)` dispatches to a dot-product-based Euclidean form for normalized vectors.
*   **Formula**: For unit vectors, Euclidean distance is computed from the dot product as $\sqrt{2(1 - a \cdot b)}$.
*   **Effect**: This avoids recomputing norms inside the hot search loop while preserving the same ranking behavior expected from Euclidean distance on normalized vectors.

### Deployment Standards
*   **ACID Compliance**: Full Two-Phase Commit (2PC) support for distributed transactions.
*   **Storage**: Uses standard filesystem paths (no proprietary binary blobs hidden in OS folders).
*   **Caching**: Supports standard Redis protocol for clustered caching. **Note**: Redis is NOT used for data storage, just for coordination & to offer built-in caching.
*   **Replication & High Availability**:
    *   **General Purpose**: Supports full replication (Erasure Coding, Active/Passive) in all modes.
    *   **AI Package**:
        *   **Standalone Mode**: Supports replication if configured, though typically used for single-folder local storage.
        *   **Clustered Mode**: Supports full replication. Configurable via `VectorDBOptions` (Python) or `Database` struct (Go). Replication is **optional** in both modes.

## Unified Architecture

The SOP AI package is built as a high-level abstraction layer on top of the General Purpose SOP engine. This design ensures that both use cases share the same robust foundation while offering appropriate interfaces for their respective domains.

*   **Shared Engine**: Both packages use the same `infs` B-Tree storage engine, ensuring identical performance, reliability, and ACID compliance.
*   **Separation of Concerns**:
    *   **General Purpose (`sop`)**: Exposes low-level B-Tree primitives and explicit transaction management for building custom data structures (Key-Value stores, Registries).
    *   **AI Package (`sop/ai`)**: Abstracts B-Trees into domain-specific "Vector Stores" and "Model Stores" with implicit transaction handling for ease of use.
*   **The Bridge**: You can mix both worlds in a single atomic transaction. By creating a General Purpose transaction and "binding" an AI Store to it, you can update a User Profile (Key-Value) and their Embedding (Vector) simultaneously. See the [Tutorial](TUTORIAL.md#step-12-unified-architecture-the-bridge) for an example.

## API Cookbook

For detailed code examples and usage patterns, please see the [AI Cookbook](COOKBOOK.md).

## Model Store Tutorial

For a deep dive into persisting AI models, configurations, and weights, see the [Model Store Tutorial](MODEL_STORE_TUTORIAL.md).

## Usage as a Library

You can use the `ai` package directly in your Go applications to build custom solutions.

### Space Management in the App Runtime

The runtime-side flow mirrors the UI workflow:

1. **Open the authored Space** with `ai/database` / `ai/memory`.
2. **Manage categories and items** through the `KnowledgeBase` abstraction.
3. **Vectorize** the Space or selected categories/items when you are ready to enable semantic retrieval.
4. **Search** using the unified `Search(...)` API for keyword, semantic, and mixed retrieval flows.

This gives your application the same Space-aware reasoning path that the AI Copilot uses, while keeping the authoring experience in SOP Data Manager.

### Authoring Spaces in SOP Data Manager, then consuming them in code

A common pattern is to use the SOP Data Manager as the **authoring studio** for your Knowledge Bases (Spaces), then consume the curated data from your application with the `ai` library:

1. Create or curate a Space in the SOP Data Manager / Knowledge Base Studio UI.
2. Use the `ai/database` package to open that Space in your Go code with `OpenKnowledgeBase(...)`.
3. Query it with the rich `KnowledgeBase` API, using `Search(...)` as the single entry point for retrieval in RAG or agent workflows.

This keeps the human-facing management and authoring experience in the UI, while your application uses the SOP AI runtime to manage, digest, and search the authored Spaces in-process.

### Example: Building a Simple RAG App

```go
package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
)

func main() {
	// 1. Initialize the Vector Database
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./my_knowledge_base"},
	})

	// 2. Start a Transaction
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	defer trans.Rollback(ctx) // Safety rollback

	// 3. Open an index for a specific domain (e.g., "documents")
	idx, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})

	// 4. Initialize an Embedder
	// (In production, use a real embedding model. Here we use the simple keyword hasher)
	emb := embed.NewSimple("simple-embedder", 64, nil)

	// 5. Add Data (Upsert)
	item := ai.Item[map[string]any]{
		ID:     "doc-1",
		Vector: nil, // Will be filled below
		Payload: map[string]any{
			"text":     "SOP is a high-performance Go library for storage.",
			"category": "tech",
		},
	}
	// Generate vector
	vecs, _ := emb.EmbedTexts(ctx, []string{item.Payload["text"].(string)})
	item.Vector = vecs[0]

	// Save to DB
	idx.UpsertBatch(ctx, []ai.Item[map[string]any]{item})

	// Commit the transaction
	trans.Commit(ctx)

	// 6. Search (Retrieve) - New Read Transaction
	trans, _ = db.BeginTransaction(ctx, sop.ForReading)
	idx, _ = db.OpenVectorStore(ctx, "documents", trans, vector.Config{})

	query := "storage library"
	queryVecs, _ := emb.EmbedTexts(ctx, []string{query})

	hits, _ := idx.Query(ctx, queryVecs[0], 5, nil)

	for _, hit := range hits {
		fmt.Printf("Found: %s (Score: %.2f)\n", hit.Payload["text"], hit.Score)
	}
	trans.Commit(ctx)
}
```

## The Doctor Demo: A Real RAG Pipeline powered by Gemini

This demo showcases a complete, real-world RAG (Retrieval-Augmented Generation) pipeline. It demonstrates how to chain agents and vector databases together using the SOP AI framework.

Specifically, it implements a genuine RAG flow:
- **Embedding (Text-to-Vector)**: Uses Google's Gemini API (`gemini-embedding-001`) to convert patient symptoms into vector embeddings.
- **Retrieval**: Uses SOP's high-performance hybrid search (Vector + BM25) to locate the closest clinical mappings and disease data in your local stores.
- **Context-Aware Ranking (Active Memory)**: Intercepts the Reciprocal Rank Fusion (RRF) algorithm to mathematically boost documents (e.g., up to 1.5x) mathematically if they match the user's ongoing conversation/topic thread. This seamlessly solves the "forgetful RAG" problem for follow-up questions.
- **Generation (LLM)**: Uses Google's Gemini API (`gemini-2.5-flash` or similar) as the "Doctor" to synthesize the retrieved medical context and provide an educated diagnosis.

## Architecture

The system consists of two agents working in a pipeline:

1.  **Nurse Agent (`nurse_local`)**:
    *   **Role**: The "Translator".
    *   **Task**: Takes colloquial patient symptoms (e.g., "tummy hurt", "hot") and translates them into standardized clinical terms (e.g., "abdominal pain", "fever").
    *   **Mechanism**: Uses a local vector database to find the closest matching clinical terms.

2.  **Doctor Agent (`doctor_pipeline`)**:
    *   **Role**: The "Diagnostician".
    *   **Task**: Takes the clinical terms from the Nurse and searches its medical knowledge base to suggest possible conditions.
    *   **Mechanism**: Uses a separate local vector database populated with disease-symptom mappings.

## ETL Workflow (Data Ingestion)

Before the agents can run, we must build their knowledge bases. We use a dedicated ETL (Extract, Transform, Load) tool called `sop-etl`.

The entire process is defined in `etl_workflow.json` and consists of three steps:

1.  **Prepare**: Downloads a raw healthcare dataset (CSV) and converts it into JSON format (`doctor_data.json`).
2.  **Build Nurse DB**: Ingests the data into the Nurse's vector store (`data/nurse_local`), indexing symptoms for semantic retrieval.
3.  **Build Doctor DB**: Ingests the data into the Doctor's vector store (`data/doctor_core`), indexing diseases and their associated symptoms.

## Quick Start

We provide a script to build the tools, run the ETL pipeline, and verify the agents.

1.  **Run the Rebuild & Demo Script**:
    ```bash
    ./run_demo_gemini.sh
    ```
    This script will:
    *   Initialize necessary dependencies.
    *   Clean up old local databases.
    *   Run the ETL pipeline via `go run main.go` in the demo folder to ingest data (handling Gemini API 429 rate limits seamlessly with backoffs).
    *   Automatically launch the interactive doctor repl.

2.  **Run the Agent Manually (Interactive REPL)**:
    Once the data is built (using the ETL pipelines or scripts like `./run_demo_gemini.sh`), you can chat with the Doctor agent in a fully interactive terminal session powered by Gemini 2.5 Flash and robust Hybrid Search (Vector + BM25):
    ```bash
    go run ai/cmd/demo_doctor/main.go
    ```
    **Example Interaction**:
    ```text
    Patient> I have a bad cough and a runny nose
    ...Doctor is thinking (and searching)...

    🩺 Doctor Answer:
    Based on your symptoms...
    ```

## Configuration Files

*   **`etl_workflow.json`**: Defines the ETL pipeline steps and parameters (e.g., batch sizes, input/output paths).
*   **`data/doctor_pipeline.json`**: Configuration for the main Doctor agent. It specifies that it should use the `nurse_local` agent as its "embedder" (translator).
*   **`data/nurse_local.json`**: Configuration for the Nurse agent.

## Heuristic vs LLM Embedders

The system supports two types of "Nurse" agents for embedding/translation:

1.  **Heuristic Agent (`nurse_local`)**:
    *   **How it works**: Uses a local dictionary and vector search with manually curated synonyms.
    *   **Performance**: Extremely fast and deterministic.
    *   **Use Case**: Default for this demo. Tuned for high performance in specific areas (e.g., lung-related diseases).
    *   **Pros**: No external dependencies (no Ollama required), predictable.
    *   **Cons**: Requires manual tuning for new slang/terms.

2.  **LLM Agent (`nurse_translator`)**:
    *   **How it works**: Uses a local LLM (via Ollama) to semantically understand and translate user input.
    *   **Performance**: Slower (depends on GPU/CPU), but more flexible.
    *   **Use Case**: General-purpose understanding without manual synonym mapping.
    *   **Pros**: Understands context and nuance better out-of-the-box.
    *   **Cons**: Requires running Ollama, higher latency.

To switch between them, you would update the `embedder` configuration in the agent's JSON file.

### Knowledge Compiler & Setup Wizard Integration

To dramatically speed up the instantiation of the SOP intelligent agent environment, we provide a **Knowledge Compiler** (`ai/cmd/knowledge_compiler/main.go`). This tool pre-compiles baseline architectural knowledge into a static blob (`ai/sop_base_knowledge.json`). 

Instead of waiting for the LLM or embedding endpoints during the first run, the **SOP Setup Wizard** UI automatically catches this JSON payload. When configuring endpoints (e.g., via the frontend `fetch('/api/config/save')` integration), a unified loading screen directly injects the compiled vectors into your target databases synchronously alongside your Demo Data or Medical Expert configurations.


### Engine Constraints & Query Rules (Behavioral Vectors)

*   **Query Filtering**: When filtering with `select`, use MongoDB-style operators (`$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`) for comparisons. Example: `{"age": {"$gt": 18}}`.
*   **Sorting**: Sorting/Ordering is **ONLY** supported by the store's Key or a prefix of the Key. You CANNOT sort by arbitrary fields (e.g. `salary`, `date`) unless they are the Key.
*   **Secondary Indexes**: Check if a secondary index store exists (e.g., `users_by_age`). If so, use it to fulfill sort/filter requests by joining it with the main store (e.g. scan `users_by_age` and join `users`). If no index exists, explain that SOP only supports sorting by Key.

### Scripting & Join Execution Strategies

*   **Join Strategy**:
    *   Use `inner` (default) when the query implies "intersection" or strict matching (e.g., "Find orders for user X").
    *   Use `left` (Left Outer Join) when the query implies "optional" relationships.
    *   Use `right` or `full` only if explicitly requested or logically required.
*   **Return Values**:
    *   The `return` command in a script must refer to an EXPLICIT variable name defined in a previous step (e.g., `'result_var': "my_data"` -> `'return {"value": "my_data"}'`).
    *   Do NOT assume a variable named `final_result` exists unless you created it.
*   **Contextual Projection**:
    *   When joining entities, ALWAYS project identifying fields (e.g., Name, Email) from the parent/source entity alongside the child data in the final result.
    *   Do NOT return orphaned child records without their parent's context if the user filtered by the parent.
    *   Always use the Store Name as prefix (e.g., `users.age`) or the explicit Alias defined in the `join` step.

### Critical Aliasing Rules

*   **STRICTLY FORBIDDEN**: Do NOT use `right.*`, `left.*`, `l.*`, or `r.*` in projection fields. Using `right.*` will fail.
*   You MUST list specific fields (e.g., `orders.key`, `users.name`) or use the exact store name wildcard (e.g., `orders.*`) if applicable.

### Intent Detection (Conversation vs. Action)

*   Distinguish between a request to PERFORM an action (e.g., "Add a user", "Find records") and a request to GENERATE data or EXPLAIN concepts (e.g., "Give me a new UUID", "How does this work?").
*   If a user asks for a "new UUID" or "random ID" in isolation, simply generate it and reply with the text. Do NOT add it to any store unless explicitly instructed to "save" or "add" it.
*   Engage in conversation freely to clarify intent before taking destructive or additive actions.

### UI Handlers (Client-Side Actions)

*   To switch the active database context in the UI, do NOT use a tool. Instead, strictly output the following text in your final response: `[[SWITCH_DATABASE: <db_name>]]`. The frontend will detect this and perform the switch.

### Active Memory & Self-Correction

*   Your pipeline is powered by an automated Context-Aware "Active Memory" backend.
*   When correcting mistakes or learning rules (e.g., "We use 'TotalAmount', not 'Cost'"), simply ACKNOWLEDGE the prompt. The vector retrieval backend natively handles the propagation of definitions without you needing to explicitly 'save' them. Proceed immediately with corrected logic.
