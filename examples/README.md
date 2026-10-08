# SOP Go Examples

This directory contains example applications demonstrating various features of Scalable Objects Persistence (SOP) in Go.

## 🌍 Interoperability & Polyglot Usage

SOP is designed to be a polyglot database. To ensure data written in Go can be read by Python, C#, Java, or Rust (and vice versa), you should use the `jsondb` package. This provides **symmetry** across all language bindings.

### 1. Basic Interop (`interop_jsondb`)
Demonstrates how to store Go structs so they are automatically serialized to the "Universal" JSON format.
- **Key Feature**: `jsondb.NewJsonBtree[K, V]`
- **Why**: Ensures your data is accessible to AI models in Python or services in C#.

### 2. Secondary Indexes (`interop_secondary_indexes`)
Demonstrates "Schema-less" storage with composite secondary indexes.
- **Key Feature**: `jsondb.NewJsonBtreeMapKey` + `IndexSpecification`
- **Why**: Allows you to define sorting rules (e.g., "Sort by Category ASC, then Price DESC") that are respected by all language bindings.
- **New**: Also includes `struct_key_main.go` demonstrating `jsondb.NewJsonBtreeStructKey` for a more idiomatic Go experience using structs as keys.

---

## 🚀 High-Performance Native Go

For pure Go microservices where you don't need to share data with other languages, you can use the native generic API.

### 3. Swarm Computing (`swarm_standalone` & `swarm_clustered`)
Demonstrates SOP's ability to handle concurrent transactions from multiple threads (or processes) without external locks.
- **Key Feature**: `database.NewBtree[K, V]` (Native)
- **Scenario**: 40 concurrent threads updating the same B-Tree.
- **Variants**:
    - `swarm_standalone`: Runs on local disk.
    - `swarm_clustered`: Runs on Redis (simulating a distributed cluster).

---

## 🏢 Multi-Tenancy & Configuration

### 4. Multi-Redis (`multi_redis_url`)
Demonstrates connecting to multiple Redis databases (e.g., DB 0 and DB 1) in the same application using the standard URL format.
- **Key Feature**: `RedisCacheConfig.URL` (`redis://host:port/db`)

### 5. KnowledgeBase CLI (`knowledgebase_cli`)
Demonstrates a standalone in-memory KnowledgeBase workflow with nested categories, item upserts, category listing, and search.
- **Key Feature**: `memory.KnowledgeBase` + `memory.NewStore`
- **Why**: Gives you a minimal app you can run directly to see the high-level API in action.

### 6. Grounded search with Hugging Face embeddings (`hf_grounded_search`)
Embeds Joltrin's own docs with `all-MiniLM-L6-v2`, stores the vectors in a Joltrin vector store through the Python bindings, and answers questions with citations or says "I don't know". The embedding step is written with `transformers` and `torch` directly so each part is visible.
- **Key Feature**: `sop.ai` `upsert_batch` and `query`, with the model pinned to one Hub commit.
- **Check**: `PYTHONPATH=bindings/python python examples/hf_grounded_search/test_search.py` (needs the native library built for your machine)

### 7. LangGraph agent over MCP (`langgraph_agent`)
A LangGraph `StateGraph` that calls the verification barrier through `sop-mcp-server`, gets blocked on `drop_prod_db`, and recovers using the fields in the block. Python, with a pinned `requirements.txt`.
- **Key Feature**: `langchain-mcp-adapters` over stdio, one session for the whole run.
- **Models**: Claude through `langchain-anthropic` when `ANTHROPIC_API_KEY` is set, otherwise a scripted policy (not an LLM) so it runs offline.
- **Check**: `python examples/langgraph_agent/test_agent.py`

### 8. Local agent with Gemma over Ollama (`gemma_ollama`)
Gemma (`gemma3:4b`) proposes actions and `embeddinggemma` provides the vectors. Joltrin retrieves context from an embedded vector store, runs the verification barrier, and commits each accepted action in a transaction. Everything stays in one Go process with no remote database.
- **Key Feature**: app-level schema check, then `verify.Workflow.CheckSafety`, then an ACID commit. A model that asks to publish before a human approval is blocked on every attempt and writes nothing.
- **Why**: Small open-weight models send malformed or out-of-order actions. Splitting payload validation (app code) from ordering rules (the barrier) keeps each layer simple.
- **Run**: `ollama pull gemma3:4b && ollama pull embeddinggemma`, then `go run ./examples/gemma_ollama`
- **Check**: `go test ./examples/gemma_ollama` (offline). Add `JOLTRIN_OLLAMA_LIVE=1 -run TestLiveGemma` to run against your local Ollama.

---

## ▶️ Running the Examples

You can run all examples in sequence using the suite script in the root directory:

```bash
./run_go_suite.sh
```

Or run individual examples:

```bash
go run examples/interop_jsondb/main.go
```
