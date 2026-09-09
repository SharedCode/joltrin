<div align="center">

# ⚡ Joltrin ⚡

### From milliseconds to microseconds: durable memory and verification infrastructure for AI agents.

<p align="center">
  <a href="#-performance-benchmarks"><strong>⚡ Verified by benchmark: &lt;0.3ms latency &amp; ~6.8µs B-Tree operations →</strong></a>
</p>

<p align="center">
  <img src="docs/assets/joltrin-org-logo.jpg" alt="Joltrin logo" width="120" />
</p>

[![Discussions](https://img.shields.io/github/discussions/SharedCode/joltrin)](https://github.com/SharedCode/joltrin/discussions)
[![CI](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml)
[![Go Tests](https://github.com/SharedCode/joltrin/actions/workflows/go.yml/badge.svg?event=push&branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/SharedCode/joltrin)](https://github.com/SharedCode/joltrin/releases)
[![codecov](https://codecov.io/gh/SharedCode/sop/branch/master/graph/badge.svg)](https://app.codecov.io/github/SharedCode/sop)
[![Go Reference](https://pkg.go.dev/badge/github.com/sharedcode/joltrin.svg)](https://pkg.go.dev/github.com/sharedcode/joltrin)
[![Go version](https://img.shields.io/github/go-mod/go-version/SharedCode/joltrin)](go.mod)
[![License](https://img.shields.io/github/license/SharedCode/joltrin)](LICENSE)
[![Live Demos](https://img.shields.io/badge/Live_Demos-GitHub_Pages-10B981?logo=github)](https://sharedcode.github.io/joltrin/arena/)
[![MCP](https://img.shields.io/badge/MCP-tools%2Fmcpserver-4A4A4A)](docs/MCP_A2A_AND_VERIFICATION_ENGINE.md)
[![A2A](https://img.shields.io/badge/A2A-tools%2Fa2aagent-4A4A4A)](docs/MCP_A2A_AND_VERIFICATION_ENGINE.md)

</div>

**Joltrin** (formerly SOP / Scalable Object Persistence) is a unified in-process state engine providing transactional persistence, durable agent memory, distributed storage primitives, explicit-state verification, and WebAssembly persistence. It combines a sector-aligned **copy-on-write B-Tree**, **checkpointed episodic agent memory**, **vector similarity search**, and a **deterministic safety verification barrier** for MCP and A2A runbooks into one library.

Instead of managing separate vector databases, message brokers, caching tiers, distributed lock managers, and fragile external checkpoint stores, Joltrin lets your AI agents maintain crash-resilient memory and enforce operational invariants directly within the execution boundary.

> **Why "from milliseconds to microseconds"?** Traditional multi-tier architectures incur an estimated 15-50ms network round-trip penalty across external services (Redis, message queues, relational databases). Joltrin runs embedded in-process, shifting latency from milliseconds to microseconds: empirical benchmarks measure **<0.3ms** end-to-end in-process execution, **~6.87µs** per B-Tree write (>145,000 ops/sec), and **~6.95µs** per read (>143,000 ops/sec) with full ACID consistency.  
> 🔗 **Proof & Benchmark Reference:** [View Detailed Benchmarks & Microsecond Breakdown →](#-performance-benchmarks) · Benchmark suite: [`tools/benchmark`](tools/benchmark) · Live client-side run: [Technical WASM Demo](https://sharedcode.github.io/joltrin/)

<p align="center">
  <img src="docs/assets/joltrin-demo.gif" alt="Live Joltrin demo: in-process ACID transactions, distributed Arena simulation, and deterministic AI agent verification barrier" width="760" />
</p>

<p align="center">
  <a href="https://sharedcode.github.io/joltrin/"><strong>🧠 Launch Technical Demo →</strong></a> &nbsp;&nbsp;|&nbsp;&nbsp;
  <a href="https://sharedcode.github.io/joltrin/arena/"><strong>🎮 Play Joltrin Arena →</strong></a> &nbsp;&nbsp;|&nbsp;&nbsp;
  <a href="https://sharedcode.github.io/joltrin/agents/"><strong>🔌 Launch Agent Barrier →</strong></a>
</p>

| Experience | Description | Live Interactive Link |
| :--- | :--- | :--- |
| 🧠 **Joltrin Technical Demo** | **Client-Side Zero-Server WebAssembly Engine**<br>Execute live ACID transactions, 128-dimensional vector cosine searches, microsecond benchmarks, and durable AI agent memory checkpoints (kill the agent mid-task, watch a successor resume from the B-Tree) running 100% in your browser with **0 runtime HTTP network calls after initial load**. | [**Launch Technical Demo →**](https://sharedcode.github.io/joltrin/) |
| 🎮 **Joltrin Arena** | **Distributed Systems Survival Simulation**<br>Command a live digital cluster. Scale worker swarms, crash storage nodes, trigger transaction storms, and watch Joltrin automatically redistribute tasks and rebuild parity in real-time. | [**Play Joltrin Arena →**](https://sharedcode.github.io/joltrin/arena/) |
| 🔌 **Joltrin Agent Verification Barrier** | **The MCP/A2A Safety Check, Clickable**<br>The same `ai/verify` barrier gating `tools/mcpserver` and `tools/a2aagent`, compiled to WASM. Try dropping a database before validating a backup and watch it get blocked, in your browser, with the trace persisted to OPFS. | [**Launch Agent Barrier →**](https://sharedcode.github.io/joltrin/agents/) |

---

## ⚡ Try It in 30 Seconds

Clone the repository and run the unified interactive demo:

```bash
git clone https://github.com/sharedcode/joltrin.git && cd joltrin && ./scripts/demo.sh
```

The interactive script lets you execute and verify each workflow shown on this page:
1. **Verification Barrier**: Safety precedence check gating destructive operations (`examples/verify_barrier`).
2. **AI Agent Memory**: B-Tree reasoning checkpoints with mid-task worker failure and sub-15ms recovery (`examples/agent_memory`).
3. **Core Test Suite**: Sanity check running core storage, filesystem, and server unit tests.
4. **Local Protocol Probe**: JSON-RPC over stdio (`cmd/sop-mcp-server`) and live A2A agent-card probe (`cmd/sop-a2a-agent`).

You can also run any step directly with flags:
```bash
./scripts/demo.sh --barrier    # Option 1: Precedence barrier check
./scripts/demo.sh --memory     # Option 2: AI agent memory failover
./scripts/demo.sh --test       # Option 3: Core engine test suite
./scripts/demo.sh --protocol   # Option 4: Local MCP and A2A reachability probe
./scripts/demo.sh --all        # Run all 4 stages sequentially
```

Prefer raw Go commands without scripts? Run them directly:
```bash
go run ./examples/verify_barrier  # Option 1
go run ./examples/agent_memory    # Option 2
go test ./...                     # Option 3
```

No local Go toolchain? Run the exact same demo suite inside Docker:
```bash
# Run the interactive demo suite via Docker
docker run --rm -it -v "$PWD":/src -w /src golang:1.26-alpine ./scripts/demo.sh

# Or run the published quickstart container from GHCR
docker run --rm ghcr.io/sharedcode/joltrin-quickstart
```

### 📉 Engineering ROI, Verified in This Repo

No revenue or customer numbers exist yet for this project (see [For Investors](#-for-investors) for the honest version of that). What is verified today, in this repo, is the infrastructure cost this architecture removes:

| What collapses | From | To |
| :--- | :--- | :--- |
| **Network hops per operation** | 3-4 hops across Redis, a queue, and Postgres/Cassandra (estimated 15-50ms network round-trip overhead) | 1 embedded in-process call (<0.3ms measured latency, >145k ops/sec) |
| **Stateful services to operate, patch, and page on** | Redis + Kafka/RabbitMQ + Postgres/Cassandra + ZooKeeper (4+) | 1 embedded library |
| **Language surfaces shipped** | N/A | Go (native), Python (`sop4py` on PyPI), C# (`Sop` on NuGet); Java and Rust bindings exist in-repo with tests, not yet published |
| **CI rigor on every change** | N/A | `govulncheck` clean on every push; race detector on the core engine packages (`btree`, `common`, `fs`, `inmemory`); 3-OS build and test matrix (Linux, macOS, Windows) |
| **Deployment footprint of the technical demo** | A server-backed demo stack | WASM build running ACID transactions, vector search, and agent-memory checkpointing 100% client-side, 0 runtime HTTP calls after page load ([live](https://sharedcode.github.io/joltrin/)) |

Every row above is something you can run yourself, not a projection. See [Performance Benchmarks](#-performance-benchmarks) for the throughput numbers behind the latency claim, and [What Has Not Yet Been Proven](#-for-investors) for what this table deliberately leaves out.

---

## 🚀 Experience Joltrin

You can test Joltrin directly in your browser without installing anything via the live interactive experiences above ([Technical Demo](https://sharedcode.github.io/joltrin/), [Joltrin Arena](https://sharedcode.github.io/joltrin/arena/), and [Agent Verification Barrier](https://sharedcode.github.io/joltrin/agents/)). The technical demo demonstrates the engine's core power directly: safe, ACID-transactional storage running on web storage itself (OPFS), with zero server and zero network calls after the initial page loads the WASM binary. Everything else on this page, including the agent verification barrier below, is built on top of that same engine, a reference implementation showing one concrete use case.

The technical demo persists across reloads now, to Origin Private File System, via the browser's async File System Access API. The diagram below is the real tradeoff behind that choice, not a benchmark; no throughput numbers are shown because none have been measured for either path in this repo.

<p align="center">
  <img src="docs/assets/opfs-path-taken.svg" alt="OPFS persistence: the async File System Access API path this repo built, versus the synchronous createSyncAccessHandle path that would require moving the WASM binary into a dedicated Worker, deliberately not built this pass" width="900" />
</p>

That same WASM-compiled engine is what the Agent Verification Barrier row above actually runs on, not a separate reimplementation: it's the concrete reference implementation this repo ships to answer "what do you actually build with a durable, transactional engine running client-side?" It is an AI agent safety check an agent cannot talk its way around, with the trace itself durable in OPFS across reloads. The next section is that barrier in depth, plus the same check reachable server-side over MCP and A2A.

---

## 🔌 Agent Protocols: MCP, A2A, and a Real Verification Barrier

Joltrin runbooks are reachable from two agent protocols, [Model Context Protocol](https://modelcontextprotocol.io/) and [Agent2Agent](https://a2a-project.github.io/A2A/), both gated by the same safety-and-reachability check before a step is allowed to commit. Real, tested code (`ai/verify`, `tools/mcpserver`, `tools/a2aagent`), not a diagram of an idea; see [MCP, A2A, and the Verification Engine](docs/MCP_A2A_AND_VERIFICATION_ENGINE.md) for the full audit and design writeup.

<p align="center">
  <img src="docs/assets/mcp-a2a-architecture.svg" alt="An MCP client and an A2A orchestrator each reach a separate protocol server, both backed by the same tools/runbookstore.Store and gated by the same ai/verify safety check before a step commits" width="900" />
</p>

**Try the barrier yourself, live: [sharedcode.github.io/joltrin/agents](https://sharedcode.github.io/joltrin/agents/).** GitHub Pages can't run a real MCP or A2A network server (no backend), so this page runs the actual `ai/verify` check compiled to WASM, wired to buttons instead of protocol calls, the same logic those servers call before committing a step. Click "Drop Prod DB" first and watch it block; the trace persists to OPFS, so a reload picks up where you left off. This is a real recording of that page, not a mockup:

<p align="center">
  <img src="docs/assets/agent-barrier-demo.gif" alt="Real browser recording of the live agent verification barrier demo: dropping the database is blocked until backup and validation steps actually commit, then the same drop is allowed" width="900" />
</p>

The same scenario also runs as a terminal program, `examples/verify_barrier`, and the servers themselves are one command away:

<p align="center">
  <img src="docs/assets/ltl-barrier.gif" alt="Real terminal recording of ai/verify blocking a database drop until a backup is validated, then allowing it once the precondition is actually met" width="760" />
</p>

```bash
# Run the barrier demo yourself
go run ./examples/verify_barrier

# Serve the same runbook over MCP (stdio)
# Note: this speaks JSON-RPC over stdin/stdout for an MCP client (Claude
# Desktop, an SDK, etc). Run bare in a terminal, it'll print "Parse error"
# for every line you type, since your keystrokes aren't valid JSON-RPC -
# that's expected, not a bug. Point an MCP client at this command instead.
go run ./cmd/sop-mcp-server

# Serve it over A2A instead, then fetch its agent card
go run ./cmd/sop-a2a-agent &
curl localhost:8087/.well-known/agent-card.json

# Claude has no native A2A client, so bridge the two: sop-a2a-bridge
# resolves the agent card above and re-exposes execute_step as an MCP tool
go run ./cmd/sop-a2a-bridge -agent-url http://localhost:8087
```

### Wiring `sop-mcp-server` into Claude

`cmd/sop-mcp-server` speaks JSON-RPC over stdio and evaluates the barrier policies below (`ai/verify`'s `CheckSafety`) before `execute_step` is allowed to commit; a blocked step comes back as `input-required`, not a crash. Point either Claude client at the command:

**Claude Desktop** (`claude_desktop_config.json`, stdio transport):

```json
{
  "mcpServers": {
    "joltrin": {
      "command": "go",
      "args": ["run", "./cmd/sop-mcp-server"],
      "cwd": "/absolute/path/to/joltrin"
    }
  }
}
```

Swap `"command"/"args"` for a prebuilt binary once you've run `go build -o sop-mcp-server ./cmd/sop-mcp-server`:

```json
{
  "mcpServers": {
    "joltrin": {
      "command": "/absolute/path/to/joltrin/sop-mcp-server"
    }
  }
}
```

**Claude Code** (CLI):

```bash
claude mcp add --transport stdio joltrin -- go run ./cmd/sop-mcp-server
```

### Wiring `sop-a2a-agent` into Claude (via `sop-a2a-bridge`)

Claude doesn't speak A2A natively, MCP is the protocol its clients actually implement, so reaching an A2A agent means bridging the two, not writing an A2A client into Claude itself. `tools/a2abridge` is that bridge: an MCP server that resolves a running `sop-a2a-agent`'s card and re-exposes its `execute_step` skill as an MCP tool of the same name, translating each call into a real A2A task delegation over the wire and translating the resulting task state (`completed` / `input-required` / `failed`) back into an MCP tool result. It's built on the official `a2aclient` SDK package, not a hand-rolled JSON-RPC client, and it's covered by its own integration tests (`tools/a2abridge/bridge_test.go`) that drive the full MCP -> bridge -> real A2A wire protocol -> executor round trip, including the blocked, allowed, and remote-failure paths.

Start the agent, then point the bridge at it:

```bash
go run ./cmd/sop-a2a-agent &
go run ./cmd/sop-a2a-bridge -agent-url http://localhost:8087
```

**Claude Desktop:**

```json
{
  "mcpServers": {
    "joltrin-a2a": {
      "command": "go",
      "args": ["run", "./cmd/sop-a2a-bridge", "-agent-url", "http://localhost:8087"],
      "cwd": "/absolute/path/to/joltrin"
    }
  }
}
```

**Claude Code** (CLI):

```bash
claude mcp add --transport stdio joltrin-a2a -- go run ./cmd/sop-a2a-bridge -agent-url http://localhost:8087
```

### Barrier policies `ai/verify` enforces

`ai/verify` is a general-purpose explicit-state precondition/postcondition graph (`Step`, `SafetyRule`, `ReachabilityRule` in `ai/verify/verify.go`) with no built-in notion of databases, clusters, or money. Every state is an opaque string, so a barrier policy for any category of risky action is defined the same way: name the states that must hold, name the step that establishes the dangerous one, and let `CheckSafety` gate it. This repo ships three concrete runbooks in `tools/runbookstore` built on that same generic mechanism, one per risky-action category, plus the generic out-of-order rejection that applies to all of them:

- **Destructive operations** (`DBMaintenanceWorkflow`, e.g. dropping a database): `drop_prod_db` requires `backup_validated`, which only `validate_backup` establishes after `take_backup`. A `SafetyRule` (`no-drop-without-validated-backup`) names the barrier explicitly, and a `ReachabilityRule` guarantees `rollback_complete` stays reachable even after the drop.
- **Resource & topology mutations** (`ClusterTopologyWorkflow`, e.g. draining a node, failing over a cluster): `drain_node` and `failover_cluster` both require `replica_parity_verified`, which requires `health_check_passed` first. Reinstating the node or cluster (`topology_rollback_complete`) stays reachable from every state in the graph, including after a worker is terminated post-drain.
- **Financial / ledger-mutating actions** (`LedgerTransferWorkflow`, e.g. balance updates, account transfers): `commit_transfer` requires `zero_sum_verified`, which only `verify_zero_sum_invariant` establishes after balances are mutated inside a `transaction_serialized` scope (`begin_serializable_transaction` -> `snapshot_balances` -> `apply_debit_credit`). Reversal (`ledger_rollback_complete`) stays reachable both before and after commit.
- **Unverified / out-of-order execution**: this is the same mechanism underlying all three, not a separate check. `CheckSafety` rejects any step whose `Requires` states haven't been established yet in the current `Trace`, and rejects any step that would establish a `Forbidden` state without its paired `Requires` state already holding. An agent (or a client bug) trying to call `drain_node` or `commit_transfer` before its preconditions land gets a named, actionable violation back, never a silent no-op.

Only `DBMaintenanceWorkflow` is registered by the example binaries (`cmd/sop-mcp-server`, `cmd/sop-a2a-agent`) today; `ClusterTopologyWorkflow` and `LedgerTransferWorkflow` are available in `tools/runbookstore` (with tests in `tools/runbookstore/examples_test.go`) as worked examples of modeling the other two categories on the same engine. Register them with `store.RegisterWorkflow` in your own server to serve them.

What this checker is, precisely, matters more than what it sounds like it might be: explicit-state safety and reachability checking over a finite workflow graph, the "P is preceded by Q" precedence pattern from Dwyer/Avrunin/Corbett's property specification patterns (ICSE 1999), not general-purpose LTL/CTL model checking. No formula parser, no Büchi automata, no neural component translating natural language into the graph today. The full accounting of what's built versus proposed is in the linked doc, not summarized rosily here.

---

## 💡 What Problem Does Joltrin Solve?

Most distributed applications require two fundamentally different operations:
1. **Storing state reliably** (databases, key-value stores, vector indexes)
2. **Coordinating work across machines** (task queues, locks, retries, worker failovers)

Today, developers solve this by assembling a multi-component infrastructure stack:

```
THE FRAGMENTED MULTI-COMPONENT STACK (Without Joltrin):

[ Application ]
       │
       ├──► (TCP Hop 1: 5-15ms)  ──► Redis (Distributed Locks & Leases)
       ├──► (TCP Hop 2: 5-15ms)  ──► RabbitMQ / Kafka (Task Queue)
       ├──► (TCP Hop 3: 10-30ms) ──► PostgreSQL / Cassandra (Persistent Storage)
       └──► (Failover Glue)      ──► ZooKeeper / Custom Retry & Outbox Daemons

⚠️ 6+ infrastructure boundaries | Estimated 15-50ms network latency tax | High split-brain failure risk | High maintenance overhead
```

When an application worker crashes between releasing a lock in Redis and committing to PostgreSQL, state can enter an inconsistent split-brain condition. Engineering teams end up spending substantial time writing and maintaining outbox listeners, lock renewers, and compensating retry logic.

---

## ⚡ Why Joltrin?

Joltrin takes a different approach: **co-locate storage and compute inside the same engine boundary.**

```
THE UNIFIED DATA & COMPUTE PLATFORM (With Joltrin):

[ Application ]
       │
       └──► (Embedded In-Process Call: < 0.3ms latency)
            ┌─────────────────────────────────────────────────────────────┐
            │                        JOLTRIN ENGINE                       │
            │  • Persistent B-Tree Storage (Sector-aligned Direct I/O)    │
            │  • Strict Serializable ACID Transactions (WAL + 2PC)       │
            │  • Swarm Compute & Autonomous Task Redistribution           │
            │  • High-Dimensional Vector Similarity Indexing (SIMD)       │
            │  • Reed-Solomon Erasure Coding & Partition Resilience       │
            └─────────────────────────────────────────────────────────────┘

✓ 1 Single Engine | Sub-millisecond execution | 100% ACID consistency | Automated failover
```

Because compute workers, task queues, and storage partitions share the same transaction boundary, a worker failure triggers an automatic rollback of uncommitted work and re-assigns the task in milliseconds with zero orphan locks.

---

## ⏱️ Why Now?

Three industry shifts make this architecture increasingly relevant:

1. **The Explosion of Autonomous AI Agents**: Multi-agent swarms require frequent context checkpointing, vector similarity searches, and task coordination. Assembling this across Postgres, Pinecone, Redis, and Celery creates high failure surface area.
2. **Edge and Local-First Computing**: Devices in factory automation, vehicles, and retail branches cannot rely on constant connections to central cloud databases. They need full ACID storage and local coordination that works offline.
3. **Infrastructure Simplification**: Engineering organizations are seeking to reduce the operational overhead and cloud bills associated with running dozens of discrete microservices just to manage state and queues.

---

## 🔍 What Makes Joltrin Different?

Joltrin is built on five core technical principles:

1. **Embedded Storage Engine**: Operates in-process in Go, Python, and C#, eliminating TCP network hops for local reads and writes.
2. **ACID Transactions without Database Servers**: Implements Write-Ahead Logging (WAL) and Two-Phase Commit (2PC) with copy-on-write page isolation.
3. **Swarm Compute Coordination**: Workers coordinate task execution using storage-anchored sector claims and heartbeat leases without requiring global consensus bottlenecks (like Paxos or Raft) on the hot path.
4. **Reed-Solomon Erasure Coding**: Protects storage shards from hardware failure by striping parity blocks across drives rather than paying the 3x disk storage cost of full replication.
5. **Integrated Vector & Structured Storage**: Stores high-dimensional vector embeddings in the same B-Tree segments as structured metadata, allowing single-transaction memory commits.

---

## ⚖️ Joltrin vs. Alternatives

Every architecture involves tradeoffs. Here is an honest comparison of where Joltrin fits relative to industry standards:

| Capability | PostgreSQL | Redis | Kafka | Temporal | Pinecone | SQLite | Joltrin |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **ACID Transactions** | ✓ | △ | ✗ | ✗ | ✗ | ✓ | ✓ |
| **Ordered B-Tree Range Scans** | ✓ | △ | ✗ | ✗ | ✗ | ✓ | ✓ |
| **Embedded In-Process** | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| **Swarm Work Coordination** | ✗ | △ | △ | ✓ | ✗ | ✗ | ✓ |
| **Vector Similarity Search** | △ (pgvector) | △ | ✗ | ✗ | ✓ | ✗ | ✓ |
| **Erasure Coding (N+K)** | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| **Zero Standalone Daemons** | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |

*Legend: `✓` First-class native capability | `△` Partial or requires plugin/extension | `✗` Not designed for this capability*

### Detailed Tradeoffs by Competitor:

- **PostgreSQL**: Industry standard for general relational databases. Choose Postgres when you need complex relational schemas, advanced SQL aggregations, or standard ecosystem tooling. Joltrin is better suited when you want an embedded storage engine inside your application process without database server management.
- **Redis**: Industry standard for ultra-low-latency in-memory key-value caching. Choose Redis when all data fits in RAM and you need simple cache operations. Joltrin provides durable B-Tree disk persistence, multi-account ACID transactions, and erasure coding.
- **Kafka / RabbitMQ**: Industry standards for high-volume streaming and pub/sub. Choose Kafka when you need multi-datacenter event streams and log retention. Joltrin provides transactional task queues co-located with storage state for local swarms.
- **Temporal**: Industry standard for long-running durable workflows spanning external microservices. Choose Temporal for multi-week human-in-the-loop workflows across disparate clouds. Joltrin is designed for local-to-cluster co-located data and task execution.
- **SQLite**: Industry standard for embedded single-file relational databases. Choose SQLite for client desktop/mobile apps needing SQL. Joltrin is designed for high-concurrency multi-threaded workers, clustered coordination, partitioned vector stores, and erasure coding.

---

## 🎯 When Joltrin Is a Great Fit

- **AI Agent Memory & Swarm Workforces**: Autonomous agents requiring durable conversation memory, vector similarity search, and task hand-offs without fragmented external databases. Checkpoints commit directly to B-Tree segments with atomic rollback if a worker crashes mid-reasoning.
- **Real-Time Systems & Simulation State**: Game servers, robotics, and spatial computing needing sub-millisecond in-process transactional serialization (measured at 100k-145k ops/sec in local benchmarks) without database network hops.
- **Financial & Escrow Ledgers**: Systems requiring snapshot isolation, optimistic concurrency control (OCC), two-phase commit (2PC), and invariant verification (such as validating zero-sum account deltas before commit).
- **Edge & IoT Computing**: Devices operating in local or intermittent network environments that need local embedded ACID persistence, with experimental peer coordination.
- **Serverless Workloads**: Cloud functions and containers that need durable storage without exhausting external database connection pools.

---

## 🚫 When Joltrin is NOT the Right Tool

To be completely clear on architectural boundaries:

- **Massive Analytical Warehousing**: If you are running multi-petabyte columnar analytics across billions of historical events, specialized OLAP warehouses (like ClickHouse or Snowflake) are the right choice.
- **Global Multi-Region Consensus**: If your application requires synchronous commits across continents with multi-region Raft/Paxos quorums, dedicated distributed SQL databases (like CockroachDB or Google Spanner) are designed for that problem.
- **Simple Stateless CRUD Apps**: If your application is a standard CRUD dashboard with low traffic, standard PostgreSQL or MySQL with an ORM is simpler and has more ecosystem plugins.

---

## 🎮 See Joltrin in Action (Joltrin Arena Simulation)

In **[Joltrin Arena](https://sharedcode.github.io/joltrin/arena/)**, every control maps directly to a real distributed systems concept:

| Simulation Control | Distributed Systems Concept | Joltrin Technical Mechanism |
| :--- | :--- | :--- |
| **Add Worker** | Swarm Compute | Dynamic queue rebalancing across peer worker nodes without central master bottlenecks. |
| **Remove Worker** | Graceful Degradation | Active tasks drained and re-assigned to healthy nodes with zero dropped writes. |
| **Kill Node / Storage Fault** | Fault Tolerance | **Reed-Solomon Erasure Coding** reconstructs missing B-Tree blocks in-memory from parity chunks. |
| **Transaction Storm** | Concurrency & Isolation | **Optimistic Concurrency Control (OCC)** serializes conflicting writes in microseconds. |
| **Increase Workload (100k TPS)** | Scalability | B-Tree node segments partition write load across sector-aligned storage handles. |
| **Automatic Self-Healing** | Resilient Coordination | Heartbeat lease detection triggers automated task redistribution in `<15ms`. |

---

## 👥 Who Joltrin Is For

Joltrin is one codebase, but different people will care about it for different reasons. Jump to the section that matches you:

[Investors](#-for-investors) · [Investment Banking & Tech Finance](#-for-investment-banking--technology-finance) · [Potential Customers](#-for-potential-customers) · [CTOs & Engineering Executives](#-for-ctos--engineering-executives) · [AI Infrastructure Teams](#-for-ai-infrastructure-teams) · [Platform, SRE & Cloud Engineers](#-for-platform-sre--cloud-engineers) · [Researchers & Distributed Systems Engineers](#-for-researchers--distributed-systems-engineers) · [Students & Learners](#-for-students--learners) · [Developers](#-for-developers) · [Engineering Leaders & Hiring Managers](#-for-engineering-leaders--hiring-managers)

### 💰 For Investors

**The problem.** Teams building stateful distributed applications, agent systems especially, routinely wire together a database, a cache, a message queue, a lock manager, and a workflow engine just to get durable state and coordinated work. Each boundary between those systems is a place where consistency breaks during a partial failure. That integration tax is paid by every team that builds this kind of system, repeatedly.

**What Joltrin uniquely combines.** A B-Tree storage engine, ACID transactions, and swarm task coordination live inside one embedded library instead of behind separate network services. That is an architectural bet, not a settled fact: it trades the maturity and ecosystem of specialized tools (Postgres, Kafka, Temporal) for fewer moving parts and a single consistency boundary. Whether that tradeoff wins in a given workload is something a team has to evaluate, which is exactly what the [comparison table](#️-joltrin-vs-alternatives) below is for.

**Investment Thesis**
Joltrin is an open-source bet that "data plus compute in one embedded engine" is a better default for a growing category of workloads (AI agents, edge devices, real-time systems) than assembling that stack from five separate products. If that thesis is right, the project that owns the reference implementation of that architecture has a shot at becoming the default choice for it, the way SQLite became the default embedded relational store. That is a multi-year distribution bet, not a proven outcome.

**Why Now**
- AI agent systems increasingly need durable memory, checkpointing, and multi-worker coordination, and today that is usually stitched together from a vector database, a cache, and a job queue.
- Edge and local-first computing (factory automation, vehicles, retail devices) need ACID storage that keeps working without a constant connection to a central database.
- Engineering organizations are actively trying to cut the number of discrete stateful services they operate, both for cost and for on-call load.

These are real, observable industry trends. No specific market-sizing figures are cited here because this repository has not commissioned or verified any (see Market Opportunity below).

**Market Opportunity**
Joltrin overlaps several existing categories rather than creating one from nothing: embedded databases (SQLite, RocksDB), distributed coordination (Zookeeper, etcd, Temporal), vector databases (Pinecone, Weaviate, pgvector), and workflow/task systems (Celery, Ray). Plausible buyers are teams building AI agent infrastructure, edge and IoT platforms, real-time/simulation backends, and fintech ledgers with strict transactional invariants. No independently sourced TAM/SAM/SOM figures are presented here; a rigorous estimate would require external market research (for example, from Gartner or IDC) that this project has not commissioned.

**Business Model Opportunities**
The project is MIT-licensed with no commercial product today. The open-core progression and architectural foundations for commercial governance are detailed in [Monetization & Editions Architecture](#-monetization--editions-architecture) below.

**What Has Been Proven**
- A working Go engine with ACID transactions (WAL plus two-phase commit), a custom B-Tree, and Reed-Solomon erasure coding, each with passing automated tests (18 packages carry tests in the core Go module; run them with `go test ./...`, while the two WASM-only packages build under `GOOS=js GOARCH=wasm`, see [Performance Benchmarks](#-performance-benchmarks) below for the throughput numbers).
- A real WebAssembly build of the engine running ACID transactions, vector search, and agent-memory checkpointing entirely in-browser with zero runtime network calls after initial page load ([live demo](https://sharedcode.github.io/joltrin/)).
- Working language bindings for Go (native), Python (`sop4py`, published to PyPI), and C# (`Sop`, published to NuGet), plus Java and Rust bindings that exist in-repo with tests but are not yet published to their package registries.
- CI that runs the race detector and `govulncheck` on every change, and a changelog showing multiple rounds of real dependency and CVE remediation.

**What Has Not Yet Been Proven**
- No production deployments or paying customers are documented anywhere in this repository.
- No independent, third-party, or peer-reviewed benchmarks exist; the performance numbers below come from this project's own benchmark harness on a single workstation, not a controlled multi-system comparison.
- Joltrin Arena's cluster view is a UI simulation of the underlying concepts for demonstration purposes, not a live multi-node deployment; multi-node swarm clustering itself is real and tested (`examples/swarm_clustered`, `examples/swarm_standalone`), but has not been run at meaningful scale or under adversarial network conditions in public.
- No formal third-party security audit has been performed.
- No case studies, design partners, or committed customers exist yet.

### 🏦 For Investment Banking & Technology Finance

**Technology category.** Joltrin sits in the embedded data infrastructure layer: a storage and coordination engine that applications link against directly, similar in category placement to SQLite or RocksDB, but extended with distributed ACID transactions and task coordination that those two do not attempt.

**Adjacent markets.** Embedded/operational databases, distributed coordination and workflow orchestration, vector search infrastructure, and AI agent infrastructure tooling. Each of those adjacent markets has established commercial players (see the [comparison table](#️-joltrin-vs-alternatives)), which is useful context for sizing the competitive landscape Joltrin would need to differentiate against.

**Potential strategic relevance.** Potential strategic relevance could include: infrastructure vendors looking to add an embedded, agent-friendly storage layer to an existing platform; cloud providers evaluating lightweight alternatives to running separate managed database, cache, and queue services for edge or agent workloads; or AI infrastructure companies needing a durable state layer under an agent runtime. None of this reflects any actual approach, interest, or discussion from any party; it is offered as a way to reason about where the technology could fit strategically.

**Open-source distribution.** The project is distributed under the MIT license with no dual-licensing or commercial tier today. That maximizes adoption friction reduction (any team can use it in production immediately) at the cost of no current monetization mechanism. See [Monetization & Editions Architecture](#-monetization--editions-architecture) for the open-core progression and architectural separation.

**Competitive landscape.** Summarized in the [Joltrin vs. Alternatives](#️-joltrin-vs-alternatives) table further down. No competitor is presented as inferior; each is a mature, widely deployed system that Joltrin would need to displace or complement for any given workload.

### 🏢 For Potential Customers

**Is Joltrin Right For Me?** Start from the existing [When Joltrin is a Great Fit](#-when-joltrin-is-a-great-fit) and [When Joltrin is NOT the Right Tool](#-when-joltrin-is-not-the-right-tool) sections above, they are the concrete answer. As a quick filter:

- If you are currently running Redis plus Postgres plus a queue just to get durable state and coordinated background work for one application, and that application's data fits comfortably on the machines it runs on, Joltrin is worth evaluating as a replacement for that stack.
- If you already run Postgres or Kafka at scale for reasons unrelated to this problem (complex SQL, multi-datacenter event retention, an existing team's expertise), Joltrin is more likely to complement than replace what you have.
- If your workload is petabyte-scale analytics or requires synchronous multi-region consensus, Joltrin is not the right tool today; see the section above for specifics.

Joltrin is a library you embed, not a managed service you sign up for. There is no hosted offering today; you run it yourself, in-process, in your own infrastructure.

### 👔 For CTOs & Engineering Executives

Every service you run that exists only to hold state or coordinate work (a cache, a queue, a lock manager) is a service your team has to patch, monitor, upgrade, and page on. Joltrin's bet is that collapsing storage, transactions, and task coordination into one embedded library reduces that surface for the workloads it fits, at the cost of giving up the specialized tooling and operational maturity of dedicated systems your team may already know well.

Concretely, that means: fewer network hops in your hot path (sub-millisecond, in-process calls instead of 15 to 50ms across Redis, a queue, and Postgres), one dependency to patch and upgrade instead of several, and a transaction boundary that spans your data and your background work instead of stopping at the database. It also means your team takes on a less mature, less battle-tested piece of infrastructure than Postgres or Kafka, with a correspondingly smaller ecosystem, smaller hiring pool of people who already know it, and no enterprise support contract available today. Evaluate it the way you would any early infrastructure bet: pilot it on one bounded, non-critical workload before committing a core system to it.

### 🧠 For AI Infrastructure Teams

**What Joltrin already provides.** Durable, transactional checkpointing for agent reasoning state: each step an agent commits is a separate, durable B-Tree write, so a killed agent process loses nothing already committed, and a successor process can resume from the last checkpoint. This is not a diagram, it runs today in the [browser demo](https://sharedcode.github.io/joltrin/) (the "AI Agent Memory" tab) and as a Go example (`go run ./examples/agent_memory`). Joltrin also provides vector similarity search over embeddings stored in the same B-Tree as structured data (`ai/memory`, `ai/vector`), and a real swarm/worker package (`ai/swarm`) with job and result stores.

**What could be built on Joltrin, but is not shipped today.** A production multi-agent orchestration framework, a hosted durable-memory-as-a-service for agent frameworks like LangGraph or AutoGen, and distributed MapReduce-style helpers across a live agent swarm are all described as design proposals in [`ai/SWARM_DESIGN.md`](ai/SWARM_DESIGN.md) (explicitly marked "Proposal / Vision" in that file) but are not implemented and tested the way the checkpointing and vector search primitives are. Treat anything not demonstrated in the linked demo or example as a direction, not a delivered feature.

**Protocol interoperability, actually implemented.** `tools/mcpserver` and `tools/a2aagent` expose Joltrin runbooks to MCP and A2A clients respectively, both gated by a real safety-and-reachability barrier certificate (`ai/verify`) so a step can't execute out of order regardless of what a calling agent claims. Both protocols share one execution trace store, proven by a test that commits steps via one protocol and confirms the other sees them. See [MCP, A2A, and the Verification Engine](docs/MCP_A2A_AND_VERIFICATION_ENGINE.md) for the audit, the design, and an honest accounting of what this checker is and is not (it is not general-purpose LTL model checking).

### ⚙️ For Platform, SRE & Cloud Engineers

Joltrin Engine is a library, not a server: there is no separate database process to provision, patch, or fail over for the embedded case. The optional `tools/httpserver` Data Manager is a standalone service with its own `/metrics` endpoint (tested in `tools/httpserver/metrics_test.go`) if you do want a network-accessible console. Failure recovery is handled by Reed-Solomon erasure coding across storage shards (`fs/erasure`, 12 passing tests at the time of writing) rather than full N-way replication, which trades some recovery latency for lower disk overhead. A prebuilt quickstart container is published to `ghcr.io/sharedcode/joltrin-quickstart`. Multi-node swarm clustering exists and is tested (`examples/swarm_clustered`, `examples/swarm_standalone`), but has not been documented or proven at production scale.

**Supply-Chain Security & Release Provenance:** Release builds are secured by an automated pre-publish quality gate ([`scripts/verify_release.sh`](scripts/verify_release.sh)), cryptographic SHA-256 manifests (`SHA256SUMS`), SPDX Software Bill of Materials (SBOM), and cryptographically signed build provenance attestations via GitHub Actions OIDC (`actions/attest-build-provenance`, SLSA Level 3 compliance). Consumers can independently verify any downloaded artifact using the standalone verification script.

### 🧪 For Researchers & Distributed Systems Engineers

The interesting parts to read are the B-Tree implementation with copy-on-write page isolation (`btree/`), the WAL plus two-phase commit transaction protocol (`transaction.go`, `common/`), the Reed-Solomon erasure coding layer (`fs/erasure/`), and the swarm coordination model described in [`ai/SWARM_DESIGN.md`](ai/SWARM_DESIGN.md). The [Architecture Whitepaper](docs/SOP_ARCHITECTURE_WHITEPAPER.md) and [Architecture vs. Big Tech](docs/ARCHITECTURE_VS_BIG_TECH.md) go deeper into the design tradeoffs than this README does.

### 🎓 For Students & Learners

Reading this codebase is a reasonable way to see real (not textbook-simplified) implementations of a B-Tree with node splitting and range iteration, optimistic concurrency control, write-ahead logging with two-phase commit, and erasure coding, all in readable Go with test coverage next to the implementation. Start with [`docs/WHAT_IS_SOP.md`](docs/WHAT_IS_SOP.md) for a plain-language overview, then run the zero-dependency quickstart below before reading `btree/` and `fs/erasure/`.

---

## 💎 Monetization & Editions Architecture

Joltrin follows an **Open-Core and Governance** architecture. The core database, vector similarity search, and agent memory engine are, and will always remain, **100% free and open-source under the permissive MIT License**. 

Commercial tiers are focused entirely on **enterprise governance, compliance, policy enforcement, multi-tenancy, and managed cloud infrastructure**, leaving the open-source core complete, unhindered, and unthrottled.

For deep architectural documentation on package boundaries and code separation, see [Monetization & Governance Architecture](docs/MONETIZATION_AND_TIERS.md).

| Tier / Edition | What It Provides | Distribution & Licensing | Implementation Status |
| :--- | :--- | :--- | :--- |
| **Free / Open-Source Core** | • Embedded copy-on-write B-Tree storage engine<br>• WAL + 2PC strict ACID transactions<br>• Reed-Solomon erasure coding and bitrot healing<br>• Durable AI agent memory & checkpointed buffers<br>• In-memory 128-d cosine vector similarity<br>• Embedded MCP server (`cmd/sop-mcp-server`)<br>• Embedded A2A agent runtime (`cmd/sop-a2a-agent`)<br>• Local runbook verification barrier (`ai/verify`)<br>• Developer GitHub OIDC authentication | Embedded Library & CLI<br>**Permissive MIT License** ($0) | **Available Today** |
| **Pro Governance** | • Policy-as-Code declarative runtime compiler<br>• Tamper-evident SHA-256 audit lineage & verification<br>• Signed cryptographic audit export<br>• Team-level workspaces and quota management<br>• Priority MCP gateways and traffic shaping<br>• Stripe Checkout, Customer Portal & Webhook Engine | Team Commercial Add-on<br>($49/team/mo) | **Available Today** ([`governance/`](governance/)) |
| **Enterprise Governance** | • Enterprise SSO: **Okta** & **Microsoft Entra ID**<br>• Multi-tenant RBAC & tenant isolation boundaries<br>• Enterprise audit streaming (real-time SIEM / Kafka)<br>• Fine-grained verification rules & custom safety invariants<br>• Custom invariant enforcement engine<br>• Enterprise compliance guarantees and SLA | Self-Hosted Enterprise Commercial<br>(Custom / Annual) | **Foundation Implemented** ([`governance/`](governance/)) |
| **Hosted Cloud** | • Managed Joltrin instances (zero-ops)<br>• Cloud-hosted MCP hub & multi-agent routing<br>• Multi-region database replication<br>• Managed agent coordination network<br>• Automated off-site snapshots & backup verification | Managed Cloud SaaS | **Planned** |

### Open-Source Guarantees
- **No Artificial Paywalls**: The open-source core will never cap database size, transaction limits, memory buffers, or local MCP/A2A concurrency.
- **Permanent MIT License**: Core storage, vector search, and agent safety verification remain permanently open-source under the MIT license.
- **Decoupled Architecture**: Commercial and governance modules interact via clean, decoupled Go interfaces ([`governance/`](governance/)) rather than invasive runtime licensing checks.

For a longer strategic view of enterprise defensibility and local-first architecture, see [Strategic Architecture & Investor Moat](docs/STRATEGIC_ARCHITECTURE_AND_MOAT.md).

---

## 🗺️ Roadmap

**Shipped and tested today**: the Go core engine, Python bindings (`sop4py`, on PyPI), C# bindings (`Sop`, on NuGet), the WebAssembly browser demo, the standalone HTTP Data Manager, and the interactive AI agent memory checkpointing demo.

**In progress, code exists in-repo**: Java bindings (`sop4j`), complete with tests, blocked on Maven Central Portal credential setup rather than on missing functionality (see [`docs/RELEASE_PROCESS_JAVA_STATUS.md`](docs/RELEASE_PROCESS_JAVA_STATUS.md)). Rust bindings (`sop4rs`), with tests and examples in-repo, not yet published to crates.io.

**Proposed, not yet implemented**: the swarm job distribution, `Await`, and `MapReduce` helpers described in [`ai/SWARM_DESIGN.md`](ai/SWARM_DESIGN.md), which that document itself labels "Proposal / Vision" rather than shipped.

This list reflects what is actually in the repository at the time of writing. It is not a committed release schedule.

---

## 🌎 Cross-Platform

CI (`.github/workflows/ci.yml`) builds, vets, and runs the core unit tests (`inmemory`, `btree`, `common`, `cache`, `encoding`, `database`) on `ubuntu-latest`, `macos-latest`, and `windows-latest` on every push and pull request, as three independent, parallel jobs. `macos-latest` runs on Apple Silicon (arm64), so that leg also verifies arm64 for free.

The Redis- and Cassandra-backed integration and stress test suites stay Linux-only: GitHub Actions' `services:` containers require a Linux-hosted runner, so those specific suites are not run on macOS or Windows today. That is a real gap in what is verified there, not a hidden one. All core unit test packages (`inmemory`, `btree`, `common`, `cache`, `encoding`, `database`) run and pass cleanly across all three operating systems (Linux, macOS, and Windows).

---

## 💻 For Developers

### 1. In-Memory Quickstart (Zero Dependencies)

```bash
# Clone the repository and run the quickstart
git clone https://github.com/sharedcode/joltrin.git
cd joltrin
go run ./examples/quickstart
```

```go
package main

import (
	"fmt"
	"github.com/sharedcode/joltrin/inmemory"
)

func main() {
	// Create an in-memory B-Tree with unique integer keys and string values
	tree := inmemory.NewBtree[int, string](true)

	// Add records
	tree.Add(101, "Build #101: tests passed")
	tree.Add(102, "Build #102: deployed to staging")

	// Ordered range scan (keys 100 to 105)
	for k, v := range tree.Range(100, 105) {
		fmt.Printf("Key %d -> %s\n", k, v)
	}
}
```

### 2. AI Agent Memory & Swarm Task Hand-off

Run the new dedicated Agent Memory demo:

```bash
go run ./examples/agent_memory
```

This demo demonstrates an AI worker creating a context checkpoint, crashing mid-step, rolling back cleanly, and having a healthy peer worker resume the task in `<15ms`.

---

## ⚡ Performance Benchmarks

Below are benchmark results from the repository benchmark harness (`tools/benchmark`) run on a 2015 MacBook Pro (Dual-Core Intel Core i5, 8GB RAM, macOS).

What is measured: these runs benchmark Joltrin Engine's in-memory L2 cache with `/tmp` storage backing full ACID transactions, not disk-only storage without cache.

### Microsecond-Scale Latency Profile

The benchmark measurements confirm sub-millisecond execution down to microsecond item lookups:

- **Embedded In-Process Latency**: `< 0.3ms` (<300µs) per transaction or safety barrier check, versus 15-50ms for multi-tier network round trips.
- **Per-Item Write Latency**: `~6.87µs` (at 145,417 ops/sec with full ACID WAL logging).
- **Per-Item Read Latency**: `~6.95µs` (at 143,770 ops/sec).
- **Swarm Failover & Re-assignment**: `< 15ms` heartbeat lease detection with automated rollback.

Exact reproduction command:
```bash
go run ./tools/benchmark -count <N> -slotlength <N>
```

For example:
```bash
go run ./tools/benchmark -count 10000 -slotlength 2000
go run ./tools/benchmark -count 100000 -slotlength 4000
```

### Tuning `SlotLength` (Items per B-Tree Node)

#### 10,000 Items Benchmark
| SlotLength | Insert (ops/sec) | Read (ops/sec) | Delete (ops/sec) |
| :--- | :--- | :--- | :--- |
| 1,000 | 107,652 | 136,754 | 40,964 |
| **2,000 (Balanced)** | **132,901** | **142,907** | **50,093** |
| 3,000 | 135,066 | 137,035 | 49,754 |
| 4,000 | 123,190 | 122,228 | 48,094 |

#### 100,000 Items Benchmark
| SlotLength | Insert (ops/sec) | Read (ops/sec) | Delete (ops/sec) |
| :--- | :--- | :--- | :--- |
| 1,000 | 121,139 | 145,195 | 48,346 |
| 2,000 | 132,805 | 136,684 | 51,817 |
| 3,000 | 137,296 | 141,764 | 50,605 |
| **4,000 (Write-Heavy)** | **145,417** | 143,770 | **51,988** |

---

## 👥 For Engineering Leaders & Hiring Managers

For technical leaders, CTOs, and hiring managers, this repository serves as a working demonstration of systems engineering across:

- **Storage Engine Design**: Custom B-Tree implementation with sector-aligned direct I/O, node slot tuning, and multi-tier L1/L2 caching.
- **Transactional Systems**: Strict ACID guarantees, Write-Ahead Logging (WAL), Two-Phase Commit (2PC), Snapshot Isolation, and Optimistic Concurrency Control.
- **Fault Tolerance & Reliability**: Reed-Solomon Erasure Coding (N+K striping), active/passive metadata redundancy, and automated partition healing.
- **High Concurrency**: Lock-free data structures, multi-goroutine worker swarms, and SIMD vector dot-product calculation.
- **Polyglot Architecture**: Native Go kernel, Python bindings (`sop4py`), C# bindings (`Sop`), and browser WebAssembly.
- **Production Delivery**: GitHub Actions CI/CD matrix, distroless container builds on GHCR, Codecov integration, and static GitHub Pages deployments.

If you are building distributed systems, cloud infrastructure, or AI data platforms and want to discuss architecture, feel free to connect via [GitHub Discussions](https://github.com/SharedCode/joltrin/discussions).

---

## 📦 Language Packages & Tooling

| Language | Installation | Description |
| :--- | :--- | :--- |
| **Go** | `go get github.com/sharedcode/joltrin` | Native high-performance core engine. |
| **Python** | `pip install sop4py` | Python bindings with Data Manager and AI scripts. |
| **C#** | `dotnet add package Sop` | Complete .NET Core integration. |
| **WebAssembly** | `GOOS=js GOARCH=wasm go build` | Browser-sandboxed zero-server execution. |
| **HTTP Data Manager** | `sop-httpserver` | Standalone UI console and AI Copilot interface. |
| **Java** *(in progress)* | source in `bindings/java`, not yet on Maven Central | `sop4j` bindings and tests are complete; publishing is blocked on Central Portal credential setup, tracked in [`docs/RELEASE_PROCESS_JAVA_STATUS.md`](docs/RELEASE_PROCESS_JAVA_STATUS.md). |
| **Rust** *(in progress)* | source in `bindings/rust`, not yet on crates.io | `sop4rs` bindings, tests, and examples exist in-repo but are not yet published as a crate. |

### How to Consume Joltrin: Releases vs. In-Repo Source

When integrating Joltrin into your stack, choose between official versioned releases and in-repo source consumption based on your development and operational needs:

| Dimension | Official Tagged Releases (Recommended for Production) | In-Repo Source / Submodule (Active Prototyping & Contribution) |
| :--- | :--- | :--- |
| **Artifacts** | `go get github.com/sharedcode/joltrin@vX.Y.Z`<br>PyPI: `pip install sop4py`<br>NuGet: `dotnet add package Sop` | Git clone or submodule linked directly to `HEAD` or a feature branch |
| **Best For** | Production services, reproducible CI/CD builds, audited dependencies | Modifying engine internals, local benchmarking, custom protocol servers |
| **Stability** | Semantic versioning, tagged releases, audited dependency graph | Bleeding-edge features, experimental branches, unreleased protocol bridges |
| **Maintenance** | Handled by standard language package managers | Requires manual git fetch/rebase and local workspace management |

#### 1. Official Tagged Releases (Recommended for Production)
For production deployments, pin your dependency to a tagged release. This guarantees reproducible builds, backward-compatible API guarantees, and security-scanned transitive dependencies:
- **Go**: `go get github.com/sharedcode/joltrin@v5.4.0` (see [tags](https://github.com/sharedcode/joltrin/tags) for the latest)
- **Python**: `pip install sop4py==0.1.0`
- **C# / .NET**: `dotnet add package Sop --version 0.1.0`

#### 2. In-Repo Source / Submodule (Prototyping & Contribution)
If you are extending storage engine internals (`btree/`, `fs/`), modifying protocol servers (`ai/verify`, `cmd/sop-mcp-server`, `cmd/sop-a2a-agent`), or benchmarking performance enhancements, consuming from source is recommended:
```bash
# Add as a git submodule in your project
git submodule add https://github.com/sharedcode/joltrin.git vendor/joltrin

# Or configure a Go workspace (go.work) for local development
go work use ./vendor/joltrin
```

### Cutting a Release (Maintainers)

Releases are cut from this repo with the scripts in `scripts/`, then tagged and pushed to GitHub. The full step-by-step for building native bindings and publishing to PyPI/NuGet/Maven is in [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md); the short version:

```bash
# 1. Bump the version everywhere (VERSION file, go.mod-adjacent metadata, bindings)
./scripts/update_version.sh 5.4.0

# 2. Review the diff, then commit the bump
git add -A && git commit -m "chore: bump version to 5.4.0"

# 3. Build release artifacts (native libs for Python/Java/C# bindings)
./scripts/build_release.sh

# 4. Verify checksums, archive integrity, and SBOM before publishing
./scripts/verify_release.sh release

# 5. Tag and push. This is what makes `go get github.com/sharedcode/joltrin@v5.4.0` resolve.
git tag v5.4.0
git push origin master v5.4.0

# 6. Create the GitHub Release from the tag (attaches release notes + artifacts)
gh release create v5.4.0 --generate-notes
```

Go's package proxy needs no separate publish step: once the tag is pushed, `go get ...@v5.4.0` works immediately. Python, C#, and Java bindings still require the explicit `twine upload` / `dotnet nuget push` / `mvn deploy` steps in `RELEASE_PROCESS.md`.

## 📚 Technical Reference Guides

- **[What is SOP, in Plain Words](docs/WHAT_IS_SOP.md)**: High-level conceptual overview.
- **[Architecture Whitepaper](docs/SOP_ARCHITECTURE_WHITEPAPER.md)**: Deep dive into B-Tree layout and transactions.
- **[Platform Tools & Relational Intelligence](docs/SOP_PLATFORM_TOOLS.md)**: Data Manager, CEL expressions, and AI Copilot.
- **[AI Copilot & Agent Architecture](docs/AI_COPILOT.md)**: Multi-agent memory model and Space partitioning.
- **[Operations & Failover Guide](docs/OPERATIONS.md)**: Erasure coding, recovery, and cluster management.
- **[Scalability & Capacity Math](docs/SCALABILITY.md)**: Architectural scaling model for billions of items.

---

## 🤝 Get Involved

We welcome feedback, issues, and contributions:

1. **Fork & Clone**: `git clone https://github.com/sharedcode/joltrin.git`
2. **Run Tests**: `go test -v ./...`
3. **Join Discussions**: [GitHub Discussions](https://github.com/SharedCode/joltrin/discussions)
4. **Submit a PR**: Follow Go formatting standards (`gofmt`) and include test coverage.

---

<p align="center">
  <sub>Licensed under the MIT License. Built by <a href="https://github.com/sharedcode">SharedCode</a>.</sub>
</p>
