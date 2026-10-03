# Live demos

Three browser experiences run from the same engine. None of them need a backend.

| Experience | Description | Live Interactive Link |
| :--- | :--- | :--- |
| **Joltrin Technical Demo** | **Client-Side Zero-Server WebAssembly Engine**<br>Execute live ACID transactions, 128-dimensional vector cosine searches, microsecond benchmarks, and durable AI agent memory checkpoints (kill the agent mid-task, watch a successor resume from the B-Tree) running 100% in your browser with **0 runtime HTTP network calls after initial load**. | [**Launch Technical Demo →**](https://joltrinhq.com/) |
| **Joltrin Arena** | **Distributed Systems Survival Simulation**<br>Command a live digital cluster. Scale worker swarms, crash storage nodes, trigger transaction storms, and watch Joltrin automatically redistribute tasks and rebuild parity in real-time. | [**Play Joltrin Arena →**](https://joltrinhq.com/arena/) |
| **Joltrin Agent Verification Barrier** | **The MCP/A2A Safety Check, Clickable**<br>The same `verify` barrier gating `tools/mcpserver` and `tools/a2aagent`, compiled to WASM. Try dropping a database before validating a backup and watch it get blocked, in your browser, with the trace persisted to OPFS. | [**Launch Agent Barrier →**](https://joltrinhq.com/agents/) |

## Experience Joltrin

You can test Joltrin directly in your browser without installing anything via the live interactive experiences above ([Technical Demo](https://joltrinhq.com/), [Joltrin Arena](https://joltrinhq.com/arena/), and [Agent Verification Barrier](https://joltrinhq.com/agents/)). The technical demo demonstrates the engine's core power directly: safe, ACID-transactional storage running on web storage itself (OPFS), with zero server and zero network calls after the initial page loads the WASM binary. Everything else on this page, including the agent verification barrier below, is built on top of that same engine, a reference implementation showing one concrete use case.

The technical demo persists across reloads now, to Origin Private File System, via the browser's async File System Access API. The diagram below is the real tradeoff behind that choice, not a benchmark; no throughput numbers are shown because none have been measured for either path in this repo.

<p align="center">
  <img src="assets/opfs-path-taken.svg" alt="OPFS persistence: the async File System Access API path this repo built, versus the synchronous createSyncAccessHandle path that would require moving the WASM binary into a dedicated Worker, deliberately not built this pass" width="900" />
</p>

That same WASM-compiled engine is what the Agent Verification Barrier row above actually runs on, not a separate reimplementation: it's the concrete reference implementation this repo ships to answer "what do you actually build with a durable, transactional engine running client-side?" It is an AI agent safety check an agent cannot talk its way around, with the trace itself durable in OPFS across reloads. The next section is that barrier in depth, plus the same check reachable server-side over MCP and A2A.

## See Joltrin in Action (Joltrin Arena Simulation)

In **[Joltrin Arena](https://joltrinhq.com/arena/)**, every control maps directly to a real distributed systems concept:

| Simulation Control | Distributed Systems Concept | Joltrin Technical Mechanism |
| :--- | :--- | :--- |
| **Add Worker** | Swarm Compute | Dynamic queue rebalancing across peer worker nodes without central master bottlenecks. |
| **Remove Worker** | Graceful Degradation | Active tasks drained and re-assigned to healthy nodes with zero dropped writes. |
| **Kill Node / Storage Fault** | Fault Tolerance | **Reed-Solomon Erasure Coding** reconstructs missing B-Tree blocks in-memory from parity chunks. |
| **Transaction Storm** | Concurrency & Isolation | **Optimistic Concurrency Control (OCC)** serializes conflicting writes in microseconds. |
| **Increase Workload (100k TPS)** | Scalability | B-Tree node segments partition write load across sector-aligned storage handles. |
| **Automatic Self-Healing** | Resilient Coordination | Heartbeat lease detection triggers automated task redistribution in `<15ms`. |
