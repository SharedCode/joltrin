# Build Your Own AI Expert System

Welcome to the "Green Field" of AI development. 

In this tutorial, we will build a real-world **RAG (Retrieval-Augmented Generation)** Expert System using the **SOP (Scalable Objects Persistence)** library powered by **Google Gemini**. We will accomplish this by implementing a **"Doctor & Nurse"** pattern.

This acts as a full, live RAG pipeline:
1. **Embedding Stage (Text-to-Vector)**: We use Gemini (`gemini-embedding-001`) to vectorize textual queries.
2. **Retrieval**: We query the local SOP vector database securely using Hybrid Search (BM25 + Semantic Vector search).
3. **Context-Aware Ranking (Active Memory)**: The BM25+Vector scoring algorithm evaluates the current `Active Memory` thread (what the user was just talking about). Matching documents get a mathematical multiplier to prioritize them, preventing the pipeline from "forgetting" the ongoing diagnosis context between prompts.
4. **Generation**: We use the Gemini LLM as the main agent ("The Doctor") to reason over the retrieved documents and offer diagnosis.

<h2>The Concept: Why Two Agents?</h2>

Most RAG (Retrieval-Augmented Generation) systems fail because users speak casually ("my tummy hurts"), but the database contains technical facts ("abdominal pain causes").

We solve this with two specialized roles:

1.  **The Nurse (Local Embedder/LLM)**: 
    *   **Role**: The "Front Desk". It listens to the user, sanitizes input, and translates vague complaints into precise domain terminology.
    *   **Tech**: A small, fast local LLM (like Llama 3 via Ollama).
2.  **The Doctor (SOP Vector Store)**: 
    *   **Role**: The "Specialist". It holds the massive knowledge base (textbooks, manuals, logs) and performs the deep search.
    *   **Tech**: SOP Vector Database (Transactional B-Trees).

---

<h2>The SOP Advantage: Why Not Just Use a Generic Vector DB?</h2>

Before we code, it is crucial to understand why SOP produces **higher quality results** than standard vector stores.

### 1. The "Perfect" Index (The Lookup Tree)
Most vector databases train their index (K-Means clustering) by grabbing the first 1,000 items they find. If your data is sorted by date, your index only knows about "January" and fails to categorize "December" data correctly.

SOP implements a **Transactional Lookup B-Tree**. 
*   It maintains a dense integer map (`0, 1, 2... N`) to every item ID in the system.
*   **Result**: When SOP trains its index, it performs **Ideal Random Sampling** across the *entire* dataset (TB+). 
*   **Benefit**: Your search clusters (Centroids) are mathematically representative of your total knowledge, not just the data you loaded first.

### 2. Scalable Deduping
"Garbage In, Garbage Out." Duplicate vectors skew search results and confuse LLMs.
*   SOP is built on **B-Trees**, which enforce key uniqueness at the storage engine level.
*   **Benefit**: You can enforce strict uniqueness on Document IDs or Content Hashes. If you try to load the same medical manual twice, SOP handles the deduplication transactionally. Your "Doctor" never gets confused by duplicate conflicting info.

---

<h2>Step 1: The Setup</h2>

You need Go (1.24+) and the SOP library.

```bash
go get github.com/sharedcode/joltrin/v5
```

