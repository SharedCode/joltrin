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

### 9. Runbook order lab (`runbook_order_lab`)
Enumerates every order the verifier accepts that reaches `rollback_complete` on the five-step barrier workflow, and shows that a naive drop-first order is blocked once. Every step goes through `verify.CheckAndCommit`.
- **Run**: `go run ./examples/runbook_order_lab`
- **Check**: `go test ./examples/runbook_order_lab -race -count=1`
- **Note**: a small local lab, not a product feature. Its README records the measured enumeration cost and its limits.

### 10. Verification barrier blocking an out-of-order step (`verify_barrier`)
An agent tries to drop the production database before a backup is validated. The barrier blocks the step, then allows the valid sequence. Start here to see the barrier decide in real time.
- **Run**: `go run ./examples/verify_barrier`

### 11. Barrier decisions published to NATS (`verify_barrier_nats`)
The same barrier as `verify_barrier`, with every decision published through `adapters/nats` so a separate subscriber can watch it. The barrier behaves the same with or without the bridge.
- **Needs**: a NATS server at `nats://127.0.0.1:4222` (`nats-server`, or `docker run -p 4222:4222 nats`)
- **Run**: `go run ./examples/verify_barrier_nats`

### 12. Replaying recorded decisions against a stricter workflow (`replay`)
Records barrier decisions into an `ai/ledger` run, then replays that history against a stricter candidate workflow to catch a safety regression before it ships.
- **Run**: `go run ./examples/replay`

### 13. Crash recovery with the ledger (`ledger`)
One process runs an agent partway and is interrupted. A second process opens the same durable storage, resumes from the last checkpoint, finishes the run, and replays the full history.
- **Run**: `go run ./examples/ledger`

### 14. Quickstart (`quickstart`)
The smallest SOP program. It uses the in-memory B-tree, so it needs no infrastructure.
- **Run**: `go run ./examples/quickstart`

### 15. Agent memory with atomic checkpoints (`agent_memory`)
Stores agent memory and its metadata in one atomic commit, then recalls it by vector similarity across agent memory spaces. Runs in one Go process with no external daemons.
- **Run**: `go run ./examples/agent_memory`

### 16. Agents handing work to each other (`agent_team`)
Jira, Grafana, AWS, and PagerDuty agents finish a task by passing work to each other. Each call passes a scope check (the agent's own allowlist and limits), a grounding check (a claim must match evidence a tool returned), and the verification barrier.
- **Run**: `go run ./examples/agent_team`

### 17. Data center failover: stop the primary last (`failover_barrier`)
An agent running a "pull the plug" failover tries to stop the primary site first. The barrier blocks it until the standby is verified, traffic is on the standby and the error rate is confirmed there, then the same agent finishes the runbook in the safe order.
- **Run**: `go run ./examples/failover_barrier`
- **Check**: `go test ./internal/examplecmd/failover_barrier -race -count=1`
- **Note**: the same barrier as `verify_barrier` on an operations runbook that is not a database. It orders steps; it does not move traffic or talk to a load balancer.

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
