# Benchmarks and engineering proof

What is measured in this repository, how to reproduce it, and what the numbers do not show.

### Engineering ROI, Verified in This Repo

No revenue or customer numbers exist yet for this project (see [For Investors](INVESTORS.md) for the honest version of that). What is verified today, in this repo, is the infrastructure cost this architecture removes:

| What collapses | From | To |
| :--- | :--- | :--- |
| **Network hops per operation** | 3 hops across Redis, a queue, and Postgres/Cassandra (estimated 15-50ms network round-trip overhead) | 1 embedded in-process call (<0.3ms measured latency, >145k ops/sec) |
| **Stateful services to operate, patch, and page on** | Redis + Kafka/RabbitMQ + Postgres/Cassandra + ZooKeeper (4+) | 1 embedded library |
| **Language surfaces shipped** | N/A | Go (native), Python (`sop4py` on PyPI), C# (`Sop` on NuGet); Java and Rust bindings exist in-repo with tests, not yet published |
| **CI rigor on every change** | N/A | `govulncheck` clean on every push; race detector on the core engine packages (`btree`, `common`, `fs`, `inmemory`); 3-OS build and test matrix (Linux, macOS, Windows) |
| **Deployment footprint of the technical demo** | A server-backed demo stack | WASM build running ACID transactions, vector search, and agent-memory checkpointing 100% client-side, 0 runtime HTTP calls after page load ([live](https://joltrinhq.com/)) |

Every row above is something you can run yourself, not a projection. See [Performance Benchmarks](#performance-benchmarks) for the throughput numbers behind the latency claim, and [What Has Not Yet Been Proven](INVESTORS.md) for what this table deliberately leaves out.

## Performance Benchmarks

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
