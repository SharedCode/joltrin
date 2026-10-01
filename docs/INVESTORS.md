# Notes for investors

What has and has not been proven. No revenue, customer, or market-size figures are claimed anywhere in this repository.

### For Investors

**The problem.** Teams building stateful distributed applications, agent systems especially, routinely wire together a database, a cache, a message queue, a lock manager, and a workflow engine just to get durable state and coordinated work. Each boundary between those systems is a place where consistency breaks during a partial failure. That integration tax is paid by every team that builds this kind of system, repeatedly.

**What Joltrin uniquely combines.** A B-Tree storage engine, ACID transactions, and swarm task coordination live inside one embedded library instead of behind separate network services. That is an architectural bet, not a settled fact: it trades the maturity and ecosystem of specialized tools (Postgres, Kafka, Temporal) for fewer moving parts and a single consistency boundary. Whether that tradeoff wins in a given workload is something a team has to evaluate, which is exactly what the [comparison table](WHY_JOLTRIN.md#joltrin-vs-alternatives) below is for.

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
The core is MIT-licensed. Commercial governance features (policy-as-code, audit lineage, billing) exist as code in `governance/`, but no revenue or paying customers are documented in this repository. Whether Pro checkout is live depends on production Stripe configuration, not on the code. The open-core progression and architectural foundations for commercial governance are detailed in [Monetization and Tiers](MONETIZATION_AND_TIERS.md).

**What Has Been Proven**
- A working Go engine with ACID transactions (WAL plus two-phase commit), a custom B-Tree, and Reed-Solomon erasure coding, each with passing automated tests (23 packages carry tests in the core Go module; run them with `go test ./...`, while the two WASM-only packages build under `GOOS=js GOARCH=wasm`, see [Performance Benchmarks](BENCHMARKS.md#performance-benchmarks) below for the throughput numbers).
- A real WebAssembly build of the engine running ACID transactions, vector search, and agent-memory checkpointing entirely in-browser with zero runtime network calls after initial page load ([live demo](https://joltrinhq.com/)).
- Working language bindings for Go (native), Python (`sop4py`, published to PyPI), and C# (`Sop`, published to NuGet), plus Java and Rust bindings that exist in-repo with tests but are not yet published to their package registries.
- CI that runs the race detector and `govulncheck` on every change, and a changelog showing multiple rounds of real dependency and CVE remediation.

**What Has Not Yet Been Proven**
- No production deployments or paying customers are documented anywhere in this repository.
- No independent, third-party, or peer-reviewed benchmarks exist; the performance numbers below come from this project's own benchmark harness on a single workstation, not a controlled multi-system comparison.
- Joltrin Arena's cluster view is a UI simulation of the underlying concepts for demonstration purposes, not a live multi-node deployment; multi-node swarm clustering itself is real and tested (`examples/swarm_clustered`, `examples/swarm_standalone`), but has not been run at meaningful scale or under adversarial network conditions in public.
- No formal third-party security audit has been performed.
- No case studies, design partners, or committed customers exist yet.

### For Investment Banking & Technology Finance

**Technology category.** Joltrin sits in the embedded data infrastructure layer: a storage and coordination engine that applications link against directly, similar in category placement to SQLite or RocksDB, but extended with distributed ACID transactions and task coordination that those two do not attempt.

**Adjacent markets.** Embedded/operational databases, distributed coordination and workflow orchestration, vector search infrastructure, and AI agent infrastructure tooling. Each of those adjacent markets has established commercial players (see the [comparison table](WHY_JOLTRIN.md#joltrin-vs-alternatives)), which is useful context for sizing the competitive landscape Joltrin would need to differentiate against.

**Potential strategic relevance.** This could include: infrastructure vendors looking to add an embedded, agent-friendly storage layer to an existing platform; cloud providers evaluating lightweight alternatives to running separate managed database, cache, and queue services for edge or agent workloads; or AI infrastructure companies needing a durable state layer under an agent runtime. None of this reflects any actual approach, interest, or discussion from any party; it is offered as a way to reason about where the technology could fit strategically.

**Open-source distribution.** The core is distributed under the MIT license, so any team can use it in production immediately. Commercial tiers add governance features on top, and none of them are required to use the engine. See [Monetization and Tiers](MONETIZATION_AND_TIERS.md) for the open-core progression and architectural separation.

**Competitive landscape.** Summarized in the [Joltrin vs. Alternatives](WHY_JOLTRIN.md#joltrin-vs-alternatives) table further down. No competitor is presented as inferior; each is a mature, widely deployed system that Joltrin would need to displace or complement for any given workload.
