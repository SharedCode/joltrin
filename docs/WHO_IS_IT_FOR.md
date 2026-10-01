# Who Joltrin is for

### For Potential Customers

**Is Joltrin Right For Me?** Start from the existing [When Joltrin is a Great Fit](WHY_JOLTRIN.md#when-joltrin-is-a-great-fit) and [When Joltrin is NOT the Right Tool](WHY_JOLTRIN.md#when-joltrin-is-not-the-right-tool) sections above, they are the concrete answer. As a quick filter:

- If you are currently running Redis plus Postgres plus a queue just to get durable state and coordinated background work for one application, and that application's data fits comfortably on the machines it runs on, Joltrin is worth evaluating as a replacement for that stack.
- If you already run Postgres or Kafka at scale for reasons unrelated to this problem (complex SQL, multi-datacenter event retention, an existing team's expertise), Joltrin is more likely to complement than replace what you have.
- If your workload is petabyte-scale analytics or requires synchronous multi-region consensus, Joltrin is not the right tool today; see the section above for specifics.

Joltrin is a library you embed, not a managed service you sign up for. There is no hosted offering today; you run it yourself, in-process, in your own infrastructure.

### For CTOs & Engineering Executives

Every service you run that exists only to hold state or coordinate work (a cache, a queue, a lock manager) is a service your team has to patch, monitor, upgrade, and page on. Joltrin's bet is that collapsing storage, transactions, and task coordination into one embedded library reduces that surface for the workloads it fits, at the cost of giving up the specialized tooling and operational maturity of dedicated systems your team may already know well.

Concretely, that means: fewer network hops in your hot path (sub-millisecond, in-process calls instead of 15 to 50ms across Redis, a queue, and Postgres), one dependency to patch and upgrade instead of several, and a transaction boundary that spans your data and your background work instead of stopping at the database. It also means your team takes on a less mature, less battle-tested piece of infrastructure than Postgres or Kafka, with a correspondingly smaller ecosystem, smaller hiring pool of people who already know it, and no enterprise support contract available today. Evaluate it the way you would any early infrastructure bet: pilot it on one bounded, non-critical workload before committing a core system to it.

### For AI Infrastructure Teams

**What Joltrin already provides.** Durable, transactional checkpointing for agent reasoning state: each step an agent commits is a separate, durable B-Tree write, so a killed agent process loses nothing already committed, and a successor process can resume from the last checkpoint. This is not a diagram, it runs today in the [browser demo](https://joltrinhq.com/) (the "AI Agent Memory" tab) and as a Go example (`go run ./examples/agent_memory`). Joltrin also provides vector similarity search over embeddings stored in the same B-Tree as structured data (`ai/memory`, `ai/vector`), and a real swarm/worker package (`ai/swarm`) with job and result stores.

**What could be built on Joltrin, but is not shipped today.** A production multi-agent orchestration framework, a hosted durable-memory-as-a-service for agent frameworks like LangGraph or AutoGen, and distributed MapReduce-style helpers across a live agent swarm are all described as design proposals in [`ai/SWARM_DESIGN.md`](../ai/SWARM_DESIGN.md) (explicitly marked "Proposal / Vision" in that file) but are not implemented and tested the way the checkpointing and vector search primitives are. Treat anything not demonstrated in the linked demo or example as a direction, not a delivered feature.

**Protocol interoperability, actually implemented.** `tools/mcpserver` and `tools/a2aagent` expose Joltrin runbooks to MCP and A2A clients respectively, both gated by a real safety-and-reachability barrier certificate (`ai/verify`) so a step can't execute out of order regardless of what a calling agent claims. Both protocols share one execution trace store, proven by a test that commits steps via one protocol and confirms the other sees them. See [MCP, A2A, and the Verification Engine](MCP_A2A_AND_VERIFICATION_ENGINE.md) for the audit, the design, and an honest accounting of what this checker is and is not (it is not general-purpose LTL model checking).

### For Platform, SRE & Cloud Engineers

Joltrin Engine is a library, not a server: there is no separate database process to provision, patch, or fail over for the embedded case. The optional `tools/httpserver` Data Manager is a standalone service with its own `/metrics` endpoint (tested in `tools/httpserver/metrics_test.go`) if you do want a network-accessible console. Failure recovery is handled by Reed-Solomon erasure coding across storage shards (`fs/erasure`, 12 passing tests at the time of writing) rather than full N-way replication, which trades some recovery latency for lower disk overhead. A prebuilt quickstart container is published to `ghcr.io/sharedcode/joltrin-quickstart`. Multi-node swarm clustering exists and is tested (`examples/swarm_clustered`, `examples/swarm_standalone`), but has not been documented or proven at production scale.

**Supply-Chain Security & Release Provenance:** Release builds are secured by an automated pre-publish quality gate ([`scripts/verify_release.sh`](../scripts/verify_release.sh)), cryptographic SHA-256 manifests (`SHA256SUMS`), SPDX Software Bill of Materials (SBOM), and cryptographically signed build provenance attestations via GitHub Actions OIDC (`actions/attest-build-provenance`, SLSA Level 3 compliance). Consumers can independently verify any downloaded artifact using the standalone verification script.

### For Researchers & Distributed Systems Engineers

The interesting parts to read are the B-Tree implementation with copy-on-write page isolation (`btree/`), the WAL plus two-phase commit transaction protocol (`transaction.go`, `common/`), the Reed-Solomon erasure coding layer (`fs/erasure/`), and the swarm coordination model described in [`ai/SWARM_DESIGN.md`](../ai/SWARM_DESIGN.md). The [Architecture Whitepaper](SOP_ARCHITECTURE_WHITEPAPER.md) and [Architecture vs. Big Tech](ARCHITECTURE_VS_BIG_TECH.md) go deeper into the design tradeoffs than this README does.

### For Students & Learners

Reading this codebase is a reasonable way to see real (not textbook-simplified) implementations of a B-Tree with node splitting and range iteration, optimistic concurrency control, write-ahead logging with two-phase commit, and erasure coding, all in readable Go with test coverage next to the implementation. Start with [`docs/WHAT_IS_SOP.md`](WHAT_IS_SOP.md) for a plain-language overview, then run the zero-dependency quickstart below before reading `btree/` and `fs/erasure/`.

## For Engineering Leaders & Hiring Managers

For technical leaders, CTOs, and hiring managers, this repository serves as a working demonstration of systems engineering across:

- **Storage Engine Design**: Custom B-Tree implementation with sector-aligned direct I/O, node slot tuning, and multi-tier L1/L2 caching.
- **Transactional Systems**: Strict ACID guarantees, Write-Ahead Logging (WAL), Two-Phase Commit (2PC), Snapshot Isolation, and Optimistic Concurrency Control.
- **Fault Tolerance & Reliability**: Reed-Solomon Erasure Coding (N+K striping), active/passive metadata redundancy, and automated partition healing.
- **High Concurrency**: Lock-free data structures, multi-goroutine worker swarms, and SIMD vector dot-product calculation.
- **Polyglot Architecture**: Native Go kernel, Python bindings (`sop4py`), C# bindings (`Sop`), and browser WebAssembly.
- **Production Delivery**: GitHub Actions CI/CD matrix, distroless container builds on GHCR, Codecov integration, and static GitHub Pages deployments.

If you are building distributed systems, cloud infrastructure, or AI data platforms and want to discuss architecture, feel free to connect via [GitHub Discussions](https://github.com/SharedCode/joltrin/discussions).