(Optional) For the "Nurse" LLM, install [Ollama](https://ollama.com/) and pull a model:
```bash
ollama pull llama3
```

<h2>Step 2: The Data (ETL)</h2>

First, we need to teach our Doctor. We will ingest "medical knowledge" (text chunks). SOP handles the heavy lifting of chunking and indexing.

```go
package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
)

func main() {
	// 1. Initialize the Database (No Redis required for standalone!)
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/doctor_brain"},
	})

	// 2. Start Transaction
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	defer trans.Rollback(ctx)

	// 3. Open the "Doctor" index
	doctor, _ := db.OpenVectorStore(ctx, "doctor", trans, vector.Config{})

	// 4. Create some knowledge (In reality, you'd load this from PDFs/Textbooks)
	knowledge := []ai.Item[map[string]any]{
		{
			ID:      "doc-101",
			Vector:  []float32{0.1, 0.2, 0.9}, // Simplified vector
			Payload: map[string]any{"text": "Appendicitis presents with pain in the lower right abdomen."},
		},
		{
			ID:      "doc-102",
			Vector:  []float32{0.8, 0.1, 0.1},
			Payload: map[string]any{"text": "Migraines are often accompanied by sensitivity to light."},
		},
	}

	// 5. Upsert (Transactional!)
	// SOP automatically handles the "Lookup" tree updates and Centroid assignment here.
	if err := doctor.UpsertBatch(ctx, knowledge); err != nil {
		panic(err)
	}

	// 6. Commit
	trans.Commit(ctx)

	fmt.Println("The Doctor has studied the material.")
}
```

<h2>Step 3: The Nurse (The Translator)</h2>

The Nurse intercepts the user's raw query.

```go
func NurseTranslate(userQuery string) []float32 {
	// In a real app, you would call Ollama here:
	// prompt := "Translate this symptom to medical terms: " + userQuery
	
	fmt.Printf("Nurse: User complained of '%s'. Translating to medical terminology...\n", userQuery)
	
	// Mocking the embedding generation for this tutorial
	// "My tummy hurts" -> "Abdominal pain" -> [0.1, 0.2, 0.9]
	if userQuery == "my tummy hurts" {
		return []float32{0.1, 0.2, 0.9} 
	}
	return []float32{0.0, 0.0, 0.0}
}
```

<h2>Step 4: The Consultation (Search)</h2>

Now we put it together. The user speaks to the Nurse, the Nurse speaks to the Doctor.

```go
func main() {
	// ... (Open DB as before) ...
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForReading)
	doctor, _ := db.OpenVectorStore(ctx, "doctor", trans, vector.Config{})

	// 1. User Input
	userComplaint := "my tummy hurts"

	// 2. Nurse Action
	searchVector := NurseTranslate(userComplaint)

	// 3. Doctor Action (The Search)
	// SOP performs a partitioned search using the high-quality Centroids.
	results, _ := doctor.Query(ctx, searchVector, 1, nil)
	
	trans.Commit(ctx)

	// 4. Diagnosis
	if len(results) > 0 {
		fmt.Printf("Doctor found match (Score: %.2f):\n", results[0].Score)
		fmt.Printf("Reference: %s\n", results[0].Payload["text"])
	} else {
		fmt.Println("Doctor: I need more information.")
	}
}
```

<h2>Step 5: Long-Term Health (Rebalancing & Self-Healing)</h2>

A Vector Database is a living organism. As you add more knowledge, your initial clusters (Centroids) might become unbalanced - one topic might get too huge while others remain empty.

SOP includes a built-in **Optimize** feature that uses its self-generated statistics to heal itself.

### The "Self-Aware" Index
Remember the `Centroid` struct? It tracks its own `VectorCount`.
*   **Real-time Stats**: Every time you add or delete a document, SOP updates the count on the affected Centroid.
*   **Smart Management**: The system knows exactly which clusters are "heavy" and which are "light" without expensive scans.

### The Optimize Protocol
When your Doctor's knowledge grows significantly, you simply call:

```go
// Re-trains the index using the current data distribution
// Note: Optimize commits the transaction internally.
if err := doctor.Optimize(context.Background()); err != nil {
    panic(err)
}
```

This triggers a process that:
1.  Uses the **Lookup Tree** to pull a perfect random sample of the *current* data.
2.  Calculates new, optimal Centroids.
3.  Re-assigns vectors to these new homes in **batches** (to handle millions of records).
4.  Swaps the index atomically.

**Note**: During this process, the "Doctor" enters a **Read-Only** mode. You can still ask questions (Search), but you cannot teach it new things (Upsert) until optimization finishes.

This ensures your Expert System gets *smarter* and *faster* as it grows, rather than degrading like traditional vector stores.

<h2>Step 6: Performance Tuning (Modes & Caching)</h2>

SOP gives you two powerful knobs to tune your Expert System for its specific role.

### 1. Usage Modes: Build-Once vs. Dynamic
*   **`BuildOnceQueryMany`**: Ideal for static knowledge bases (e.g., a Law Library).
    *   **Workflow**: Ingest all data -> Call `Optimize()` -> Serve queries.
    *   **Benefit**: SOP optimizes the index for pure read speed and discards temporary build artifacts, saving space and improving query performance.
*   **`Dynamic`**: Ideal for dynamic systems (e.g., User Logs). SOP maintains the auxiliary structures needed for continuous updates.
*   **`DynamicWithVectorCountTracking`**: Advanced mode for external management.
    *   **Benefit**: Tracks the number of vectors per centroid. You can use this metric to programmatically decide *when* to call `Optimize()` (e.g., "Optimize if any cluster grows by > 20%"). This is useful for Agents that manage their own memory maintenance.

```go
// Configure via Config struct
cfg := vector.Config{
    UsageMode: ai.BuildOnceQueryMany,
}
store, _ := db.OpenVectorStore(ctx, "doctor", trans, cfg)
```

### 2. The "NoCheck" Speed Mode
For the "Doctor" agent serving queries, you want raw speed. SOP supports a **`NoCheck`** transaction mode.
*   **What it does**: It bypasses the heavy transactional isolation checks (conflict detection) during reads.
*   **Result**: Reads become as fast as accessing a raw flat file, but with the structure of a B-Tree.
*   **Bonus**: SOP includes a built-in **MRU (Most Recently Used) Cache**. Frequently accessed medical facts stay in RAM, making subsequent queries instant.

```go
// Configure the Doctor for maximum read speed
trans, _ := db.BeginTransaction(ctx, sop.NoCheck)
```

### 3. Deduplication Flag
By default, SOP checks for existing IDs before every insert to ensure data integrity.
*   **On (Default)**: Safe. Prevents duplicate records.
*   **Off**: Fast. Skips the read-before-write check.
*   **Use Case**: Initial bulk load of a fresh dataset where you know IDs are unique.

```go
// Disable deduplication for faster ingestion
store.SetDeduplication(false)
```

All of these enterprise-grade features - Transactional Integrity, Self-Healing Indexes, and In-Memory Caching - come **for free** just by using the SOP library.

<h2>Step 7: Zero-Code Agents (JSON Config & ETL)</h2>

You don't always need to write Go code to build an agent. SOP includes a powerful configuration system that lets you define agents using simple JSON files. It is a prebuilt expert system, just needing your content! And has ability to delegate to LLM (Gemini, ChatGPT) or local heuristics right out of the box.

### The Agent Configuration
You can define your agent's personality, knowledge base, and policies in a file like `doctor_pipeline.json`:

```json
{
  "id": "doctor_pipeline",
  "name": "Dr. AI Pipeline",
  "description": "Orchestrates the interaction between Nurse and Doctor.",
  "agents": [
    {
      "id": "nurse_local",
      "name": "Nurse Joy",
      "description": "Translates symptoms to medical terms.",
      "system_prompt": "You are a nurse. Translate user symptoms to medical terminology.",
      "storage_path": "nurse_local",
      "embedder": { "type": "simple" },
      "data": [
        { "id": "1", "text": "tummy hurt", "description": "abdominal pain" },
        { "id": "2", "text": "hot", "description": "fever" }
      ]
    },
    {
      "id": "doctor_core",
      "name": "Dr. House",
      "description": "Medical specialist.",
      "system_prompt": "Analyze the medical terms and provide a diagnosis.",
      "storage_path": "doctor_core",
      "embedder": {
        "type": "agent",
        "agent_id": "nurse_local",
        "instruction": "Find matching symptoms:"
      }
    }
  ],
  "pipeline": [
    {
      "agent": "nurse_local",
      "output_to": "context"
    },
    {
      "agent": "doctor_core"
    }
  ]
}
```

### Running the Configurable Agent
SOP provides a standard runner that loads these configurations:

```bash
go run ai/cmd/agent/main.go -config ai/data/doctor_pipeline.json
```

This command:
1.  Loads the JSON config.
2.  Initializes the pipeline and all referenced agents.
3.  Starts the interactive chat loop.

### ETL: Automating Knowledge Ingestion (Production Mode)
For real-world agents, you can't type thousands of records into the `data` array manually. This is where **ETL (Extract, Transform, Load)** comes in.

We provide a dedicated ETL tool (`sop-etl`) to ingest massive datasets into the Vector Store efficiently.

**1. Define the Workflow**
Create a workflow file (e.g., `etl_workflow.json`) that defines the pipeline steps:

```json
{
  "steps": [
    {
      "name": "Prepare Data",
      "action": "prepare",
      "params": {
        "url": "https://example.com/data.csv",
        "output": "data/doctor_data.json"
      }
    },
    {
      "name": "Build Nurse DB",
      "action": "ingest",
      "params": {
        "agent_config": "data/nurse_local.json",
        "source_data": "data/doctor_data.json"
      }
    },
    {
      "name": "Build Doctor DB",
      "action": "ingest",
      "params": {
        "agent_config": "data/doctor_core.json",
        "source_data": "data/doctor_data.json"
      }
    }
  ]
}
```

**2. Run the ETL Tool**
Run the tool with the workflow flag:

```bash
./sop-etl -workflow etl_workflow.json
```

This will sequentially download the data, process it, and populate the vector databases for both agents.

**3. Run the Agent**
Now you can run your agent using the pre-populated database:
```bash
./sop-ai -config data/doctor_pipeline.json
```

<h2>Step 8: Running the Full Example</h2>

We have included a complete, working example in the repository. You can build the tools, ingest the data, and run the "Doctor & Nurse" agents in an interactive terminal REPL using Google's Gemini Flash.

### 1. Run the Rebuild Script
The `run_demo_gemini.sh` script performs the following:
1.  **Sets** up API keys and dependencies.
2.  **Cleans** up old data stores.
3.  **Runs** the ETL workflow to index dataset symptoms via `go run...` with built-in Gemini Rate Limits retry logic.
4.  **Automatically launches** the interactive doctor REPL.

```bash
./run_demo_gemini.sh
```

### 2. Or Chat with the Doctor Manually
Once the knowledge base is built from the bash script above, you can start the interactive agent loop independently any time using:

```bash
go run ai/cmd/demo_doctor/main.go
```

**What happens in the REPL:**
*   You type: "I have a bad cough and a runny nose"
*   The REPL leverages Gemini to embed your query.
*   It performs Hybrid Search (Vector + BM25) across your local DB.
*   The RRF engine scores result sets - multiplying and prioritizing matches mathematically bound to your **Active Memory** thread topics.
*   The agent generates an educated medical diagnostic based on actual vector matches and its system instructions.

<h2>Step 9: The Developer's Toolkit (Go API)</h2>

The `sop/ai` module is a modular kit. You can use the high-level `agent` package, or pick and choose the components you need.

### 1. `ai/vector`: The Vector Database
If you just want a high-performance, local vector store without the agent logic, use the `vector` package directly.

```go
import "github.com/sharedcode/joltrin/v5/database"

// Create a persistent store
db := database.NewDatabase(sop.DatabaseOptions{
    Type:          sop.Standalone,
    StoresFolders: []string{"data/my_vectors"},
})
ctx := context.Background()
trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
store, _ := db.OpenVectorStore(ctx, "my_vectors", trans, vector.Config{})

// Add a vector
err := store.Upsert(ctx, ai.Item[map[string]any]{
    ID: "item1", 
    Vector: []float32{0.1, 0.2, 0.3}, 
    Payload: map[string]any{"label": "test"},
})
trans.Commit(ctx)

// Search
trans, _ = db.BeginTransaction(ctx, sop.ForReading)
store, _ = db.OpenVectorStore(ctx, "my_vectors", trans, vector.Config{})
hits, err := store.Query(ctx, []float32{0.1, 0.2, 0.3}, 5, nil)
trans.Commit(ctx)
```

### 2. `ai/policy`: Safety & Guardrails
SOP includes a flexible policy engine designed to build **Responsible, Secured, and Safe AI systems**.

The kit supports a hierarchical policy model:
*   **Global Policies**: Enforced across all agents (e.g., "No Hate Speech", "GDPR Compliance").
*   **Local Policies**: Specific to a single agent (e.g., "Medical Disclaimer" for the Doctor agent).

This allows software teams to easily author and manage governance at the appropriate level.

```go
import "github.com/sharedcode/joltrin/ai/policy"

// 1. Define a Global Policy (e.g., Corporate Safety Standards)
globalPol, _ := policy.NewProfanityGuardrail(3)

// 2. Define a Local Policy (e.g., Custom Business Logic)
// You can implement the ai.PolicyEngine interface for custom rules
localPol := &MyCustomPolicy{AllowedTopics: []string{"medical"}}

// 3. Chain them together for enforcement
// The chain evaluates policies in order; if any policy blocks, the action is blocked.
finalPol := policy.NewChain(globalPol, localPol)

// Evaluate content
decision, err := finalPol.Evaluate(context.Background(), "input", sample, labels)
if decision.Action == "block" {
    fmt.Println("Blocked by Policy:", decision.PolicyID)
}
```

### 3. `ai/embed`: Embeddings
The `embed` package provides a unified interface for turning text into vectors. It supports local heuristics and can wrap other agents.

```go
import "github.com/sharedcode/joltrin/ai/embed"

// A simple embedder (e.g., for testing or simple keyword matching)
embedder := embed.NewSimple("simple-embedder", 64, nil)
vectors, _ := embedder.EmbedTexts(context.Background(), []string{"Hello world"})
```

### 4. `ai/etl`: Data Pipelines
The `etl` package helps you ingest data from various sources (CSV, Web, APIs) and prepare it for the Vector Store.

```go
import "github.com/sharedcode/joltrin/ai/etl"

// Example: Fetching and cleaning data
err := etl.PrepareData("https://example.com/data.csv", "output.json", 5000)
```

### Putting It All Together: A Custom Agent
You can mix these packages to build something unique. For example, a "Safe Search" agent:

```go
package main

import (
    "context"
    "github.com/sharedcode/joltrin/ai/agent"
    "github.com/sharedcode/joltrin/ai/generator"
    "github.com/sharedcode/joltrin/ai/policy"
)

func main() {
    // 1. Load Domain (Vector Store + Embedder)
    domain := myCustomDomainLoader() 

    // 2. Add Safety
    pol, cls := policy.NewProfanityGuardrail(1)
    domain.SetPolicy(pol)
    domain.SetClassifier(cls)

    // 3. Connect Brain
    brain, _ := generator.NewGeminiClient("KEY", "gemini-pro")

    // 4. Launch
    svc := agent.NewService(domain, brain)
    svc.RunLoop(context.Background(), os.Stdin, os.Stdout)
}
```

<h2>Going Further: Hosting Other Experts</h2>
```

<h2>The Vision: Building "Smart Systems" of Any Scale</h2>

The SOP AI Kit is designed to address the entire chain of building intelligent software, from simple automation to enterprise-class AI.

### Lightweight "Automatons"
Developers can build custom agents that act as very lightweight, super high-performance **modules** or **automatons**.
*   **Reuse & Extend**: Start with prebuilt agents and layer new logic on top.
*   **Hybrid Intelligence**: Seamlessly combine **Local Heuristics** (for speed and determinism) with **LLMs** like Gemini or ChatGPT (for reasoning and creativity).
*   **Full Spectrum**: Whether you are building a smarter RESTful API or a complex expert system, the kit provides the foundational blocks.

### Enterprise-Class Architecture
By leveraging SOP's core **Clustered Database** features, software teams can build systems that are not just smart, but robust and scalable.
*   **Collaborative AI**: The kit treats Gemini, ChatGPT, and Local Agents as interoperable components. They can reuse each other's capabilities to solve problems that no single model could handle alone.
*   **Transactional Integrity**: Unlike simple vector libraries, SOP ensures your AI's memory is ACID-compliant, making it suitable for critical enterprise applications.

<h2>Step 10: Going Enterprise (Clustered Mode)</h2>

While `database.Standalone` is perfect for local development and single-node deployments, SOP AI also supports a **Clustered** mode for high availability and scale.

### Switching to Clustered Mode
To enable clustered mode, simply change the database type and ensure you have a Redis instance running (for the L2 Cache).

```go
// 1. Initialize the Database in Clustered Mode
// This will automatically connect to a local Redis instance (localhost:6379) for caching.
db := database.NewDatabase(sop.DatabaseOptions{
    Type:          sop.Clustered,
    StoresFolders: []string{"./data/doctor_brain_cluster"},
})

// 2. Open the "Doctor" index
ctx := context.Background()
trans, _ := db.BeginTransaction(ctx, sop.ForReading)
doctor, _ := db.OpenVectorStore(ctx, "doctor", trans, vector.Config{})
```

In Clustered mode:
*   **L2 Cache**: SOP uses Redis to cache B-Tree nodes, enabling high-performance shared access across multiple application instances.
*   **Storage**: Data is still persisted to the filesystem (or a shared volume), but the Redis cache ensures consistency and speed in a distributed environment. **Note**: Redis is NOT used for data storage, just for coordination & to offer built-in caching.

### Seamless Migration (SDLC)
SOP is designed to support your Software Development Life Cycle (SDLC) from local dev to production.

1.  **Develop Locally**: Build and test your agent on your laptop using `database.Standalone`.
2.  **Deploy to Prod**: When moving to a higher environment (QA/Prod), simply:
    *   **Data Availability**: Ensure your data folder (e.g., `./data/doctor_brain`) is accessible to the production nodes. You can copy it to a shared volume, or if it's already on a network share, just use it in-place.
    *   **Flip the Switch**: Change the database type from `Standalone` to `Clustered`. If you are using a configuration file, just update the config. If hardcoded, update the code and rebuild.
    *   **Restart**: Restart your application. Ensure Redis is running.

**Note**: For the "easy flip" to work in a multi-node cluster, the storage path must be a **shared volume** (e.g., NFS, EFS, or a mounted SAN) accessible to all nodes. If running on a single node (just for caching benefits), a local path is fine.

The next time your application runs, it will automatically pick up the existing data and start using the Redis cache for coordination. No data migration or export/import is required.

<h2>Step 11: Advanced - Atomic Updates (Transactional Integrity)</h2>

One of SOP's unique superpowers is the ability to update **multiple** stores (e.g., the Vector DB and the Model Registry) in a single, atomic transaction.

If your training process crashes halfway through, you don't want a "ghost" state where the vector index is updated but the model weights aren't.

```go
func AtomicTrainAndIndex(ctx context.Context, doc ai.Item[any], newWeights []float64) error {
    // 1. Start a Transaction
    // This transaction will span across both the Vector Store and the Model Store.
    trans, _ := db.BeginTransaction(ctx, sop.ForWriting)

    // 2. Open Transactional Views
    // Bind the stores to this specific transaction.
    vecStore, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})
    modelStore, _ := db.OpenModelStore(ctx, "classifiers", trans)

    // 3. Perform Updates
    // A. Update the Vector Index
    if err := vecStore.Upsert(ctx, doc); err != nil {
        trans.Rollback(ctx)
        return err
    }

    // B. Update the Model Weights
    if err := modelStore.Save(ctx, "sentiment_v2", newWeights); err != nil {
        trans.Rollback(ctx)
        return err
    }

    // 4. Commit
    // Both updates are applied instantly and atomically.
    // If this fails (e.g., power loss), NOTHING is saved.
    return trans.Commit(ctx)
}
```

This pattern is essential for building robust, enterprise-grade AI systems that can recover from failures without data corruption.

<h2>Step 12: Unified Architecture (The Bridge)</h2>

SOP's architecture is "Layered". The AI package is a specialized layer built on top of the General Purpose engine. This means you can mix low-level Key-Value operations with high-level Vector operations in the **same atomic transaction**.

This is powerful for scenarios like "User Registration", where you need to create a User Profile (KV) and index their Bio (Vector) simultaneously.

```go
func RegisterUser(ctx context.Context, userID string, bio string) error {
    // 1. Start a General Purpose Transaction
    // This gives us raw access to the storage engine.
    trans, _ := db.BeginTransaction(ctx, sop.ForWriting)

    // 2. General Purpose Work (Key-Value Store)
    // Open a raw B-Tree to store user profiles.
    userStore, _ := db.NewBtree(ctx, "users", trans)
    
    profile := UserProfile{ID: userID, Bio: bio, CreatedAt: time.Now()}
    if _, err := userStore.Add(ctx, userID, profile); err != nil {
        trans.Rollback(ctx)
        return err
    }

    // 3. AI Work (Vector Store)
    // "Bind" the AI Vector Store to the SAME transaction.
    // Now, the vector upsert participates in 'trans'.
    vecStore, _ := db.OpenVectorStore(ctx, "user_bios", trans, vector.Config{})
    
    // Generate embedding (mocked)
    vector := embedder.Embed(bio)
    
    item := ai.Item[any]{ID: userID, Vector: vector, Payload: nil}
    if err := vecStore.Upsert(ctx, item); err != nil {
        trans.Rollback(ctx)
        return err
    }

    // 4. Commit
    // Both the User Profile and the Vector Index are saved atomically.
    return trans.Commit(ctx)
}
```

This unification allows you to build complex, data-intensive applications without needing separate databases for your structured data (SQL/KV) and your AI data (Vectors).

<h2>Summary</h2>

By using SOP, you aren't just storing vectors; you are managing a **Transactional Knowledge Base**. 

1.  **Data Quality**: The `Lookup` B-Tree ensures your AI is trained on a mathematically perfect sample of your data.
2.  **Deduping**: The B-Tree backend prevents knowledge pollution.
3.  **Privacy**: Everything runs locally in `./data/doctor_brain`. No data leaves your machine.
4.  **AI/LLM**: Wrappers/hooks are available to "talk" (delegate) to Gemini/ChatGPT/Ollama/etc... AI/LLM. Allowing their reuse and your application freedom to combine/take each one(s) strengths and plugin their "intelligence" to your application.

Welcome to the future of Local AI.
